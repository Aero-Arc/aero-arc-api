package deconfliction

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	dss "github.com/Aero-Arc/dss-clients/interuss"
)

type recordingPublisher struct {
	creates    int
	updates    int
	deletes    int
	gets       int
	deliveries int
	createFn   func(airspaceprovider.PublicationRequest) (airspaceprovider.PublicationReceipt, error)
	updateFn   func(airspaceprovider.PublicationRequest) (airspaceprovider.PublicationReceipt, error)
	getFn      func(string) (airspaceprovider.PublicationReceipt, error)
	deleteOVN  string
}

func (p *recordingPublisher) ID() string { return "recording" }
func (p *recordingPublisher) FindOperationalIntents(context.Context, airspaceprovider.Query) ([]airspaceprovider.OperationalIntent, error) {
	return []airspaceprovider.OperationalIntent{}, nil
}
func (p *recordingPublisher) PublicationEnabled() bool { return true }
func (p *recordingPublisher) CreateOperationalIntent(_ context.Context, request airspaceprovider.PublicationRequest) (airspaceprovider.PublicationReceipt, error) {
	p.creates++
	if p.createFn != nil {
		return p.createFn(request)
	}
	return testReceipt(request, p.creates+p.updates), nil
}
func (p *recordingPublisher) UpdateOperationalIntent(_ context.Context, request airspaceprovider.PublicationRequest) (airspaceprovider.PublicationReceipt, error) {
	p.updates++
	if p.updateFn != nil {
		return p.updateFn(request)
	}
	return testReceipt(request, p.creates+p.updates), nil
}
func (p *recordingPublisher) DeleteOperationalIntent(_ context.Context, intentID, ovn string) (airspaceprovider.PublicationReceipt, error) {
	p.deletes++
	p.deleteOVN = ovn
	return airspaceprovider.PublicationReceipt{Version: p.creates + p.updates + p.deletes, OVN: intentID + "-deleted", ReferenceJSON: []byte(`{}`)}, nil
}
func (p *recordingPublisher) GetOperationalIntentReference(_ context.Context, intentID string) (airspaceprovider.PublicationReceipt, error) {
	p.gets++
	if p.getFn != nil {
		return p.getFn(intentID)
	}
	return airspaceprovider.PublicationReceipt{}, nil
}
func (p *recordingPublisher) BuildPeerNotification(airspaceprovider.PublicationRequest, airspaceprovider.PublicationReceipt, airspaceprovider.Subscriber, bool) ([]byte, error) {
	return []byte(`{}`), nil
}
func (p *recordingPublisher) DeliverPeerNotification(context.Context, string, []byte) error {
	p.deliveries++
	return nil
}

