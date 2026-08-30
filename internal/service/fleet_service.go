package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/readmodel"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/Aero-Arc/aero-arc-api/internal/store/replay"
	"github.com/Aero-Arc/aero-arc-api/internal/store/telemetry"
	conformancev1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/conformance/v1"
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
	missionDeployer    MissionDeployer
}

const (
	defaultRegistryFreshness  = 30 * time.Second
	defaultTelemetryFreshness = 15 * time.Second
	maxPlacementLookups       = 8
	maxConformanceBatch       = 250
)

type placementLookup struct {
	resultIndex int
	agentID     string
}

type placementLookupResult struct {
	response *registryv1.GetAgentPlacementResponse
	err      error
}

type ReplayResponse struct {
	Flight            domain.FlightRecord       `json:"flight"`
	ReplayManifest    *domain.ReplayManifest    `json:"replay_manifest,omitempty"`
	Samples           []domain.TelemetrySample  `json:"samples"`
	ConformanceEvents []domain.ConformanceEvent `json:"conformance_events"`
}

type InstallBatteryRequest struct {
	ID          string    `json:"id"`
	OperatorID  string    `json:"operator_id,omitempty"`
	BatteryID   string    `json:"battery_id"`
	InstalledAt time.Time `json:"installed_at,omitempty"`
}

type CreateFlightRequest struct {
	ID           string `json:"id"`
	OperatorID   string `json:"operator_id,omitempty"`
	Origin       string `json:"origin,omitempty"`
	Destination  string `json:"destination,omitempty"`
	MissionType  string `json:"mission_type,omitempty"`
	TelemetryURI string `json:"telemetry_uri,omitempty"`
}

// NewFleetService constructs the fleet read/write façade used by HTTP handlers.
// Dashboard requests are composed from durable business records, time-series
// telemetry, replay manifests, and ephemeral Registry placement.
//
// Parameters:
//   - durableStore: owns aircraft, intent, maintenance, and compliance records.
//   - telemetryStore: supplies current and historical aircraft observations.
//   - replayStore: resolves durable replay manifests for completed flights.
//   - registry: supplies current Agent placement and Relay liveness.
//
// Returns:
//   - service: uses default Registry and telemetry freshness thresholds.
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

// WithLiveStatePolicy overrides live-state freshness thresholds and the clock
// used to classify observations. Non-positive durations and a nil clock leave
// the corresponding defaults unchanged.
//
// Parameters:
//   - registryFreshness: bounds how old a Registry heartbeat may be.
//   - telemetryFreshness: bounds how old each telemetry group may be.
//   - now: supplies one clock source for deterministic freshness classification.
//
// Returns:
//   - service: is the same receiver, allowing constructor-style chaining.
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

// WithMissionDeployer installs the authenticated API-to-Relay mission command transport.
//
// Parameters:
//   - deployer: is the trusted server-side adapter for context and mission commands.
//
// Returns:
//   - result: is the same FleetService for fluent construction.
func (s *FleetService) WithMissionDeployer(deployer MissionDeployer) *FleetService {
	s.missionDeployer = deployer
	return s
}

// CreateAircraft delegates durable aircraft creation to the configured store.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - aircraft: is the domain.Aircraft value supplied to CreateAircraft.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *FleetService) CreateAircraft(ctx context.Context, aircraft domain.Aircraft) error {
	return s.durable.CreateAircraft(ctx, aircraft)
}

// CreateBattery delegates durable battery creation to the configured store.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - battery: is the domain.Battery value supplied to CreateBattery.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *FleetService) CreateBattery(ctx context.Context, battery domain.Battery) error {
	return s.durable.CreateBattery(ctx, battery)
}

