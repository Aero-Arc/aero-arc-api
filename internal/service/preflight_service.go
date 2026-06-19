package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

type PreflightService struct {
	durable durable.Store
	now     func() time.Time
}

type PreflightEvaluation struct {
	Intent   domain.OperationalIntent   `json:"intent"`
	Checks   []domain.PreflightCheck    `json:"checks"`
	Findings []domain.ComplianceFinding `json:"findings"`
	Blocked  bool                       `json:"blocked"`
}

func NewPreflightService(durableStore durable.Store) *PreflightService {
	return NewPreflightServiceWithClock(durableStore, nil)
}

func NewPreflightServiceWithClock(durableStore durable.Store, now func() time.Time) *PreflightService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PreflightService{durable: durableStore, now: now}
}

func (s *PreflightService) EvaluateIntent(ctx context.Context, intentID string) (PreflightEvaluation, error) {
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return PreflightEvaluation{}, fmt.Errorf("get operational intent: %w", err)
	}

	aircraft, aircraftErr := s.durable.GetAircraft(ctx, intent.AircraftID)
	volumes, err := s.durable.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		return PreflightEvaluation{}, fmt.Errorf("list operational volumes: %w", err)
	}
	volumes = volumesForVersion(volumes, intent.Version)

	now := s.now().UTC()
	builder := preflightBuilder{intent: intent, now: now}
	if aircraftErr != nil {
		builder.block(domain.PreflightCheckAirspace, "aircraft_exists", "fleet_registry", "AIRCRAFT-EXISTS", "aircraft does not exist", "create or select a valid aircraft")
	} else {
		builder.clear(domain.PreflightCheckAirspace, "aircraft_exists", "fleet_registry", "AIRCRAFT-EXISTS", "aircraft exists")
		if aircraft.Status != domain.AircraftStatusActive || aircraft.AcceptanceStatus != domain.AcceptanceStatusAccepted {
			builder.block(domain.PreflightCheckAirspace, "aircraft_operational_status", "fleet_registry", "AIRCRAFT-STATUS", "aircraft is not active or accepted", "set aircraft active or complete acceptance")
		} else {
			builder.clear(domain.PreflightCheckAirspace, "aircraft_operational_status", "fleet_registry", "AIRCRAFT-STATUS", "aircraft status allows operation")
		}
		if aircraft.RemoteIDStatus == domain.RemoteIDStatusOffline {
			builder.block(domain.PreflightCheckRemoteID, "remote_id_online", "remote_id_monitor", "RID-ONLINE", "remote ID is offline", "restore Remote ID broadcast before activation")
		} else {
			builder.clear(domain.PreflightCheckRemoteID, "remote_id_online", "remote_id_monitor", "RID-ONLINE", "remote ID is not offline")
		}
	}

	if intent.PlannedStartAt.Before(intent.PlannedEndAt) {
		builder.clear(domain.PreflightCheckAirspace, "intent_time_window", "intent_service", "INTENT-WINDOW", "intent planned time window is valid")
	} else {
		builder.block(domain.PreflightCheckAirspace, "intent_time_window", "intent_service", "INTENT-WINDOW", "intent planned start must be before planned end", "set planned_start_at before planned_end_at")
	}

	if len(volumes) == 0 {
		builder.block(domain.PreflightCheckAirspace, "operational_volume_exists", "intent_service", "VOLUME-EXISTS", "at least one operational volume is required", "add an operational volume")
	} else {
		builder.clear(domain.PreflightCheckAirspace, "operational_volume_exists", "intent_service", "VOLUME-EXISTS", "operational volume exists")
	}
	for _, volume := range volumes {
		prefix := fmt.Sprintf("volume_%s", volume.ID)
		if volume.StartsAt.Before(volume.EndsAt) {
			builder.clear(domain.PreflightCheckAirspace, prefix+"_time_window", "intent_service", "VOLUME-WINDOW", "operational volume time window is valid")
		} else {
			builder.block(domain.PreflightCheckAirspace, prefix+"_time_window", "intent_service", "VOLUME-WINDOW", "operational volume start must be before end", "set volume starts_at before ends_at")
		}
		if volume.MinAltitudeM <= volume.MaxAltitudeM {
			builder.clear(domain.PreflightCheckAirspace, prefix+"_altitude_range", "intent_service", "VOLUME-ALTITUDE", "operational volume altitude range is valid")
		} else {
			builder.block(domain.PreflightCheckAirspace, prefix+"_altitude_range", "intent_service", "VOLUME-ALTITUDE", "operational volume minimum altitude exceeds maximum altitude", "set min_altitude_m <= max_altitude_m")
		}
		if !volume.StartsAt.Before(intent.PlannedStartAt) && !volume.EndsAt.After(intent.PlannedEndAt) {
			builder.clear(domain.PreflightCheckAirspace, prefix+"_inside_intent_window", "intent_service", "VOLUME-IN-INTENT", "operational volume is inside planned intent window")
		} else {
			builder.block(domain.PreflightCheckAirspace, prefix+"_inside_intent_window", "intent_service", "VOLUME-IN-INTENT", "operational volume must be inside planned intent window", "adjust volume or planned intent time window")
		}
		if volume.GeoJSON != "" {
			builder.clear(domain.PreflightCheckAirspace, prefix+"_inline_geojson", "intent_service", "VOLUME-GEOJSON", "operational volume has inline GeoJSON evaluable by this server")
		} else {
			builder.block(domain.PreflightCheckAirspace, prefix+"_inline_geojson", "intent_service", "VOLUME-GEOJSON", "operational volume requires inline GeoJSON for local conformance evaluation", "provide inline GeoJSON; geometry_uri resolution is not implemented")
		}
	}

	s.evaluateBattery(ctx, &builder, intent)
	s.evaluateMaintenance(ctx, &builder, intent)
	builder.clear(domain.PreflightCheckWeather, "demo_weather", "demo_weather_provider", "WX-DEMO", "demo weather check clear")
	builder.clear(domain.PreflightCheckNOTAM, "demo_notam", "demo_notam_provider", "NOTAM-DEMO", "demo NOTAM check clear")

	for _, check := range builder.checks {
		if err := s.durable.RecordPreflightCheck(ctx, check); err != nil {
			return PreflightEvaluation{}, fmt.Errorf("record preflight check: %w", err)
		}
	}
	for _, finding := range builder.findings {
		if err := s.durable.RecordComplianceFinding(ctx, finding); err != nil {
			return PreflightEvaluation{}, fmt.Errorf("record compliance finding: %w", err)
		}
	}

	return PreflightEvaluation{
		Intent:   intent,
		Checks:   builder.checks,
		Findings: builder.findings,
		Blocked:  builder.blocked,
	}, nil
}

