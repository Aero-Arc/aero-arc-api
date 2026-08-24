package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/readmodel"
	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	replaymemory "github.com/Aero-Arc/aero-arc-api/internal/store/replay/memory"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
	conformancev1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/conformance/v1"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type failingRegistry struct{}

func (failingRegistry) RegisterRelay(context.Context, *registryv1.RegisterRelayRequest, ...grpc.CallOption) (*registryv1.RegisterRelayResponse, error) {
	return nil, errors.New("registry unavailable")
}

func (failingRegistry) HeartbeatRelay(context.Context, *registryv1.HeartbeatRelayRequest, ...grpc.CallOption) (*registryv1.HeartbeatRelayResponse, error) {
	return nil, errors.New("registry unavailable")
}

func (failingRegistry) ListRelays(context.Context, *registryv1.ListRelaysRequest, ...grpc.CallOption) (*registryv1.ListRelaysResponse, error) {
	return nil, errors.New("registry unavailable")
}

func (failingRegistry) RegisterAgent(context.Context, *registryv1.RegisterAgentRequest, ...grpc.CallOption) (*registryv1.RegisterAgentResponse, error) {
	return nil, errors.New("registry unavailable")
}

func (failingRegistry) HeartbeatAgent(context.Context, *registryv1.HeartbeatAgentRequest, ...grpc.CallOption) (*registryv1.HeartbeatAgentResponse, error) {
	return nil, errors.New("registry unavailable")
}

func (failingRegistry) ListAgents(context.Context, *registryv1.ListAgentsRequest, ...grpc.CallOption) (*registryv1.ListAgentsResponse, error) {
	return nil, errors.New("registry unavailable")
}

func (failingRegistry) GetAgentPlacement(context.Context, *registryv1.GetAgentPlacementRequest, ...grpc.CallOption) (*registryv1.GetAgentPlacementResponse, error) {
	return nil, errors.New("registry unavailable")
}

func (failingRegistry) PublishConformanceSummary(context.Context, *registryv1.PublishConformanceSummaryRequest, ...grpc.CallOption) (*registryv1.PublishConformanceSummaryResponse, error) {
	return nil, errors.New("registry unavailable")
}

func (failingRegistry) GetConformanceSummary(context.Context, *registryv1.GetConformanceSummaryRequest, ...grpc.CallOption) (*registryv1.GetConformanceSummaryResponse, error) {
	return nil, errors.New("registry unavailable")
}

func (failingRegistry) BatchGetConformanceSummaries(context.Context, *registryv1.BatchGetConformanceSummariesRequest, ...grpc.CallOption) (*registryv1.BatchGetConformanceSummariesResponse, error) {
	return nil, status.Error(codes.Unavailable, "registry unavailable")
}

func TestFleetServiceComposesAircraftDashboard(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	registry := newTestRegistry()

	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", AgentID: "agent-1", TailNumber: "N100AA"}))
	must(t, durable.CreateBattery(ctx, domain.Battery{ID: "battery-1", StateOfHealth: float64Ptr(94)}))
	must(t, durable.RecordBatteryInstallation(ctx, domain.BatteryInstallation{
		ID: "install-1", AircraftID: "aircraft-1", BatteryID: "battery-1", InstalledAt: now,
	}))
	must(t, telemetry.AddSample(ctx, domain.TelemetrySample{
		ID: "sample-1", AircraftID: "aircraft-1", RecordedAt: now, BatteryPct: float64Ptr(87),
	}))
	must(t, registry.SetLiveAircraftState(ctx, domain.LiveAircraftState{
		AircraftID: "aircraft-1", AgentID: "agent-1", RelayID: "relay-1", Connected: true,
	}))

	svc := NewFleetService(durable, telemetry, replay, registry)
	dashboard, err := svc.GetAircraftDashboard(ctx, "aircraft-1")
	if err != nil {
		t.Fatalf("GetAircraftDashboard returned error: %v", err)
	}

	if dashboard.Aircraft.ID != "aircraft-1" {
		t.Fatalf("aircraft ID = %q, want aircraft-1", dashboard.Aircraft.ID)
	}
	if dashboard.ActiveBattery == nil || dashboard.ActiveBattery.ID != "battery-1" {
		t.Fatalf("active battery = %#v, want battery-1", dashboard.ActiveBattery)
	}
	if dashboard.LatestTelemetry == nil || dashboard.LatestTelemetry.ID != "sample-1" {
		t.Fatalf("latest telemetry = %#v, want sample-1", dashboard.LatestTelemetry)
	}
	if !dashboard.LiveStateAvailable {
		t.Fatal("live state should be available")
	}
	if dashboard.Readiness.Status != "ready" {
		t.Fatalf("readiness = %q, want ready", dashboard.Readiness.Status)
	}
}

