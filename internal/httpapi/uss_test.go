package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

type allowUSSAuthorizer struct{}

func (allowUSSAuthorizer) Authorize(*http.Request, string) (string, error) { return "peer", nil }

type servingPublisher struct{}

func (servingPublisher) ID() string { return "serving" }
func (servingPublisher) FindOperationalIntents(context.Context, airspaceprovider.Query) ([]airspaceprovider.OperationalIntent, error) {
	return nil, nil
}
func (servingPublisher) PublicationEnabled() bool { return true }
func (servingPublisher) CreateOperationalIntent(context.Context, airspaceprovider.PublicationRequest) (airspaceprovider.PublicationReceipt, error) {
	return airspaceprovider.PublicationReceipt{}, nil
}
func (servingPublisher) UpdateOperationalIntent(context.Context, airspaceprovider.PublicationRequest) (airspaceprovider.PublicationReceipt, error) {
	return airspaceprovider.PublicationReceipt{}, nil
}
func (servingPublisher) DeleteOperationalIntent(context.Context, string, string) (airspaceprovider.PublicationReceipt, error) {
	return airspaceprovider.PublicationReceipt{}, nil
}
func (servingPublisher) GetOperationalIntentReference(context.Context, string) (airspaceprovider.PublicationReceipt, error) {
	return airspaceprovider.PublicationReceipt{}, nil
}
func (servingPublisher) BuildPeerNotification(airspaceprovider.PublicationRequest, airspaceprovider.PublicationReceipt, airspaceprovider.Subscriber, bool) ([]byte, error) {
	return nil, nil
}
func (servingPublisher) DeliverPeerNotification(context.Context, string, []byte) error { return nil }

