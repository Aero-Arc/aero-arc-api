package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	preflightsvc "github.com/Aero-Arc/aero-arc-api/internal/service/preflight"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
)

func TestCreateIntentRejectsInvalidPlannedWindow(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	request := workflowIntentRequest(now)
	request.PlannedEndAt = request.PlannedStartAt

	_, err := NewIntentServiceWithClock(store, fixedClock(now), nil).CreateIntent(ctx, request)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("CreateIntent error = %v, want ErrValidation", err)
	}
}

func TestCreateIntentPreservesNonUUIDIdentifierWithoutPublishing(t *testing.T) {
	now := fixedWorkflowTime()
	request := workflowIntentRequest(now)
	request.ID = "human-readable-intent"
	intent, err := NewIntentServiceWithClock(durablememory.NewStore(), fixedClock(now), nil).CreateIntent(context.Background(), request)
	if err != nil || intent.ID != request.ID {
		t.Fatalf("CreateIntent intent = %#v, error = %v", intent, err)
	}
}

func TestCreateIntentRejectsNonUUIDIdentifierWithPublishing(t *testing.T) {
	now := fixedWorkflowTime()
	request := workflowIntentRequest(now)
	request.ID = "human-readable-intent"
	_, err := NewIntentServiceWithClock(durablememory.NewStore(), fixedClock(now), &workflowCoordinator{}).CreateIntent(context.Background(), request)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("CreateIntent error = %v, want ErrValidation", err)
	}
}

func TestAcceptIntentRejectsDSSIncompatiblePublication(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)
	coordinator := &durableWorkflowCoordinator{
		store: store, now: now, validationErr: errors.New("altitude reference AGL is not supported by SCD"),
	}

	_, err := NewIntentServiceWithClock(store, fixedClock(now), coordinator).AcceptIntent(ctx, intent.ID)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("AcceptIntent error = %v, want ErrValidation", err)
	}
	current, getErr := store.GetOperationalIntent(ctx, intent.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if current.Status != domain.IntentStatusSubmitted {
		t.Fatalf("status = %q, want submitted", current.Status)
	}
	if _, publicationErr := store.GetOperationalIntentPublication(ctx, intent.ID); !errors.Is(publicationErr, durable.ErrNotFound) {
		t.Fatalf("publication error = %v, want not found", publicationErr)
	}
}