func TestReadinessCalculation(t *testing.T) {
	now := time.Now().UTC()
	battery := &domain.Battery{ID: "battery-1", StateOfHealth: float64Ptr(90)}
	critical := []domain.MaintenanceEvent{{
		ID: "mx-1", Severity: "critical", OpenedAt: now,
	}}

	if got := CalculateReadiness(battery, critical, true); got.Status != "blocked" {
		t.Fatalf("critical readiness = %q, want blocked", got.Status)
	}

	lowBattery := &domain.Battery{ID: "battery-1", StateOfHealth: float64Ptr(79)}
	if got := CalculateReadiness(lowBattery, nil, true); got.Status != "warning" {
		t.Fatalf("low battery readiness = %q, want warning", got.Status)
	}

	if got := CalculateReadiness(battery, nil, false); got.Status != "warning" {
		t.Fatalf("missing live state readiness = %q, want warning", got.Status)
	}

	unknownSOH := &domain.Battery{ID: "battery-1"}
	got := CalculateReadiness(unknownSOH, nil, true)
	if got.Status != "warning" {
		t.Fatalf("unknown SOH readiness = %q, want warning", got.Status)
	}
	if len(got.Reasons) != 1 || got.Reasons[0] != "battery state of health unknown" {
		t.Fatalf("unknown SOH reasons = %#v, want battery state of health unknown", got.Reasons)
	}

	if got := CalculateReadiness(battery, nil, true); got.Status != "ready" {
		t.Fatalf("healthy readiness = %q, want ready", got.Status)
	}
}

func TestFleetServiceReadinessDoesNotReportReadyWhenBatterySOHUnknown(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	registry := newTestRegistry()

	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", AgentID: "agent-1", TailNumber: "N100AA"}))
	must(t, durable.CreateBattery(ctx, domain.Battery{ID: "battery-1"}))
	must(t, durable.RecordBatteryInstallation(ctx, domain.BatteryInstallation{
		ID: "install-1", AircraftID: "aircraft-1", BatteryID: "battery-1", InstalledAt: now,
	}))
	must(t, registry.SetLiveAircraftState(ctx, domain.LiveAircraftState{
		AircraftID: "aircraft-1", AgentID: "agent-1", RelayID: "relay-1", Connected: true,
	}))

	svc := NewFleetService(durable, telemetry, replay, registry)
	dashboard, err := svc.GetAircraftDashboard(ctx, "aircraft-1")
	if err != nil {
		t.Fatalf("GetAircraftDashboard returned error: %v", err)
	}
	if dashboard.Readiness.Status == domain.ReadinessStatusReady {
		t.Fatal("readiness should not be ready when battery state of health is unknown")
	}
	if len(dashboard.Readiness.Reasons) != 1 || dashboard.Readiness.Reasons[0] != "battery state of health unknown" {
		t.Fatalf("readiness reasons = %#v, want battery state of health unknown", dashboard.Readiness.Reasons)
	}
}

func TestFleetServiceGracefullyDegradesWhenRegistryUnavailable(t *testing.T) {
	ctx := context.Background()
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()

	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", AgentID: "agent-1"}))
	must(t, durable.CreateBattery(ctx, domain.Battery{ID: "battery-1", StateOfHealth: float64Ptr(92)}))
	must(t, durable.RecordBatteryInstallation(ctx, domain.BatteryInstallation{
		ID: "install-1", AircraftID: "aircraft-1", BatteryID: "battery-1", InstalledAt: time.Now().UTC(),
	}))

	svc := NewFleetService(durable, telemetry, replay, failingRegistry{})
	dashboard, err := svc.GetAircraftDashboard(ctx, "aircraft-1")
	if err != nil {
		t.Fatalf("GetAircraftDashboard returned error: %v", err)
	}
	if dashboard.LiveStateAvailable {
		t.Fatal("live state should not be available")
	}
	if dashboard.Readiness.Status != "warning" {
		t.Fatalf("readiness = %q, want warning", dashboard.Readiness.Status)
	}
}

