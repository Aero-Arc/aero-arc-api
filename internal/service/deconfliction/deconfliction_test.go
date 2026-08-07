package deconfliction_test

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	. "github.com/Aero-Arc/aero-arc-api/internal/service"
	. "github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func fixedWorkflowTime() time.Time {
	return time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
}

func fixedClock(now time.Time) func() time.Time {
	return func() time.Time { return now }
}

func newTestDeconflictionService(t *testing.T, store durable.Store, now time.Time) *DeconflictionService {
	t.Helper()
	service, err := NewDeconflictionServiceWithClock(store, fixedClock(now), newTestLocalProvider(store))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func squareGeoJSON() string {
	return `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`
}

func eastSquareGeoJSON() string {
	return `{"type":"Polygon","coordinates":[[[-96.5,35],[-96,35],[-96,36],[-96.5,36],[-96.5,35]]]}`
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func float64Ptr(value float64) *float64 {
	return &value
}

func seedWorkflowAircraft(t *testing.T, ctx context.Context, store durable.Store, now time.Time, soh *float64) {
	t.Helper()
	must(t, store.CreateAircraft(ctx, domain.Aircraft{
		ID: "aircraft-1", OperatorID: "operator-1", Status: domain.AircraftStatusActive,
		AcceptanceStatus: domain.AcceptanceStatusAccepted, RemoteIDStatus: domain.RemoteIDStatusBroadcasting,
		CreatedAt: now, UpdatedAt: now,
	}))
	must(t, store.CreateBattery(ctx, domain.Battery{
		ID: "battery-1", OperatorID: "operator-1", SerialNumber: "B101",
		StateOfHealth: soh, Status: domain.MaintenanceStatusCurrent, CreatedAt: now, UpdatedAt: now,
	}))
	must(t, store.RecordBatteryInstallation(ctx, domain.BatteryInstallation{
		ID: "install-1", OperatorID: "operator-1", AircraftID: "aircraft-1",
		BatteryID: "battery-1", InstalledAt: now.Add(-24 * time.Hour),
	}))
}

func seedSubmittedIntentWithVolume(t *testing.T, ctx context.Context, store durable.Store, now time.Time) domain.OperationalIntent {
	t.Helper()
	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := intents.CreateIntent(ctx, CreateIntentRequest{
		ID: "intent-1", OperatorID: "operator-1", AircraftID: "aircraft-1",
		Name: "Demo intent", Summary: "deconfliction test intent",
		AuthorizationPath: domain.AuthorizationPathDemo, PopulationCategory: domain.PopulationCategoryOne,
		ConformanceRequired: true, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = intents.AddOperationalVolume(ctx, intent.ID, AddOperationalVolumeRequest{
		ID: "volume-1", Sequence: 1, GeoJSON: squareGeoJSON(),
		MinAltitudeM: float64Ptr(10), MaxAltitudeM: float64Ptr(120),
		AltitudeRef: domain.AltitudeReferenceAGL, StartsAt: now, EndsAt: now.Add(time.Hour),
		VolumeType: domain.OperationalVolumeLoiter,
	}); err != nil {
		t.Fatal(err)
	}
	intent, err = intents.SubmitIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func TestDeconflictionClearWhenNoLocalVolumeOverlap(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", eastSquareGeoJSON(), 10, 120, now)

	result, err := newTestDeconflictionService(t, store, now).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPostureClear {
		t.Fatalf("posture = %q, want clear; findings=%#v", result.Posture, result.Findings)
	}
	if len(result.Findings) != 1 || result.Findings[0].Status != domain.ConflictFindingStatusClear {
		t.Fatalf("findings = %#v, want one clear finding", result.Findings)
	}
}

func TestDeconflictionPotentialConflictWhenBBoxTimeAndAltitudeOverlap(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 20, 100, now)
	deconfliction := newTestDeconflictionService(t, store, now)

	first, err := deconfliction.CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("first CheckIntent returned error: %v", err)
	}
	second, err := deconfliction.CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("second CheckIntent returned error: %v", err)
	}
	if first.Posture != domain.DeconflictionPosturePotentialConflict || second.Posture != domain.DeconflictionPosturePotentialConflict {
		t.Fatalf("postures = %q/%q, want potential_conflict/potential_conflict", first.Posture, second.Posture)
	}
	stored, err := store.ListConflictFindings(ctx, target.ID, target.Version)
	if err != nil {
		t.Fatalf("ListConflictFindings returned error: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored findings = %d, want idempotent single finding: %#v", len(stored), stored)
	}
	if stored[0].Status != domain.ConflictFindingStatusPotentialConflict || !stored[0].Blocking {
		t.Fatalf("stored finding = %#v, want blocking potential conflict", stored[0])
	}
	if stored[0].ConflictingIntentID != "intent-peer" || stored[0].ConflictingVolumeID != "volume-peer" {
		t.Fatalf("stored finding conflict target = %#v, want peer/volume IDs", stored[0])
	}
	if stored[0].ConflictingBounds == nil {
		t.Fatalf("stored finding missing conflicting bounds: %#v", stored[0])
	}
	if stored[0].ConflictingBounds.MinLat != 35 || stored[0].ConflictingBounds.MinLon != -98 ||
		stored[0].ConflictingBounds.MaxLat != 36 || stored[0].ConflictingBounds.MaxLon != -97 {
		t.Fatalf("conflicting bounds = %#v, want peer bbox", stored[0].ConflictingBounds)
	}
	if stored[0].OwnBounds == nil {
		t.Fatalf("stored finding missing own bounds: %#v", stored[0])
	}
	if stored[0].OwnBounds.MinLat != 35 || stored[0].OwnBounds.MinLon != -98 ||
		stored[0].OwnBounds.MaxLat != 36 || stored[0].OwnBounds.MaxLon != -97 {
		t.Fatalf("own bounds = %#v, want target bbox", stored[0].OwnBounds)
	}
}

func TestDeconflictionReplacesStaleFindingsAfterRemediation(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 20, 100, now)
	deconfliction := newTestDeconflictionService(t, store, now)

	first, err := deconfliction.CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("first CheckIntent returned error: %v", err)
	}
	if first.Posture != domain.DeconflictionPosturePotentialConflict {
		t.Fatalf("first posture = %q, want potential_conflict", first.Posture)
	}
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-peer",
		OperatorID:    "operator-1",
		IntentID:      "intent-peer",
		IntentVersion: 1,
		Sequence:      1,
		GeoJSON:       eastSquareGeoJSON(),
		MinAltitudeM:  20,
		MaxAltitudeM:  100,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))

	second, err := deconfliction.CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("second CheckIntent returned error: %v", err)
	}
	if second.Posture != domain.DeconflictionPostureClear {
		t.Fatalf("second posture = %q, want clear; findings=%#v", second.Posture, second.Findings)
	}
	stored, err := store.ListConflictFindings(ctx, target.ID, target.Version)
	if err != nil {
		t.Fatalf("ListConflictFindings returned error: %v", err)
	}
	if len(stored) != 1 || stored[0].Status != domain.ConflictFindingStatusClear {
		t.Fatalf("stored findings = %#v, want stale conflict replaced by clear finding", stored)
	}
}

