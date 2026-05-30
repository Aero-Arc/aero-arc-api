package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	"github.com/Aero-Arc/aero-arc-api/internal/store"
)

type FleetService struct {
	durable   store.DurableStore
	telemetry store.TelemetryStore
	replay    store.ReplayStore
	registry  registry.Client
}

type ReplayResponse struct {
	Flight            domain.FlightRecord       `json:"flight"`
	ReplayManifest    *domain.ReplayManifest    `json:"replay_manifest,omitempty"`
	Samples           []domain.TelemetrySample  `json:"samples"`
	ConformanceEvents []domain.ConformanceEvent `json:"conformance_events"`
}

func NewFleetService(durable store.DurableStore, telemetry store.TelemetryStore, replay store.ReplayStore, registry registry.Client) *FleetService {
	return &FleetService{
		durable:   durable,
		telemetry: telemetry,
		replay:    replay,
		registry:  registry,
	}
}

func (s *FleetService) CreateAircraft(ctx context.Context, aircraft domain.Aircraft) error {
	return s.durable.CreateAircraft(ctx, aircraft)
}

func (s *FleetService) CreateBattery(ctx context.Context, battery domain.Battery) error {
	return s.durable.CreateBattery(ctx, battery)
}

func (s *FleetService) RecordMaintenanceEvent(ctx context.Context, event domain.MaintenanceEvent) error {
	return s.durable.RecordMaintenanceEvent(ctx, event)
}

func (s *FleetService) ListAircraftDashboards(ctx context.Context) ([]domain.AircraftDashboard, error) {
	aircraft, err := s.durable.ListAircraft(ctx)
	if err != nil {
		return nil, fmt.Errorf("list aircraft: %w", err)
	}

	dashboards := make([]domain.AircraftDashboard, 0, len(aircraft))
	for _, item := range aircraft {
		dashboard, err := s.buildDashboard(ctx, item)
		if err != nil {
			return nil, err
		}
		dashboards = append(dashboards, dashboard)
	}
	return dashboards, nil
}

func (s *FleetService) GetAircraftDashboard(ctx context.Context, aircraftID string) (domain.AircraftDashboard, error) {
	aircraft, err := s.durable.GetAircraft(ctx, aircraftID)
	if err != nil {
		return domain.AircraftDashboard{}, fmt.Errorf("get aircraft: %w", err)
	}
	return s.buildDashboard(ctx, aircraft)
}

func (s *FleetService) ListFlightRecords(ctx context.Context, aircraftID string) ([]domain.FlightRecord, error) {
	if _, err := s.durable.GetAircraft(ctx, aircraftID); err != nil {
		return nil, fmt.Errorf("get aircraft: %w", err)
	}
	flights, err := s.durable.ListFlightRecords(ctx, aircraftID)
	if err != nil {
		return nil, fmt.Errorf("list flight records: %w", err)
	}
	return flights, nil
}

func (s *FleetService) GetFlightRecord(ctx context.Context, flightID string) (domain.FlightRecord, error) {
	flight, err := s.durable.GetFlightRecord(ctx, flightID)
	if err != nil {
		return domain.FlightRecord{}, fmt.Errorf("get flight record: %w", err)
	}
	return flight, nil
}

func (s *FleetService) GetFlightReplay(ctx context.Context, flightID string, limit int) (ReplayResponse, error) {
	flight, err := s.durable.GetFlightRecord(ctx, flightID)
	if err != nil {
		return ReplayResponse{}, fmt.Errorf("get flight record: %w", err)
	}

	manifest, err := s.replay.GetReplayManifest(ctx, flightID)
	if err != nil {
		return ReplayResponse{}, fmt.Errorf("get replay manifest: %w", err)
	}

	samples, err := s.telemetry.QueryFlightSamples(ctx, flightID, limit)
	if err != nil {
		return ReplayResponse{}, fmt.Errorf("query flight samples: %w", err)
	}

	events, err := s.durable.ListConformanceEvents(ctx, flightID)
	if err != nil {
		return ReplayResponse{}, fmt.Errorf("list conformance events: %w", err)
	}

	return ReplayResponse{
		Flight:            flight,
		ReplayManifest:    manifest,
		Samples:           samples,
		ConformanceEvents: events,
	}, nil
}

func (s *FleetService) buildDashboard(ctx context.Context, aircraft domain.Aircraft) (domain.AircraftDashboard, error) {
	battery, err := s.activeBattery(ctx, aircraft.ID)
	if err != nil {
		return domain.AircraftDashboard{}, err
	}

	maintenanceEvents, err := s.durable.ListMaintenanceEvents(ctx, aircraft.ID)
	if err != nil {
		return domain.AircraftDashboard{}, fmt.Errorf("list maintenance events: %w", err)
	}

	latestTelemetry, _ := s.telemetry.GetLatestSample(ctx, aircraft.ID)
	liveState, liveAvailable := s.liveState(ctx, aircraft)

	return domain.AircraftDashboard{
		Aircraft:           aircraft,
		ActiveBattery:      battery,
		MaintenanceEvents:  maintenanceEvents,
		LatestTelemetry:    latestTelemetry,
		LiveState:          liveState,
		LiveStateAvailable: liveAvailable,
		Readiness:          CalculateReadiness(battery, maintenanceEvents, liveAvailable),
	}, nil
}

func (s *FleetService) activeBattery(ctx context.Context, aircraftID string) (*domain.Battery, error) {
	installation, err := s.durable.GetActiveBatteryInstallation(ctx, aircraftID)
	if err != nil {
		return nil, fmt.Errorf("get active battery installation: %w", err)
	}
	if installation == nil {
		return nil, nil
	}

	battery, err := s.durable.GetBattery(ctx, installation.BatteryID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get active battery: %w", err)
	}
	return &battery, nil
}

func (s *FleetService) liveState(ctx context.Context, aircraft domain.Aircraft) (*domain.LiveAircraftState, bool) {
	agentID := aircraft.AgentID
	if agentID == "" {
		agentID = aircraft.ID
	}

	state, err := s.registry.GetLiveAircraftState(ctx, agentID)
	if err != nil || state == nil {
		return nil, false
	}
	state.AircraftID = aircraft.ID
	return state, true
}

func CalculateReadiness(battery *domain.Battery, maintenanceEvents []domain.MaintenanceEvent, liveStateAvailable bool) domain.Readiness {
	reasons := make([]string, 0)
	for _, event := range maintenanceEvents {
		if strings.EqualFold(event.Severity, "critical") && event.ResolvedAt == nil {
			reasons = append(reasons, "open critical maintenance event")
		}
	}
	if len(reasons) > 0 {
		return domain.Readiness{Status: "blocked", Reasons: reasons}
	}

	if battery == nil {
		reasons = append(reasons, "battery missing")
	} else if battery.StateOfHealth < 80 {
		reasons = append(reasons, "battery state of health below 80")
	}
	if !liveStateAvailable {
		reasons = append(reasons, "live state unavailable")
	}

	if len(reasons) > 0 {
		return domain.Readiness{Status: "warning", Reasons: reasons}
	}
	if battery != nil && liveStateAvailable {
		return domain.Readiness{Status: "ready", Reasons: nil}
	}
	return domain.Readiness{Status: "unknown", Reasons: []string{"not enough data"}}
}