func TestCancelLegacyIntentSkipsDSSWithdrawal(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	intent := domain.OperationalIntent{
		ID: "legacy-intent", Version: 1, Status: domain.IntentStatusSubmitted,
		PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	coordinator := &durableWorkflowCoordinator{store: store, now: now}

	canceled, err := NewIntentServiceWithClock(store, fixedClock(now), coordinator).CancelIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Status != domain.IntentStatusCanceled {
		t.Fatalf("status = %q, want canceled", canceled.Status)
	}
	if _, publicationErr := store.GetOperationalIntentPublication(ctx, intent.ID); !errors.Is(publicationErr, durable.ErrNotFound) {
		t.Fatalf("publication error = %v, want not found", publicationErr)
	}
}

type workflowCoordinator struct {
	publication domain.OperationalIntentPublication
	reconciles  int
}

type durableWorkflowCoordinator struct {
	store         durable.Store
	now           time.Time
	reconciles    int
	validationErr error
	reconcileErr  error
	beforeConfirm func(context.Context, domain.OperationalIntentPublication) error
}

type activationRejectingStore struct {
	durable.Store
	err error
}

func (s *activationRejectingStore) ActivateOperationalIntent(context.Context, domain.OperationalIntent, int64) error {
	return s.err
}

func (c *durableWorkflowCoordinator) CheckIntent(context.Context, string) (domain.DeconflictionResult, error) {
	return domain.DeconflictionResult{Posture: domain.DeconflictionPostureClear}, nil
}

func (c *durableWorkflowCoordinator) PublishingEnabled() bool { return true }

func (c *durableWorkflowCoordinator) ValidatePublication(context.Context, domain.OperationalIntent, domain.OperationalIntentExternalState) error {
	return c.validationErr
}

func (c *durableWorkflowCoordinator) PublicationRequest(intent domain.OperationalIntent, state domain.OperationalIntentExternalState) domain.OperationalIntentPublication {
	return domain.OperationalIntentPublication{
		IntentID: intent.ID, DesiredIntentVersion: intent.Version, DesiredState: state,
		SyncStatus: domain.PublicationSyncPending, NextAttemptAt: c.now, UpdatedAt: c.now,
	}
}

func (c *durableWorkflowCoordinator) GetPublication(ctx context.Context, intentID string) (domain.OperationalIntentPublication, error) {
	return c.store.GetOperationalIntentPublication(ctx, intentID)
}

func (c *durableWorkflowCoordinator) ReconcileIntent(ctx context.Context, intentID string) error {
	c.reconciles++
	if c.reconcileErr != nil {
		err := c.reconcileErr
		c.reconcileErr = nil
		return err
	}
	publication, err := c.store.ClaimOperationalIntentPublication(ctx, intentID, c.now, c.now.Add(time.Minute))
	if err != nil {
		return err
	}
	if c.beforeConfirm != nil {
		if err := c.beforeConfirm(ctx, publication); err != nil {
			return err
		}
	}
	if publication.DesiredState == domain.OperationalIntentExternalStateWithdrawn {
		publication.PublishedIntentVersion = 0
	} else {
		publication.PublishedIntentVersion = publication.DesiredIntentVersion
	}
	publication.ConfirmedState = publication.DesiredState
	publication.SyncStatus = domain.PublicationSyncConfirmed
	publication.ConfirmedAt = &c.now
	publication.UpdatedAt = c.now
	return c.store.ConfirmOperationalIntentPublication(ctx, publication, publication.Revision, nil)
}

func (c *workflowCoordinator) CheckIntent(context.Context, string) (domain.DeconflictionResult, error) {
	return domain.DeconflictionResult{Posture: domain.DeconflictionPostureClear}, nil
}

func (c *workflowCoordinator) PublishingEnabled() bool { return true }

func (c *workflowCoordinator) ValidatePublication(context.Context, domain.OperationalIntent, domain.OperationalIntentExternalState) error {
	return nil
}

func (c *workflowCoordinator) PublicationRequest(intent domain.OperationalIntent, state domain.OperationalIntentExternalState) domain.OperationalIntentPublication {
	return domain.OperationalIntentPublication{
		IntentID: intent.ID, DesiredIntentVersion: intent.Version, DesiredState: state,
		SyncStatus: domain.PublicationSyncPending, NextAttemptAt: intent.UpdatedAt, UpdatedAt: intent.UpdatedAt,
	}
}

func (c *workflowCoordinator) GetPublication(context.Context, string) (domain.OperationalIntentPublication, error) {
	return c.publication, nil
}

func (c *workflowCoordinator) ReconcileIntent(context.Context, string) error {
	c.reconciles++
	c.publication.ConfirmedState = domain.OperationalIntentExternalStateActivated
	c.publication.SyncStatus = domain.PublicationSyncConfirmed
	return nil
}

func TestIntentLifecycleCoordinatesPublicationAndWithdrawal(t *testing.T) {
	ctx := context.Background()
	now := fixedWorkflowTime()
	store := durablememory.NewStore()
	coordinator := &workflowCoordinator{}
	intents := NewIntentServiceWithClock(store, fixedClock(now), coordinator)
	intent, err := intents.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := intents.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	intent, err = intents.AcceptIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.publication = domain.OperationalIntentPublication{
		IntentID: intent.ID, PublishedIntentVersion: intent.Version,
		ConfirmedState: domain.OperationalIntentExternalStateAccepted,
		SyncStatus:     domain.PublicationSyncConfirmed,
	}
	if err := store.RecordPreflightCheck(ctx, domain.PreflightCheck{
		ID: "preflight", IntentID: intent.ID, IntentVersion: intent.Version,
		Status: domain.PreflightStatusClear, CapturedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	intent, err = intents.ActivateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Status != domain.IntentStatusActive || coordinator.reconciles != 1 {
		t.Fatalf("activated intent=%+v coordinator=%+v", intent, coordinator)
	}
	intent, err = intents.CompleteIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := store.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Status != domain.IntentStatusComplete || publication.DesiredState != domain.OperationalIntentExternalStateWithdrawn {
		t.Fatalf("completed intent=%+v publication=%+v", intent, publication)
	}
}

func TestAcceptIntentRejectsLegacyIDBeforePublication(t *testing.T) {
	ctx := context.Background()
	now := fixedWorkflowTime()
	store := durablememory.NewStore()
	local := NewIntentServiceWithClock(store, fixedClock(now), nil)
	request := workflowIntentRequest(now)
	request.ID = "legacy-intent"
	intent, err := local.CreateIntent(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if intent, err = local.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	publishing := NewIntentServiceWithClock(store, fixedClock(now), &workflowCoordinator{})
	if _, err := publishing.AcceptIntent(ctx, intent.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("AcceptIntent error = %v, want validation failure", err)
	}
	stored, err := store.GetOperationalIntent(ctx, intent.ID)
	if err != nil || stored.Status != domain.IntentStatusSubmitted {
		t.Fatalf("stored intent = %#v, %v; want submitted", stored, err)
	}
	if _, err := store.GetOperationalIntentPublication(ctx, intent.ID); !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("publication error = %v, want not found", err)
	}
}

func TestActivateIntentBackfillsAcceptedPublication(t *testing.T) {
	ctx := context.Background()
	now := fixedWorkflowTime()
	store := durablememory.NewStore()
	local := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := local.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now)); err != nil {
		t.Fatal(err)
	}
	if intent, err = local.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if intent, err = local.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPreflightCheck(ctx, domain.PreflightCheck{
		ID: "preflight", IntentID: intent.ID, IntentVersion: intent.Version,
		Status: domain.PreflightStatusClear, CapturedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	coordinator := &durableWorkflowCoordinator{store: store, now: now}
	publishing := NewIntentServiceWithClock(store, fixedClock(now), coordinator)
	intent, err = publishing.ActivateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := store.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Status != domain.IntentStatusActive || coordinator.reconciles != 2 ||
		publication.ConfirmedState != domain.OperationalIntentExternalStateActivated {
		t.Fatalf("intent=%+v coordinator=%+v publication=%+v", intent, coordinator, publication)
	}
}

func TestActivateIntentRestoresAcceptedDSSPublicationAfterConcurrentModification(t *testing.T) {
	ctx := context.Background()
	now := fixedWorkflowTime()
	store := durablememory.NewStore()
	coordinator := &durableWorkflowCoordinator{store: store, now: now}
	intents := NewIntentServiceWithClock(store, fixedClock(now), coordinator)
	intent, err := intents.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := intents.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now)); err != nil {
		t.Fatal(err)
	}
	if intent, err = intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if intent, err = intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ReconcileIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPreflightCheck(ctx, domain.PreflightCheck{
		ID: "preflight", IntentID: intent.ID, IntentVersion: intent.Version,
		Status: domain.PreflightStatusClear, CapturedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	modified := false
	coordinator.beforeConfirm = func(ctx context.Context, publication domain.OperationalIntentPublication) error {
		if modified || publication.DesiredState != domain.OperationalIntentExternalStateActivated {
			return nil
		}
		modified = true
		current, err := store.GetOperationalIntent(ctx, intent.ID)
		if err != nil {
			return err
		}
		volumes, err := store.ListOperationalVolumes(ctx, intent.ID)
		if err != nil {
			return err
		}
		current.Version++
		current.Status = domain.IntentStatusDraft
		current.UpdatedAt = now.Add(time.Second)
		for index := range volumes {
			volumes[index].IntentVersion = current.Version
		}
		return store.ReplaceOperationalIntent(ctx, intent.Version, intent.Revision, current, volumes)
	}
	if _, err := intents.ActivateIntent(ctx, intent.ID); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("ActivateIntent error = %v, want version conflict", err)
	}
	publication, err := store.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.GetOperationalIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 2 || current.Status != domain.IntentStatusDraft ||
		publication.DesiredIntentVersion != 1 || publication.PublishedIntentVersion != 1 ||
		publication.DesiredState != domain.OperationalIntentExternalStateAccepted ||
		publication.ConfirmedState != domain.OperationalIntentExternalStateAccepted {
		t.Fatalf("current=%+v publication=%+v", current, publication)
	}
}

func TestActivateIntentRestoresAcceptedPublicationAfterReconcileFailure(t *testing.T) {
	ctx := context.Background()
	now := fixedWorkflowTime()
	store := durablememory.NewStore()
	coordinator := &durableWorkflowCoordinator{store: store, now: now}
	intents := NewIntentServiceWithClock(store, fixedClock(now), coordinator)
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)
	var err error
	if intent, err = intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ReconcileIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPreflightCheck(ctx, domain.PreflightCheck{
		ID: "preflight", IntentID: intent.ID, IntentVersion: intent.Version,
		Status: domain.PreflightStatusClear, CapturedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	coordinator.reconcileErr = context.DeadlineExceeded
	if _, err := intents.ActivateIntent(ctx, intent.ID); !errors.Is(err, ErrActivationBlocked) {
		t.Fatalf("ActivateIntent error = %v, want activation blocked", err)
	}
	current, err := store.GetOperationalIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := store.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.IntentStatusAccepted ||
		publication.DesiredState != domain.OperationalIntentExternalStateAccepted ||
		publication.ConfirmedState != domain.OperationalIntentExternalStateAccepted {
		t.Fatalf("current=%+v publication=%+v", current, publication)
	}
}

func TestActivateIntentCompensatesDSSAfterAircraftActivationRace(t *testing.T) {
	ctx := context.Background()
	now := fixedWorkflowTime()
	store := durablememory.NewStore()
	coordinator := &durableWorkflowCoordinator{store: store, now: now}
	intents := NewIntentServiceWithClock(
		&activationRejectingStore{Store: store, err: durable.ErrActiveIntent},
		fixedClock(now),
		coordinator,
	)
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)
	var err error
	if intent, err = intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ReconcileIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPreflightCheck(ctx, domain.PreflightCheck{
		ID: "preflight", IntentID: intent.ID, IntentVersion: intent.Version,
		Status: domain.PreflightStatusClear, CapturedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := intents.ActivateIntent(ctx, intent.ID); !errors.Is(err, ErrActivationBlocked) {
		t.Fatalf("ActivateIntent error = %v, want activation blocked", err)
	}
	current, err := store.GetOperationalIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := store.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != domain.IntentStatusAccepted ||
		publication.DesiredState != domain.OperationalIntentExternalStateAccepted ||
		publication.ConfirmedState != domain.OperationalIntentExternalStateAccepted {
		t.Fatalf("current=%+v publication=%+v", current, publication)
	}
}

func TestActivationUsesExistingDSSActivationConfirmation(t *testing.T) {
	ctx := context.Background()
	now := fixedWorkflowTime()
	store := durablememory.NewStore()
	coordinator := &workflowCoordinator{}
	intents := NewIntentServiceWithClock(store, fixedClock(now), coordinator)
	intent, err := intents.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := intents.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatal(err)
	}
	intent, err = intents.AcceptIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.publication = domain.OperationalIntentPublication{
		IntentID: intent.ID, PublishedIntentVersion: intent.Version,
		ConfirmedState: domain.OperationalIntentExternalStateActivated,
		SyncStatus:     domain.PublicationSyncConfirmed,
	}
	if err := store.RecordPreflightCheck(ctx, domain.PreflightCheck{
		ID: "preflight", IntentID: intent.ID, IntentVersion: intent.Version,
		Status: domain.PreflightStatusClear, CapturedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	intent, err = intents.ActivateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Status != domain.IntentStatusActive || coordinator.reconciles != 0 {
		t.Fatalf("recovered intent=%+v coordinator=%+v", intent, coordinator)
	}
}

func TestIntentLifecycleHappyPath(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))

	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	preflight := preflightsvc.NewPreflightServiceWithClock(store, fixedClock(now))

	intent, err := intents.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if intent.Status != domain.IntentStatusDraft {
		t.Fatalf("created status = %q, want draft", intent.Status)
	}

	_, err = intents.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now))
	if err != nil {
		t.Fatalf("AddOperationalVolume returned error: %v", err)
	}
	if intent, err = intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatalf("SubmitIntent returned error: %v", err)
	}
	if intent.Status != domain.IntentStatusSubmitted {
		t.Fatalf("submitted status = %q, want submitted", intent.Status)
	}

	evaluation, err := preflight.EvaluateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	}
	if evaluation.Blocked {
		t.Fatalf("preflight blocked unexpectedly: %#v", evaluation.Findings)
	}

	if intent, err = intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}
	if intent, err = intents.ActivateIntent(ctx, intent.ID); err != nil {
		t.Fatalf("ActivateIntent returned error: %v", err)
	}
	if intent.Status != domain.IntentStatusActive {
		t.Fatalf("activated status = %q, want active", intent.Status)
	}
}