func TestFleetServiceOverlaysRegistryConformanceInOneBatch(t *testing.T) {
	ctx := context.Background()
	observedAt := time.Date(2026, 8, 24, 5, 30, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	reg := newTestRegistry()
	for _, intentID := range []string{"intent-live-only", "intent-merge", "intent-missing"} {
		must(t, durable.CreateOperationalIntent(ctx, domain.OperationalIntent{ID: intentID, Version: 2, AircraftID: "aircraft-1", ConformanceRequired: true}))
	}
	score := 0.91
	must(t, durable.UpsertConformanceSummary(ctx, domain.ConformanceSummary{
		ID: "durable-merge", IntentID: "intent-merge", IntentVersion: 2, AircraftID: "aircraft-old",
		Status: domain.ConformanceStatusConforming, Score: &score, AlertCount: 9,
		ReportabilityStatus: domain.ReportabilityStatusReportable, UpdatedAt: observedAt.Add(-time.Hour),
	}))
	must(t, durable.UpsertConformanceSummary(ctx, domain.ConformanceSummary{
		ID: "durable-missing", IntentID: "intent-missing", IntentVersion: 2, AircraftID: "aircraft-1",
		Status: domain.ConformanceStatusConforming, ReportabilityStatus: domain.ReportabilityStatusNo,
		UpdatedAt: observedAt.Add(-2 * time.Hour),
	}))
	must(t, durable.RecordConformanceEvent(ctx, domain.ConformanceEvent{ID: "event-durable", IntentID: "intent-merge", OccurredAt: observedAt.Add(-time.Minute)}))

	publishTestConformance(t, ctx, reg.MemoryClient, testConformanceProto("intent-live-only", observedAt, conformancev1.ConformanceCondition_CONFORMANCE_CONDITION_CONFORMING))
	mergedProto := testConformanceProto("intent-merge", observedAt.Add(time.Minute), conformancev1.ConformanceCondition_CONFORMANCE_CONDITION_NON_CONFORMING)
	mergedProto.EvaluationRevision = 7
	mergedProto.EvaluationId = "evaluation-merge"
	mergedProto.AircraftId = "aircraft-live"
	mergedProto.Violations = []*conformancev1.ViolationSummary{{
		ViolationType:   conformancev1.ViolationType_VIOLATION_TYPE_LATERAL_DEVIATION,
		Phase:           conformancev1.IncidentPhase_INCIDENT_PHASE_OPEN,
		OpeningFrameId:  "frame-opening",
		OpenedAt:        timestamppb.New(observedAt.Add(-time.Minute)),
		LastObservedAt:  timestamppb.New(observedAt),
		WorstDeviationM: 12.5,
	}}
	publishTestConformance(t, ctx, reg.MemoryClient, mergedProto)

	svc := NewFleetService(durable, telemetrymemory.NewStore(), replaymemory.NewStore(), reg)
	operations, err := svc.GetOperationsDashboard(ctx)
	if err != nil {
		t.Fatalf("GetOperationsDashboard returned error: %v", err)
	}
	if reg.batchConformanceCalls != 1 {
		t.Fatalf("BatchGetConformanceSummaries calls = %d, want 1", reg.batchConformanceCalls)
	}
	if got := reg.batchAssignmentIDs[0]; fmt.Sprint(got) != "[intent-live-only intent-merge intent-missing]" {
		t.Fatalf("assignment IDs = %v", got)
	}
	byIntent := conformanceByIntent(operations.Conformance)
	if len(byIntent) != 3 {
		t.Fatalf("conformance summaries = %#v, want live-only, merged, and durable fallback", operations.Conformance)
	}
	liveOnly := byIntent["intent-live-only"]
	if liveOnly.AssignmentID != "intent-live-only" || liveOnly.Status != domain.ConformanceStatusConforming || liveOnly.ObservedAt == nil || !liveOnly.ObservedAt.Equal(observedAt) {
		t.Fatalf("live-only summary = %#v", liveOnly)
	}
	merged := byIntent["intent-merge"]
	if merged.ID != "durable-merge" || merged.Status != domain.ConformanceStatusNonConforming || merged.Condition != "non_conforming" || merged.EvaluationRevision != 7 {
		t.Fatalf("merged summary identity/status = %#v", merged)
	}
	if merged.Score == nil || *merged.Score != score || merged.AlertCount != 9 || merged.ReportabilityStatus != domain.ReportabilityStatusReportable {
		t.Fatalf("merged durable fields = %#v, want preserved score/alerts/reportability", merged)
	}
	if len(merged.Violations) != 1 || merged.Violations[0].ViolationType != "lateral_deviation" || merged.Violations[0].Phase != "open" || merged.Violations[0].WorstDeviationM != 12.5 {
		t.Fatalf("merged violations = %#v", merged.Violations)
	}
	if missing := byIntent["intent-missing"]; missing.ID != "durable-missing" || missing.AssignmentID != "" {
		t.Fatalf("missing projection fallback = %#v", missing)
	}

	conformance, err := svc.GetConformanceDashboard(ctx)
	if err != nil {
		t.Fatalf("GetConformanceDashboard returned error: %v", err)
	}
	if reg.batchConformanceCalls != 2 {
		t.Fatalf("batch calls after both dashboards = %d, want one per dashboard", reg.batchConformanceCalls)
	}
	if len(conformance.Summaries) != 3 || len(conformance.Events) != 1 || conformance.Events[0].ID != "event-durable" {
		t.Fatalf("conformance dashboard = %#v, want live summaries plus durable event", conformance)
	}
}

func TestFleetServiceConformanceOverlayDegradesOnRegistryFailure(t *testing.T) {
	ctx := context.Background()
	for _, code := range []codes.Code{codes.Unimplemented, codes.Unavailable} {
		t.Run(code.String(), func(t *testing.T) {
			durable := durablememory.NewStore()
			must(t, durable.CreateOperationalIntent(ctx, domain.OperationalIntent{ID: "intent-1", Version: 1, ConformanceRequired: true}))
			must(t, durable.UpsertConformanceSummary(ctx, domain.ConformanceSummary{
				ID: "durable-1", IntentID: "intent-1", IntentVersion: 1,
				Status: domain.ConformanceStatusConforming, ReportabilityStatus: domain.ReportabilityStatusNo,
			}))
			reg := &conformanceFailureRegistry{testRegistry: newTestRegistry(), err: status.Error(code, "projection unavailable")}
			svc := NewFleetService(durable, telemetrymemory.NewStore(), replaymemory.NewStore(), reg)
			operations, err := svc.GetOperationsDashboard(ctx)
			if err != nil {
				t.Fatalf("GetOperationsDashboard returned error: %v", err)
			}
			if len(operations.Conformance) != 1 || operations.Conformance[0].ID != "durable-1" {
				t.Fatalf("operations conformance = %#v, want durable fallback", operations.Conformance)
			}
			conformance, err := svc.GetConformanceDashboard(ctx)
			if err != nil {
				t.Fatalf("GetConformanceDashboard returned error: %v", err)
			}
			if len(conformance.Summaries) != 1 || conformance.Summaries[0].ID != "durable-1" {
				t.Fatalf("conformance summaries = %#v, want durable fallback", conformance.Summaries)
			}
		})
	}
}

func TestFleetServiceBootstrapsBatteryAndFlightLifecycle(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC)
	store := durablememory.NewStore()
	must(t, store.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", OperatorID: "operator-1"}))
	must(t, store.CreateBattery(ctx, domain.Battery{ID: "battery-1", OperatorID: "operator-1"}))
	intent := domain.OperationalIntent{
		ID: "intent-1", Version: 3, OperatorID: "operator-1", AircraftID: "aircraft-1",
		Status: domain.IntentStatusAccepted, UpdatedAt: now.Add(-time.Minute),
	}
	must(t, store.CreateOperationalIntent(ctx, intent))
	svc := NewFleetService(store, telemetrymemory.NewStore(), replaymemory.NewStore(), newTestRegistry())
	svc.now = func() time.Time { return now }

	installation, err := svc.InstallBattery(ctx, "aircraft-1", InstallBatteryRequest{ID: "install-1", BatteryID: "battery-1"})
	if err != nil {
		t.Fatalf("InstallBattery returned error: %v", err)
	}
	if installation.OperatorID != "operator-1" || !installation.InstalledAt.Equal(now) {
		t.Fatalf("installation = %#v, want derived operator and server time", installation)
	}
	if _, err := svc.InstallBattery(ctx, "aircraft-1", InstallBatteryRequest{ID: "install-2", BatteryID: "battery-1"}); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("second InstallBattery error = %v, want version conflict", err)
	}

	flight, err := svc.CreatePlannedFlight(ctx, "intent-1", CreateFlightRequest{ID: "flight-1", MissionType: "sitl"})
	if err != nil {
		t.Fatalf("CreatePlannedFlight returned error: %v", err)
	}
	if flight.Status != domain.FlightStatusPlanned || flight.OperatorID != "operator-1" || flight.AircraftID != "aircraft-1" || flight.IntentVersion != 3 || !flight.StartedAt.IsZero() {
		t.Fatalf("planned flight = %#v", flight)
	}
	if _, err := svc.CreatePlannedFlight(ctx, "intent-1", CreateFlightRequest{ID: "flight-1"}); !errors.Is(err, durable.ErrAlreadyExists) {
		t.Fatalf("duplicate CreatePlannedFlight error = %v, want already exists", err)
	}
	if _, err := svc.StartFlight(ctx, "flight-1"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("StartFlight with accepted intent error = %v, want invalid transition", err)
	}

	intent.Status = domain.IntentStatusActive
	intent.UpdatedAt = now
	must(t, store.UpdateOperationalIntent(ctx, intent, 0))
	started, err := svc.StartFlight(ctx, "flight-1")
	if err != nil {
		t.Fatalf("StartFlight returned error: %v", err)
	}
	if started.Status != domain.FlightStatusActive || !started.StartedAt.Equal(now) {
		t.Fatalf("started flight = %#v", started)
	}
	retry, err := svc.StartFlight(ctx, "flight-1")
	if err != nil || retry.Status != domain.FlightStatusActive || !retry.StartedAt.Equal(started.StartedAt) {
		t.Fatalf("StartFlight retry = %#v, %v", retry, err)
	}
}