func TestDeconflictionClearWhenAltitudeSeparated(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 80)
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 120, 200, now)

	result, err := newTestDeconflictionService(t, store, now).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPostureClear {
		t.Fatalf("posture = %q, want clear; findings=%#v", result.Posture, result.Findings)
	}
}

func TestDeconflictionIgnoresSelfAndNonCoordinatedStatuses(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-target-extra",
		OperatorID:    "operator-1",
		IntentID:      target.ID,
		IntentVersion: target.Version,
		Sequence:      2,
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
	for _, tc := range []struct {
		id     string
		status domain.IntentStatus
	}{
		{id: "intent-draft", status: domain.IntentStatusDraft},
		{id: "intent-submitted", status: domain.IntentStatusSubmitted},
		{id: "intent-review", status: domain.IntentStatusReview},
		{id: "intent-rejected", status: domain.IntentStatusRejected},
		{id: "intent-canceled", status: domain.IntentStatusCanceled},
		{id: "intent-complete", status: domain.IntentStatusComplete},
		{id: "intent-superseded", status: domain.IntentStatusSuperseded},
	} {
		intent := createAcceptedIntentWithVolume(t, ctx, store, now, tc.id, "aircraft-2", "volume-"+tc.id, squareGeoJSON(), 10, 120, now)
		intent.Status = tc.status
		must(t, store.UpdateOperationalIntent(ctx, intent))
	}

	result, err := newTestDeconflictionService(t, store, now).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPostureClear {
		t.Fatalf("posture = %q, want clear; findings=%#v", result.Posture, result.Findings)
	}
}

func TestDeconflictionBackToBackTimeWindowsDoNotOverlap(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 10, 120, now.Add(time.Hour))

	result, err := newTestDeconflictionService(t, store, now).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPostureClear {
		t.Fatalf("posture = %q, want clear for half-open adjacent windows; findings=%#v", result.Posture, result.Findings)
	}
}