func TestActivationBlockedWhenNoOperationalVolumeExists(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))

	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := intents.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if _, err = intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatalf("SubmitIntent returned error: %v", err)
	}
	if _, err = intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}

	if _, err = intents.ActivateIntent(ctx, intent.ID); !errors.Is(err, ErrActivationBlocked) {
		t.Fatalf("ActivateIntent error = %v, want ErrActivationBlocked", err)
	}
}

func TestActivationBlockedWhenNoPreflightChecksExist(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))

	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := intents.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if _, err = intents.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now)); err != nil {
		t.Fatalf("AddOperationalVolume returned error: %v", err)
	}
	if _, err = intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatalf("SubmitIntent returned error: %v", err)
	}
	if _, err = intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}

	if _, err = intents.ActivateIntent(ctx, intent.ID); !errors.Is(err, ErrActivationBlocked) {
		t.Fatalf("ActivateIntent error = %v, want ErrActivationBlocked", err)
	}
}

func TestAddOperationalVolumeSucceedsForDraftIntent(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))

	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := intents.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}

	volume, err := intents.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now))
	if err != nil {
		t.Fatalf("AddOperationalVolume returned error: %v", err)
	}
	if volume.IntentID != intent.ID {
		t.Fatalf("volume intent ID = %q, want %q", volume.IntentID, intent.ID)
	}
}

func TestAddOperationalVolumeFailsAfterSubmit(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))

	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := intents.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if _, err = intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatalf("SubmitIntent returned error: %v", err)
	}

	if _, err = intents.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AddOperationalVolume error = %v, want ErrInvalidTransition", err)
	}
}

func TestAddOperationalVolumeFailsAfterAccept(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))

	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)
	if _, err := intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}

	if _, err := intents.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AddOperationalVolume error = %v, want ErrInvalidTransition", err)
	}
}

func TestAddOperationalVolumeFailsAfterActivate(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))

	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent := seedActiveIntentWithVolume(t, ctx, store, now)

	if _, err := intents.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AddOperationalVolume error = %v, want ErrInvalidTransition", err)
	}
}

func TestModifyDraftIntentReplacesEditableVersionVolumes(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))

	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := intents.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if _, err = intents.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now)); err != nil {
		t.Fatalf("AddOperationalVolume returned error: %v", err)
	}

	result, err := intents.ModifyIntent(ctx, intent.ID, ModifyIntentRequest{
		Reason:          "operator_adjustment",
		ExpectedVersion: 1,
		Intent: ModifyIntentFields{
			Name: stringPtr("Adjusted mission"),
		},
		Volumes: []AddOperationalVolumeRequest{{
			ID:           "volume-adjusted",
			Sequence:     1,
			GeoJSON:      eastSquareGeoJSON(),
			MinAltitudeM: float64Ptr(30.48),
			MaxAltitudeM: float64Ptr(76.2),
			AltitudeRef:  domain.AltitudeReferenceAGL,
			StartsAt:     now,
			EndsAt:       now.Add(time.Hour),
			VolumeType:   domain.OperationalVolumeLoiter,
		}},
	})
	if err != nil {
		t.Fatalf("ModifyIntent returned error: %v", err)
	}
	if result.Intent.Version != 1 || result.Intent.Status != domain.IntentStatusDraft {
		t.Fatalf("intent = version %d status %q, want v1 draft", result.Intent.Version, result.Intent.Status)
	}
	if result.Intent.Name != "Adjusted mission" {
		t.Fatalf("intent name = %q, want adjusted", result.Intent.Name)
	}
	volumes, err := store.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		t.Fatalf("ListOperationalVolumes returned error: %v", err)
	}
	current := volumesForVersion(volumes, 1)
	if len(current) != 1 || current[0].ID != "volume-adjusted" {
		t.Fatalf("current volumes = %#v, want only adjusted volume", current)
	}
}

func TestModifySubmittedIntentRequiresFreshPreflightForReplacementVolumes(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)
	if evaluation, err := preflightsvc.NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, intent.ID); err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	} else if evaluation.Blocked {
		t.Fatalf("preflight blocked unexpectedly: %#v", evaluation.Findings)
	}

	modifyAt := now.Add(10 * time.Minute)
	intents := NewIntentServiceWithClock(store, fixedClock(modifyAt), nil)
	if _, err := intents.ModifyIntent(ctx, intent.ID, ModifyIntentRequest{
		Reason:          "operator_adjustment",
		ExpectedVersion: 1,
		Intent: ModifyIntentFields{
			RouteSummary: stringPtr("Adjusted local operational volume"),
		},
		Volumes: []AddOperationalVolumeRequest{{
			ID:           "volume-adjusted",
			Sequence:     1,
			GeoJSON:      eastSquareGeoJSON(),
			MinAltitudeM: float64Ptr(30.48),
			MaxAltitudeM: float64Ptr(76.2),
			AltitudeRef:  domain.AltitudeReferenceAGL,
			StartsAt:     now,
			EndsAt:       now.Add(time.Hour),
			VolumeType:   domain.OperationalVolumeLoiter,
		}},
	}); err != nil {
		t.Fatalf("ModifyIntent returned error: %v", err)
	}
	if _, err := intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}
	if _, err := intents.ActivateIntent(ctx, intent.ID); !errors.Is(err, ErrActivationBlocked) {
		t.Fatalf("ActivateIntent stale preflight error = %v, want ErrActivationBlocked", err)
	}

	if evaluation, err := preflightsvc.NewPreflightServiceWithClock(store, fixedClock(modifyAt.Add(time.Minute))).EvaluateIntent(ctx, intent.ID); err != nil {
		t.Fatalf("EvaluateIntent after modify returned error: %v", err)
	} else if evaluation.Blocked {
		t.Fatalf("preflight after modify blocked unexpectedly: %#v", evaluation.Findings)
	}
	activated, err := intents.ActivateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("ActivateIntent after fresh preflight returned error: %v", err)
	}
	if activated.Status != domain.IntentStatusActive {
		t.Fatalf("status = %q, want active", activated.Status)
	}
}