func TestFleetServiceBootstrapRejectsOperatorMismatch(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	must(t, store.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", OperatorID: "operator-aircraft"}))
	must(t, store.CreateBattery(ctx, domain.Battery{ID: "battery-1", OperatorID: "operator-battery"}))
	svc := NewFleetService(store, telemetrymemory.NewStore(), replaymemory.NewStore(), newTestRegistry())
	if _, err := svc.InstallBattery(ctx, "aircraft-1", InstallBatteryRequest{ID: "install-1", BatteryID: "battery-1"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("InstallBattery error = %v, want validation", err)
	}

	must(t, store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: "intent-1", Version: 1, OperatorID: "operator-intent", AircraftID: "aircraft-1", Status: domain.IntentStatusAccepted,
	}))
	if _, err := svc.CreatePlannedFlight(ctx, "intent-1", CreateFlightRequest{ID: "flight-1"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("CreatePlannedFlight error = %v, want validation", err)
	}
}

func TestFleetServiceBatchesRegistryAndComposesFreshness(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 11, 18, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	registry := newTestRegistry()
	for _, aircraft := range []domain.Aircraft{
		{ID: "aircraft-fresh", AgentID: "agent-fresh"},
		{ID: "aircraft-stale", AgentID: "agent-stale"},
		{ID: "aircraft-unmapped"},
	} {
		must(t, durable.CreateAircraft(ctx, aircraft))
	}
	must(t, registry.SetLiveAircraftState(ctx, domain.LiveAircraftState{AgentID: "agent-fresh", RelayID: "relay-1", Connected: true, LastHeartbeatAt: now.Add(-5 * time.Second)}))
	must(t, registry.SetLiveAircraftState(ctx, domain.LiveAircraftState{AgentID: "agent-stale", RelayID: "relay-2", Connected: true, LastHeartbeatAt: now.Add(-31 * time.Second)}))
	must(t, telemetry.AddSample(ctx, domain.TelemetrySample{ID: "battery-frame", AircraftID: "aircraft-fresh", RecordedAt: now.Add(-20 * time.Second), BatteryPct: float64Ptr(70)}))
	must(t, telemetry.AddSample(ctx, domain.TelemetrySample{ID: "position-frame", AircraftID: "aircraft-fresh", RecordedAt: now.Add(-2 * time.Second), Latitude: 41, Longitude: -87}))

	svc := NewFleetService(durable, telemetry, replay, registry).WithLiveStatePolicy(30*time.Second, 15*time.Second, func() time.Time { return now })
	dashboards, err := svc.ListAircraftDashboards(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if registry.listAgentCalls != 1 {
		t.Fatalf("ListAgents calls = %d, want 1 for the whole collection", registry.listAgentCalls)
	}
	byID := make(map[string]readmodel.AircraftDashboard, len(dashboards))
	for _, dashboard := range dashboards {
		byID[dashboard.Aircraft.ID] = dashboard
	}
	fresh := byID["aircraft-fresh"]
	if fresh.LiveState == nil || fresh.LiveState.ConnectionStatus != domain.ConnectionStatusConnected || !fresh.LiveState.Connected {
		t.Fatalf("fresh connection = %#v", fresh.LiveState)
	}
	if fresh.Telemetry.Status != domain.DataFreshnessFresh || fresh.Telemetry.Position == nil || fresh.Telemetry.Position.Status != domain.DataFreshnessFresh {
		t.Fatalf("fresh telemetry = %#v", fresh.Telemetry)
	}
	if fresh.Telemetry.Battery == nil || fresh.Telemetry.Battery.Status != domain.DataFreshnessStale {
		t.Fatalf("battery freshness = %#v", fresh.Telemetry.Battery)
	}
	stale := byID["aircraft-stale"]
	if stale.LiveState.ConnectionStatus != domain.ConnectionStatusStale || stale.LiveState.Connected {
		t.Fatalf("stale connection = %#v", stale.LiveState)
	}
	unmapped := byID["aircraft-unmapped"]
	if unmapped.LiveState.ConnectionStatus != domain.ConnectionStatusUnmapped || unmapped.LiveState.AgentID != "" {
		t.Fatalf("unmapped connection = %#v", unmapped.LiveState)
	}
}

func TestFleetServiceBoundsConcurrentPlacementLookups(t *testing.T) {
	ctx := context.Background()
	durable := durablememory.NewStore()
	baseRegistry := newTestRegistry()
	release := make(chan struct{})
	registry := &controlledPlacementRegistry{
		testRegistry: baseRegistry,
		started:      make(chan string, maxPlacementLookups+4),
		release:      release,
	}
	for index := range maxPlacementLookups + 4 {
		aircraftID := fmt.Sprintf("aircraft-%02d", index)
		agentID := fmt.Sprintf("agent-%02d", index)
		relayID := fmt.Sprintf("relay-%02d", index)
		must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: aircraftID, AgentID: agentID}))
		must(t, baseRegistry.SetLiveAircraftState(ctx, domain.LiveAircraftState{
			AgentID: agentID, RelayID: relayID, Connected: true, LastHeartbeatAt: time.Now().UTC(),
		}))
	}

	type response struct {
		dashboards []readmodel.AircraftDashboard
		err        error
	}
	responseCh := make(chan response, 1)
	go func() {
		dashboards, err := NewFleetService(durable, telemetrymemory.NewStore(), replaymemory.NewStore(), registry).
			ListAircraftDashboards(ctx)
		responseCh <- response{dashboards: dashboards, err: err}
	}()

	for range maxPlacementLookups {
		select {
		case <-registry.started:
		case <-time.After(time.Second):
			t.Fatal("placement lookups were serialized")
		}
	}
	select {
	case agentID := <-registry.started:
		t.Fatalf("placement concurrency exceeded %d; started %q before a worker was released", maxPlacementLookups, agentID)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	select {
	case got := <-responseCh:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.dashboards) != maxPlacementLookups+4 {
			t.Fatalf("dashboards = %d, want %d", len(got.dashboards), maxPlacementLookups+4)
		}
		for _, dashboard := range got.dashboards {
			wantRelayID := "relay-" + dashboard.Aircraft.ID[len("aircraft-"):]
			if dashboard.LiveState == nil || dashboard.LiveState.RelayID != wantRelayID || !dashboard.LiveState.Connected {
				t.Fatalf("live state for %q = %#v, want connected through %q", dashboard.Aircraft.ID, dashboard.LiveState, wantRelayID)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("fleet lookup did not finish after placement workers were released")
	}
	if baseRegistry.listAgentCalls != 1 {
		t.Fatalf("ListAgents calls = %d, want 1", baseRegistry.listAgentCalls)
	}
	if calls, _, maxActive := registry.counts(); calls != maxPlacementLookups+4 || maxActive != maxPlacementLookups {
		t.Fatalf("placement calls = %d, max active = %d; want %d calls capped at %d", calls, maxActive, maxPlacementLookups+4, maxPlacementLookups)
	}
}

func TestFleetServiceQueriesTelemetryAndRegistryConcurrently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	now := time.Date(2026, 8, 11, 18, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	baseRegistry := newTestRegistry()
	registryRelease := make(chan struct{})
	close(registryRelease)
	registry := &controlledPlacementRegistry{
		testRegistry: baseRegistry,
		started:      make(chan string, 1),
		release:      registryRelease,
	}
	telemetryRelease := make(chan struct{})
	telemetry := &blockingLatestTelemetryStore{
		Store:   telemetrymemory.NewStore(),
		started: make(chan struct{}, 1),
		release: telemetryRelease,
	}
	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", AgentID: "agent-1"}))
	must(t, baseRegistry.SetLiveAircraftState(ctx, domain.LiveAircraftState{
		AgentID: "agent-1", RelayID: "relay-1", Connected: true, LastHeartbeatAt: now,
	}))

	type response struct {
		dashboards []readmodel.AircraftDashboard
		err        error
	}
	responseCh := make(chan response, 1)
	go func() {
		dashboards, err := NewFleetService(durable, telemetry, replaymemory.NewStore(), registry).
			WithLiveStatePolicy(30*time.Second, 15*time.Second, func() time.Time { return now }).
			ListAircraftDashboards(ctx)
		responseCh <- response{dashboards: dashboards, err: err}
	}()

	select {
	case <-telemetry.started:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("telemetry query did not start")
	}
	select {
	case <-registry.started:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("registry placement lookup did not start while telemetry was blocked")
	}
	close(telemetryRelease)

	select {
	case got := <-responseCh:
		if got.err != nil {
			t.Fatal(got.err)
		}
		if len(got.dashboards) != 1 || got.dashboards[0].LiveState == nil {
			t.Fatalf("dashboards = %#v", got.dashboards)
		}
		if state := got.dashboards[0].LiveState; !state.Connected || state.RelayID != "relay-1" || state.ConnectionStatus != domain.ConnectionStatusConnected {
			t.Fatalf("connection = %#v, want connected through relay-1", state)
		}
		if got.dashboards[0].Telemetry.Status != domain.DataFreshnessMissing {
			t.Fatalf("telemetry status = %q, want missing", got.dashboards[0].Telemetry.Status)
		}
	case <-ctx.Done():
		t.Fatal("fleet lookup did not finish before its deadline")
	}
}

func TestFleetServiceCancelsPlacementLookups(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	durable := durablememory.NewStore()
	baseRegistry := newTestRegistry()
	registry := &controlledPlacementRegistry{
		testRegistry: baseRegistry,
		started:      make(chan string, 2),
		release:      make(chan struct{}),
	}
	for index := range 2 {
		aircraftID := fmt.Sprintf("aircraft-%d", index)
		agentID := fmt.Sprintf("agent-%d", index)
		must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: aircraftID, AgentID: agentID}))
		must(t, baseRegistry.SetLiveAircraftState(ctx, domain.LiveAircraftState{
			AgentID: agentID, RelayID: "relay-1", Connected: true, LastHeartbeatAt: time.Now().UTC(),
		}))
	}

	resultCh := make(chan []readmodel.AircraftLiveState, 1)
	go func() {
		resultCh <- NewFleetService(durable, telemetrymemory.NewStore(), replaymemory.NewStore(), registry).
			composeLiveAircraft(ctx, []domain.Aircraft{{ID: "aircraft-0", AgentID: "agent-0"}, {ID: "aircraft-1", AgentID: "agent-1"}})
	}()
	for range 2 {
		select {
		case <-registry.started:
		case <-time.After(time.Second):
			t.Fatal("placement lookup did not start")
		}
	}
	cancel()

	select {
	case states := <-resultCh:
		for _, state := range states {
			if state.Connection.ConnectionStatus != domain.ConnectionStatusUnavailable || state.Connection.Connected {
				t.Fatalf("connection after cancellation = %#v, want unavailable", state.Connection)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("canceled placement lookups did not return")
	}
	if _, active, _ := registry.counts(); active != 0 {
		t.Fatalf("active placement calls = %d after cancellation, want 0", active)
	}
}

func TestFleetServiceDoesNotUseAircraftIDAsAgentFallback(t *testing.T) {
	ctx := context.Background()
	durable := durablememory.NewStore()
	registry := newTestRegistry()
	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "same-id"}))
	_, err := registry.RegisterAgent(ctx, &registryv1.RegisterAgentRequest{Agent: &registryv1.Agent{AgentId: "same-id", LastHeartbeatUnixMs: time.Now().UnixMilli()}})
	if err != nil {
		t.Fatal(err)
	}

	state, err := NewFleetService(durable, telemetrymemory.NewStore(), replaymemory.NewStore(), registry).GetAircraftLiveState(ctx, "same-id")
	if err != nil {
		t.Fatal(err)
	}
	if state.Connection.ConnectionStatus != domain.ConnectionStatusUnmapped || state.Connection.Connected {
		t.Fatalf("connection = %#v, want unmapped", state.Connection)
	}
	if registry.listAgentCalls != 0 {
		t.Fatalf("ListAgents calls = %d, want none without an explicit mapping", registry.listAgentCalls)
	}
}

