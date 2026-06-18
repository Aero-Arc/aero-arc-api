package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	replaymemory "github.com/Aero-Arc/aero-arc-api/internal/store/replay/memory"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc"
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

	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1"}))
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