// InstallBattery creates the active battery installation for one aircraft
// after verifying both resources and their operator ownership agree.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - aircraftID: identifies the aircraft receiving the battery.
//   - req: identifies the installation and battery; a zero time uses the service clock.
//
// Returns:
//   - installation: is the newly recorded active installation.
//   - error: reports validation, missing resources, ownership mismatch, or an existing active installation.
func (s *FleetService) InstallBattery(ctx context.Context, aircraftID string, req InstallBatteryRequest) (domain.BatteryInstallation, error) {
	aircraftID = strings.TrimSpace(aircraftID)
	req.ID = strings.TrimSpace(req.ID)
	req.BatteryID = strings.TrimSpace(req.BatteryID)
	if aircraftID == "" || req.ID == "" || req.BatteryID == "" {
		return domain.BatteryInstallation{}, fmt.Errorf("%w: aircraft_id, id, and battery_id are required", ErrValidation)
	}
	aircraft, err := s.durable.GetAircraft(ctx, aircraftID)
	if err != nil {
		return domain.BatteryInstallation{}, fmt.Errorf("get aircraft: %w", err)
	}
	battery, err := s.durable.GetBattery(ctx, req.BatteryID)
	if err != nil {
		return domain.BatteryInstallation{}, fmt.Errorf("get battery: %w", err)
	}
	operatorID, err := consistentOperatorID(req.OperatorID, aircraft.OperatorID, battery.OperatorID)
	if err != nil {
		return domain.BatteryInstallation{}, err
	}
	if active, err := s.durable.GetActiveBatteryInstallation(ctx, aircraftID); err != nil {
		return domain.BatteryInstallation{}, fmt.Errorf("get active battery installation: %w", err)
	} else if active != nil {
		return domain.BatteryInstallation{}, fmt.Errorf("%w: aircraft %s already has active installation %s", durable.ErrVersionConflict, aircraftID, active.ID)
	}
	installedAt := req.InstalledAt.UTC()
	if installedAt.IsZero() {
		installedAt = s.now().UTC()
	}
	installation := domain.BatteryInstallation{
		ID: req.ID, OperatorID: operatorID, AircraftID: aircraftID,
		BatteryID: req.BatteryID, InstalledAt: installedAt,
	}
	if err := s.durable.RecordBatteryInstallation(ctx, installation); err != nil {
		return domain.BatteryInstallation{}, fmt.Errorf("record battery installation: %w", err)
	}
	return installation, nil
}

// CreatePlannedFlight reserves a flight identity for the current accepted or
// active intent and derives its aircraft, operator, and intent-version linkage.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - intentID: identifies the current operational intent.
//   - req: supplies the flight identity and optional descriptive metadata.
//
// Returns:
//   - flight: is the newly persisted planned flight.
//   - error: reports validation, missing resources, ownership mismatch, lifecycle conflict, or duplicate identity.
func (s *FleetService) CreatePlannedFlight(ctx context.Context, intentID string, req CreateFlightRequest) (domain.FlightRecord, error) {
	intentID = strings.TrimSpace(intentID)
	req.ID = strings.TrimSpace(req.ID)
	if intentID == "" || req.ID == "" {
		return domain.FlightRecord{}, fmt.Errorf("%w: intent_id and id are required", ErrValidation)
	}
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return domain.FlightRecord{}, fmt.Errorf("get operational intent: %w", err)
	}
	if intent.Status != domain.IntentStatusAccepted && intent.Status != domain.IntentStatusActive {
		return domain.FlightRecord{}, fmt.Errorf("%w: cannot create flight for intent in %s status", ErrInvalidTransition, intent.Status)
	}
	aircraft, err := s.durable.GetAircraft(ctx, intent.AircraftID)
	if err != nil {
		return domain.FlightRecord{}, fmt.Errorf("get aircraft: %w", err)
	}
	operatorID, err := consistentOperatorID(req.OperatorID, intent.OperatorID, aircraft.OperatorID)
	if err != nil {
		return domain.FlightRecord{}, err
	}
	flight := domain.FlightRecord{
		ID: req.ID, OperatorID: operatorID, AircraftID: intent.AircraftID,
		IntentID: intent.ID, IntentVersion: intent.Version, Status: domain.FlightStatusPlanned,
		Origin: req.Origin, Destination: req.Destination, MissionType: req.MissionType, TelemetryURI: req.TelemetryURI,
	}
	if err := s.durable.CreateFlightRecord(ctx, flight); err != nil {
		return domain.FlightRecord{}, fmt.Errorf("create flight record: %w", err)
	}
	return flight, nil
}