func TestPublicationReconcilerCreatesUpdatesAndWithdrawsOneDSSReference(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	store := durablememory.NewStore()
	publisher := &recordingPublisher{}
	deconflictionService, err := NewDeconflictionServiceWithClock(store, func() time.Time { return now }, publisher)
	if err != nil {
		t.Fatal(err)
	}
	intents := service.NewIntentServiceWithClock(store, func() time.Time { return now }, deconflictionService)
	intent, err := intents.CreateIntent(ctx, service.CreateIntentRequest{
		ID: uuid.NewString(), AircraftID: "aircraft-1", Name: "publish me", Summary: "test",
		PlannedStartAt: now.Add(time.Hour), PlannedEndAt: now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	minAltitude, maxAltitude := 10.0, 100.0
	_, err = intents.AddOperationalVolume(ctx, intent.ID, service.AddOperationalVolumeRequest{
		ID: "volume-1", GeoJSON: `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
		MinAltitudeM: &minAltitude, MaxAltitudeM: &maxAltitude, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour), VolumeType: domain.OperationalVolumeRoute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := deconflictionService.ReconcileDue(ctx); err != nil {
		t.Fatal(err)
	}
	publication, err := deconflictionService.GetPublication(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if publisher.creates != 1 || publisher.deliveries != 1 || publication.SyncStatus != domain.PublicationSyncConfirmed || publication.PublishedIntentVersion != 1 {
		t.Fatalf("after create: publisher=%+v publication=%+v", publisher, publication)
	}

	request := deconflictionService.PublicationRequest(intent, domain.OperationalIntentExternalStateAccepted)
	if err := store.RequestOperationalIntentPublication(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := deconflictionService.ReconcileDue(ctx); err != nil {
		t.Fatal(err)
	}
	if publisher.updates != 1 {
		t.Fatalf("updates = %d, want 1", publisher.updates)
	}

	request = deconflictionService.PublicationRequest(intent, domain.OperationalIntentExternalStateWithdrawn)
	if err := store.RequestOperationalIntentPublication(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := deconflictionService.ReconcileDue(ctx); err != nil {
		t.Fatal(err)
	}
	publication, _ = deconflictionService.GetPublication(ctx, intent.ID)
	if publisher.deletes != 1 || publication.SyncStatus != domain.PublicationSyncWithdrawn || publication.PublishedIntentVersion != 0 {
		t.Fatalf("after delete: publisher=%+v publication=%+v", publisher, publication)
	}
}

func TestActivationRecoversWhenDSSIsAlreadyActivated(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	store := durablememory.NewStore()
	publisher := &recordingPublisher{}
	deconflictionService, err := NewDeconflictionServiceWithClock(store, func() time.Time { return now }, publisher)
	if err != nil {
		t.Fatal(err)
	}
	intents := service.NewIntentServiceWithClock(store, func() time.Time { return now }, deconflictionService)
	intent, err := intents.CreateIntent(ctx, service.CreateIntentRequest{
		ID: uuid.NewString(), AircraftID: "aircraft-1", Name: "recover activation", Summary: "test",
		PlannedStartAt: now.Add(time.Hour), PlannedEndAt: now.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	minimum, maximum := 10.0, 100.0
	if _, err := intents.AddOperationalVolume(ctx, intent.ID, service.AddOperationalVolumeRequest{
		ID: "volume-1", GeoJSON: `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
		MinAltitudeM: &minimum, MaxAltitudeM: &maximum, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour), VolumeType: domain.OperationalVolumeRoute,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	intent, err = intents.AcceptIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := deconflictionService.ReconcileIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPreflightCheck(ctx, domain.PreflightCheck{
		ID: "preflight", IntentID: intent.ID, IntentVersion: intent.Version,
		AircraftID: intent.AircraftID, Status: domain.PreflightStatusClear, CapturedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	request := deconflictionService.PublicationRequest(intent, domain.OperationalIntentExternalStateActivated)
	if err := store.RequestOperationalIntentPublication(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := deconflictionService.ReconcileIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}

	intent, err = intents.ActivateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Status != domain.IntentStatusActive || publisher.updates != 1 {
		t.Fatalf("intent=%+v publisher=%+v", intent, publisher)
	}
}

func TestWithdrawalRecoversReferenceCreatedByStaleWorker(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	currentTime := now
	store := durablememory.NewStore()
	intent := publicationTestIntent(t, ctx, store, now)
	publisher := &recordingPublisher{}
	created := testReceipt(airspaceprovider.PublicationRequest{
		Intent: intent, State: domain.OperationalIntentExternalStateAccepted,
	}, 1)
	created.Subscribers = nil
	publisher.getFn = func(string) (airspaceprovider.PublicationReceipt, error) { return created, nil }
	deconflictionService, err := NewDeconflictionServiceWithClock(store, func() time.Time { return currentTime }, publisher)
	if err != nil {
		t.Fatal(err)
	}
	accepted := deconflictionService.PublicationRequest(intent, domain.OperationalIntentExternalStateAccepted)
	if err := store.RequestOperationalIntentPublication(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimOperationalIntentPublication(ctx, intent.ID, now, now.Add(publicationLease))
	if err != nil {
		t.Fatal(err)
	}
	withdrawn := deconflictionService.PublicationRequest(intent, domain.OperationalIntentExternalStateWithdrawn)
	if err := store.RequestOperationalIntentPublication(ctx, withdrawn); err != nil {
		t.Fatal(err)
	}
	applyReceipt(&claimed, created)
	claimed.SyncStatus = domain.PublicationSyncConfirmed
	if err := store.ConfirmOperationalIntentPublication(ctx, claimed, claimed.Revision, nil); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("stale create confirmation error = %v, want version conflict", err)
	}
	if err := deconflictionService.ReconcileDue(ctx); err != nil {
		t.Fatal(err)
	}
	if publisher.gets != 0 || publisher.deletes != 0 {
		t.Fatalf("withdrawal ran during create lease: gets=%d deletes=%d", publisher.gets, publisher.deletes)
	}
	currentTime = now.Add(publicationLease + time.Second)
	if err := deconflictionService.ReconcileDue(ctx); err != nil {
		t.Fatal(err)
	}
	publication, err := store.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if publisher.gets != 1 || publisher.deletes != 1 || publisher.deleteOVN != created.OVN {
		t.Fatalf("publisher recovery = %+v, recovered OVN = %q", publisher, publisher.deleteOVN)
	}
	if publication.SyncStatus != domain.PublicationSyncWithdrawn || publication.PublishedIntentVersion != 0 {
		t.Fatalf("publication = %+v, want withdrawn", publication)
	}
}

func TestLostMutationResponseRetriesUpdateToRecoverSubscribers(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	store := durablememory.NewStore()
	intent := publicationTestIntent(t, ctx, store, now)
	lostResponse := errors.New("connection closed after DSS mutation")
	publisher := &recordingPublisher{}
	recovered := testReceipt(airspaceprovider.PublicationRequest{
		Intent: intent, State: domain.OperationalIntentExternalStateAccepted,
	}, 1)
	recovered.Subscribers = nil
	publisher.createFn = func(airspaceprovider.PublicationRequest) (airspaceprovider.PublicationReceipt, error) {
		return airspaceprovider.PublicationReceipt{}, lostResponse
	}
	publisher.getFn = func(string) (airspaceprovider.PublicationReceipt, error) { return recovered, nil }
	deconflictionService, err := NewDeconflictionServiceWithClock(store, func() time.Time { return now }, publisher)
	if err != nil {
		t.Fatal(err)
	}
	request := deconflictionService.PublicationRequest(intent, domain.OperationalIntentExternalStateAccepted)
	if err := store.RequestOperationalIntentPublication(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := deconflictionService.ReconcileIntent(ctx, intent.ID); !errors.Is(err, lostResponse) {
		t.Fatalf("first reconciliation error = %v, want lost response", err)
	}
	publication, err := store.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if publication.SyncStatus != domain.PublicationSyncRetrying || publication.OVN != recovered.OVN {
		t.Fatalf("recovered publication = %+v", publication)
	}
	if err := deconflictionService.ReconcileIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	publication, err = store.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if publisher.updates != 1 || publication.SyncStatus != domain.PublicationSyncConfirmed {
		t.Fatalf("publisher=%+v publication=%+v", publisher, publication)
	}
	notifications, err := store.ClaimDuePeerNotifications(ctx, now, now.Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 {
		t.Fatalf("notifications = %#v, want recovered subscriber notification", notifications)
	}
}

func TestDSSConflictResponseRemainsRetryable(t *testing.T) {
	err := &dss.SCDResponseError{StatusCode: 409, Status: "409 Conflict"}
	if permanentPublicationError(err) {
		t.Fatal("DSS conflict was classified as permanent")
	}
	if !permanentPublicationError(&dss.SCDResponseError{StatusCode: 403, Status: "403 Forbidden"}) {
		t.Fatal("DSS authorization failure was not classified as permanent")
	}
	if permanentPublicationError(errors.New("network failure")) {
		t.Fatal("network failure was classified as permanent")
	}
}

func publicationTestIntent(t *testing.T, ctx context.Context, store *durablememory.Store, now time.Time) domain.OperationalIntent {
	t.Helper()
	intent := domain.OperationalIntent{
		ID: uuid.NewString(), Version: 1, AircraftID: "aircraft-1", Status: domain.IntentStatusAccepted,
		PlannedStartAt: now.Add(time.Hour), PlannedEndAt: now.Add(2 * time.Hour), UpdatedAt: now,
	}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID: "volume-1", IntentID: intent.ID, IntentVersion: intent.Version,
		GeoJSON:      `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
		MinAltitudeM: 10, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour), VolumeType: domain.OperationalVolumeRoute,
	}); err != nil {
		t.Fatal(err)
	}
	return intent
}

func testReceipt(request airspaceprovider.PublicationRequest, version int) airspaceprovider.PublicationReceipt {
	reference, _ := json.Marshal(map[string]any{
		"id": request.Intent.ID, "manager": "aero-arc", "ovn": request.Intent.ID + "-ovn",
		"state": request.State, "subscription_id": "11111111-1111-4111-8111-111111111111",
		"uss_availability": "Normal", "uss_base_url": "https://uss.example", "version": version,
		"time_start": map[string]any{"format": "RFC3339", "value": request.Intent.PlannedStartAt},
		"time_end":   map[string]any{"format": "RFC3339", "value": request.Intent.PlannedEndAt},
	})
	return airspaceprovider.PublicationReceipt{
		Manager: "aero-arc", Version: version, OVN: request.Intent.ID + "-ovn",
		SubscriptionID: "11111111-1111-4111-8111-111111111111", USSBaseURL: "https://uss.example", ReferenceJSON: reference,
		Subscribers: []airspaceprovider.Subscriber{{USSBaseURL: "https://subscriber.example"}},
	}
}
