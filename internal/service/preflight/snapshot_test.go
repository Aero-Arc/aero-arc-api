package preflight

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestVolumesForVersionKeepsCurrentIntentVersion(t *testing.T) {
	volumes := []domain.OperationalVolume{
		{ID: "old", IntentVersion: 1},
		{ID: "current", IntentVersion: 2},
		{ID: "other", IntentVersion: 3},
	}
	got := volumesForVersion(volumes, 2)
	if len(got) != 1 || got[0].ID != "current" {
		t.Fatalf("volumesForVersion = %#v, want only current", got)
	}
}

func TestLoadSnapshotPreservesMissingAircraftAsCheckInput(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	if err := store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID:             "intent-1",
		AircraftID:     "missing-aircraft",
		Version:        2,
		PlannedStartAt: now,
		PlannedEndAt:   now.Add(time.Hour),
		UpdatedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-old",
		IntentID:      "intent-1",
		IntentVersion: 1,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-current",
		IntentID:      "intent-1",
		IntentVersion: 2,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}

	service := NewPreflightServiceWithClock(store, func() time.Time { return now })
	snapshot, err := service.loadSnapshot(ctx, "intent-1")
	if err != nil {
		t.Fatalf("loadSnapshot returned error: %v", err)
	}
	if snapshot.AircraftErr == nil || !errors.Is(snapshot.AircraftErr, durable.ErrNotFound) {
		t.Fatalf("AircraftErr = %v, want durable.ErrNotFound", snapshot.AircraftErr)
	}
	if len(snapshot.Volumes) != 1 || snapshot.Volumes[0].ID != "volume-current" {
		t.Fatalf("volumes = %#v, want current version only", snapshot.Volumes)
	}
	if !snapshot.Now.Equal(now) {
		t.Fatalf("Now = %v, want %v", snapshot.Now, now)
	}
}

func TestLoadSnapshotMissingIntentIsAnError(t *testing.T) {
	ctx := context.Background()
	service := NewPreflightService(durablememory.NewStore())
	_, err := service.loadSnapshot(ctx, "missing")
	if err == nil || !errors.Is(err, durable.ErrNotFound) {
		t.Fatalf("error = %v, want wrapped durable.ErrNotFound", err)
	}
}
