package memory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func TestReplaceOperationalIntentIsAtomicAndVersionChecked(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	v1 := domain.OperationalIntent{ID: "intent", Version: 1, Status: domain.IntentStatusAccepted}
	if err := store.CreateOperationalIntent(ctx, v1); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperationalVolume(ctx, domain.OperationalVolume{ID: "old", IntentID: v1.ID, IntentVersion: 1}); err != nil {
		t.Fatal(err)
	}
	v2 := v1
	v2.Version = 2
	v2.Status = domain.IntentStatusDraft
	volumes := []domain.OperationalVolume{{ID: "new", IntentID: v2.ID, IntentVersion: 2}}
	if err := store.ReplaceOperationalIntent(ctx, 1, v1.Revision, v2, volumes); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetOperationalIntent(ctx, v2.ID)
	if err != nil || stored.Version != 2 {
		t.Fatalf("stored = %#v, error = %v", stored, err)
	}
	storedVolumes, err := store.ListOperationalVolumes(ctx, v2.ID)
	if err != nil || len(storedVolumes) != 2 {
		t.Fatalf("volumes = %#v, error = %v", storedVolumes, err)
	}
	if err := store.ReplaceOperationalIntent(ctx, 1, v1.Revision, v2, nil); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("version conflict error = %v", err)
	}
}

func TestOperationalIntentCreateAndUpdateConcurrency(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	intent := domain.OperationalIntent{ID: "intent", Version: 1, UpdatedAt: now}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOperationalIntent(ctx, intent); !errors.Is(err, durable.ErrAlreadyExists) {
		t.Fatalf("duplicate create error = %v, want ErrAlreadyExists", err)
	}

	first := intent
	first.Status = domain.IntentStatusSubmitted
	first.UpdatedAt = now.Add(time.Second)
	if err := store.UpdateOperationalIntent(ctx, first, 0); err != nil {
		t.Fatal(err)
	}
	second := intent
	second.Status = domain.IntentStatusCanceled
	second.UpdatedAt = now.Add(2 * time.Second)
	if err := store.UpdateOperationalIntent(ctx, second, 0); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("stale update error = %v, want ErrVersionConflict", err)
	}
}

func TestConcurrentAircraftActivationsAllowExactlyOneIntent(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	intents := []domain.OperationalIntent{
		{ID: "intent-a", Version: 1, AircraftID: "aircraft-1", Status: domain.IntentStatusAccepted, UpdatedAt: now},
		{ID: "intent-b", Version: 1, AircraftID: "aircraft-1", Status: domain.IntentStatusAccepted, UpdatedAt: now},
	}
	for _, intent := range intents {
		if err := store.CreateOperationalIntent(ctx, intent); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, len(intents))
	var ready sync.WaitGroup
	ready.Add(len(intents))
	for _, accepted := range intents {
		active := accepted
		active.Status = domain.IntentStatusActive
		go func() {
			ready.Done()
			<-start
			errs <- store.ActivateOperationalIntent(ctx, active, active.Revision)
		}()
	}
	ready.Wait()
	close(start)

	var activated, rejected int
	for range intents {
		switch err := <-errs; {
		case err == nil:
			activated++
		case errors.Is(err, durable.ErrActiveIntent):
			rejected++
		default:
			t.Fatalf("activation error = %v", err)
		}
	}
	if activated != 1 || rejected != 1 {
		t.Fatalf("activated = %d, rejected = %d", activated, rejected)
	}
	stored, err := store.ListOperationalIntents(ctx, "aircraft-1")
	if err != nil {
		t.Fatal(err)
	}
	activeCount := 0
	for _, intent := range stored {
		if intent.Status == domain.IntentStatusActive {
			activeCount++
		}
	}
	if activeCount != 1 {
		t.Fatalf("active intents = %d, want 1; intents=%#v", activeCount, stored)
	}
}

func TestUpdateOperationalIntentRejectsSupersededVersion(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	v1 := domain.OperationalIntent{ID: "intent", Version: 1, Status: domain.IntentStatusAccepted}
	if err := store.CreateOperationalIntent(ctx, v1); err != nil {
		t.Fatal(err)
	}
	v2 := v1
	v2.Version = 2
	v2.Status = domain.IntentStatusDraft
	if err := store.ReplaceOperationalIntent(ctx, v1.Version, v1.Revision, v2, nil); err != nil {
		t.Fatal(err)
	}

	stale := v1
	stale.Status = domain.IntentStatusActive
	if err := store.UpdateOperationalIntent(ctx, stale, v1.Revision); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("superseded update error = %v, want ErrVersionConflict", err)
	}
	stored, err := store.GetOperationalIntentVersion(ctx, v1.ID, v1.Version)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.IntentStatusAccepted || stored.Revision != 0 {
		t.Fatalf("superseded version changed: %#v", stored)
	}
}

func TestTerminalUpdateRetiresPriorAcceptedVersion(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	v1 := domain.OperationalIntent{ID: "intent", Version: 1, Status: domain.IntentStatusAccepted, UpdatedAt: now}
	if err := store.CreateOperationalIntent(ctx, v1); err != nil {
		t.Fatal(err)
	}
	v2 := v1
	v2.Version = 2
	v2.Status = domain.IntentStatusDraft
	if err := store.ReplaceOperationalIntent(ctx, 1, 0, v2, nil); err != nil {
		t.Fatal(err)
	}
	canceledAt := now.Add(time.Minute)
	v2.Status = domain.IntentStatusCanceled
	v2.CanceledAt = &canceledAt
	v2.UpdatedAt = canceledAt
	if err := store.UpdateOperationalIntent(ctx, v2, 0); err != nil {
		t.Fatal(err)
	}
	storedV1, err := store.GetOperationalIntentVersion(ctx, v1.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if storedV1.Status != domain.IntentStatusCanceled || storedV1.CanceledAt == nil || !storedV1.CanceledAt.Equal(canceledAt) {
		t.Fatalf("stored v1 = %#v, want canceled at %v", storedV1, canceledAt)
	}
}

func TestReplacementScopeIsValidatedBeforeMutation(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	original := domain.OperationalVolume{ID: "original", IntentID: "intent", IntentVersion: 1}
	if err := store.RecordOperationalVolume(ctx, original); err != nil {
		t.Fatal(err)
	}
	wrong := domain.OperationalVolume{ID: "wrong", IntentID: "another-intent", IntentVersion: 1}
	if err := store.ReplaceOperationalVolumes(ctx, "intent", 1, []domain.OperationalVolume{wrong}); err == nil {
		t.Fatal("expected replacement scope error")
	}
	volumes, err := store.ListOperationalVolumes(ctx, "intent")
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 1 || volumes[0].ID != original.ID {
		t.Fatalf("volumes after rejected replacement = %#v", volumes)
	}
}
