package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	durable            durable.Store
	telemetry          telemetry.Store
	replay             replay.Store
	registry           registryv1.AeroRegistryClient
	now                func() time.Time
	registryFreshness  time.Duration
	telemetryFreshness time.Duration
}

const (
	defaultRegistryFreshness  = 30 * time.Second
	defaultTelemetryFreshness = 15 * time.Second
)

type ReplayResponse struct {
	Flight            domain.FlightRecord       `json:"flight"`
	ReplayManifest    *domain.ReplayManifest    `json:"replay_manifest,omitempty"`
	Samples           []domain.TelemetrySample  `json:"samples"`
	ConformanceEvents []domain.ConformanceEvent `json:"conformance_events"`
}

func NewFleetService(durableStore durable.Store, telemetryStore telemetry.Store, replayStore replay.Store, registry registryv1.AeroRegistryClient) *FleetService {
	return &FleetService{
		durable:            durableStore,
		telemetry:          telemetryStore,
		replay:             replayStore,
		registry:           registry,
		now:                time.Now,
		registryFreshness:  defaultRegistryFreshness,
		telemetryFreshness: defaultTelemetryFreshness,
	}
}

func (s *FleetService) WithLiveStatePolicy(registryFreshness, telemetryFreshness time.Duration, now func() time.Time) *FleetService {
	if registryFreshness > 0 {
		s.registryFreshness = registryFreshness
	}
	if telemetryFreshness > 0 {
		s.telemetryFreshness = telemetryFreshness
	}
	if now != nil {
		s.now = now
	}
	return s
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
	aircraft, err := s.durable.ListAircraft(ctx)
	if err != nil {
		return readmodel.OperationsDashboard{}, fmt.Errorf("list aircraft: %w", err)
	}
	liveAircraft := s.composeLiveAircraft(ctx, aircraft)

	return readmodel.OperationsDashboard{
		Metrics:            operationsMetrics(intents, conformance),
		OperationalIntents: intents,
		Conformance:        conformance,
		LiveAircraft:       liveAircraft,
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

	liveAircraft := s.composeLiveAircraft(ctx, aircraft)
	dashboards := make([]readmodel.AircraftDashboard, 0, len(aircraft))
	for _, item := range aircraft {
		live := liveAircraftForID(liveAircraft, item.ID)
		dashboard, err := s.buildDashboard(ctx, item, live)
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
	live := s.composeLiveAircraft(ctx, []domain.Aircraft{aircraft})[0]
	return s.buildDashboard(ctx, aircraft, live)
}

func (s *FleetService) GetAircraftLiveState(ctx context.Context, aircraftID string) (readmodel.AircraftLiveState, error) {
	aircraft, err := s.durable.GetAircraft(ctx, aircraftID)
	if err != nil {
		return readmodel.AircraftLiveState{}, fmt.Errorf("get aircraft: %w", err)
	}
	return s.composeLiveAircraft(ctx, []domain.Aircraft{aircraft})[0], nil
}

func (s *FleetService) GetAircraftMapView(ctx context.Context, aircraftID string, limit int) (readmodel.AircraftMapView, error) {
	aircraft, err := s.durable.GetAircraft(ctx, aircraftID)
	if err != nil {
		return readmodel.AircraftMapView{}, fmt.Errorf("get aircraft: %w", err)
	}

	live := s.composeLiveAircraft(ctx, []domain.Aircraft{aircraft})[0]
	latestTelemetry := legacySample(aircraft.ID, live.Telemetry)
	liveState := &live.Connection
	liveAvailable := live.Connection.Connected

	replaySamples, err := s.telemetry.QueryAircraftSamples(ctx, aircraft.ID, limit)
	if err != nil {
		return readmodel.AircraftMapView{}, fmt.Errorf("query aircraft samples: %w", err)
	}

	view := readmodel.AircraftMapView{
		Aircraft:           aircraft,
		LiveState:          liveState,
		LiveStateAvailable: liveAvailable,
		LatestTelemetry:    latestTelemetry,
		Telemetry:          live.Telemetry,
		ReplaySamples:      replaySamples,
		OperationalVolumes: make([]domain.OperationalVolume, 0),
		ConformanceEvents:  make([]domain.ConformanceEvent, 0),
	}

	intent, err := s.activeIntent(ctx, aircraft.ID)
	if err != nil {
		return readmodel.AircraftMapView{}, err
	}
	if intent == nil {
		return view, nil
	}
	view.ActiveIntent = intent

	volumes, err := s.durable.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		return readmodel.AircraftMapView{}, fmt.Errorf("list operational volumes: %w", err)
	}
	view.OperationalVolumes = volumesForVersion(volumes, intent.Version)

	summary, err := conformanceSummaryForVersion(ctx, s.durable, *intent)
	if err != nil {
		return readmodel.AircraftMapView{}, err
	}
	view.ConformanceSummary = summary

	events, err := s.durable.ListConformanceEvents(ctx, "")
	if err != nil {
		return readmodel.AircraftMapView{}, fmt.Errorf("list conformance events: %w", err)
	}
	for _, event := range events {
		if event.IntentID == intent.ID && event.IntentVersion == intent.Version {
			view.ConformanceEvents = append(view.ConformanceEvents, event)
		}
	}

	return view, nil
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

func (s *FleetService) buildDashboard(ctx context.Context, aircraft domain.Aircraft, live readmodel.AircraftLiveState) (readmodel.AircraftDashboard, error) {
	battery, err := s.activeBattery(ctx, aircraft.ID)
	if err != nil {
		return readmodel.AircraftDashboard{}, err
	}

	maintenanceEvents, err := s.durable.ListMaintenanceEvents(ctx, aircraft.ID)
	if err != nil {
		return readmodel.AircraftDashboard{}, fmt.Errorf("list maintenance events: %w", err)
	}

	latestTelemetry := legacySample(aircraft.ID, live.Telemetry)
	liveState := &live.Connection
	liveAvailable := live.Connection.Connected
	currentIntent, err := s.currentIntent(ctx, aircraft.ID)
	if err != nil {
		return readmodel.AircraftDashboard{}, err
	}

	return readmodel.AircraftDashboard{
		Aircraft:           aircraft,
		ActiveBattery:      battery,
		MaintenanceEvents:  maintenanceEvents,
		LatestTelemetry:    latestTelemetry,
		Telemetry:          live.Telemetry,
		LiveState:          liveState,
		LiveStateAvailable: liveAvailable,
		Readiness:          CalculateReadiness(battery, maintenanceEvents, liveAvailable),
		CurrentIntent:      currentIntent,
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

func (s *FleetService) activeIntent(ctx context.Context, aircraftID string) (*domain.OperationalIntent, error) {
	return s.intentByStatus(ctx, aircraftID, domain.IntentStatusActive)
}

func (s *FleetService) currentIntent(ctx context.Context, aircraftID string) (*domain.OperationalIntent, error) {
	intent, err := s.intentByStatus(ctx, aircraftID, domain.IntentStatusActive)
	if err != nil || intent != nil {
		return intent, err
	}
	return s.intentByStatus(ctx, aircraftID, domain.IntentStatusAccepted)
}

func (s *FleetService) intentByStatus(ctx context.Context, aircraftID string, status domain.IntentStatus) (*domain.OperationalIntent, error) {
	intents, err := s.durable.ListOperationalIntents(ctx, aircraftID)
	if err != nil {
		return nil, fmt.Errorf("list operational intents: %w", err)
	}
	matching := make([]domain.OperationalIntent, 0)
	for _, intent := range intents {
		if intent.Status == status {
			matching = append(matching, intent)
		}
	}
	if len(matching) == 0 {
		return nil, nil
	}
	sort.Slice(matching, func(i, j int) bool {
		if matching[i].PlannedStartAt.Equal(matching[j].PlannedStartAt) {
			return matching[i].UpdatedAt.After(matching[j].UpdatedAt)
		}
		return matching[i].PlannedStartAt.After(matching[j].PlannedStartAt)
	})
	return &matching[0], nil
}

func (s *FleetService) composeLiveAircraft(ctx context.Context, aircraft []domain.Aircraft) []readmodel.AircraftLiveState {
	result := make([]readmodel.AircraftLiveState, len(aircraft))
	telemetryIDs := make([]string, 0, len(aircraft))
	hasMappings := false
	for index, item := range aircraft {
		result[index] = readmodel.AircraftLiveState{
			AircraftID: item.ID, OperatorID: item.OperatorID, AgentID: item.AgentID,
			Connection: domain.LiveAircraftState{AircraftID: item.ID, OperatorID: item.OperatorID, AgentID: item.AgentID, ConnectionStatus: domain.ConnectionStatusUnmapped},
			Telemetry:  domain.AircraftTelemetryState{Status: domain.DataFreshnessMissing},
		}
		telemetryIDs = append(telemetryIDs, item.ID)
		hasMappings = hasMappings || item.AgentID != ""
	}

	telemetryStates, telemetryErr := s.telemetry.GetLatestAircraftStates(ctx, telemetryIDs)
	for index := range result {
		if telemetryErr != nil {
			result[index].Telemetry.Status = domain.DataFreshnessUnavailable
			continue
		}
		state := telemetryStates[result[index].AircraftID]
		s.applyTelemetryFreshness(&state)
		result[index].Telemetry = state
	}
	if !hasMappings {
		return result
	}

	agentResponse, err := s.registry.ListAgents(ctx, &registryv1.ListAgentsRequest{})
	if err != nil {
		for index := range result {
			if result[index].AgentID != "" {
				result[index].Connection.ConnectionStatus = domain.ConnectionStatusUnavailable
			}
		}
		return result
	}
	agents := make(map[string]*registryv1.Agent, len(agentResponse.GetAgents()))
	for _, agent := range agentResponse.GetAgents() {
		if agent != nil && agent.GetAgentId() != "" {
			agents[agent.GetAgentId()] = agent
		}
	}
	for index := range result {
		state := &result[index].Connection
		if state.AgentID == "" {
			continue
		}
		state.ConnectionStatus = domain.ConnectionStatusOffline
		agent := agents[state.AgentID]
		if agent == nil {
			continue
		}
		state.LastHeartbeatAt = unixMillis(agent.GetLastHeartbeatUnixMs())
		state.LastConnectedAt = state.LastHeartbeatAt
		if freshAt(state.LastHeartbeatAt, s.now().UTC(), s.registryFreshness) {
			state.Connected = true
			state.ConnectionStatus = domain.ConnectionStatusConnected
		} else {
			state.ConnectionStatus = domain.ConnectionStatusStale
		}
		placement, placementErr := s.registry.GetAgentPlacement(ctx, &registryv1.GetAgentPlacementRequest{AgentId: state.AgentID})
		if placementErr != nil {
			state.Connected = false
			if status.Code(placementErr) == codes.NotFound {
				state.ConnectionStatus = domain.ConnectionStatusOffline
			} else {
				state.ConnectionStatus = domain.ConnectionStatusUnavailable
			}
			continue
		}
		if placement.GetPlacement() == nil || placement.GetPlacement().GetRelayId() == "" {
			state.Connected = false
			state.ConnectionStatus = domain.ConnectionStatusOffline
			continue
		}
		state.RelayID = placement.GetPlacement().GetRelayId()
		state.PlacementLastUpdatedAt = unixMillis(placement.GetPlacement().GetLastUpdatedUnixMs())
	}
	return result
}

func (s *FleetService) applyTelemetryFreshness(state *domain.AircraftTelemetryState) {
	now := s.now().UTC()
	observations := []*domain.TelemetryObservation{}
	if state.Position != nil {
		observations = append(observations, &state.Position.TelemetryObservation)
	}
	if state.Battery != nil {
		observations = append(observations, &state.Battery.TelemetryObservation)
	}
	if state.Vehicle != nil {
		observations = append(observations, &state.Vehicle.TelemetryObservation)
	}
	if state.System != nil {
		observations = append(observations, &state.System.TelemetryObservation)
	}
	if state.HUD != nil {
		observations = append(observations, &state.HUD.TelemetryObservation)
	}
	if state.ExtendedState != nil {
		observations = append(observations, &state.ExtendedState.TelemetryObservation)
	}
	if state.GPS != nil {
		observations = append(observations, &state.GPS.TelemetryObservation)
	}
	if len(observations) == 0 {
		state.Status = domain.DataFreshnessMissing
		return
	}
	state.Status = domain.DataFreshnessStale
	for _, observation := range observations {
		observation.Status = domain.DataFreshnessStale
		if freshAt(observation.RecordedAt, now, s.telemetryFreshness) {
			observation.Status = domain.DataFreshnessFresh
		}
		if state.LastObservedAt == nil || observation.RecordedAt.After(*state.LastObservedAt) {
			observedAt := observation.RecordedAt
			state.LastObservedAt = &observedAt
		}
	}
	if state.LastObservedAt != nil && freshAt(*state.LastObservedAt, now, s.telemetryFreshness) {
		state.Status = domain.DataFreshnessFresh
	}
}

func freshAt(observedAt, now time.Time, freshness time.Duration) bool {
	return !observedAt.IsZero() && !observedAt.Before(now.Add(-freshness)) && !observedAt.After(now.Add(freshness))
}

func legacySample(aircraftID string, telemetry domain.AircraftTelemetryState) *domain.TelemetrySample {
	if telemetry.Position == nil {
		return nil
	}
	position := telemetry.Position
	sample := &domain.TelemetrySample{ID: position.FrameID, AircraftID: aircraftID, RecordedAt: position.RecordedAt,
		OperatorID: position.OperatorID, IntentID: position.IntentID, IntentVersion: position.IntentVersion, FlightID: position.FlightID,
		Latitude: position.LatitudeDeg, Longitude: position.LongitudeDeg}
	if position.AltitudeMSLM != nil {
		sample.AltitudeM = *position.AltitudeMSLM
	}
	if position.GroundspeedMPS != nil {
		sample.VelocityMPS = *position.GroundspeedMPS
	}
	if position.HeadingDeg != nil {
		sample.HeadingDeg = *position.HeadingDeg
	}
	if telemetry.Battery != nil && telemetry.Battery.RecordedAt.Equal(position.RecordedAt) && telemetry.Battery.FrameID == position.FrameID {
		sample.BatteryPct = telemetry.Battery.BatteryRemainingPct
	}
	return sample
}

func liveAircraftForID(states []readmodel.AircraftLiveState, aircraftID string) readmodel.AircraftLiveState {
	for _, state := range states {
		if state.AircraftID == aircraftID {
			return state
		}
	}
	return readmodel.AircraftLiveState{AircraftID: aircraftID,
		Connection: domain.LiveAircraftState{AircraftID: aircraftID, ConnectionStatus: domain.ConnectionStatusUnavailable},
		Telemetry:  domain.AircraftTelemetryState{Status: domain.DataFreshnessUnavailable}}
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