func TestFleetServiceRequiresLivePlacementForConnectedState(t *testing.T) {
	ctx := context.Background()
	durable := durablememory.NewStore()
	registry := newTestRegistry()
	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", AgentID: "agent-1"}))
	must(t, registry.SetLiveAircraftState(ctx, domain.LiveAircraftState{
		AgentID: "agent-1", Connected: true, LastHeartbeatAt: time.Now().UTC(),
	}))

	state, err := NewFleetService(durable, telemetrymemory.NewStore(), replaymemory.NewStore(), registry).
		GetAircraftLiveState(ctx, "aircraft-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Connection.Connected || state.Connection.ConnectionStatus != domain.ConnectionStatusOffline {
		t.Fatalf("connection without placement = %#v, want offline", state.Connection)
	}
}

func TestFleetServiceComposesAircraftMapView(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 17, 15, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	registry := newTestRegistry()

	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", AgentID: "agent-1", TailNumber: "N100AA", CreatedAt: now, UpdatedAt: now}))
	must(t, registry.SetLiveAircraftState(ctx, domain.LiveAircraftState{
		AircraftID: "aircraft-1", AgentID: "agent-1", RelayID: "relay-1", Connected: true,
	}))
	for i := 1; i <= 3; i++ {
		must(t, telemetry.AddSample(ctx, domain.TelemetrySample{
			ID:         fmt.Sprintf("sample-%d", i),
			AircraftID: "aircraft-1",
			IntentID:   "intent-1",
			RecordedAt: now.Add(time.Duration(i) * time.Minute),
			Latitude:   35 + float64(i)/100,
			Longitude:  -97 - float64(i)/100,
			AltitudeM:  50 + float64(i),
		}))
	}
	intent := domain.OperationalIntent{
		ID:             "intent-1",
		OperatorID:     "operator-1",
		AircraftID:     "aircraft-1",
		Version:        2,
		Status:         domain.IntentStatusActive,
		PlannedStartAt: now.Add(-time.Minute),
		PlannedEndAt:   now.Add(time.Hour),
		UpdatedAt:      now,
	}
	must(t, durable.CreateOperationalIntent(ctx, intent))
	must(t, durable.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-1",
		OperatorID:    "operator-1",
		IntentID:      "intent-1",
		IntentVersion: 2,
		Sequence:      1,
		GeoJSON:       `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		CreatedAt:     now,
		UpdatedAt:     now,
	}))
	must(t, durable.UpsertConformanceSummary(ctx, domain.ConformanceSummary{
		ID:                  "conformance-1",
		OperatorID:          "operator-1",
		IntentID:            "intent-1",
		IntentVersion:       2,
		AircraftID:          "aircraft-1",
		Status:              domain.ConformanceStatusConforming,
		AlertCount:          1,
		ReportabilityStatus: domain.ReportabilityStatusReview,
		UpdatedAt:           now,
	}))
	must(t, durable.RecordConformanceEvent(ctx, domain.ConformanceEvent{
		ID:            "event-1",
		OperatorID:    "operator-1",
		IntentID:      "intent-1",
		IntentVersion: 2,
		AircraftID:    "aircraft-1",
		EventCode:     domain.ConformanceEventIntentExit,
		OccurredAt:    now.Add(10 * time.Minute),
	}))

	svc := NewFleetService(durable, telemetry, replay, registry)
	view, err := svc.GetAircraftMapView(ctx, "aircraft-1", 2)
	if err != nil {
		t.Fatalf("GetAircraftMapView returned error: %v", err)
	}
	if view.Aircraft.ID != "aircraft-1" {
		t.Fatalf("aircraft ID = %q, want aircraft-1", view.Aircraft.ID)
	}
	if !view.LiveStateAvailable || view.LiveState == nil || view.LiveState.RelayID != "relay-1" {
		t.Fatalf("live state = %#v available=%v, want relay-1 available", view.LiveState, view.LiveStateAvailable)
	}
	if view.LatestTelemetry == nil || view.LatestTelemetry.ID != "sample-3" {
		t.Fatalf("latest telemetry = %#v, want sample-3", view.LatestTelemetry)
	}
	if len(view.ReplaySamples) != 2 || view.ReplaySamples[0].ID != "sample-2" || view.ReplaySamples[1].ID != "sample-3" {
		t.Fatalf("replay samples = %#v, want sample-2/sample-3", view.ReplaySamples)
	}
	if view.ActiveIntent == nil || view.ActiveIntent.ID != "intent-1" {
		t.Fatalf("active intent = %#v, want intent-1", view.ActiveIntent)
	}
	if len(view.OperationalVolumes) != 1 || view.OperationalVolumes[0].ID != "volume-1" {
		t.Fatalf("volumes = %#v, want volume-1", view.OperationalVolumes)
	}
	if view.ConformanceSummary == nil || view.ConformanceSummary.Status != domain.ConformanceStatusConforming {
		t.Fatalf("summary = %#v, want conforming", view.ConformanceSummary)
	}
	if len(view.ConformanceEvents) != 1 || view.ConformanceEvents[0].ID != "event-1" {
		t.Fatalf("events = %#v, want event-1", view.ConformanceEvents)
	}
}

func TestFleetServiceAircraftMapViewDegradesWhenRegistryUnavailable(t *testing.T) {
	ctx := context.Background()
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()

	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1"}))

	svc := NewFleetService(durable, telemetry, replay, failingRegistry{})
	view, err := svc.GetAircraftMapView(ctx, "aircraft-1", 100)
	if err != nil {
		t.Fatalf("GetAircraftMapView returned error: %v", err)
	}
	if view.LiveStateAvailable {
		t.Fatal("live state should not be available")
	}
}

func TestFleetServiceAircraftMapViewWithoutActiveIntent(t *testing.T) {
	ctx := context.Background()
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	registry := newTestRegistry()

	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1"}))

	svc := NewFleetService(durable, telemetry, replay, registry)
	view, err := svc.GetAircraftMapView(ctx, "aircraft-1", 100)
	if err != nil {
		t.Fatalf("GetAircraftMapView returned error: %v", err)
	}
	if view.ActiveIntent != nil {
		t.Fatalf("active intent = %#v, want nil", view.ActiveIntent)
	}
	if view.ConformanceSummary != nil {
		t.Fatalf("summary = %#v, want nil", view.ConformanceSummary)
	}
	if len(view.OperationalVolumes) != 0 {
		t.Fatalf("volumes = %#v, want empty", view.OperationalVolumes)
	}
	if len(view.ConformanceEvents) != 0 {
		t.Fatalf("events = %#v, want empty", view.ConformanceEvents)
	}
}

func newTestRegistry() *testRegistry {
	return &testRegistry{MemoryClient: registry.NewMemoryClient()}
}

type testRegistry struct {
	*registry.MemoryClient
	listAgentCalls        int
	batchConformanceCalls int
	batchAssignmentIDs    [][]string
}

type conformanceFailureRegistry struct {
	*testRegistry
	err error
}

type controlledPlacementRegistry struct {
	*testRegistry
	started chan string
	release <-chan struct{}

	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
}

type blockingLatestTelemetryStore struct {
	*telemetrymemory.Store
	started chan struct{}
	release <-chan struct{}
}

func (s *blockingLatestTelemetryStore) GetLatestAircraftStates(ctx context.Context, aircraftIDs []string) (map[string]domain.AircraftTelemetryState, error) {
	s.started <- struct{}{}
	select {
	case <-s.release:
		return s.Store.GetLatestAircraftStates(ctx, aircraftIDs)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *controlledPlacementRegistry) GetAgentPlacement(ctx context.Context, request *registryv1.GetAgentPlacementRequest, options ...grpc.CallOption) (*registryv1.GetAgentPlacementResponse, error) {
	r.mu.Lock()
	r.calls++
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
	}()

	r.started <- request.GetAgentId()
	select {
	case <-r.release:
		return r.MemoryClient.GetAgentPlacement(ctx, request, options...)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *controlledPlacementRegistry) counts() (calls, active, maxActive int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.active, r.maxActive
}

func (r *testRegistry) ListAgents(ctx context.Context, request *registryv1.ListAgentsRequest, options ...grpc.CallOption) (*registryv1.ListAgentsResponse, error) {
	r.listAgentCalls++
	return r.MemoryClient.ListAgents(ctx, request, options...)
}

func (r *testRegistry) BatchGetConformanceSummaries(ctx context.Context, request *registryv1.BatchGetConformanceSummariesRequest, options ...grpc.CallOption) (*registryv1.BatchGetConformanceSummariesResponse, error) {
	r.batchConformanceCalls++
	r.batchAssignmentIDs = append(r.batchAssignmentIDs, append([]string(nil), request.GetAssignmentIds()...))
	return r.MemoryClient.BatchGetConformanceSummaries(ctx, request, options...)
}

func (r *conformanceFailureRegistry) BatchGetConformanceSummaries(context.Context, *registryv1.BatchGetConformanceSummariesRequest, ...grpc.CallOption) (*registryv1.BatchGetConformanceSummariesResponse, error) {
	return nil, r.err
}

func testConformanceProto(intentID string, observedAt time.Time, condition conformancev1.ConformanceCondition) *conformancev1.ConformanceSummary {
	return &conformancev1.ConformanceSummary{
		AssignmentId:         intentID,
		AssignmentGeneration: 3,
		EvaluationRevision:   4,
		EvaluationId:         "evaluation-" + intentID,
		OperatorId:           "operator-1",
		AircraftId:           "aircraft-1",
		FlightId:             "flight-1",
		IntentId:             intentID,
		IntentVersion:        2,
		Condition:            condition,
		MonitoringStatus:     conformancev1.MonitoringStatus_MONITORING_STATUS_CURRENT,
		RecordingStatus:      conformancev1.RecordingStatus_RECORDING_STATUS_CONFIRMED,
		ObservedAt:           timestamppb.New(observedAt),
		FrameId:              "frame-1",
	}
}

func publishTestConformance(t *testing.T, ctx context.Context, client *registry.MemoryClient, summary *conformancev1.ConformanceSummary) {
	t.Helper()
	if _, err := client.PublishConformanceSummary(ctx, &registryv1.PublishConformanceSummaryRequest{Summary: summary}); err != nil {
		t.Fatalf("PublishConformanceSummary returned error: %v", err)
	}
}

func conformanceByIntent(summaries []domain.ConformanceSummary) map[string]domain.ConformanceSummary {
	result := make(map[string]domain.ConformanceSummary, len(summaries))
	for _, summary := range summaries {
		result[summary.IntentID] = summary
	}
	return result
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
