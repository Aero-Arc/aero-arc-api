package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func TestQueryConflictCandidatesFiltersByStatusAndTime(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	targetVolume := domain.OperationalVolume{
		ID:            "volume-target",
		IntentID:      "intent-target",
		IntentVersion: 1,
		Sequence:      1,
		GeoJSON:       squareGeoJSON(),
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
	}
	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: "intent-target", Version: 1, Status: domain.IntentStatusDraft, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}))
	must(t, store.RecordOperationalVolume(ctx, targetVolume))

	seedPeerIntent(t, ctx, store, now, "intent-accepted", domain.IntentStatusAccepted, squareGeoJSON(), 10, 120, now)
	seedPeerIntent(t, ctx, store, now, "intent-draft", domain.IntentStatusDraft, squareGeoJSON(), 10, 120, now)
	seedPeerIntent(t, ctx, store, now, "intent-later", domain.IntentStatusAccepted, squareGeoJSON(), 10, 120, now.Add(time.Hour))

	candidates, err := store.QueryConflictCandidates(ctx, durable.ConflictCandidateQuery{
		ExcludeIntentID: "intent-target",
		TargetVolumes:   []domain.OperationalVolume{targetVolume},
	})
	if err != nil {
		t.Fatalf("QueryConflictCandidates returned error: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Intent.ID != "intent-accepted" {
		t.Fatalf("candidates = %#v, want only overlapping accepted peer", candidates)
	}
}

func TestQueryConflictCandidatesScopesPeerVolumesByVersion(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	targetVolume := domain.OperationalVolume{
		ID: "volume-target", IntentID: "intent-target", IntentVersion: 1, Sequence: 1,
		GeoJSON: squareGeoJSON(), MinAltitudeM: 10, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceAGL,
		StartsAt: now, EndsAt: now.Add(time.Hour),
	}
	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: "intent-target", Version: 1, Status: domain.IntentStatusDraft, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}))
	must(t, store.RecordOperationalVolume(ctx, targetVolume))

	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: "intent-peer", Version: 1, Status: domain.IntentStatusAccepted, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID: "volume-peer-v1", IntentID: "intent-peer", IntentVersion: 1, Sequence: 1,
		GeoJSON: squareGeoJSON(), MinAltitudeM: 10, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceAGL,
		StartsAt: now, EndsAt: now.Add(time.Hour),
	}))
	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: "intent-peer", Version: 2, Status: domain.IntentStatusAccepted, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID: "volume-peer-v2", IntentID: "intent-peer", IntentVersion: 2, Sequence: 1,
		GeoJSON: squareGeoJSON(), MinAltitudeM: 10, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceAGL,
		StartsAt: now, EndsAt: now.Add(time.Hour),
	}))

	candidates, err := store.QueryConflictCandidates(ctx, durable.ConflictCandidateQuery{
		ExcludeIntentID: "intent-target",
		TargetVolumes:   []domain.OperationalVolume{targetVolume},
	})
	if err != nil {
		t.Fatalf("QueryConflictCandidates returned error: %v", err)
	}
	if len(candidates) != 1 || len(candidates[0].Volumes) != 1 || candidates[0].Volumes[0].ID != "volume-peer-v2" {
		t.Fatalf("candidates = %#v, want only current-version peer volume", candidates)
	}
}

func TestQueryConflictCandidatesKeepsUnusableDimensionVolumes(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	targetVolume := domain.OperationalVolume{
		ID: "volume-target", IntentID: "intent-target", IntentVersion: 1, Sequence: 1,
		GeoJSON: squareGeoJSON(), MinAltitudeM: 10, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceAGL,
		StartsAt: now, EndsAt: now.Add(time.Hour),
	}
	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: "intent-target", Version: 1, Status: domain.IntentStatusDraft, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}))
	must(t, store.RecordOperationalVolume(ctx, targetVolume))

	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: "intent-peer", Version: 1, Status: domain.IntentStatusAccepted, PlannedStartAt: now, PlannedEndAt: now.Add(3*time.Hour), UpdatedAt: now,
	}))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID: "volume-peer", IntentID: "intent-peer", IntentVersion: 1, Sequence: 1,
		GeoJSON: squareGeoJSON(), MinAltitudeM: 0, MaxAltitudeM: 0, AltitudeRef: domain.AltitudeReferenceAGL,
		StartsAt: now.Add(2 * time.Hour), EndsAt: now.Add(3 * time.Hour),
	}))

	candidates, err := store.QueryConflictCandidates(ctx, durable.ConflictCandidateQuery{
		ExcludeIntentID: "intent-target",
		TargetVolumes:   []domain.OperationalVolume{targetVolume},
	})
	if err != nil {
		t.Fatalf("QueryConflictCandidates returned error: %v", err)
	}
	if len(candidates) != 1 || len(candidates[0].Volumes) != 1 {
		t.Fatalf("candidates = %#v, want unusable-dimension peer volume kept", candidates)
	}
}

func seedPeerIntent(t *testing.T, ctx context.Context, store *Store, now time.Time, intentID string, status domain.IntentStatus, geoJSON string, minAltitudeM, maxAltitudeM float64, startsAt time.Time) {
	t.Helper()
	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: intentID, Version: 1, Status: status, PlannedStartAt: startsAt, PlannedEndAt: startsAt.Add(time.Hour), UpdatedAt: now,
	}))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID: "volume-" + intentID, IntentID: intentID, IntentVersion: 1, Sequence: 1,
		GeoJSON: geoJSON, MinAltitudeM: minAltitudeM, MaxAltitudeM: maxAltitudeM, AltitudeRef: domain.AltitudeReferenceAGL,
		StartsAt: startsAt, EndsAt: startsAt.Add(time.Hour),
	}))
}

func squareGeoJSON() string {
	return `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