// StartFlight atomically advances a planned flight to active once its exact
// linked intent version is active. Retrying an already-active flight is safe.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - flightID: identifies the planned flight.
//
// Returns:
//   - flight: is the active flight with its server-owned start timestamp.
//   - error: reports missing records, stale intent linkage, lifecycle conflict, or persistence failure.
func (s *FleetService) StartFlight(ctx context.Context, flightID string) (domain.FlightRecord, error) {
	flightID = strings.TrimSpace(flightID)
	if flightID == "" {
		return domain.FlightRecord{}, fmt.Errorf("%w: flight_id is required", ErrValidation)
	}
	flight, err := s.durable.GetFlightRecord(ctx, flightID)
	if err != nil {
		return domain.FlightRecord{}, fmt.Errorf("get flight record: %w", err)
	}
	if flight.Status == domain.FlightStatusActive {
		return flight, nil
	}
	if flight.Status != domain.FlightStatusPlanned {
		return domain.FlightRecord{}, fmt.Errorf("%w: cannot start flight in %s status", ErrInvalidTransition, flight.Status)
	}
	intent, err := s.durable.GetOperationalIntent(ctx, flight.IntentID)
	if err != nil {
		return domain.FlightRecord{}, fmt.Errorf("get operational intent: %w", err)
	}
	if intent.Status != domain.IntentStatusActive || intent.Version != flight.IntentVersion || intent.AircraftID != flight.AircraftID {
		return domain.FlightRecord{}, fmt.Errorf("%w: linked intent version is not active", ErrInvalidTransition)
	}
	flight.Status = domain.FlightStatusActive
	flight.StartedAt = s.now().UTC()
	if err := s.durable.StartFlightWithCurrentMissionDeployment(ctx, flight, domain.FlightStatusPlanned); err != nil {
		if errors.Is(err, durable.ErrVersionConflict) {
			current, getErr := s.durable.GetFlightRecord(ctx, flightID)
			if getErr == nil && current.Status == domain.FlightStatusActive {
				return current, nil
			}
		}
		return domain.FlightRecord{}, fmt.Errorf("start flight record: %w", err)
	}
	return flight, nil
}

func consistentOperatorID(values ...string) (string, error) {
	operatorID := ""
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if operatorID != "" && value != operatorID {
			return "", fmt.Errorf("%w: operator_id does not match linked resources", ErrValidation)
		}
		operatorID = value
	}
	return operatorID, nil
}

// RecordMaintenanceEvent appends a durable aircraft maintenance record.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - event: is the domain.MaintenanceEvent value supplied to RecordMaintenanceEvent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *FleetService) RecordMaintenanceEvent(ctx context.Context, event domain.MaintenanceEvent) error {
	return s.durable.RecordMaintenanceEvent(ctx, event)
}

// GetOverviewDashboard composes fleet readiness, operational intents, evidence,
// and reportability reviews into the Ops overview read model.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - dashboard: contains metrics and the underlying records used to derive them.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// GetOperationsDashboard composes operational intents, conformance summaries,
// and per-aircraft live Registry/telemetry state for the Ops operations page.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - dashboard: contains operational metrics and independently sourced live state.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *FleetService) GetOperationsDashboard(ctx context.Context) (readmodel.OperationsDashboard, error) {
	intents, err := s.durable.ListOperationalIntents(ctx, "")
	if err != nil {
		return readmodel.OperationsDashboard{}, fmt.Errorf("list operational intents: %w", err)
	}
	conformance, err := s.durable.ListConformanceSummaries(ctx, "")
	if err != nil {
		return readmodel.OperationsDashboard{}, fmt.Errorf("list conformance summaries: %w", err)
	}
	conformance = s.overlayRegistryConformance(ctx, intents, conformance)
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

// GetPreflightDashboard composes recorded preflight checks and their aggregate metrics.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the readmodel.PreflightDashboard value produced by GetPreflightDashboard.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// GetConformanceDashboard composes durable conformance summaries, incident
// events, and aggregate conformance metrics.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the readmodel.ConformanceDashboard value produced by GetConformanceDashboard.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *FleetService) GetConformanceDashboard(ctx context.Context) (readmodel.ConformanceDashboard, error) {
	summaries, err := s.durable.ListConformanceSummaries(ctx, "")
	if err != nil {
		return readmodel.ConformanceDashboard{}, fmt.Errorf("list conformance summaries: %w", err)
	}
	events, err := s.durable.ListConformanceEvents(ctx, "")
	if err != nil {
		return readmodel.ConformanceDashboard{}, fmt.Errorf("list conformance events: %w", err)
	}
	intents, err := s.durable.ListOperationalIntents(ctx, "")
	if err != nil {
		return readmodel.ConformanceDashboard{}, fmt.Errorf("list operational intents: %w", err)
	}
	summaries = s.overlayRegistryConformance(ctx, intents, summaries)

	return readmodel.ConformanceDashboard{
		Metrics:   conformanceMetrics(summaries, events),
		Summaries: summaries,
		Events:    events,
	}, nil
}