func (s *PreflightService) evaluateBattery(ctx context.Context, builder *preflightBuilder, intent domain.OperationalIntent) {
	installation, err := s.durable.GetActiveBatteryInstallation(ctx, intent.AircraftID)
	if err != nil || installation == nil {
		builder.block(domain.PreflightCheckBattery, "battery_installed", "maintenance_control", "BATTERY-INSTALLED", "battery is not installed", "install a battery")
		return
	}
	builder.clear(domain.PreflightCheckBattery, "battery_installed", "maintenance_control", "BATTERY-INSTALLED", "battery is installed")

	battery, err := s.durable.GetBattery(ctx, installation.BatteryID)
	if err != nil {
		builder.block(domain.PreflightCheckBattery, "battery_soh_known", "maintenance_control", "BATTERY-SOH-KNOWN", "battery state of health is unknown", "record battery state of health")
		return
	}
	if battery.StateOfHealth == nil {
		builder.block(domain.PreflightCheckBattery, "battery_soh_known", "maintenance_control", "BATTERY-SOH-KNOWN", "battery state of health is unknown", "record battery state of health")
		return
	}
	builder.clear(domain.PreflightCheckBattery, "battery_soh_known", "maintenance_control", "BATTERY-SOH-KNOWN", "battery state of health is known")
	if *battery.StateOfHealth < 80 {
		builder.block(domain.PreflightCheckBattery, "battery_soh_minimum", "maintenance_control", "BATTERY-SOH-80", "battery state of health is below 80", "replace or service battery")
		return
	}
	builder.clear(domain.PreflightCheckBattery, "battery_soh_minimum", "maintenance_control", "BATTERY-SOH-80", "battery state of health is at least 80")
}