func TestModifyAcceptedIntentCreatesDraftNextVersion(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))

	coordinator := &durableWorkflowCoordinator{store: store, now: now}
	intents := NewIntentServiceWithClock(store, fixedClock(now), coordinator)
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)
	intent, err := intents.AcceptIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}

	result, err := intents.ModifyIntent(ctx, intent.ID, ModifyIntentRequest{
		Reason:          "operator_adjustment",
		ExpectedVersion: 1,
		Intent: ModifyIntentFields{
			Summary: stringPtr("Adjusted inspection area"),
		},
		Volumes: []AddOperationalVolumeRequest{{
			ID:           "volume-v2",
			Sequence:     1,
			GeoJSON:      eastSquareGeoJSON(),
			MinAltitudeM: float64Ptr(30.48),
			MaxAltitudeM: float64Ptr(76.2),
			AltitudeRef:  domain.AltitudeReferenceAGL,
			StartsAt:     now,
			EndsAt:       now.Add(time.Hour),
			VolumeType:   domain.OperationalVolumeLoiter,
		}},
	})
	if err != nil {
		t.Fatalf("ModifyIntent returned error: %v", err)
	}
	if result.Intent.Version != 2 || result.Intent.Status != domain.IntentStatusDraft {
		t.Fatalf("intent = version %d status %q, want v2 draft", result.Intent.Version, result.Intent.Status)
	}
	if result.SupersedesIntentID != intent.ID || result.SupersedesVersion != 1 {
		t.Fatalf("supersedes = %q/%d, want %q/1", result.SupersedesIntentID, result.SupersedesVersion, intent.ID)
	}
	publication, err := store.GetOperationalIntentPublication(ctx, intent.ID)
	if err != nil {
		t.Fatalf("GetOperationalIntentPublication returned error: %v", err)
	}
	if publication.DesiredState != domain.OperationalIntentExternalStateAccepted || publication.DesiredIntentVersion != 1 {
		t.Fatalf("publication = %#v, want accepted v1 preserved", publication)
	}
	v1, err := store.GetOperationalIntentVersion(ctx, intent.ID, 1)
	if err != nil {
		t.Fatalf("GetOperationalIntentVersion v1 returned error: %v", err)
	}
	if v1.Status != domain.IntentStatusAccepted {
		t.Fatalf("v1 status = %q, want accepted", v1.Status)
	}
	latest, err := store.GetOperationalIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("GetOperationalIntent returned error: %v", err)
	}
	if latest.Version != 2 || latest.Status != domain.IntentStatusDraft {
		t.Fatalf("latest = v%d %q, want v2 draft", latest.Version, latest.Status)
	}
	versions, err := store.ListOperationalIntentVersions(ctx, intent.ID)
	if err != nil {
		t.Fatalf("ListOperationalIntentVersions returned error: %v", err)
	}
	if len(versions) != 2 || versions[0].Version != 1 || versions[1].Version != 2 {
		t.Fatalf("versions = %#v, want v1 and v2 history", versions)
	}
	volumes, err := store.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		t.Fatalf("ListOperationalVolumes returned error: %v", err)
	}
	if len(volumesForVersion(volumes, 1)) != 1 || len(volumesForVersion(volumes, 2)) != 1 {
		t.Fatalf("volumes by version = %#v, want v1 preserved and v2 created", volumes)
	}
}

func TestAcceptReplacementSupersedesPriorAcceptedVersion(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))

	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)
	intent, err := intents.AcceptIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("AcceptIntent v1 returned error: %v", err)
	}
	result, err := intents.ModifyIntent(ctx, intent.ID, ModifyIntentRequest{
		Reason:          "operator_adjustment",
		ExpectedVersion: intent.Version,
		Volumes: []AddOperationalVolumeRequest{{
			ID:           "volume-v2",
			Sequence:     1,
			GeoJSON:      eastSquareGeoJSON(),
			MinAltitudeM: float64Ptr(30.48),
			MaxAltitudeM: float64Ptr(76.2),
			AltitudeRef:  domain.AltitudeReferenceAGL,
			StartsAt:     now,
			EndsAt:       now.Add(time.Hour),
			VolumeType:   domain.OperationalVolumeLoiter,
		}},
	})
	if err != nil {
		t.Fatalf("ModifyIntent returned error: %v", err)
	}
	if _, err := intents.SubmitIntent(ctx, result.Intent.ID); err != nil {
		t.Fatalf("SubmitIntent v2 returned error: %v", err)
	}

	acceptedAt := now.Add(10 * time.Minute)
	intents = NewIntentServiceWithClock(store, fixedClock(acceptedAt), nil)
	accepted, err := intents.AcceptIntent(ctx, result.Intent.ID)
	if err != nil {
		t.Fatalf("AcceptIntent v2 returned error: %v", err)
	}
	if accepted.Version != 2 || accepted.Status != domain.IntentStatusAccepted {
		t.Fatalf("accepted replacement = %#v, want accepted v2", accepted)
	}
	prior, err := store.GetOperationalIntentVersion(ctx, intent.ID, 1)
	if err != nil {
		t.Fatalf("GetOperationalIntentVersion v1 returned error: %v", err)
	}
	if prior.Status != domain.IntentStatusSuperseded {
		t.Fatalf("v1 status = %q, want superseded", prior.Status)
	}
	if prior.SupersededAt == nil || !prior.SupersededAt.Equal(acceptedAt) {
		t.Fatalf("v1 superseded_at = %v, want %v", prior.SupersededAt, acceptedAt)
	}
}

func TestModifyActiveIntentBlocked(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	intent := seedActiveIntentWithVolume(t, ctx, store, now)

	_, err := NewIntentServiceWithClock(store, fixedClock(now), nil).ModifyIntent(ctx, intent.ID, ModifyIntentRequest{
		Reason:          "operator_adjustment",
		ExpectedVersion: intent.Version,
		Intent: ModifyIntentFields{
			Summary: stringPtr("blocked active adjustment"),
		},
	})
	var activeErr ActiveIntentModificationError
	if !errors.As(err, &activeErr) {
		t.Fatalf("ModifyIntent error = %v, want ActiveIntentModificationError", err)
	}
	if activeErr.IntentID != intent.ID || activeErr.Status != domain.IntentStatusActive || activeErr.Version != intent.Version {
		t.Fatalf("active error = %#v, want active intent identity", activeErr)
	}
}

func TestPreflightBlockedWhenBatterySOHMissing(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, nil)
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)

	evaluation, err := preflightsvc.NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	}
	if !evaluation.Blocked {
		t.Fatal("preflight should be blocked")
	}
	if !hasFinding(evaluation.Findings, "BATTERY-SOH-KNOWN") {
		t.Fatalf("findings = %#v, want BATTERY-SOH-KNOWN", evaluation.Findings)
	}
}

