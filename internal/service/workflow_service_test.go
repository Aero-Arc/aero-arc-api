package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
)

func TestIntentLifecycleHappyPath(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, float64Ptr(95))

	intents := NewIntentServiceWithClock(store, fixedClock(now))
	preflight := NewPreflightServiceWithClock(store, fixedClock(now))

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

	intents := NewIntentServiceWithClock(store, fixedClock(now))
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

	intents := NewIntentServiceWithClock(store, fixedClock(now))
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

func TestPreflightBlockedWhenBatterySOHMissing(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := fixedWorkflowTime()
	seedWorkflowAircraft(t, ctx, store, now, nil)
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)

	evaluation, err := NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, intent.ID)
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
	preflight := NewPreflightServiceWithClock(store, fixedClock(now))

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

	intents := NewIntentServiceWithClock(store, fixedClock(now))
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

	evaluation, err := NewPreflightServiceWithClock(store, fixedClock(now)).EvaluateIntent(ctx, intent.ID)
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
	intents := NewIntentServiceWithClock(store, fixedClock(now))
	intent, err := intents.CreateIntent(ctx, workflowIntentRequest(now))
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if _, err = intents.AddOperationalVolume(ctx, intent.ID, workflowVolumeRequest(now)); err != nil {
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
	intent := seedSubmittedIntentWithVolume(t, ctx, store, now)
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
	intent, err = intents.ActivateIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("ActivateIntent returned error: %v", err)
	}
	return intent
}

func workflowIntentRequest(now time.Time) CreateIntentRequest {
	return CreateIntentRequest{
		ID:                  "intent-1",
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
		MinAltitudeM: 10,
		MaxAltitudeM: 120,
		AltitudeRef:  domain.AltitudeReferenceAGL,
		StartsAt:     now,
		EndsAt:       now.Add(time.Hour),
		VolumeType:   domain.OperationalVolumeLoiter,
	}
}

func squareGeoJSON() string {
	return `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`
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
