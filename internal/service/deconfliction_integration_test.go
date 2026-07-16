package service

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestIntentServiceActivateRunsDeconflictionChecker(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)
	must(t, store.CreateAircraft(ctx, domain.Aircraft{
		ID:               "aircraft-2",
		OperatorID:       "operator-1",
		Status:           domain.AircraftStatusActive,
		AcceptanceStatus: domain.AcceptanceStatusAccepted,
		RemoteIDStatus:   domain.RemoteIDStatusBroadcasting,
		CreatedAt:        now,
		UpdatedAt:        now,
	}))
	seedAcceptedPeerIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 10, 120, now)
	if evaluation, err := NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, intent.ID); err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	} else if evaluation.Blocked {
		t.Fatalf("preflight blocked unexpectedly: %#v", evaluation.Findings)
	}
	plainIntents := NewIntentServiceWithClock(store, fixedClock(now))
	intent, err := plainIntents.AcceptIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}

	guardedIntents := NewIntentServiceWithClock(store, fixedClock(now), deconfliction.NewDeconflictionServiceWithClock(store, fixedClock(now)))
	if _, err := guardedIntents.ActivateIntent(ctx, intent.ID); err == nil {
		t.Fatal("ActivateIntent returned nil, want deconfliction block")
	}
	findings, err := store.ListConflictFindings(ctx, intent.ID, intent.Version)
	if err != nil {
		t.Fatalf("ListConflictFindings returned error: %v", err)
	}
	if len(findings) != 1 || findings[0].Status != domain.ConflictFindingStatusPotentialConflict {
		t.Fatalf("findings = %#v, want stored potential conflict", findings)
	}
}

func TestIntentServiceDefaultConstructorRunsDeconflictionChecker(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)
	must(t, store.CreateAircraft(ctx, domain.Aircraft{
		ID:               "aircraft-2",
		OperatorID:       "operator-1",
		Status:           domain.AircraftStatusActive,
		AcceptanceStatus: domain.AcceptanceStatusAccepted,
		RemoteIDStatus:   domain.RemoteIDStatusBroadcasting,
		CreatedAt:        now,
		UpdatedAt:        now,
	}))
	seedAcceptedPeerIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 10, 120, now)
	if evaluation, err := NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, intent.ID); err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	} else if evaluation.Blocked {
		t.Fatalf("preflight blocked unexpectedly: %#v", evaluation.Findings)
	}
	intents := NewIntentServiceWithClock(store, fixedClock(now))
	intent, err := intents.AcceptIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}

	if _, err := intents.ActivateIntent(ctx, intent.ID); err == nil {
		t.Fatal("ActivateIntent returned nil, want default deconfliction block")
	}
	findings, err := store.ListConflictFindings(ctx, intent.ID, intent.Version)
	if err != nil {
		t.Fatalf("ListConflictFindings returned error: %v", err)
	}
	if len(findings) != 1 || findings[0].Status != domain.ConflictFindingStatusPotentialConflict {
		t.Fatalf("findings = %#v, want stored potential conflict", findings)
	}
}

func seedAcceptedPeerIntentWithVolume(t *testing.T, ctx context.Context, store *durablememory.Store, now time.Time, intentID, aircraftID, volumeID, geoJSON string, minAltitudeM, maxAltitudeM float64, startsAt time.Time) domain.OperationalIntent {
	t.Helper()
	intents := NewIntentServiceWithClock(store, fixedClock(now))
	intent, err := intents.CreateIntent(ctx, CreateIntentRequest{
		ID:                  intentID,
		OperatorID:          "operator-1",
		AircraftID:          aircraftID,
		Name:                intentID,
		Summary:             "accepted deconfliction peer",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		ConformanceRequired: true,
		PlannedStartAt:      startsAt,
		PlannedEndAt:        startsAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if _, err = intents.AddOperationalVolume(ctx, intent.ID, AddOperationalVolumeRequest{
		ID:           volumeID,
		Sequence:     1,
		GeoJSON:      geoJSON,
		MinAltitudeM: float64Ptr(minAltitudeM),
		MaxAltitudeM: float64Ptr(maxAltitudeM),
		AltitudeRef:  domain.AltitudeReferenceAGL,
		StartsAt:     startsAt,
		EndsAt:       startsAt.Add(time.Hour),
		VolumeType:   domain.OperationalVolumeLoiter,
	}); err != nil {
		t.Fatalf("AddOperationalVolume returned error: %v", err)
	}
	if intent, err = intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatalf("SubmitIntent returned error: %v", err)
	}
	if intent, err = intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}
	return intent
}