func (s *PreflightService) evaluateMaintenance(ctx context.Context, builder *preflightBuilder, intent domain.OperationalIntent) {
	events, err := s.durable.ListMaintenanceEvents(ctx, intent.AircraftID)
	if err != nil {
		builder.block(domain.PreflightCheckMaintenance, "maintenance_events_available", "maintenance_control", "MX-AVAILABLE", "maintenance status could not be loaded", "retry maintenance status lookup")
		return
	}
	for _, event := range events {
		if event.Severity == domain.SeverityCritical && event.ResolvedAt == nil && event.Status != domain.MaintenanceStatusClosed {
			builder.block(domain.PreflightCheckMaintenance, "critical_open_maintenance", "maintenance_control", "MX-CRITICAL-OPEN", "critical open maintenance event exists", "resolve critical maintenance before activation")
			return
		}
	}
	builder.clear(domain.PreflightCheckMaintenance, "critical_open_maintenance", "maintenance_control", "MX-CRITICAL-OPEN", "no critical open maintenance events")
}

type preflightBuilder struct {
	intent   domain.OperationalIntent
	now      time.Time
	checks   []domain.PreflightCheck
	findings []domain.ComplianceFinding
	blocked  bool
}

func (b *preflightBuilder) clear(category domain.PreflightCheckCategory, key, source, requirementCode, summary string) {
	b.check(category, key, source, requirementCode, summary, "", domain.PreflightStatusClear, false)
}

func (b *preflightBuilder) block(category domain.PreflightCheckCategory, key, source, requirementCode, summary, remediation string) {
	b.check(category, key, source, requirementCode, summary, remediation, domain.PreflightStatusBlocked, true)
	b.blocked = true
}

func (b *preflightBuilder) check(category domain.PreflightCheckCategory, key, source, requirementCode, summary, remediation string, status domain.PreflightStatus, blocking bool) {
	check := domain.PreflightCheck{
		ID:              fmt.Sprintf("preflight-%s-v%d-%s", b.intent.ID, b.intent.Version, key),
		OperatorID:      b.intent.OperatorID,
		IntentID:        b.intent.ID,
		IntentVersion:   b.intent.Version,
		AircraftID:      b.intent.AircraftID,
		Category:        category,
		Source:          source,
		Status:          status,
		Summary:         summary,
		RequirementCode: requirementCode,
		RuleVersion:     "demo.v1",
		Blocking:        blocking,
		CapturedAt:      b.now,
	}
	b.checks = append(b.checks, check)
	if !blocking {
		b.findings = append(b.findings, domain.ComplianceFinding{
			ID:              fmt.Sprintf("finding-%s-v%d-%s", b.intent.ID, b.intent.Version, key),
			OperatorID:      b.intent.OperatorID,
			IntentID:        b.intent.ID,
			IntentVersion:   b.intent.Version,
			SubjectType:     "operational_intent",
			SubjectID:       b.intent.ID,
			RequirementCode: requirementCode,
			Status:          domain.ComplianceFindingPass,
			Severity:        domain.SeverityInfo,
			Blocking:        false,
			RuleVersion:     "demo.v1",
			Message:         summary,
			EvaluatedAt:     b.now,
		})
		return
	}
	b.findings = append(b.findings, domain.ComplianceFinding{
		ID:              fmt.Sprintf("finding-%s-v%d-%s", b.intent.ID, b.intent.Version, key),
		OperatorID:      b.intent.OperatorID,
		IntentID:        b.intent.ID,
		IntentVersion:   b.intent.Version,
		SubjectType:     "operational_intent",
		SubjectID:       b.intent.ID,
		RequirementCode: requirementCode,
		Status:          domain.ComplianceFindingFail,
		Severity:        domain.SeverityCritical,
		Blocking:        true,
		RuleVersion:     "demo.v1",
		Remediation:     remediation,
		Message:         summary,
		EvaluatedAt:     b.now,
	})
}