func TestPreflightClearOverwritesStaleBlockingFinding(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, nil)
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)
	preflight := preflightsvc.NewPreflightServiceWithClock(store, fixedClock(now))

	evaluation, err := preflight.EvaluateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	}
	if !evaluation.Blocked || !hasFinding(evaluation.Findings, "BATTERY-SOH-KNOWN") {
		t.Fatalf("initial evaluation = %#v, want blocked battery SOH finding", evaluation)
	}

	must(t, store.CreateBattery(ctx, domain.Battery{
		ID:            "battery-1",
		OperatorID:    "operator-1",
		SerialNumber:  "B101",
		StateOfHealth: float64Ptr(95),
		Status:        domain.MaintenanceStatusCurrent,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))
	evaluation, err = preflight.EvaluateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("EvaluateIntent after remediation returned error: %v", err)
	}
	if evaluation.Blocked {
		t.Fatalf("remediated evaluation blocked unexpectedly: %#v", evaluation.Findings)
	}
	findings, err := store.ListComplianceFindingsForIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("ListComplianceFindingsForIntent returned error: %v", err)
	}
	if hasBlockingFinding(findings, "BATTERY-SOH-KNOWN") {
		t.Fatalf("stale blocking battery SOH finding remained: %#v", findings)
	}

	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	if _, err = intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}
	if _, err = intents.ActivateIntent(ctx, intent.ID); err != nil {
		t.Fatalf("ActivateIntent after remediation returned error: %v", err)
	}
}

func TestPreflightBlockedWhenCriticalMaintenanceOpen(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	must(t, store.RecordMaintenanceEvent(ctx, domain.MaintenanceEvent{
		ID:         "mx-critical",
		AircraftID: "aircraft-1",
		Severity:   domain.SeverityCritical,
		Status:     domain.MaintenanceStatusOpen,
		Title:      "critical item",
		OpenedAt:   now.Add(-time.Hour),
	}))
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)

	evaluation, err := preflightsvc.NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	}
	if !evaluation.Blocked {
		t.Fatal("preflight should be blocked")
	}
	if !hasFinding(evaluation.Findings, "MX-CRITICAL-OPEN") {
		t.Fatalf("findings = %#v, want MX-CRITICAL-OPEN", evaluation.Findings)
	}
}

func TestPreflightBlockedWhenOperationalVolumeMissingInlineGeoJSON(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	intent := seedSubmittedIntentWithVolumeRequest(t, ctx, store, now, AddOperationalVolumeRequest{
		ID:           "volume-uri-only",
		Sequence:     1,
		GeometryURI:  "s3://demo/volume.geojson",
		MinAltitudeM: float64Ptr(10),
		MaxAltitudeM: float64Ptr(120),
		AltitudeRef:  domain.AltitudeReferenceAGL,
		StartsAt:     now,
		EndsAt:       now.Add(time.Hour),
		VolumeType:   domain.OperationalVolumeLoiter,
	})

	evaluation, err := preflightsvc.NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	}
	if !evaluation.Blocked {
		t.Fatal("preflight should block URI-only geometry")
	}
	if !hasFinding(evaluation.Findings, "VOLUME-GEOJSON") {
		t.Fatalf("findings = %#v, want VOLUME-GEOJSON", evaluation.Findings)
	}
}

func TestPreflightIgnoresOperationalVolumesFromOldIntentVersion(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID:                  "intent-versioned-preflight",
		OperatorID:          "operator-1",
		AircraftID:          "aircraft-1",
		Version:             2,
		Name:                "versioned preflight",
		Summary:             "preflight should only use current volume version",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		Status:              domain.IntentStatusSubmitted,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
		UpdatedAt:           now,
	}))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-old-invalid",
		OperatorID:    "operator-1",
		IntentID:      "intent-versioned-preflight",
		IntentVersion: 1,
		Sequence:      1,
		MinAltitudeM:  120,
		MaxAltitudeM:  10,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now.Add(2 * time.Hour),
		EndsAt:        now.Add(time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-current-valid",
		OperatorID:    "operator-1",
		IntentID:      "intent-versioned-preflight",
		IntentVersion: 2,
		Sequence:      1,
		GeoJSON:       squareGeoJSON(),
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))

	evaluation, err := preflightsvc.NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, "intent-versioned-preflight")
	if err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	}
	if evaluation.Blocked {
		t.Fatalf("preflight blocked on stale volume unexpectedly: %#v", evaluation.Findings)
	}
	for _, check := range evaluation.Checks {
		if check.IntentVersion != 2 {
			t.Fatalf("check version = %d, want current version 2: %#v", check.IntentVersion, check)
		}
	}
}

func TestConformanceTelemetryInsideVolumeConforming(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	intent := seedActiveIntentWithVolume(t, ctx, store, now)

	evaluation, err := NewConformanceServiceWithClock(store, telemetry, fixedClock(now)).EvaluateTelemetry(ctx, domain.TelemetrySample{
		ID:         "sample-inside",
		AircraftID: "aircraft-1",
		RecordedAt: now.Add(30 * time.Minute),
		Latitude:   35.5,
		Longitude:  -97.5,
		AltitudeM:  60,
	})
	if err != nil {
		t.Fatalf("EvaluateTelemetry returned error: %v", err)
	}
	if evaluation.Intent.ID != intent.ID {
		t.Fatalf("intent = %q, want %q", evaluation.Intent.ID, intent.ID)
	}
	if evaluation.Summary.Status != domain.ConformanceStatusConforming {
		t.Fatalf("summary status = %q, want conforming", evaluation.Summary.Status)
	}
	if len(evaluation.Events) != 0 {
		t.Fatalf("events = %#v, want none", evaluation.Events)
	}
	events, err := store.ListConformanceEvents(ctx, "")
	if err != nil {
		t.Fatalf("ListConformanceEvents returned error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("stored events = %#v, want none", events)
	}
}

func TestConformanceTelemetryIgnoresOperationalVolumesFromOldIntentVersion(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID:                  "intent-versioned-conformance",
		OperatorID:          "operator-1",
		AircraftID:          "aircraft-1",
		Version:             2,
		Name:                "versioned conformance",
		Summary:             "conformance should only use current volume version",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		Status:              domain.IntentStatusActive,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
		UpdatedAt:           now,
	}))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-old-matching",
		OperatorID:    "operator-1",
		IntentID:      "intent-versioned-conformance",
		IntentVersion: 1,
		Sequence:      1,
		GeoJSON:       squareGeoJSON(),
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-current-east",
		OperatorID:    "operator-1",
		IntentID:      "intent-versioned-conformance",
		IntentVersion: 2,
		Sequence:      1,
		GeoJSON:       eastSquareGeoJSON(),
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))

	evaluation, err := NewConformanceServiceWithClock(store, telemetry, fixedClock(now)).EvaluateTelemetry(ctx, domain.TelemetrySample{
		ID:         "sample-in-old-volume-only",
		IntentID:   "intent-versioned-conformance",
		AircraftID: "aircraft-1",
		RecordedAt: now.Add(30 * time.Minute),
		Latitude:   35.5,
		Longitude:  -97.5,
		AltitudeM:  60,
	})
	if err != nil {
		t.Fatalf("EvaluateTelemetry returned error: %v", err)
	}
	if evaluation.Summary.Status != domain.ConformanceStatusNonConforming {
		t.Fatalf("summary status = %q, want non_conforming from current v2 volume", evaluation.Summary.Status)
	}
	if len(evaluation.Events) != 1 || evaluation.Events[0].ExpectedVolumeID != "volume-current-east" {
		t.Fatalf("events = %#v, want intent_exit against current v2 volume", evaluation.Events)
	}
}