func (s *FleetService) overlayRegistryConformance(ctx context.Context, intents []domain.OperationalIntent, durableSummaries []domain.ConformanceSummary) []domain.ConformanceSummary {
	if s.registry == nil || len(intents) == 0 {
		return durableSummaries
	}
	requested := make(map[string]struct{}, len(intents))
	assignmentIDs := make([]string, 0, len(intents))
	for _, intent := range intents {
		assignmentID := strings.TrimSpace(intent.ID)
		if assignmentID == "" {
			continue
		}
		if _, exists := requested[assignmentID]; exists {
			continue
		}
		requested[assignmentID] = struct{}{}
		assignmentIDs = append(assignmentIDs, assignmentID)
	}
	if len(assignmentIDs) == 0 {
		return durableSummaries
	}
	sort.Strings(assignmentIDs)

	liveByVersion := make(map[string]domain.ConformanceSummary)
	for start := 0; start < len(assignmentIDs); start += maxConformanceBatch {
		end := min(start+maxConformanceBatch, len(assignmentIDs))
		response, err := s.registry.BatchGetConformanceSummaries(ctx, &registryv1.BatchGetConformanceSummariesRequest{AssignmentIds: assignmentIDs[start:end]})
		if err != nil || response == nil {
			continue
		}
		for _, projection := range response.GetProjections() {
			live, ok := registryConformanceSummary(projection.GetSummary(), requested)
			if !ok {
				continue
			}
			key := conformanceVersionKey(live.IntentID, live.IntentVersion)
			current, exists := liveByVersion[key]
			if !exists || live.AssignmentGeneration > current.AssignmentGeneration || (live.AssignmentGeneration == current.AssignmentGeneration && live.EvaluationRevision > current.EvaluationRevision) {
				liveByVersion[key] = live
			}
		}
	}
	if len(liveByVersion) == 0 {
		return durableSummaries
	}

	result := make([]domain.ConformanceSummary, 0, len(durableSummaries)+len(liveByVersion))
	for _, durableSummary := range durableSummaries {
		key := conformanceVersionKey(durableSummary.IntentID, durableSummary.IntentVersion)
		live, exists := liveByVersion[key]
		if !exists {
			result = append(result, durableSummary)
			continue
		}
		result = append(result, mergeLiveConformance(durableSummary, live))
		delete(liveByVersion, key)
	}
	for _, live := range liveByVersion {
		result = append(result, live)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return conformanceVersionKey(result[i].IntentID, result[i].IntentVersion) < conformanceVersionKey(result[j].IntentID, result[j].IntentVersion)
		}
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result
}

func registryConformanceSummary(summary *conformancev1.ConformanceSummary, requested map[string]struct{}) (domain.ConformanceSummary, bool) {
	if summary == nil {
		return domain.ConformanceSummary{}, false
	}
	assignmentID := strings.TrimSpace(summary.GetAssignmentId())
	intentID := strings.TrimSpace(summary.GetIntentId())
	if assignmentID == "" || assignmentID != intentID || summary.GetIntentVersion() == 0 {
		return domain.ConformanceSummary{}, false
	}
	if _, exists := requested[assignmentID]; !exists {
		return domain.ConformanceSummary{}, false
	}
	observedAt := validProtoTime(summary.GetObservedAt())
	condition := enumJSONName(summary.GetCondition().String(), "CONFORMANCE_CONDITION_")
	violations := make([]domain.ConformanceViolationSummary, 0, len(summary.GetViolations()))
	for _, violation := range summary.GetViolations() {
		if violation == nil {
			continue
		}
		violations = append(violations, domain.ConformanceViolationSummary{
			ViolationType:   enumJSONName(violation.GetViolationType().String(), "VIOLATION_TYPE_"),
			Phase:           enumJSONName(violation.GetPhase().String(), "INCIDENT_PHASE_"),
			OpeningFrameID:  violation.GetOpeningFrameId(),
			OpenedAt:        validProtoTime(violation.GetOpenedAt()),
			LastObservedAt:  validProtoTime(violation.GetLastObservedAt()),
			WorstDeviationM: violation.GetWorstDeviationM(),
		})
	}
	updatedAt := time.Time{}
	if observedAt != nil {
		updatedAt = *observedAt
	}
	return domain.ConformanceSummary{
		ID:                   summary.GetEvaluationId(),
		OperatorID:           summary.GetOperatorId(),
		IntentID:             intentID,
		IntentVersion:        int(summary.GetIntentVersion()),
		FlightID:             summary.GetFlightId(),
		AircraftID:           summary.GetAircraftId(),
		Status:               legacyConformanceStatus(condition),
		AlertCount:           activeViolationCount(violations),
		ReportabilityStatus:  domain.ReportabilityStatusNo,
		UpdatedAt:            updatedAt,
		AssignmentID:         assignmentID,
		AssignmentGeneration: summary.GetAssignmentGeneration(),
		EvaluationRevision:   summary.GetEvaluationRevision(),
		EvaluationID:         summary.GetEvaluationId(),
		Condition:            condition,
		MonitoringStatus:     enumJSONName(summary.GetMonitoringStatus().String(), "MONITORING_STATUS_"),
		RecordingStatus:      enumJSONName(summary.GetRecordingStatus().String(), "RECORDING_STATUS_"),
		ObservedAt:           observedAt,
		FrameID:              summary.GetFrameId(),
		Violations:           violations,
	}, true
}