func TestDeconflictionOverlappingTimeWindowsProducePotentialConflict(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 10, 120, now.Add(59*time.Minute))

	result, err := newTestDeconflictionService(t, store, now).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPosturePotentialConflict {
		t.Fatalf("posture = %q, want potential_conflict for one-minute overlap; findings=%#v", result.Posture, result.Findings)
	}
}

func TestDeconflictionMissingAltitudeFailsClosed(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 0, 0)

	result, err := newTestDeconflictionService(t, store, now).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPostureIndeterminate {
		t.Fatalf("posture = %q, want indeterminate; findings=%#v", result.Posture, result.Findings)
	}
}

func TestDeconflictionPeerMissingAltitudeFailsClosed(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 0, 0, now)

	result, err := newTestDeconflictionService(t, store, now).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPostureIndeterminate {
		t.Fatalf("posture = %q, want indeterminate; findings=%#v", result.Posture, result.Findings)
	}
}

func TestDeconflictionAltitudeReferenceMismatchFailsClosed(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 10, 120, now)
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-peer",
		OperatorID:    "operator-1",
		IntentID:      "intent-peer",
		IntentVersion: 1,
		Sequence:      1,
		GeoJSON:       squareGeoJSON(),
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceMSL,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}))

	result, err := newTestDeconflictionService(t, store, now).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPostureIndeterminate {
		t.Fatalf("posture = %q, want indeterminate; findings=%#v", result.Posture, result.Findings)
	}
}

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
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 10, 120, now)
	if evaluation, err := NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, intent.ID); err != nil {
		t.Fatalf("EvaluateIntent returned error: %v", err)
	} else if evaluation.Blocked {
		t.Fatalf("preflight blocked unexpectedly: %#v", evaluation.Findings)
	}
	plainIntents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := plainIntents.AcceptIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}

	guardedIntents := NewIntentServiceWithClock(store, fixedClock(now), newTestDeconflictionService(t, store, now))
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

func TestDeconflictionFindingsAreScopedByIntentVersion(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	createAcceptedIntentWithVolume(t, ctx, store, now, "intent-peer", "aircraft-2", "volume-peer", squareGeoJSON(), 10, 120, now)
	deconfliction := newTestDeconflictionService(t, store, now)

	first, err := deconfliction.CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("first CheckIntent returned error: %v", err)
	}
	if first.Posture != domain.DeconflictionPosturePotentialConflict {
		t.Fatalf("first posture = %q, want potential_conflict", first.Posture)
	}
	target.Version = 2
	must(t, store.UpdateOperationalIntent(ctx, target))
	must(t, store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-target",
		OperatorID:    "operator-1",
		IntentID:      target.ID,
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

	second, err := deconfliction.CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("second CheckIntent returned error: %v", err)
	}
	if second.Posture != domain.DeconflictionPostureClear {
		t.Fatalf("second posture = %q, want clear; findings=%#v", second.Posture, second.Findings)
	}
	v1Findings, err := store.ListConflictFindings(ctx, target.ID, 1)
	if err != nil {
		t.Fatalf("ListConflictFindings v1 returned error: %v", err)
	}
	v2Findings, err := store.ListConflictFindings(ctx, target.ID, 2)
	if err != nil {
		t.Fatalf("ListConflictFindings v2 returned error: %v", err)
	}
	if len(v1Findings) != 1 || v1Findings[0].Status != domain.ConflictFindingStatusPotentialConflict {
		t.Fatalf("v1 findings = %#v, want preserved v1 potential conflict", v1Findings)
	}
	if len(v2Findings) != 1 || v2Findings[0].Status != domain.ConflictFindingStatusClear {
		t.Fatalf("v2 findings = %#v, want current v2 clear", v2Findings)
	}
	listed, err := deconfliction.ListConflictFindings(ctx, target.ID)
	if err != nil {
		t.Fatalf("ListConflictFindings service returned error: %v", err)
	}
	if len(listed) != 1 || listed[0].IntentVersion != 2 || listed[0].Status != domain.ConflictFindingStatusClear {
		t.Fatalf("service findings = %#v, want only current v2 clear", listed)
	}
}

func TestDeconflictionIndeterminateForMalformedTargetGeometry(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", `{bad-json`, 10, 120)

	result, err := newTestDeconflictionService(t, store, now).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPostureIndeterminate {
		t.Fatalf("posture = %q, want indeterminate; findings=%#v", result.Posture, result.Findings)
	}
	if len(result.Findings) != 1 || !result.Findings[0].Blocking {
		t.Fatalf("findings = %#v, want one blocking finding", result.Findings)
	}
}