func TestConformanceTelemetryHonorsSampleIntentID(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	prior := createActiveIntentWithVolume(t, ctx, store, now, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "volume-a", squareGeoJSON(), now)
	if _, err := NewIntentServiceWithClock(store, fixedClock(now), nil).CompleteIntent(ctx, prior.ID); err != nil {
		t.Fatalf("CompleteIntent returned error: %v", err)
	}
	createActiveIntentWithVolume(t, ctx, store, now, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "volume-b", eastSquareGeoJSON(), now.Add(10*time.Minute))

	evaluation, err := NewConformanceServiceWithClock(store, telemetry, fixedClock(now)).EvaluateTelemetry(ctx, domain.TelemetrySample{
		ID:         "sample-intent-b",
		IntentID:   "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		AircraftID: "aircraft-1",
		RecordedAt: now.Add(30 * time.Minute),
		Latitude:   35.5,
		Longitude:  -96.25,
		AltitudeM:  60,
	})
	if err != nil {
		t.Fatalf("EvaluateTelemetry returned error: %v", err)
	}
	if evaluation.Intent.ID != "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" {
		t.Fatalf("evaluated intent = %q, want intent-b", evaluation.Intent.ID)
	}
	if evaluation.Summary.Status != domain.ConformanceStatusConforming {
		t.Fatalf("summary status = %q, want conforming", evaluation.Summary.Status)
	}
}

func TestConformanceTelemetryIntentIDWrongAircraftFails(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	createActiveIntentWithVolume(t, ctx, store, now, "11111111-1111-4111-8111-111111111111", "volume-1", squareGeoJSON(), now)

	_, err := NewConformanceServiceWithClock(store, telemetry, fixedClock(now)).EvaluateTelemetry(ctx, domain.TelemetrySample{
		ID:         "sample-wrong-aircraft",
		IntentID:   "11111111-1111-4111-8111-111111111111",
		AircraftID: "aircraft-2",
		RecordedAt: now.Add(30 * time.Minute),
		Latitude:   35.5,
		Longitude:  -97.5,
		AltitudeM:  60,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("EvaluateTelemetry error = %v, want ErrValidation", err)
	}
}

func TestConformanceTelemetryActiveIntentWithoutVolumesProducesUnknownNoEvent(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID:             "intent-no-volumes",
		OperatorID:     "operator-1",
		AircraftID:     "aircraft-1",
		Version:        1,
		Status:         domain.IntentStatusActive,
		PlannedStartAt: now,
		PlannedEndAt:   now.Add(time.Hour),
		UpdatedAt:      now,
	}))
	must(t, store.UpsertConformanceSummary(ctx, domain.ConformanceSummary{
		ID:                  "conformance-intent-no-volumes",
		OperatorID:          "operator-1",
		IntentID:            "intent-no-volumes",
		IntentVersion:       1,
		AircraftID:          "aircraft-1",
		Status:              domain.ConformanceStatusNonConforming,
		AlertCount:          2,
		ReportabilityStatus: domain.ReportabilityStatusReview,
		UpdatedAt:           now,
	}))

	evaluation, err := NewConformanceServiceWithClock(store, telemetry, fixedClock(now)).EvaluateTelemetry(ctx, domain.TelemetrySample{
		ID:         "sample-no-volumes",
		IntentID:   "intent-no-volumes",
		AircraftID: "aircraft-1",
		RecordedAt: now.Add(30 * time.Minute),
		Latitude:   35.5,
		Longitude:  -97.5,
		AltitudeM:  60,
	})
	if err != nil {
		t.Fatalf("EvaluateTelemetry returned error: %v", err)
	}
	if evaluation.Summary.Status != domain.ConformanceStatusUnknown {
		t.Fatalf("summary status = %q, want unknown", evaluation.Summary.Status)
	}
	if evaluation.Summary.AlertCount != 2 {
		t.Fatalf("alert count = %d, want preserved count 2", evaluation.Summary.AlertCount)
	}
	if evaluation.Summary.ReportabilityStatus != domain.ReportabilityStatusReview {
		t.Fatalf("reportability = %q, want review", evaluation.Summary.ReportabilityStatus)
	}
	if len(evaluation.Events) != 0 {
		t.Fatalf("events = %#v, want none", evaluation.Events)
	}
	events, err := store.ListConformanceEvents(ctx, "")
	if err != nil {
		t.Fatalf("ListConformanceEvents returned error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("stored events = %#v, want none", events)
	}
}

func TestConformanceTelemetryDoesNotCarryOldVersionSummaryState(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID:                  "intent-versioned-summary",
		OperatorID:          "operator-1",
		AircraftID:          "aircraft-1",
		Version:             2,
		Name:                "versioned summary",
		Summary:             "conformance summaries are version scoped",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		Status:              domain.IntentStatusActive,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
		UpdatedAt:           now,
	}))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-current",
		OperatorID:    "operator-1",
		IntentID:      "intent-versioned-summary",
		IntentVersion: 2,
		Sequence:      1,
		GeoJSON:       squareGeoJSON(),
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))
	must(t, store.UpsertConformanceSummary(ctx, domain.ConformanceSummary{
		ID:                  "conformance-intent-versioned-summary",
		OperatorID:          "operator-1",
		IntentID:            "intent-versioned-summary",
		IntentVersion:       1,
		AircraftID:          "aircraft-1",
		Status:              domain.ConformanceStatusNonConforming,
		AlertCount:          7,
		ReportabilityStatus: domain.ReportabilityStatusReview,
		UpdatedAt:           now.Add(-time.Hour),
	}))
	conformance := NewConformanceServiceWithClock(store, telemetry, fixedClock(now))
	before, err := conformance.GetIntentConformance(ctx, "intent-versioned-summary")
	if err != nil {
		t.Fatalf("GetIntentConformance returned error: %v", err)
	}
	if before.Summary.IntentVersion != 0 {
		t.Fatalf("summary before current evaluation = %#v, want no v2 summary", before.Summary)
	}

	evaluation, err := conformance.EvaluateTelemetry(ctx, domain.TelemetrySample{
		ID:         "sample-current-version-inside",
		IntentID:   "intent-versioned-summary",
		AircraftID: "aircraft-1",
		RecordedAt: now.Add(30 * time.Minute),
		Latitude:   35.5,
		Longitude:  -97.5,
		AltitudeM:  60,
	})
	if err != nil {
		t.Fatalf("EvaluateTelemetry returned error: %v", err)
	}
	if evaluation.Summary.IntentVersion != 2 {
		t.Fatalf("summary version = %d, want 2", evaluation.Summary.IntentVersion)
	}
	if evaluation.Summary.AlertCount != 0 {
		t.Fatalf("alert count = %d, want old v1 alerts ignored", evaluation.Summary.AlertCount)
	}
	if evaluation.Summary.ReportabilityStatus != domain.ReportabilityStatusNo {
		t.Fatalf("reportability = %q, want no old v1 review state", evaluation.Summary.ReportabilityStatus)
	}
}