func activeViolationCount(violations []domain.ConformanceViolationSummary) int {
	count := 0
	for _, violation := range violations {
		if violation.Phase != "" && violation.Phase != "clear" {
			count++
		}
	}
	return count
}

func mergeLiveConformance(durableSummary, live domain.ConformanceSummary) domain.ConformanceSummary {
	result := durableSummary
	result.OperatorID = firstNonEmpty(live.OperatorID, result.OperatorID)
	result.FlightID = firstNonEmpty(live.FlightID, result.FlightID)
	result.AircraftID = firstNonEmpty(live.AircraftID, result.AircraftID)
	result.Status = live.Status
	if !live.UpdatedAt.IsZero() {
		result.UpdatedAt = live.UpdatedAt
	}
	result.AssignmentID = live.AssignmentID
	result.AssignmentGeneration = live.AssignmentGeneration
	result.EvaluationRevision = live.EvaluationRevision
	result.EvaluationID = live.EvaluationID
	result.Condition = live.Condition
	result.MonitoringStatus = live.MonitoringStatus
	result.RecordingStatus = live.RecordingStatus
	result.ObservedAt = live.ObservedAt
	result.FrameID = live.FrameID
	result.Violations = live.Violations
	return result
}

func legacyConformanceStatus(condition string) domain.ConformanceStatus {
	switch condition {
	case "conforming":
		return domain.ConformanceStatusConforming
	case "non_conforming":
		return domain.ConformanceStatusNonConforming
	case "suspected", "recovering":
		return domain.ConformanceStatusContingent
	default:
		return domain.ConformanceStatusUnknown
	}
}

func enumJSONName(value, prefix string) string {
	value = strings.TrimPrefix(value, prefix)
	if value == "UNSPECIFIED" {
		return ""
	}
	return strings.ToLower(value)
}

func validProtoTime(value interface {
	CheckValid() error
	AsTime() time.Time
}) *time.Time {
	if value == nil || value.CheckValid() != nil {
		return nil
	}
	timestamp := value.AsTime().UTC()
	return &timestamp
}

func conformanceVersionKey(intentID string, intentVersion int) string {
	return fmt.Sprintf("%s:%d", intentID, intentVersion)
}

func firstNonEmpty(preferred, fallback string) string {
	if preferred != "" {
		return preferred
	}
	return fallback
}

// GetMaintenanceDashboard composes maintenance history, battery inventory, and
// their operational metrics.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the readmodel.MaintenanceDashboard value produced by GetMaintenanceDashboard.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// GetRecordsDashboard composes evidence and reportability-review records for Ops.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the readmodel.RecordsDashboard value produced by GetRecordsDashboard.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListAircraftDashboards composes one dashboard record per durable aircraft.
// Registry placement and telemetry are fetched in batches before durable
// maintenance and battery details are joined per aircraft.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//
// Returns:
//   - result: is the []readmodel.AircraftDashboard value produced by ListAircraftDashboards.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// GetAircraftDashboard composes durable, maintenance, Registry, and telemetry
// state for one aircraft.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the readmodel.AircraftDashboard value produced by GetAircraftDashboard.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *FleetService) GetAircraftDashboard(ctx context.Context, aircraftID string) (readmodel.AircraftDashboard, error) {
	aircraft, err := s.durable.GetAircraft(ctx, aircraftID)
	if err != nil {
		return readmodel.AircraftDashboard{}, fmt.Errorf("get aircraft: %w", err)
	}
	live := s.composeLiveAircraft(ctx, []domain.Aircraft{aircraft})[0]
	return s.buildDashboard(ctx, aircraft, live)
}

