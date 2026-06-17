package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/readmodel"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/Aero-Arc/aero-arc-api/internal/store/replay"
	"github.com/Aero-Arc/aero-arc-api/internal/store/telemetry"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type FleetService struct {
	durable   durable.Store
	telemetry telemetry.Store
	replay    replay.Store
	registry  registryv1.AeroRegistryClient
}

type ReplayResponse struct {
	Flight            domain.FlightRecord       `json:"flight"`
	ReplayManifest    *domain.ReplayManifest    `json:"replay_manifest,omitempty"`
	Samples           []domain.TelemetrySample  `json:"samples"`
	ConformanceEvents []domain.ConformanceEvent `json:"conformance_events"`
}

func NewFleetService(durableStore durable.Store, telemetryStore telemetry.Store, replayStore replay.Store, registry registryv1.AeroRegistryClient) *FleetService {
	return &FleetService{
		durable:   durableStore,
		telemetry: telemetryStore,
		replay:    replayStore,
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

func (s *FleetService) GetOverviewDashboard(ctx context.Context) (readmodel.OverviewDashboard, error) {
	aircraft, err := s.ListAircraftDashboards(ctx)
	if err != nil {
		return readmodel.OverviewDashboard{}, err
	}
	intents, err := s.durable.ListOperationalIntents(ctx, "")
	if err != nil {
		return readmodel.OverviewDashboard{}, fmt.Errorf("list operational intents: %w", err)
	}
	evidence, err := s.durable.ListEvidence(ctx, "")
	if err != nil {
		return readmodel.OverviewDashboard{}, fmt.Errorf("list evidence: %w", err)
	}
	reviews, err := s.durable.ListReportabilityReviews(ctx, "")
	if err != nil {
		return readmodel.OverviewDashboard{}, fmt.Errorf("list reportability reviews: %w", err)
	}

	return readmodel.OverviewDashboard{
		Metrics:             overviewMetrics(aircraft, intents, evidence, reviews),
		Aircraft:            aircraft,
		OperationalIntents:  intents,
		EvidenceRecords:     evidence,
		ReportabilityReview: reviews,
	}, nil
}

func (s *FleetService) GetOperationsDashboard(ctx context.Context) (readmodel.OperationsDashboard, error) {
	intents, err := s.durable.ListOperationalIntents(ctx, "")
	if err != nil {
		return readmodel.OperationsDashboard{}, fmt.Errorf("list operational intents: %w", err)
	}
	conformance, err := s.durable.ListConformanceSummaries(ctx, "")
	if err != nil {
		return readmodel.OperationsDashboard{}, fmt.Errorf("list conformance summaries: %w", err)
	}

	return readmodel.OperationsDashboard{
		Metrics:            operationsMetrics(intents, conformance),
		OperationalIntents: intents,
		Conformance:        conformance,
	}, nil
}

func (s *FleetService) GetPreflightDashboard(ctx context.Context) (readmodel.PreflightDashboard, error) {
	checks, err := s.durable.ListPreflightChecks(ctx, "")
	if err != nil {
		return readmodel.PreflightDashboard{}, fmt.Errorf("list preflight checks: %w", err)
	}

	return readmodel.PreflightDashboard{
		Metrics: preflightMetrics(checks),
		Checks:  checks,
	}, nil
}

func (s *FleetService) GetConformanceDashboard(ctx context.Context) (readmodel.ConformanceDashboard, error) {
	summaries, err := s.durable.ListConformanceSummaries(ctx, "")
	if err != nil {
		return readmodel.ConformanceDashboard{}, fmt.Errorf("list conformance summaries: %w", err)
	}
	events, err := s.durable.ListConformanceEvents(ctx, "")
	if err != nil {
		return readmodel.ConformanceDashboard{}, fmt.Errorf("list conformance events: %w", err)
	}

	return readmodel.ConformanceDashboard{
		Metrics:   conformanceMetrics(summaries, events),
		Summaries: summaries,
		Events:    events,
	}, nil
}

func (s *FleetService) GetMaintenanceDashboard(ctx context.Context) (readmodel.MaintenanceDashboard, error) {
	events, err := s.durable.ListMaintenanceEvents(ctx, "")
	if err != nil {
		return readmodel.MaintenanceDashboard{}, fmt.Errorf("list maintenance events: %w", err)
	}
	batteries, err := s.durable.ListBatteries(ctx)
	if err != nil {
		return readmodel.MaintenanceDashboard{}, fmt.Errorf("list batteries: %w", err)
	}

	return readmodel.MaintenanceDashboard{
		Metrics:   maintenanceMetrics(events, batteries),
		Events:    events,
		Batteries: batteries,
	}, nil
}

func (s *FleetService) GetRecordsDashboard(ctx context.Context) (readmodel.RecordsDashboard, error) {
	evidence, err := s.durable.ListEvidence(ctx, "")
	if err != nil {
		return readmodel.RecordsDashboard{}, fmt.Errorf("list evidence: %w", err)
	}
	reviews, err := s.durable.ListReportabilityReviews(ctx, "")
	if err != nil {
		return readmodel.RecordsDashboard{}, fmt.Errorf("list reportability reviews: %w", err)
	}

	return readmodel.RecordsDashboard{
		Metrics:             recordsMetrics(evidence, reviews),
		EvidenceRecords:     evidence,
		ReportabilityReview: reviews,
	}, nil
}

func (s *FleetService) ListAircraftDashboards(ctx context.Context) ([]readmodel.AircraftDashboard, error) {
	aircraft, err := s.durable.ListAircraft(ctx)
	if err != nil {
		return nil, fmt.Errorf("list aircraft: %w", err)
	}

	dashboards := make([]readmodel.AircraftDashboard, 0, len(aircraft))
	for _, item := range aircraft {
		dashboard, err := s.buildDashboard(ctx, item)
		if err != nil {
			return nil, err
		}
		dashboards = append(dashboards, dashboard)
	}
	return dashboards, nil
}

func (s *FleetService) GetAircraftDashboard(ctx context.Context, aircraftID string) (readmodel.AircraftDashboard, error) {
	aircraft, err := s.durable.GetAircraft(ctx, aircraftID)
	if err != nil {
		return readmodel.AircraftDashboard{}, fmt.Errorf("get aircraft: %w", err)
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

func (s *FleetService) buildDashboard(ctx context.Context, aircraft domain.Aircraft) (readmodel.AircraftDashboard, error) {
	battery, err := s.activeBattery(ctx, aircraft.ID)
	if err != nil {
		return readmodel.AircraftDashboard{}, err
	}

	maintenanceEvents, err := s.durable.ListMaintenanceEvents(ctx, aircraft.ID)
	if err != nil {
		return readmodel.AircraftDashboard{}, fmt.Errorf("list maintenance events: %w", err)
	}

	latestTelemetry, _ := s.telemetry.GetLatestSample(ctx, aircraft.ID)
	liveState, liveAvailable := s.liveState(ctx, aircraft)

	return readmodel.AircraftDashboard{
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
		if errors.Is(err, durable.ErrNotFound) {
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

	agents, err := s.registry.ListAgents(ctx, &registryv1.ListAgentsRequest{})
	if err != nil {
		return nil, false
	}

	var agent *registryv1.Agent
	for _, item := range agents.GetAgents() {
		if item.GetAgentId() == agentID {
			agent = item
			break
		}
	}
	if agent == nil {
		return nil, false
	}

	state := &domain.LiveAircraftState{
		AircraftID:      aircraft.ID,
		AgentID:         agent.GetAgentId(),
		Connected:       true,
		LastHeartbeatAt: unixMillis(agent.GetLastHeartbeatUnixMs()),
		LastConnectedAt: unixMillis(agent.GetLastHeartbeatUnixMs()),
	}

	placement, err := s.registry.GetAgentPlacement(ctx, &registryv1.GetAgentPlacementRequest{AgentId: agent.GetAgentId()})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return state, true
		}
		return nil, false
	}
	if placement.GetPlacement() != nil {
		state.RelayID = placement.GetPlacement().GetRelayId()
		state.PlacementLastUpdatedAt = unixMillis(placement.GetPlacement().GetLastUpdatedUnixMs())
	}

	return state, true
}

func CalculateReadiness(battery *domain.Battery, maintenanceEvents []domain.MaintenanceEvent, liveStateAvailable bool) domain.Readiness {
	reasons := make([]string, 0)
	for _, event := range maintenanceEvents {
		if strings.EqualFold(string(event.Severity), string(domain.SeverityCritical)) && event.ResolvedAt == nil {
			reasons = append(reasons, "open critical maintenance event")
		}
	}
	if len(reasons) > 0 {
		return domain.Readiness{Status: "blocked", Reasons: reasons}
	}

	if battery == nil {
		reasons = append(reasons, "battery missing")
	} else if battery.StateOfHealth == nil {
		reasons = append(reasons, "battery state of health unknown")
	} else if battery.StateOfHealth != nil && *battery.StateOfHealth < 80 {
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

func overviewMetrics(aircraft []readmodel.AircraftDashboard, intents []domain.OperationalIntent, evidence []domain.EvidenceRecord, reviews []domain.ReportabilityReview) []readmodel.DashboardMetric {
	readyAircraft := 0
	openBlocks := 0
	for _, item := range aircraft {
		if item.Readiness.Status == domain.ReadinessStatusReady {
			readyAircraft++
		}
		if item.Readiness.Status == domain.ReadinessStatusBlocked {
			openBlocks++
		}
	}

	activeIntents := 0
	for _, intent := range intents {
		if intent.Status == domain.IntentStatusActive || intent.Status == domain.IntentStatusAccepted {
			activeIntents++
		}
	}

	reportable := 0
	for _, review := range reviews {
		if review.Status == domain.ReportabilityStatusReportable {
			reportable++
		}
	}

	return []readmodel.DashboardMetric{
		{Label: "Ready aircraft", Value: fmt.Sprintf("%d/%d", readyAircraft, len(aircraft)), Status: string(domain.ReadinessStatusReady)},
		{Label: "Accepted/active intents", Value: fmt.Sprintf("%d", activeIntents), Status: string(domain.IntentStatusAccepted)},
		{Label: "Open preflight blocks", Value: fmt.Sprintf("%d", openBlocks), Status: string(domain.ReadinessStatusBlocked)},
		{Label: "Evidence records", Value: fmt.Sprintf("%d", len(evidence))},
		{Label: "Reportable events", Value: fmt.Sprintf("%d", reportable), Status: string(domain.ReportabilityStatusReportable)},
	}
}

func operationsMetrics(intents []domain.OperationalIntent, conformance []domain.ConformanceSummary) []readmodel.DashboardMetric {
	accepted := 0
	submitted := 0
	conformanceRequired := 0
	for _, intent := range intents {
		switch intent.Status {
		case domain.IntentStatusAccepted:
			accepted++
		case domain.IntentStatusSubmitted, domain.IntentStatusReview:
			submitted++
		}
		if intent.ConformanceRequired {
			conformanceRequired++
		}
	}

	nonConforming := 0
	for _, summary := range conformance {
		if summary.Status == domain.ConformanceStatusNonConforming {
			nonConforming++
		}
	}

	return []readmodel.DashboardMetric{
		{Label: "Operational intents", Value: fmt.Sprintf("%d", len(intents))},
		{Label: "Accepted", Value: fmt.Sprintf("%d", accepted), Status: string(domain.IntentStatusAccepted)},
		{Label: "Submitted/review", Value: fmt.Sprintf("%d", submitted), Status: string(domain.IntentStatusSubmitted)},
		{Label: "Conformance required", Value: fmt.Sprintf("%d", conformanceRequired), Detail: fmt.Sprintf("%d non-conforming", nonConforming)},
	}
}

func preflightMetrics(checks []domain.PreflightCheck) []readmodel.DashboardMetric {
	clear := 0
	review := 0
	blocked := 0
	for _, check := range checks {
		switch check.Status {
		case domain.PreflightStatusClear:
			clear++
		case domain.PreflightStatusReview:
			review++
		case domain.PreflightStatusAction, domain.PreflightStatusBlocked:
			blocked++
		}
	}

	return []readmodel.DashboardMetric{
		{Label: "Checks complete", Value: fmt.Sprintf("%d/%d", clear, len(checks)), Status: string(domain.PreflightStatusClear)},
		{Label: "Needs review", Value: fmt.Sprintf("%d", review), Status: string(domain.PreflightStatusReview)},
		{Label: "Blocking items", Value: fmt.Sprintf("%d", blocked), Status: string(domain.PreflightStatusBlocked)},
	}
}

func conformanceMetrics(summaries []domain.ConformanceSummary, events []domain.ConformanceEvent) []readmodel.DashboardMetric {
	conforming := 0
	reportable := 0
	totalScore := 0.0
	scores := 0
	for _, summary := range summaries {
		if summary.Status == domain.ConformanceStatusConforming {
			conforming++
		}
		if summary.ReportabilityStatus == domain.ReportabilityStatusReportable {
			reportable++
		}
		if summary.Score != nil {
			totalScore += *summary.Score
			scores++
		}
	}

	avgScore := 0.0
	if scores > 0 {
		avgScore = totalScore / float64(scores)
	}

	return []readmodel.DashboardMetric{
		{Label: "Target conformance", Value: fmt.Sprintf("%.1f%%", avgScore*100)},
		{Label: "Conforming", Value: fmt.Sprintf("%d", conforming), Status: string(domain.ConformanceStatusConforming)},
		{Label: "Open alerts", Value: fmt.Sprintf("%d", len(events))},
		{Label: "Reportable", Value: fmt.Sprintf("%d", reportable), Status: string(domain.ReportabilityStatusReportable)},
	}
}

func maintenanceMetrics(events []domain.MaintenanceEvent, batteries []domain.Battery) []readmodel.DashboardMetric {
	openEvents := 0
	critical := 0
	for _, event := range events {
		if event.Status != domain.MaintenanceStatusClosed && event.ResolvedAt == nil {
			openEvents++
		}
		if event.Severity == domain.SeverityCritical && event.ResolvedAt == nil {
			critical++
		}
	}

	avgSOH := 0.0
	batteriesWithSOH := 0
	if len(batteries) > 0 {
		total := 0.0
		for _, battery := range batteries {
			if battery.StateOfHealth != nil {
				total += *battery.StateOfHealth
				batteriesWithSOH++
			}
		}
		if batteriesWithSOH > 0 {
			avgSOH = total / float64(batteriesWithSOH)
		}
	}

	return []readmodel.DashboardMetric{
		{Label: "Open irregularities", Value: fmt.Sprintf("%d", openEvents)},
		{Label: "Critical", Value: fmt.Sprintf("%d", critical), Status: string(domain.SeverityCritical)},
		{Label: "Battery SOH avg", Value: fmt.Sprintf("%.0f%%", avgSOH)},
		{Label: "Battery packs", Value: fmt.Sprintf("%d", len(batteries))},
	}
}

func recordsMetrics(evidence []domain.EvidenceRecord, reviews []domain.ReportabilityReview) []readmodel.DashboardMetric {
	pending := 0
	reportable := 0
	for _, record := range evidence {
		if record.Status == domain.EvidenceStatusOpen || record.Status == domain.EvidenceStatusReview {
			pending++
		}
	}
	for _, review := range reviews {
		if review.Status == domain.ReportabilityStatusReportable {
			reportable++
		}
	}

	return []readmodel.DashboardMetric{
		{Label: "Evidence records", Value: fmt.Sprintf("%d", len(evidence))},
		{Label: "Pending reviews", Value: fmt.Sprintf("%d", pending), Status: string(domain.EvidenceStatusReview)},
		{Label: "Reportable events", Value: fmt.Sprintf("%d", reportable), Status: string(domain.ReportabilityStatusReportable)},
	}
}

func unixMillis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