func TestUSSDetailsEndpointServesConfirmedPublication(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	const intentID = "11111111-1111-4111-8111-111111111111"
	store := durablememory.NewStore()
	intent := domain.OperationalIntent{
		ID: intentID, Version: 1, AircraftID: "aircraft", Status: domain.IntentStatusAccepted,
		PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID: "volume", IntentID: intentID, IntentVersion: 1,
		GeoJSON:      `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
		MinAltitudeM: 20, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: now, EndsAt: now.Add(time.Hour), VolumeType: domain.OperationalVolumeRoute,
	}); err != nil {
		t.Fatal(err)
	}
	publication := domain.OperationalIntentPublication{
		IntentID: intentID, DesiredIntentVersion: 1, DesiredState: domain.OperationalIntentExternalStateAccepted,
		NextAttemptAt: now, UpdatedAt: now,
	}
	if err := store.RequestOperationalIntentPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimOperationalIntentPublication(ctx, intentID, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	claimed.PublishedIntentVersion = 1
	claimed.ConfirmedState = domain.OperationalIntentExternalStateAccepted
	claimed.SyncStatus = domain.PublicationSyncConfirmed
	claimed.DSSVersion = 3
	claimed.OVN = "owned-ovn"
	claimed.ReferenceJSON = []byte(fmt.Sprintf(`{
		"id":"%s","manager":"aero-arc","ovn":"owned-ovn","state":"Accepted",
		"subscription_id":"22222222-2222-4222-8222-222222222222",
		"uss_availability":"Normal","uss_base_url":"https://uss.example","version":3,
		"time_start":{"format":"RFC3339","value":"2026-08-10T18:00:00Z"},
		"time_end":{"format":"RFC3339","value":"2026-08-10T19:00:00Z"}}`, intentID))
	if err := store.UpdateOperationalIntentPublication(ctx, claimed, claimed.Revision); err != nil {
		t.Fatal(err)
	}
	service, err := deconfliction.NewDeconflictionService(store, servingPublisher{})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewWithWorkflows(nil, nil, nil, nil, time.Second, service).WithUSSAuthorizer(allowUSSAuthorizer{}).Handler()
	request := httptest.NewRequest(http.MethodGet, "/uss/v1/operational_intents/"+intentID+"?version=3", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !containsAll(response.Body.String(), `"manager":"aero-arc"`, `"volumes"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/uss/v1/operational_intents/"+intentID+"?version=2", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("version mismatch status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestUSSNotificationEndpointDurablyRecordsDeletion(t *testing.T) {
	const intentID = "33333333-3333-4333-8333-333333333333"
	store := durablememory.NewStore()
	service, err := deconfliction.NewDeconflictionService(store, servingPublisher{})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewWithWorkflows(nil, nil, nil, nil, time.Second, service).WithUSSAuthorizer(allowUSSAuthorizer{}).Handler()
	body := fmt.Sprintf(`{"operational_intent_id":%q,"subscriptions":[]}`, intentID)

	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/uss/v1/operational_intents", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}

	notifications, err := store.ListReceivedPeerNotifications(context.Background(), intentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 1 {
		t.Fatalf("received notifications = %d, want 1", len(notifications))
	}
	if got := notifications[0]; got.Manager != "peer" || !got.Deleted || len(got.Payload) == 0 {
		t.Fatalf("received notification = %+v", got)
	}
}

func TestUSSNotificationEndpointRecordsOperationalIntent(t *testing.T) {
	const intentID = "44444444-4444-4444-8444-444444444444"
	store := durablememory.NewStore()
	service, err := deconfliction.NewDeconflictionService(store, servingPublisher{})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewWithWorkflows(nil, nil, nil, nil, time.Second, service).WithUSSAuthorizer(allowUSSAuthorizer{}).Handler()
	body := fmt.Sprintf(`{
		"operational_intent_id":%q,
		"operational_intent":{"reference":{
			"id":%q,"manager":"peer","ovn":"peer-ovn","state":"Accepted",
			"subscription_id":"55555555-5555-4555-8555-555555555555",
			"uss_availability":"Normal","uss_base_url":"https://peer.example","version":4,
			"time_start":{"format":"RFC3339","value":"2026-08-10T18:00:00Z"},
			"time_end":{"format":"RFC3339","value":"2026-08-10T19:00:00Z"}},
			"details":{"priority":0}},"subscriptions":[]}`, intentID, intentID)
	request := httptest.NewRequest(http.MethodPost, "/uss/v1/operational_intents", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	notifications, err := store.ListReceivedPeerNotifications(context.Background(), intentID)
	if err != nil || len(notifications) != 1 {
		t.Fatalf("notifications=%#v err=%v", notifications, err)
	}
	if got := notifications[0]; got.Deleted || got.IntentVersion != 4 || got.OVN != "peer-ovn" {
		t.Fatalf("notification=%+v", got)
	}
}

func TestUSSDetailsEndpointValidatesAuthorizationAndParameters(t *testing.T) {
	store := durablememory.NewStore()
	service, err := deconfliction.NewDeconflictionService(store, servingPublisher{})
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithWorkflows(nil, nil, nil, nil, time.Second, service)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/uss/v1/operational_intents/not-a-uuid", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing authorizer status=%d body=%s", response.Code, response.Body.String())
	}

	handler := server.WithUSSAuthorizer(allowUSSAuthorizer{}).Handler()
	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/uss/v1/operational_intents/not-a-uuid", want: http.StatusBadRequest},
		{path: "/uss/v1/operational_intents/66666666-6666-4666-8666-666666666666?version=0", want: http.StatusBadRequest},
		{path: "/uss/v1/operational_intents/66666666-6666-4666-8666-666666666666", want: http.StatusNotFound},
	} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != test.want {
			t.Fatalf("%s status=%d want=%d body=%s", test.path, response.Code, test.want, response.Body.String())
		}
	}
}

func containsAll(value string, patterns ...string) bool {
	for _, pattern := range patterns {
		if !strings.Contains(value, pattern) {
			return false
		}
	}
	return true
}
