package memory

import (
	"context"
	"errors"
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