// GetAircraftLiveState composes current connection and independently sampled
// telemetry groups for one durable aircraft.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the readmodel.AircraftLiveState value produced by GetAircraftLiveState.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *FleetService) GetAircraftLiveState(ctx context.Context, aircraftID string) (readmodel.AircraftLiveState, error) {
	aircraft, err := s.durable.GetAircraft(ctx, aircraftID)
	if err != nil {
		return readmodel.AircraftLiveState{}, fmt.Errorf("get aircraft: %w", err)
	}
	return s.composeLiveAircraft(ctx, []domain.Aircraft{aircraft})[0], nil
}

// GetAircraftMapView composes the live map marker, bounded telemetry history,
// active intent geometry, and matching conformance evidence for one aircraft.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - aircraftID: identifies the target aircraft.
//   - limit: caps historical telemetry samples included in the map view.
//
// Returns:
//   - result: is the readmodel.AircraftMapView value produced by GetAircraftMapView.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

	mission, err := s.durable.GetDeployedMissionForActiveFlight(ctx, aircraft.ID, intent.ID, intent.Version)
	if err == nil {
		view.CommandedMission = &mission
	} else if !errors.Is(err, durable.ErrNotFound) {
		return readmodel.AircraftMapView{}, fmt.Errorf("get commanded mission: %w", err)
	}

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

// ListFlightRecords verifies the aircraft exists, then returns its durable
// flight records.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the []domain.FlightRecord value produced by ListFlightRecords.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// GetFlightRecord returns one durable flight record by identity.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - flightID: identifies the target flight.
//
// Returns:
//   - result: is the domain.FlightRecord value produced by GetFlightRecord.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *FleetService) GetFlightRecord(ctx context.Context, flightID string) (domain.FlightRecord, error) {
	flight, err := s.durable.GetFlightRecord(ctx, flightID)
	if err != nil {
		return domain.FlightRecord{}, fmt.Errorf("get flight record: %w", err)
	}
	return flight, nil
}

// GetFlightReplay composes a flight, its replay manifest, bounded telemetry,
// and durable conformance events into one replay response.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - flightID: identifies the target flight.
//   - limit: caps telemetry samples returned for the replay.
//
// Returns:
//   - result: is the ReplayResponse value produced by GetFlightReplay.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
	telemetryIDs := make([]string, 0, len(aircraft))
	for _, item := range aircraft {
		telemetryIDs = append(telemetryIDs, item.ID)
	}
	now := s.now().UTC()

	var connectionStates []domain.LiveAircraftState
	var telemetryStates []domain.AircraftTelemetryState
	var branches sync.WaitGroup
	branches.Add(2)
	go func() {
		defer branches.Done()
		connectionStates = s.composeAircraftConnections(ctx, aircraft, now)
	}()
	go func() {
		defer branches.Done()
		telemetryStates = s.composeAircraftTelemetry(ctx, telemetryIDs, now)
	}()
	branches.Wait()

	result := make([]readmodel.AircraftLiveState, len(aircraft))
	for index, item := range aircraft {
		result[index] = readmodel.AircraftLiveState{
			AircraftID: item.ID,
			OperatorID: item.OperatorID,
			AgentID:    item.AgentID,
			Connection: connectionStates[index],
			Telemetry:  telemetryStates[index],
		}
	}
	return result
}

func (s *FleetService) composeAircraftTelemetry(ctx context.Context, aircraftIDs []string, now time.Time) []domain.AircraftTelemetryState {
	result := make([]domain.AircraftTelemetryState, len(aircraftIDs))
	telemetryStates, err := s.telemetry.GetLatestAircraftStates(ctx, aircraftIDs)
	for index, aircraftID := range aircraftIDs {
		if err != nil {
			result[index].Status = domain.DataFreshnessUnavailable
			continue
		}
		state := telemetryStates[aircraftID]
		s.applyTelemetryFreshness(&state, now)
		result[index] = state
	}
	return result
}