func TestConformanceTelemetryRespectsPolygonInteriorRing(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	seedActiveIntentWithVolumeRequest(t, ctx, store, now, AddOperationalVolumeRequest{
		ID:           "volume-with-hole",
		Sequence:     1,
		GeoJSON:      polygonWithHoleGeoJSON(),
		MinAltitudeM: float64Ptr(10),
		MaxAltitudeM: float64Ptr(120),
		AltitudeRef:  domain.AltitudeReferenceAGL,
		StartsAt:     now,
		EndsAt:       now.Add(time.Hour),
		VolumeType:   domain.OperationalVolumeLoiter,
	})
	conformance := NewConformanceServiceWithClock(store, telemetry, fixedClock(now))

	inHole, err := conformance.EvaluateTelemetry(ctx, domain.TelemetrySample{
		ID:         "sample-in-hole",
		AircraftID: "aircraft-1",
		RecordedAt: now.Add(30 * time.Minute),
		Latitude:   35.5,
		Longitude:  -97.5,
		AltitudeM:  60,
	})
	if err != nil {
		t.Fatalf("EvaluateTelemetry in hole returned error: %v", err)
	}
	if inHole.Summary.Status != domain.ConformanceStatusNonConforming {
		t.Fatalf("hole sample status = %q, want non_conforming", inHole.Summary.Status)
	}
	if len(inHole.Events) != 1 || inHole.Events[0].EventCode != domain.ConformanceEventIntentExit {
		t.Fatalf("hole events = %#v, want one intent_exit", inHole.Events)
	}

	inExterior, err := conformance.EvaluateTelemetry(ctx, domain.TelemetrySample{
		ID:         "sample-in-exterior",
		AircraftID: "aircraft-1",
		RecordedAt: now.Add(31 * time.Minute),
		Latitude:   35.1,
		Longitude:  -97.9,
		AltitudeM:  60,
	})
	if err != nil {
		t.Fatalf("EvaluateTelemetry in exterior returned error: %v", err)
	}
	if inExterior.Summary.Status != domain.ConformanceStatusConforming {
		t.Fatalf("exterior sample status = %q, want conforming", inExterior.Summary.Status)
	}
	if len(inExterior.Events) != 0 {
		t.Fatalf("exterior events = %#v, want none", inExterior.Events)
	}
}

func TestConformanceTelemetryOutsideVolumeCreatesIntentExit(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	seedActiveIntentWithVolume(t, ctx, store, now)

	evaluation, err := NewConformanceServiceWithClock(store, telemetry, fixedClock(now)).EvaluateTelemetry(ctx, domain.TelemetrySample{
		ID:         "sample-outside",
		AircraftID: "aircraft-1",
		RecordedAt: now.Add(30 * time.Minute),
		Latitude:   36.5,
		Longitude:  -98.5,
		AltitudeM:  60,
	})
	if err != nil {
		t.Fatalf("EvaluateTelemetry returned error: %v", err)
	}
	if evaluation.Summary.Status != domain.ConformanceStatusNonConforming {
		t.Fatalf("summary status = %q, want non_conforming", evaluation.Summary.Status)
	}
	if len(evaluation.Events) != 1 {
		t.Fatalf("events len = %d, want 1", len(evaluation.Events))
	}
	if evaluation.Events[0].EventCode != domain.ConformanceEventIntentExit {
		t.Fatalf("event code = %q, want intent_exit", evaluation.Events[0].EventCode)
	}
}

func TestConformanceTelemetryDuplicateOutsideSampleDoesNotDoubleCountAlert(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	intent := seedActiveIntentWithVolume(t, ctx, store, now)
	conformance := NewConformanceServiceWithClock(store, telemetry, fixedClock(now))
	sample := domain.TelemetrySample{
		ID:         "sample-outside-retry",
		AircraftID: "aircraft-1",
		RecordedAt: now.Add(30 * time.Minute),
		Latitude:   36.5,
		Longitude:  -98.5,
		AltitudeM:  60,
	}

	first, err := conformance.EvaluateTelemetry(ctx, sample)
	if err != nil {
		t.Fatalf("first EvaluateTelemetry returned error: %v", err)
	}
	second, err := conformance.EvaluateTelemetry(ctx, sample)
	if err != nil {
		t.Fatalf("second EvaluateTelemetry returned error: %v", err)
	}
	if first.Summary.AlertCount != 1 {
		t.Fatalf("first alert count = %d, want 1", first.Summary.AlertCount)
	}
	if second.Summary.AlertCount != 1 {
		t.Fatalf("second alert count = %d, want 1", second.Summary.AlertCount)
	}
	events, err := store.ListConformanceEvents(ctx, "")
	if err != nil {
		t.Fatalf("ListConformanceEvents returned error: %v", err)
	}
	matching := 0
	for _, event := range events {
		if event.IntentID == intent.ID && event.IntentVersion == intent.Version {
			matching++
		}
	}
	if matching != 1 {
		t.Fatalf("matching conformance events = %d, want 1; events=%#v", matching, events)
	}
}

func TestConformanceTelemetryInsideAfterExitPreservesPriorAlerts(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	seedActiveIntentWithVolume(t, ctx, store, now)
	conformance := NewConformanceServiceWithClock(store, telemetry, fixedClock(now))

	if _, err := conformance.EvaluateTelemetry(ctx, domain.TelemetrySample{
		ID:         "sample-outside",
		AircraftID: "aircraft-1",
		RecordedAt: now.Add(30 * time.Minute),
		Latitude:   36.5,
		Longitude:  -98.5,
		AltitudeM:  60,
	}); err != nil {
		t.Fatalf("outside EvaluateTelemetry returned error: %v", err)
	}

	evaluation, err := conformance.EvaluateTelemetry(ctx, domain.TelemetrySample{
		ID:         "sample-inside-after-exit",
		AircraftID: "aircraft-1",
		RecordedAt: now.Add(31 * time.Minute),
		Latitude:   35.5,
		Longitude:  -97.5,
		AltitudeM:  60,
	})
	if err != nil {
		t.Fatalf("inside EvaluateTelemetry returned error: %v", err)
	}
	if evaluation.Summary.Status != domain.ConformanceStatusConforming {
		t.Fatalf("summary status = %q, want conforming", evaluation.Summary.Status)
	}
	if evaluation.Summary.AlertCount != 1 {
		t.Fatalf("alert count = %d, want 1", evaluation.Summary.AlertCount)
	}
	if evaluation.Summary.ReportabilityStatus != domain.ReportabilityStatusReview {
		t.Fatalf("reportability = %q, want review", evaluation.Summary.ReportabilityStatus)
	}
}

func TestActivationReadinessIgnoresPreflightAndFindingsFromOldIntentVersion(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID:                  "intent-versioned-activation",
		OperatorID:          "operator-1",
		AircraftID:          "aircraft-1",
		Version:             2,
		Name:                "versioned activation",
		Summary:             "activation should ignore stale old-version blockers",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		Status:              domain.IntentStatusAccepted,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
		UpdatedAt:           now,
	}))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-current",
		OperatorID:    "operator-1",
		IntentID:      "intent-versioned-activation",
		IntentVersion: 2,
		Sequence:      1,
		GeoJSON:       squareGeoJSON(),
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))
	must(t, store.RecordPreflightCheck(ctx, domain.PreflightCheck{
		ID:              "preflight-intent-versioned-activation-v1-stale",
		OperatorID:      "operator-1",
		IntentID:        "intent-versioned-activation",
		IntentVersion:   1,
		AircraftID:      "aircraft-1",
		Category:        domain.PreflightCheckBattery,
		Source:          "test",
		Status:          domain.PreflightStatusBlocked,
		Summary:         "old version block",
		RequirementCode: "OLD-BLOCK",
		RuleVersion:     "test.v1",
		Blocking:        true,
		CapturedAt:      now,
	}))
	must(t, store.RecordComplianceFinding(ctx, domain.ComplianceFinding{
		ID:              "finding-intent-versioned-activation-v1-stale",
		OperatorID:      "operator-1",
		IntentID:        "intent-versioned-activation",
		IntentVersion:   1,
		SubjectType:     "operational_intent",
		SubjectID:       "intent-versioned-activation",
		RequirementCode: "OLD-BLOCK",
		Status:          domain.ComplianceFindingFail,
		Severity:        domain.SeverityCritical,
		Blocking:        true,
		RuleVersion:     "test.v1",
		Message:         "old version block",
		EvaluatedAt:     now,
	}))
	evaluation, err := preflightsvc.NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, "intent-versioned-activation")
	if err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	}
	if evaluation.Blocked {
		t.Fatalf("current preflight blocked unexpectedly: %#v", evaluation.Findings)
	}

	intent, err := NewIntentServiceWithClock(store, fixedClock(now), nil).ActivateIntent(ctx, "intent-versioned-activation")
	if err != nil {
		t.Fatalf("ActivateIntent returned error with only stale old-version blockers: %v", err)
	}
	if intent.Status != domain.IntentStatusActive {
		t.Fatalf("status = %q, want active", intent.Status)
	}
}