func TestDeconflictionIndeterminateForUnclosedPolygon(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36]]]}`, 10, 120)

	result, err := newTestDeconflictionService(t, store, now).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPostureIndeterminate {
		t.Fatalf("posture = %q, want indeterminate; findings=%#v", result.Posture, result.Findings)
	}
}

func TestDeconflictionPotentialConflictForGeometryURIOnlyPeer(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedDeconflictionAircraft(t, ctx, store, now)
	target := createDraftIntentWithVolume(t, ctx, store, now, "intent-target", "aircraft-1", "volume-target", squareGeoJSON(), 10, 120)
	createAcceptedIntentWithVolumeRequest(t, ctx, store, now, "intent-peer", "aircraft-2", AddOperationalVolumeRequest{
		ID:           "volume-peer",
		Sequence:     1,
		GeometryURI:  "memory://external-volume",
		MinAltitudeM: float64Ptr(20),
		MaxAltitudeM: float64Ptr(100),
		AltitudeRef:  domain.AltitudeReferenceAGL,
		StartsAt:     now,
		EndsAt:       now.Add(time.Hour),
		VolumeType:   domain.OperationalVolumeLoiter,
	})

	result, err := newTestDeconflictionService(t, store, now).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPosturePotentialConflict {
		t.Fatalf("posture = %q, want potential_conflict; findings=%#v", result.Posture, result.Findings)
	}
}

func seedDeconflictionAircraft(t *testing.T, ctx context.Context, store *durablememory.Store, now time.Time) {
	t.Helper()
	must(t, store.CreateAircraft(ctx, domain.Aircraft{
		ID:               "aircraft-1",
		OperatorID:       "operator-1",
		Status:           domain.AircraftStatusActive,
		AcceptanceStatus: domain.AcceptanceStatusAccepted,
		RemoteIDStatus:   domain.RemoteIDStatusBroadcasting,
		CreatedAt:        now,
		UpdatedAt:        now,
	}))
	must(t, store.CreateAircraft(ctx, domain.Aircraft{
		ID:               "aircraft-2",
		OperatorID:       "operator-1",
		Status:           domain.AircraftStatusActive,
		AcceptanceStatus: domain.AcceptanceStatusAccepted,
		RemoteIDStatus:   domain.RemoteIDStatusBroadcasting,
		CreatedAt:        now,
		UpdatedAt:        now,
	}))
}

func createDraftIntentWithVolume(t *testing.T, ctx context.Context, store *durablememory.Store, now time.Time, intentID, aircraftID, volumeID, geoJSON string, minAltitudeM, maxAltitudeM float64) domain.OperationalIntent {
	t.Helper()
	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := intents.CreateIntent(ctx, CreateIntentRequest{
		ID:                  intentID,
		OperatorID:          "operator-1",
		AircraftID:          aircraftID,
		Name:                intentID,
		Summary:             "deconfliction test intent",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
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
		StartsAt:     now,
		EndsAt:       now.Add(time.Hour),
		VolumeType:   domain.OperationalVolumeLoiter,
	}); err != nil {
		t.Fatalf("AddOperationalVolume returned error: %v", err)
	}
	return intent
}

func createAcceptedIntentWithVolume(t *testing.T, ctx context.Context, store *durablememory.Store, now time.Time, intentID, aircraftID, volumeID, geoJSON string, minAltitudeM, maxAltitudeM float64, startsAt time.Time) domain.OperationalIntent {
	t.Helper()
	return createAcceptedIntentWithVolumeRequest(t, ctx, store, now, intentID, aircraftID, AddOperationalVolumeRequest{
		ID:           volumeID,
		Sequence:     1,
		GeoJSON:      geoJSON,
		MinAltitudeM: float64Ptr(minAltitudeM),
		MaxAltitudeM: float64Ptr(maxAltitudeM),
		AltitudeRef:  domain.AltitudeReferenceAGL,
		StartsAt:     startsAt,
		EndsAt:       startsAt.Add(time.Hour),
		VolumeType:   domain.OperationalVolumeLoiter,
	})
}

func createAcceptedIntentWithVolumeRequest(t *testing.T, ctx context.Context, store *durablememory.Store, now time.Time, intentID, aircraftID string, volumeReq AddOperationalVolumeRequest) domain.OperationalIntent {
	t.Helper()
	intents := NewIntentServiceWithClock(store, fixedClock(now), nil)
	intent, err := intents.CreateIntent(ctx, CreateIntentRequest{
		ID:                  intentID,
		OperatorID:          "operator-1",
		AircraftID:          aircraftID,
		Name:                intentID,
		Summary:             "accepted deconfliction peer",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		ConformanceRequired: true,
		PlannedStartAt:      volumeReq.StartsAt,
		PlannedEndAt:        volumeReq.EndsAt,
	})
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if _, err = intents.AddOperationalVolume(ctx, intent.ID, volumeReq); err != nil {
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