func (s *FleetService) composeAircraftConnections(ctx context.Context, aircraft []domain.Aircraft, now time.Time) []domain.LiveAircraftState {
	result := make([]domain.LiveAircraftState, len(aircraft))
	hasMappings := false
	for index, item := range aircraft {
		result[index] = domain.LiveAircraftState{
			AircraftID: item.ID, OperatorID: item.OperatorID, AgentID: item.AgentID, ConnectionStatus: domain.ConnectionStatusUnmapped,
		}
		hasMappings = hasMappings || item.AgentID != ""
	}
	if !hasMappings {
		return result
	}

	agentResponse, err := s.registry.ListAgents(ctx, &registryv1.ListAgentsRequest{})
	if err != nil {
		for index := range result {
			if result[index].AgentID != "" {
				result[index].ConnectionStatus = domain.ConnectionStatusUnavailable
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
	lookups := make([]placementLookup, 0, len(result))
	for index := range result {
		state := &result[index]
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
		if freshAt(state.LastHeartbeatAt, now, s.registryFreshness) {
			state.Connected = true
			state.ConnectionStatus = domain.ConnectionStatusConnected
		} else {
			state.ConnectionStatus = domain.ConnectionStatusStale
		}
		lookups = append(lookups, placementLookup{resultIndex: index, agentID: state.AgentID})
	}

	placementResults := s.lookupPlacements(ctx, lookups)
	for lookupIndex, lookup := range lookups {
		state := &result[lookup.resultIndex]
		placementResult := placementResults[lookupIndex]
		if placementResult.err != nil {
			state.Connected = false
			if status.Code(placementResult.err) == codes.NotFound {
				state.ConnectionStatus = domain.ConnectionStatusOffline
			} else {
				state.ConnectionStatus = domain.ConnectionStatusUnavailable
			}
			continue
		}
		placement := placementResult.response
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

func (s *FleetService) lookupPlacements(ctx context.Context, lookups []placementLookup) []placementLookupResult {
	results := make([]placementLookupResult, len(lookups))
	if len(lookups) == 0 {
		return results
	}

	jobs := make(chan int, len(lookups))
	for index := range lookups {
		jobs <- index
	}
	close(jobs)

	workerCount := min(len(lookups), maxPlacementLookups)
	done := make(chan struct{}, workerCount)
	for range workerCount {
		go func() {
			defer func() { done <- struct{}{} }()
			for index := range jobs {
				lookup := lookups[index]
				response, err := s.registry.GetAgentPlacement(ctx, &registryv1.GetAgentPlacementRequest{AgentId: lookup.agentID})
				results[index] = placementLookupResult{response: response, err: err}
			}
		}()
	}
	for range workerCount {
		<-done
	}
	return results
}

func (s *FleetService) applyTelemetryFreshness(state *domain.AircraftTelemetryState, now time.Time) {
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

// CalculateReadiness derives the aircraft readiness state from unresolved
// critical maintenance, battery health, and live-state availability.
//
// Parameters:
//   - battery: is the installed battery, or nil when no installation is known.
//   - maintenanceEvents: contains the aircraft's recorded maintenance history.
//   - liveStateAvailable: reports whether current aircraft state can be observed.
//
// Returns:
//   - readiness: contains the derived status and human-readable blocking reasons.
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
	activeFindings := 0
	hasLiveProjection := false
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
		if summary.AssignmentID != "" {
			hasLiveProjection = true
			activeFindings += activeViolationCount(summary.Violations)
		}
	}

	targetValue := "Not scored"
	targetDetail := "Live condition is reported separately"
	if scores > 0 {
		targetValue = fmt.Sprintf("%.1f%%", totalScore/float64(scores)*100)
		targetDetail = "Average of durable scored evaluations"
	}
	alertLabel := "Open alerts"
	alertValue := len(events)
	if hasLiveProjection {
		alertLabel = "Active findings"
		alertValue = activeFindings
	}

	return []readmodel.DashboardMetric{
		{Label: "Target conformance", Value: targetValue, Detail: targetDetail},
		{Label: "Conforming", Value: fmt.Sprintf("%d", conforming), Status: string(domain.ConformanceStatusConforming)},
		{Label: alertLabel, Value: fmt.Sprintf("%d", alertValue)},
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
