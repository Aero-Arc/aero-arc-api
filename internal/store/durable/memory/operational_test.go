package memory

import (
	"context"
	"errors"
	"testing"

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
	if err := store.ReplaceOperationalIntent(ctx, 1, v2, volumes); err != nil {
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
	if err := store.ReplaceOperationalIntent(ctx, 1, v2, nil); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("version conflict error = %v", err)
	}
}
