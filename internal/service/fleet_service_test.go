package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/memory"
)

type failingRegistry struct{}

func (failingRegistry) GetLiveAircraftState(context.Context, string) (*domain.LiveAircraftState, error) {
	return nil, errors.New("registry unavailable")
}

func (failingRegistry) ListLiveAircraftStates(context.Context) ([]domain.LiveAircraftState, error) {
	return nil, errors.New("registry unavailable")
}

func TestFleetServiceComposesAircraftDashboard(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	durable := memory.NewDurableStore()
	telemetry := memory.NewTelemetryStore()
	replay := memory.NewReplayStore()
	registry := newTestRegistry()

	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", TailNumber: "N100AA"}))
	must(t, durable.CreateBattery(ctx, domain.Battery{ID: "battery-1", StateOfHealth: 94}))
	must(t, durable.RecordBatteryInstallation(ctx, domain.BatteryInstallation{
		ID: "install-1", AircraftID: "aircraft-1", BatteryID: "battery-1", InstalledAt: now,
	}))
	must(t, telemetry.AddSample(ctx, domain.TelemetrySample{
		ID: "sample-1", AircraftID: "aircraft-1", RecordedAt: now, BatteryPct: 87,
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
	battery := &domain.Battery{ID: "battery-1", StateOfHealth: 90}
	critical := []domain.MaintenanceEvent{{
		ID: "mx-1", Severity: "critical", OpenedAt: now,
	}}

	if got := CalculateReadiness(battery, critical, true); got.Status != "blocked" {
		t.Fatalf("critical readiness = %q, want blocked", got.Status)
	}

	lowBattery := &domain.Battery{ID: "battery-1", StateOfHealth: 79}
	if got := CalculateReadiness(lowBattery, nil, true); got.Status != "warning" {
		t.Fatalf("low battery readiness = %q, want warning", got.Status)
	}

	if got := CalculateReadiness(battery, nil, false); got.Status != "warning" {
		t.Fatalf("missing live state readiness = %q, want warning", got.Status)
	}

	if got := CalculateReadiness(battery, nil, true); got.Status != "ready" {
		t.Fatalf("healthy readiness = %q, want ready", got.Status)
	}
}

func TestFleetServiceGracefullyDegradesWhenRegistryUnavailable(t *testing.T) {
	ctx := context.Background()
	durable := memory.NewDurableStore()
	telemetry := memory.NewTelemetryStore()
	replay := memory.NewReplayStore()

	must(t, durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1"}))
	must(t, durable.CreateBattery(ctx, domain.Battery{ID: "battery-1", StateOfHealth: 92}))
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

func newTestRegistry() *testRegistry {
	return &testRegistry{states: make(map[string]domain.LiveAircraftState)}
}

type testRegistry struct {
	states map[string]domain.LiveAircraftState
}

func (r *testRegistry) SetLiveAircraftState(_ context.Context, state domain.LiveAircraftState) error {
	r.states[state.AircraftID] = state
	return nil
}

func (r *testRegistry) GetLiveAircraftState(_ context.Context, aircraftID string) (*domain.LiveAircraftState, error) {
	state, ok := r.states[aircraftID]
	if !ok {
		return nil, nil
	}
	return &state, nil
}

func (r *testRegistry) ListLiveAircraftStates(context.Context) ([]domain.LiveAircraftState, error) {
	states := make([]domain.LiveAircraftState, 0, len(r.states))
	for _, state := range r.states {
		states = append(states, state)
	}
	return states, nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
