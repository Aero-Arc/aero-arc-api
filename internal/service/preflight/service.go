package preflight

import (
	"context"
	"fmt"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

type PreflightService struct {
	durable  durable.Store
	now      func() time.Time
	checkers []Checker
}

type PreflightEvaluation struct {
	Intent   domain.OperationalIntent   `json:"intent"`
	Checks   []domain.PreflightCheck    `json:"checks"`
	Findings []domain.ComplianceFinding `json:"findings"`
	Blocked  bool                       `json:"blocked"`
}

// NewPreflightService constructs service from the supplied configuration and dependencies.
//
// Parameters:
//   - durableStore: is the durable.Store value supplied to NewPreflightService.
//
// Returns:
//   - result: is the *PreflightService value produced by NewPreflightService.
func NewPreflightService(durableStore durable.Store) *PreflightService {
	return NewPreflightServiceWithClock(durableStore, nil)
}

// NewPreflightServiceWithClock constructs service from the supplied configuration and dependencies.
//
// Parameters:
//   - durableStore: is the durable.Store value supplied to NewPreflightServiceWithClock.
//   - now: supplies the event or wall-clock timestamp used by the operation.
//
// Returns:
//   - result: is the *PreflightService value produced by NewPreflightServiceWithClock.
func NewPreflightServiceWithClock(durableStore durable.Store, now func() time.Time) *PreflightService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PreflightService{
		durable:  durableStore,
		now:      now,
		checkers: []Checker{currentPolicy{durable: durableStore}},
	}
}

// EvaluateIntent runs the current preflight policy against an operational
// intent and records the resulting checks.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the PreflightEvaluation value produced by EvaluateIntent.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *PreflightService) EvaluateIntent(ctx context.Context, intentID string) (PreflightEvaluation, error) {
	snapshot, err := s.loadSnapshot(ctx, intentID)
	if err != nil {
		return PreflightEvaluation{}, err
	}

	builder := newBuilder(snapshot)
	for _, checker := range s.checkers {
		checker.Evaluate(ctx, snapshot, builder)
	}

	for _, check := range builder.Checks() {
		if err := s.durable.RecordPreflightCheck(ctx, check); err != nil {
			return PreflightEvaluation{}, fmt.Errorf("record preflight check: %w", err)
		}
	}
	for _, finding := range builder.Findings() {
		if err := s.durable.RecordComplianceFinding(ctx, finding); err != nil {
			return PreflightEvaluation{}, fmt.Errorf("record compliance finding: %w", err)
		}
	}

	return PreflightEvaluation{
		Intent:   snapshot.Intent,
		Checks:   builder.Checks(),
		Findings: builder.Findings(),
		Blocked:  builder.Blocked(),
	}, nil
}

type currentPolicy struct {
	durable durable.Store
}

func (currentPolicy) Name() string { return "current_policy" }