func fixedWorkflowTime() time.Time {
	return time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
}

func fixedClock(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

func seedWorkflowAircraft(t *testing.T, ctx context.Context, store durable.Store, now time.Time, soh *float64) {
	t.Helper()
	must(t, store.CreateAircraft(ctx, domain.Aircraft{
		ID:               "aircraft-1",
		OperatorID:       "operator-1",
		TailNumber:       "N101AA",
		Status:           domain.AircraftStatusActive,
		AcceptanceStatus: domain.AcceptanceStatusAccepted,
		RemoteIDStatus:   domain.RemoteIDStatusBroadcasting,
		CreatedAt:        now,
		UpdatedAt:        now,
	}))
	must(t, store.CreateBattery(ctx, domain.Battery{
		ID:            "battery-1",
		OperatorID:    "operator-1",
		SerialNumber:  "B101",
		StateOfHealth: soh,
		Status:        domain.MaintenanceStatusCurrent,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))
	must(t, store.RecordBatteryInstallation(ctx, domain.BatteryInstallation{
		ID:          "install-1",
		OperatorID:  "operator-1",
		AircraftID:  "aircraft-1",
		BatteryID:   "battery-1",
		InstalledAt: now.Add(-24 * time.Hour),
	}))
}

func seedSubmittedIntentWithVolume(t *testing.T, ctx context.Context, store durable.Store, now time.Time) domain.OperationalIntent {
	t.Helper()
	return seedSubmittedIntentWithVolumeRequest(t, ctx, store, now, workflowVolumeRequest(now))
}

func seedSubmittedIntentWithVolumeRequest(t *testing.T, ctx context.Context, store durable.Store, now time.Time, volumeReq AddOperationalVolumeRequest) domain.OperationalIntent {
	t.Helper()
	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := intents.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if _, err = intents.AddOperationalVolume(ctx, intent.ID, volumeReq); err != nil {
		t.Fatalf("AddOperationalVolume returned error: %v", err)
	}
	intent, err = intents.SubmitIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("SubmitIntent returned error: %v", err)
	}
	return intent
}

func seedActiveIntentWithVolume(t *testing.T, ctx context.Context, store durable.Store, now time.Time) domain.OperationalIntent {
	t.Helper()
	return seedActiveIntentWithVolumeRequest(t, ctx, store, now, workflowVolumeRequest(now))
}

func seedActiveIntentWithVolumeRequest(t *testing.T, ctx context.Context, store durable.Store, now time.Time, volumeReq AddOperationalVolumeRequest) domain.OperationalIntent {
	t.Helper()
	intent := seedSubmittedIntentWithVolumeRequest(t, ctx, store, now, volumeReq)
	if evaluation, err := preflightsvc.NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, intent.ID); err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	} else if evaluation.Blocked {
		t.Fatalf("preflight blocked unexpectedly: %#v", evaluation.Findings)
	}
	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := intents.AcceptIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}
	intent, err = intents.ActivateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("ActivateIntent returned error: %v", err)
	}
	return intent
}

func createActiveIntentWithVolume(t *testing.T, ctx context.Context, store durable.Store, now time.Time, intentID string, volumeID string, geoJSON string, plannedStart time.Time) domain.OperationalIntent {
	t.Helper()
	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := intents.CreateIntent(ctx, CreateIntentRequest{
		ID:                  intentID,
		OperatorID:          "operator-1",
		AircraftID:          "aircraft-1",
		Name:                intentID,
		Summary:             "test intent",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		ConformanceRequired: true,
		PlannedStartAt:      plannedStart,
		PlannedEndAt:        plannedStart.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if _, err = intents.AddOperationalVolume(ctx, intent.ID, AddOperationalVolumeRequest{
		ID:           volumeID,
		Sequence:     1,
		GeoJSON:      geoJSON,
		MinAltitudeM: float64Ptr(10),
		MaxAltitudeM: float64Ptr(120),
		AltitudeRef:  domain.AltitudeReferenceAGL,
		StartsAt:     plannedStart,
		EndsAt:       plannedStart.Add(time.Hour),
		VolumeType:   domain.OperationalVolumeLoiter,
	}); err != nil {
		t.Fatalf("AddOperationalVolume returned error: %v", err)
	}
	if intent, err = intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatalf("SubmitIntent returned error: %v", err)
	}
	if evaluation, err := preflightsvc.NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, intent.ID); err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	} else if evaluation.Blocked {
		t.Fatalf("preflight blocked unexpectedly: %#v", evaluation.Findings)
	}
	if intent, err = intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}
	if intent, err = intents.ActivateIntent(ctx, intent.ID); err != nil {
		t.Fatalf("ActivateIntent returned error: %v", err)
	}
	return intent
}

func workflowIntentRequest(now time.Time) CreateIntentRequest {
	return CreateIntentRequest{
		ID:                  "11111111-1111-4111-8111-111111111111",
		OperatorID:          "operator-1",
		AircraftID:          "aircraft-1",
		Name:                "Demo intent",
		Summary:             "Manual operational volume test intent",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
	}
}

func workflowVolumeRequest(now time.Time) AddOperationalVolumeRequest {
	return AddOperationalVolumeRequest{
		ID:           "volume-1",
		Sequence:     1,
		GeoJSON:      squareGeoJSON(),
		MinAltitudeM: float64Ptr(10),
		MaxAltitudeM: float64Ptr(120),
		AltitudeRef:  domain.AltitudeReferenceAGL,
		StartsAt:     now,
		EndsAt:       now.Add(time.Hour),
		VolumeType:   domain.OperationalVolumeLoiter,
	}
}

func squareGeoJSON() string {
	return `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`
}

func polygonWithHoleGeoJSON() string {
	return `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]],[[-97.75,35.25],[-97.25,35.25],[-97.25,35.75],[-97.75,35.75],[-97.75,35.25]]]}`
}

func eastSquareGeoJSON() string {
	return `{"type":"Polygon","coordinates":[[[-96.5,35],[-96,35],[-96,36],[-96.5,36],[-96.5,35]]]}`
}

func stringPtr(value string) *string {
	return &value
}

func hasFinding(findings []domain.ComplianceFinding, requirementCode string) bool {
	for _, finding := range findings {
		if finding.RequirementCode == requirementCode {
			return true
		}
	}
	return false
}

func hasBlockingFinding(findings []domain.ComplianceFinding, requirementCode string) bool {
	for _, finding := range findings {
		if finding.RequirementCode == requirementCode && finding.Blocking && (finding.Status == domain.ComplianceFindingFail || finding.Status == domain.ComplianceFindingReview) {
			return true
		}
	}
	return false
}