func (c currentPolicy) Evaluate(ctx context.Context, snapshot Snapshot, builder *Builder) {
	if snapshot.AircraftErr != nil {
		builder.Block(domain.PreflightCheckAirspace, "aircraft_exists", "fleet_registry", "AIRCRAFT-EXISTS", "aircraft does not exist", "create or select a valid aircraft")
	} else {
		builder.Clear(domain.PreflightCheckAirspace, "aircraft_exists", "fleet_registry", "AIRCRAFT-EXISTS", "aircraft exists")
		if snapshot.Aircraft.Status != domain.AircraftStatusActive || snapshot.Aircraft.AcceptanceStatus != domain.AcceptanceStatusAccepted {
			builder.Block(domain.PreflightCheckAirspace, "aircraft_operational_status", "fleet_registry", "AIRCRAFT-STATUS", "aircraft is not active or accepted", "set aircraft active or complete acceptance")
		} else {
			builder.Clear(domain.PreflightCheckAirspace, "aircraft_operational_status", "fleet_registry", "AIRCRAFT-STATUS", "aircraft status allows operation")
		}
		if snapshot.Aircraft.RemoteIDStatus == domain.RemoteIDStatusOffline {
			builder.Block(domain.PreflightCheckRemoteID, "remote_id_online", "remote_id_monitor", "RID-ONLINE", "remote ID is offline", "restore Remote ID broadcast before activation")
		} else {
			builder.Clear(domain.PreflightCheckRemoteID, "remote_id_online", "remote_id_monitor", "RID-ONLINE", "remote ID is not offline")
		}
	}

	if snapshot.Intent.PlannedStartAt.Before(snapshot.Intent.PlannedEndAt) {
		builder.Clear(domain.PreflightCheckAirspace, "intent_time_window", "intent_service", "INTENT-WINDOW", "intent planned time window is valid")
	} else {
		builder.Block(domain.PreflightCheckAirspace, "intent_time_window", "intent_service", "INTENT-WINDOW", "intent planned start must be before planned end", "set planned_start_at before planned_end_at")
	}

	if len(snapshot.Volumes) == 0 {
		builder.Block(domain.PreflightCheckAirspace, "operational_volume_exists", "intent_service", "VOLUME-EXISTS", "at least one operational volume is required", "add an operational volume")
	} else {
		builder.Clear(domain.PreflightCheckAirspace, "operational_volume_exists", "intent_service", "VOLUME-EXISTS", "operational volume exists")
	}
	for _, volume := range snapshot.Volumes {
		prefix := fmt.Sprintf("volume_%s", volume.ID)
		if volume.StartsAt.Before(volume.EndsAt) {
			builder.Clear(domain.PreflightCheckAirspace, prefix+"_time_window", "intent_service", "VOLUME-WINDOW", "operational volume time window is valid")
		} else {
			builder.Block(domain.PreflightCheckAirspace, prefix+"_time_window", "intent_service", "VOLUME-WINDOW", "operational volume start must be before end", "set volume starts_at before ends_at")
		}
		if volume.MinAltitudeM <= volume.MaxAltitudeM {
			builder.Clear(domain.PreflightCheckAirspace, prefix+"_altitude_range", "intent_service", "VOLUME-ALTITUDE", "operational volume altitude range is valid")
		} else {
			builder.Block(domain.PreflightCheckAirspace, prefix+"_altitude_range", "intent_service", "VOLUME-ALTITUDE", "operational volume minimum altitude exceeds maximum altitude", "set min_altitude_m <= max_altitude_m")
		}
		if !volume.StartsAt.Before(snapshot.Intent.PlannedStartAt) && !volume.EndsAt.After(snapshot.Intent.PlannedEndAt) {
			builder.Clear(domain.PreflightCheckAirspace, prefix+"_inside_intent_window", "intent_service", "VOLUME-IN-INTENT", "operational volume is inside planned intent window")
		} else {
			builder.Block(domain.PreflightCheckAirspace, prefix+"_inside_intent_window", "intent_service", "VOLUME-IN-INTENT", "operational volume must be inside planned intent window", "adjust volume or planned intent time window")
		}
		if volume.GeoJSON != "" {
			builder.Clear(domain.PreflightCheckAirspace, prefix+"_inline_geojson", "intent_service", "VOLUME-GEOJSON", "operational volume has inline GeoJSON evaluable by this server")
		} else {
			builder.Block(domain.PreflightCheckAirspace, prefix+"_inline_geojson", "intent_service", "VOLUME-GEOJSON", "operational volume requires inline GeoJSON for local conformance evaluation", "provide inline GeoJSON; geometry_uri resolution is not implemented")
		}
	}

	c.evaluateBattery(ctx, snapshot, builder)
	c.evaluateMaintenance(ctx, snapshot, builder)
	builder.Clear(domain.PreflightCheckWeather, "demo_weather", "demo_weather_provider", "WX-DEMO", "demo weather check clear")
	builder.Clear(domain.PreflightCheckNOTAM, "demo_notam", "demo_notam_provider", "NOTAM-DEMO", "demo NOTAM check clear")
}

func (c currentPolicy) evaluateBattery(ctx context.Context, snapshot Snapshot, builder *Builder) {
	installation, err := c.durable.GetActiveBatteryInstallation(ctx, snapshot.Intent.AircraftID)
	if err != nil || installation == nil {
		builder.Block(domain.PreflightCheckBattery, "battery_installed", "maintenance_control", "BATTERY-INSTALLED", "battery is not installed", "install a battery")
		return
	}
	builder.Clear(domain.PreflightCheckBattery, "battery_installed", "maintenance_control", "BATTERY-INSTALLED", "battery is installed")

	battery, err := c.durable.GetBattery(ctx, installation.BatteryID)
	if err != nil {
		builder.Block(domain.PreflightCheckBattery, "battery_soh_known", "maintenance_control", "BATTERY-SOH-KNOWN", "battery state of health is unknown", "record battery state of health")
		return
	}
	if battery.StateOfHealth == nil {
		builder.Block(domain.PreflightCheckBattery, "battery_soh_known", "maintenance_control", "BATTERY-SOH-KNOWN", "battery state of health is unknown", "record battery state of health")
		return
	}
	builder.Clear(domain.PreflightCheckBattery, "battery_soh_known", "maintenance_control", "BATTERY-SOH-KNOWN", "battery state of health is known")
	if *battery.StateOfHealth < 80 {
		builder.Block(domain.PreflightCheckBattery, "battery_soh_minimum", "maintenance_control", "BATTERY-SOH-80", "battery state of health is below 80", "replace or service battery")
		return
	}
	builder.Clear(domain.PreflightCheckBattery, "battery_soh_minimum", "maintenance_control", "BATTERY-SOH-80", "battery state of health is at least 80")
}

func (c currentPolicy) evaluateMaintenance(ctx context.Context, snapshot Snapshot, builder *Builder) {
	events, err := c.durable.ListMaintenanceEvents(ctx, snapshot.Intent.AircraftID)
	if err != nil {
		builder.Block(domain.PreflightCheckMaintenance, "maintenance_events_available", "maintenance_control", "MX-AVAILABLE", "maintenance status could not be loaded", "retry maintenance status lookup")
		return
	}
	for _, event := range events {
		if event.Severity == domain.SeverityCritical && event.ResolvedAt == nil && event.Status != domain.MaintenanceStatusClosed {
			builder.Block(domain.PreflightCheckMaintenance, "critical_open_maintenance", "maintenance_control", "MX-CRITICAL-OPEN", "critical open maintenance event exists", "resolve critical maintenance before activation")
			return
		}
	}
	builder.Clear(domain.PreflightCheckMaintenance, "critical_open_maintenance", "maintenance_control", "MX-CRITICAL-OPEN", "no critical open maintenance events")
}
