package seed

import (
	"context"
	"fmt"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

type telemetryWriter interface {
	AddSample(ctx context.Context, sample domain.TelemetrySample) error
}

type replayManifestWriter interface {
	PutReplayManifest(ctx context.Context, manifest domain.ReplayManifest) error
}

type liveStateWriter interface {
	SetLiveAircraftState(ctx context.Context, state domain.LiveAircraftState) error
}

// Demo populates in-memory stores with representative local-development data.
// It is intentionally opt-in and should not be enabled for production runs.
func Demo(ctx context.Context, durableStore durable.Store, telemetryStore any, replayStore any, registryClient any) error {
	now := time.Now().UTC().Truncate(time.Second)

	telemetry, ok := telemetryStore.(telemetryWriter)
	if !ok {
		return fmt.Errorf("demo seed requires a telemetry store that supports AddSample")
	}
	replay, ok := replayStore.(replayManifestWriter)
	if !ok {
		return fmt.Errorf("demo seed requires a replay store that supports PutReplayManifest")
	}
	live, ok := registryClient.(liveStateWriter)
	if !ok {
		return fmt.Errorf("demo seed requires a registry client that supports SetLiveAircraftState")
	}

	aircraft := []domain.Aircraft{
		{
			ID:               "aircraft-eagle-7",
			OperatorID:       "operator-demo",
			AgentID:          "agent-eagle-7",
			TailNumber:       "N108AA",
			Registration:     "N108AA",
			SerialNumber:     "ARX4-2291",
			Name:             "EAGLE-7",
			Model:            "ArcRunner X4",
			Manufacturer:     "Aero Arc",
			Status:           domain.AircraftStatusActive,
			AcceptanceStatus: domain.AcceptanceStatusAccepted,
			RemoteIDSerial:   "RID-EAGLE-7",
			RemoteIDStatus:   domain.RemoteIDStatusBroadcasting,
			ConfigVersion:    "cfg-2026.06",
			SoftwareVersion:  "2.4.1",
			HardwareVersion:  "x4.2",
			CreatedAt:        now.AddDate(0, -8, 0),
			UpdatedAt:        now.Add(-2 * time.Hour),
		},
		{
			ID:               "aircraft-falcon-3",
			OperatorID:       "operator-demo",
			AgentID:          "agent-falcon-3",
			TailNumber:       "N203AA",
			Registration:     "N203AA",
			SerialNumber:     "ARX4-2314",
			Name:             "FALCON-3",
			Model:            "ArcRunner X4",
			Manufacturer:     "Aero Arc",
			Status:           domain.AircraftStatusReview,
			AcceptanceStatus: domain.AcceptanceStatusAccepted,
			RemoteIDSerial:   "RID-FALCON-3",
			RemoteIDStatus:   domain.RemoteIDStatusDegraded,
			ConfigVersion:    "cfg-2026.05",
			SoftwareVersion:  "2.3.9",
			HardwareVersion:  "x4.2",
			CreatedAt:        now.AddDate(0, -7, 0),
			UpdatedAt:        now.Add(-18 * time.Hour),
		},
		{
			ID:               "aircraft-raven-5",
			OperatorID:       "operator-demo",
			AgentID:          "agent-raven-5",
			TailNumber:       "N405AA",
			Registration:     "N405AA",
			SerialNumber:     "CL55-0917",
			Name:             "RAVEN-5",
			Model:            "CargoLite 55",
			Manufacturer:     "Aero Arc",
			Status:           domain.AircraftStatusMaintenance,
			AcceptanceStatus: domain.AcceptanceStatusReview,
			RemoteIDSerial:   "RID-RAVEN-5",
			RemoteIDStatus:   domain.RemoteIDStatusOffline,
			ConfigVersion:    "cfg-2026.04",
			SoftwareVersion:  "2.4.1",
			HardwareVersion:  "cl55.1",
			CreatedAt:        now.AddDate(0, -5, 0),
			UpdatedAt:        now.Add(-26 * time.Hour),
		},
	}
	for _, item := range aircraft {
		if err := durableStore.CreateAircraft(ctx, item); err != nil {
			return fmt.Errorf("seed aircraft %s: %w", item.ID, err)
		}
	}

	batteries := []domain.Battery{
		{ID: "battery-b118", OperatorID: "operator-demo", SerialNumber: "B-118", Model: "ArcPack 6S", StateOfHealth: float64Ptr(91), CycleCount: 142, Status: domain.MaintenanceStatusCurrent, CreatedAt: now.AddDate(0, -10, 0), UpdatedAt: now.Add(-3 * time.Hour)},
		{ID: "battery-b104", OperatorID: "operator-demo", SerialNumber: "B-104", Model: "ArcPack 6S", StateOfHealth: float64Ptr(74), CycleCount: 206, Status: domain.MaintenanceStatusDueSoon, CreatedAt: now.AddDate(0, -11, 0), UpdatedAt: now.Add(-16 * time.Hour)},
		{ID: "battery-b177", OperatorID: "operator-demo", SerialNumber: "B-177", Model: "CargoPack 9S", StateOfHealth: float64Ptr(42), CycleCount: 264, Status: domain.MaintenanceStatusOpen, CreatedAt: now.AddDate(0, -9, 0), UpdatedAt: now.Add(-20 * time.Hour)},
		{ID: "battery-b221", OperatorID: "operator-demo", SerialNumber: "B-221", Model: "ArcPack 6S", StateOfHealth: float64Ptr(88), CycleCount: 95, Status: domain.MaintenanceStatusCurrent, CreatedAt: now.AddDate(0, -4, 0), UpdatedAt: now.Add(-24 * time.Hour)},
	}
	for _, item := range batteries {
		if err := durableStore.CreateBattery(ctx, item); err != nil {
			return fmt.Errorf("seed battery %s: %w", item.ID, err)
		}
	}

	installations := []domain.BatteryInstallation{
		{ID: "install-eagle-7", OperatorID: "operator-demo", AircraftID: "aircraft-eagle-7", BatteryID: "battery-b118", InstalledAt: now.Add(-6 * time.Hour)},
		{ID: "install-falcon-3", OperatorID: "operator-demo", AircraftID: "aircraft-falcon-3", BatteryID: "battery-b104", InstalledAt: now.Add(-30 * time.Hour)},
		{ID: "install-raven-5", OperatorID: "operator-demo", AircraftID: "aircraft-raven-5", BatteryID: "battery-b177", InstalledAt: now.Add(-48 * time.Hour)},
	}
	for _, item := range installations {
		if err := durableStore.RecordBatteryInstallation(ctx, item); err != nil {
			return fmt.Errorf("seed battery installation %s: %w", item.ID, err)
		}
	}

	intents := []domain.OperationalIntent{
		{
			ID:                  "intent-2041",
			OperatorID:          "operator-demo",
			AircraftID:          "aircraft-eagle-7",
			AuthorizationID:     "auth-permit-1",
			Version:             3,
			Name:                "Pipeline patrol",
			Summary:             "Inspect Segment 12 pipeline corridor and recovery pad.",
			UseCase:             "infrastructure_inspection",
			AuthorizationPath:   domain.AuthorizationPathPermit,
			PopulationCategory:  domain.PopulationCategoryTwo,
			Status:              domain.IntentStatusActive,
			ConformanceRequired: true,
			OperatingAreaID:     "area-west-yard",
			RouteSummary:        "West Yard launch -> Segment 12 corridor -> Pad B recovery",
			PlannedStartAt:      now.Add(-18 * time.Minute),
			PlannedEndAt:        now.Add(24 * time.Minute),
			MinAltitudeFtAGL:    float64Ptr(220),
			MaxAltitudeFtAGL:    float64Ptr(310),
			SupervisorID:        "person-dana",
			FlightCoordinatorID: "person-kim",
			ActivatedAt:         timePtr(now.Add(-18 * time.Minute)),
			UpdatedAt:           now.Add(-2 * time.Minute),
		},
		{
			ID:                  "intent-2042",
			OperatorID:          "operator-demo",
			AircraftID:          "aircraft-falcon-3",
			AuthorizationID:     "auth-permit-1",
			Version:             1,
			Name:                "Solar farm inspection",
			Summary:             "Thermal pass over the north solar field.",
			UseCase:             "energy_inspection",
			AuthorizationPath:   domain.AuthorizationPathPermit,
			PopulationCategory:  domain.PopulationCategoryOne,
			Status:              domain.IntentStatusAccepted,
			ConformanceRequired: true,
			OperatingAreaID:     "area-solar-north",
			RouteSummary:        "Pad C -> North field grid -> Pad C",
			PlannedStartAt:      now.Add(55 * time.Minute),
			PlannedEndAt:        now.Add(105 * time.Minute),
			MinAltitudeFtAGL:    float64Ptr(180),
			MaxAltitudeFtAGL:    float64Ptr(260),
			SupervisorID:        "person-dana",
			FlightCoordinatorID: "person-sofia",
			AcceptedAt:          timePtr(now.Add(-3 * time.Hour)),
			UpdatedAt:           now.Add(-45 * time.Minute),
		},
		{
			ID:                  "intent-2043",
			OperatorID:          "operator-demo",
			AircraftID:          "aircraft-raven-5",
			AuthorizationID:     "auth-demo-1",
			Version:             2,
			Name:                "Rural delivery demo",
			Summary:             "Cargo demonstration route pending return-to-service evidence.",
			UseCase:             "cargo_demo",
			AuthorizationPath:   domain.AuthorizationPathDemo,
			PopulationCategory:  domain.PopulationCategoryThree,
			Status:              domain.IntentStatusReview,
			ConformanceRequired: true,
			OperatingAreaID:     "area-demo-route",
			RouteSummary:        "Depot -> Farm Road 41 -> Depot",
			PlannedStartAt:      now.Add(2 * time.Hour),
			PlannedEndAt:        now.Add(3 * time.Hour),
			MinAltitudeFtAGL:    float64Ptr(160),
			MaxAltitudeFtAGL:    float64Ptr(240),
			SupervisorID:        "person-dana",
			FlightCoordinatorID: "person-kim",
			SubmittedAt:         timePtr(now.Add(-22 * time.Hour)),
			UpdatedAt:           now.Add(-1 * time.Hour),
		},
	}
	for _, item := range intents {
		if err := durableStore.CreateOperationalIntent(ctx, item); err != nil {
			return fmt.Errorf("seed operational intent %s: %w", item.ID, err)
		}
	}

	preflightChecks := []domain.PreflightCheck{
		{ID: "preflight-2041-weather", OperatorID: "operator-demo", IntentID: "intent-2041", IntentVersion: 3, AircraftID: "aircraft-eagle-7", Category: domain.PreflightCheckWeather, Source: "weather-feed", Status: domain.PreflightStatusClear, Summary: "Winds 8 kt, visibility 10 sm.", RequirementCode: "WX-001", RuleVersion: "2026.06", ValidUntil: timePtr(now.Add(45 * time.Minute)), EvidenceRecordID: "evidence-preflight-2041", CapturedAt: now.Add(-30 * time.Minute)},
		{ID: "preflight-2041-airspace", OperatorID: "operator-demo", IntentID: "intent-2041", IntentVersion: 3, AircraftID: "aircraft-eagle-7", Category: domain.PreflightCheckAirspace, Source: "airspace-review", Status: domain.PreflightStatusClear, Summary: "Operating volume remains inside approved corridor.", RequirementCode: "AIR-022", RuleVersion: "2026.06", ValidUntil: timePtr(now.Add(90 * time.Minute)), EvidenceRecordID: "evidence-preflight-2041", CapturedAt: now.Add(-28 * time.Minute)},
		{ID: "preflight-2042-remote-id", OperatorID: "operator-demo", IntentID: "intent-2042", IntentVersion: 1, AircraftID: "aircraft-falcon-3", Category: domain.PreflightCheckRemoteID, Source: "rid-monitor", Status: domain.PreflightStatusReview, Summary: "Remote ID degraded on previous heartbeat.", RequirementCode: "RID-010", RuleVersion: "2026.06", ValidUntil: timePtr(now.Add(40 * time.Minute)), EvidenceRecordID: "evidence-preflight-2042", CapturedAt: now.Add(-20 * time.Minute)},
		{ID: "preflight-2043-maintenance", OperatorID: "operator-demo", IntentID: "intent-2043", IntentVersion: 2, AircraftID: "aircraft-raven-5", Category: domain.PreflightCheckMaintenance, Source: "maintenance-control", Status: domain.PreflightStatusBlocked, Summary: "Lighting repair requires qualified maintainer return-to-service sign-off.", RequirementCode: "MX-RTS", RuleVersion: "2026.06", Blocking: true, EvidenceRecordID: "evidence-maint-raven-5", CapturedAt: now.Add(-16 * time.Minute)},
	}
	for _, item := range preflightChecks {
		if err := durableStore.RecordPreflightCheck(ctx, item); err != nil {
			return fmt.Errorf("seed preflight check %s: %w", item.ID, err)
		}
	}

	maintenanceEvents := []domain.MaintenanceEvent{
		{ID: "mx-falcon-battery", OperatorID: "operator-demo", AircraftID: "aircraft-falcon-3", IntentID: "intent-2042", EventType: "battery_health", Severity: domain.SeverityWarning, Status: domain.MaintenanceStatusDueSoon, Title: "Battery pack B-104 below preferred SOH", Notes: "Pack is usable but should be swapped before extended sorties.", Owner: "M. Owens", DueAt: timePtr(now.Add(24 * time.Hour)), OpenedAt: now.AddDate(0, 0, -2)},
		{ID: "mx-raven-lighting", OperatorID: "operator-demo", AircraftID: "aircraft-raven-5", IntentID: "intent-2043", EventType: "lighting", Severity: domain.SeverityCritical, Status: domain.MaintenanceStatusOpen, Title: "Anti-collision lighting repair RTS", Notes: "Intermittent anti-collision lighting during preflight test.", Owner: "Unassigned", DueAt: timePtr(now.Add(-6 * time.Hour)), CorrectiveAction: "Replaced lighting controller; awaiting operational check attachment.", OpenedAt: now.AddDate(0, 0, -3)},
	}
	for _, item := range maintenanceEvents {
		if err := durableStore.RecordMaintenanceEvent(ctx, item); err != nil {
			return fmt.Errorf("seed maintenance event %s: %w", item.ID, err)
		}
	}

	flight := domain.FlightRecord{ID: "flight-2041-a", OperatorID: "operator-demo", AircraftID: "aircraft-eagle-7", IntentID: "intent-2041", IntentVersion: 3, StartedAt: now.Add(-18 * time.Minute), Origin: "West Yard", Destination: "Pad B", Status: domain.FlightStatusActive, MissionType: "pipeline_patrol", TelemetryURI: "memory://flight-2041-a", SampleCount: 5}
	if err := durableStore.CreateFlightRecord(ctx, flight); err != nil {
		return fmt.Errorf("seed flight record %s: %w", flight.ID, err)
	}

	summaries := []domain.ConformanceSummary{
		{ID: "conf-2041", OperatorID: "operator-demo", IntentID: "intent-2041", IntentVersion: 3, FlightID: "flight-2041-a", AircraftID: "aircraft-eagle-7", Status: domain.ConformanceStatusConforming, Score: float64Ptr(0.987), AlertCount: 1, ReportabilityStatus: domain.ReportabilityStatusNo, UpdatedAt: now.Add(-90 * time.Second)},
		{ID: "conf-2042", OperatorID: "operator-demo", IntentID: "intent-2042", IntentVersion: 1, AircraftID: "aircraft-falcon-3", Status: domain.ConformanceStatusUnknown, AlertCount: 0, ReportabilityStatus: domain.ReportabilityStatusNo, UpdatedAt: now.Add(-45 * time.Minute)},
		{ID: "conf-2043", OperatorID: "operator-demo", IntentID: "intent-2043", IntentVersion: 2, AircraftID: "aircraft-raven-5", Status: domain.ConformanceStatusContingent, Score: float64Ptr(0), AlertCount: 0, ReportabilityStatus: domain.ReportabilityStatusReview, UpdatedAt: now.Add(-1 * time.Hour)},
	}
	for _, item := range summaries {
		if err := durableStore.UpsertConformanceSummary(ctx, item); err != nil {
			return fmt.Errorf("seed conformance summary %s: %w", item.ID, err)
		}
	}

	events := []domain.ConformanceEvent{
		{ID: "conf-event-2041-lateral", OperatorID: "operator-demo", IntentID: "intent-2041", IntentVersion: 3, FlightID: "flight-2041-a", AircraftID: "aircraft-eagle-7", Severity: domain.SeverityAdvisory, EventCode: domain.ConformanceEventIntentExit, ExpectedVolumeID: "volume-2041-route", Message: "Lateral drift 18 ft beyond advisory band; returned inside corridor.", Latitude: float64Ptr(35.46712), Longitude: float64Ptr(-97.51481), AltitudeM: float64Ptr(82), AltitudeRef: domain.AltitudeReferenceAGL, ObservedValue: float64Ptr(18), ThresholdValue: float64Ptr(15), DeviationMeters: float64Ptr(5.5), DeviationSeconds: float64Ptr(12), OccurredAt: now.Add(-9 * time.Minute)},
		{ID: "conf-event-2037-link", OperatorID: "operator-demo", IntentID: "intent-2037", FlightID: "flight-2037-a", AircraftID: "aircraft-falcon-3", Severity: domain.SeverityWarning, EventCode: domain.ConformanceEventTelemetryLoss, Message: "Lost telemetry link for 11 seconds on previous flight.", DeviationSeconds: float64Ptr(11), OccurredAt: now.Add(-28 * time.Hour)},
	}
	for _, item := range events {
		if err := durableStore.RecordConformanceEvent(ctx, item); err != nil {
			return fmt.Errorf("seed conformance event %s: %w", item.ID, err)
		}
	}

	evidence := []domain.EvidenceRecord{
		{ID: "evidence-flight-2041", OperatorID: "operator-demo", Type: domain.EvidenceRecordFlight, IntentID: "intent-2041", IntentVersion: 3, FlightID: "flight-2041-a", AircraftID: "aircraft-eagle-7", Status: domain.EvidenceStatusComplete, Title: "Flight record AA-OP-2041", Summary: "Active flight record with replay manifest.", ObjectURI: "memory://evidence/flight-2041", Hash: "77e3f0demo", HashAlgorithm: "sha256", SchemaVersion: "2026.06", GeneratedBy: "aero-arc-api", SourceSystem: "flight-recorder", RetentionUntil: timePtr(now.AddDate(1, 0, 0)), CreatedAt: now.Add(-14 * time.Minute), UpdatedAt: now.Add(-2 * time.Minute)},
		{ID: "evidence-preflight-2041", OperatorID: "operator-demo", Type: domain.EvidenceRecordPreflight, IntentID: "intent-2041", IntentVersion: 3, AircraftID: "aircraft-eagle-7", Status: domain.EvidenceStatusComplete, Title: "Preflight package AA-OP-2041", Summary: "Weather, airspace, aircraft, and personnel checks.", ObjectURI: "memory://evidence/preflight-2041", Hash: "4cc6d1demo", HashAlgorithm: "sha256", SchemaVersion: "2026.06", GeneratedBy: "aero-arc-api", SourceSystem: "preflight", RetentionUntil: timePtr(now.AddDate(1, 0, 0)), CreatedAt: now.Add(-35 * time.Minute), UpdatedAt: now.Add(-28 * time.Minute)},
		{ID: "evidence-maint-raven-5", OperatorID: "operator-demo", Type: domain.EvidenceRecordMaintenance, IntentID: "intent-2043", IntentVersion: 2, AircraftID: "aircraft-raven-5", Status: domain.EvidenceStatusOpen, Title: "RAVEN-5 maintenance release", Summary: "Return-to-service evidence pending maintainer sign-off.", GeneratedBy: "maintenance-control", SourceSystem: "maintenance", CreatedAt: now.Add(-6 * time.Hour), UpdatedAt: now.Add(-30 * time.Minute)},
		{ID: "evidence-reportability-2037", OperatorID: "operator-demo", Type: domain.EvidenceRecordReportability, IntentID: "intent-2037", FlightID: "flight-2037-a", AircraftID: "aircraft-falcon-3", Status: domain.EvidenceStatusReview, Title: "Lost link reportability review", Summary: "Prior flight telemetry loss requires supervisor review.", GeneratedBy: "conformance-monitor", SourceSystem: "reportability", CreatedAt: now.Add(-28 * time.Hour), UpdatedAt: now.Add(-5 * time.Hour)},
	}
	for _, item := range evidence {
		if err := durableStore.RecordEvidence(ctx, item); err != nil {
			return fmt.Errorf("seed evidence record %s: %w", item.ID, err)
		}
	}

	reviews := []domain.ReportabilityReview{
		{ID: "review-2037-link", OperatorID: "operator-demo", IntentID: "intent-2037", FlightID: "flight-2037-a", AircraftID: "aircraft-falcon-3", Trigger: "Lost telemetry link for 11 seconds", Status: domain.ReportabilityStatusReview, Decision: "Supervisor review pending.", EvidenceRecordID: "evidence-reportability-2037", CreatedAt: now.Add(-28 * time.Hour)},
		{ID: "review-2041-lateral", OperatorID: "operator-demo", IntentID: "intent-2041", IntentVersion: 3, FlightID: "flight-2041-a", AircraftID: "aircraft-eagle-7", Trigger: "Brief lateral advisory band exit", Status: domain.ReportabilityStatusNo, Decision: "No report required; remained inside approved operating volume.", EvidenceRecordID: "evidence-flight-2041", CreatedAt: now.Add(-8 * time.Minute), ResolvedAt: timePtr(now.Add(-4 * time.Minute))},
	}
	for _, item := range reviews {
		if err := durableStore.RecordReportabilityReview(ctx, item); err != nil {
			return fmt.Errorf("seed reportability review %s: %w", item.ID, err)
		}
	}

	for i := 0; i < 5; i++ {
		sampleTime := now.Add(time.Duration(i-4) * 3 * time.Minute)
		if err := telemetry.AddSample(ctx, domain.TelemetrySample{
			ID:            fmt.Sprintf("sample-2041-%d", i+1),
			OperatorID:    "operator-demo",
			AircraftID:    "aircraft-eagle-7",
			IntentID:      "intent-2041",
			IntentVersion: 3,
			FlightID:      "flight-2041-a",
			RecordedAt:    sampleTime,
			Latitude:      35.46670 + float64(i)*0.00031,
			Longitude:     -97.51520 + float64(i)*0.00024,
			AltitudeM:     78 + float64(i)*1.8,
			VelocityMPS:   17.5,
			HeadingDeg:    64,
			BatteryPct:    float64Ptr(88 - float64(i)),
		}); err != nil {
			return fmt.Errorf("seed telemetry sample %d: %w", i+1, err)
		}
	}
	if err := telemetry.AddSample(ctx, domain.TelemetrySample{ID: "sample-falcon-latest", OperatorID: "operator-demo", AircraftID: "aircraft-falcon-3", RecordedAt: now.Add(-12 * time.Minute), Latitude: 35.53210, Longitude: -97.42640, AltitudeM: 0, VelocityMPS: 0, HeadingDeg: 0, BatteryPct: float64Ptr(74)}); err != nil {
		return fmt.Errorf("seed falcon telemetry: %w", err)
	}

	if err := replay.PutReplayManifest(ctx, domain.ReplayManifest{
		OperatorID:    "operator-demo",
		FlightID:      "flight-2041-a",
		IntentID:      "intent-2041",
		IntentVersion: 3,
		ObjectURI:     "memory://replay/flight-2041-a",
		ChunkURIs:     []string{"memory://replay/flight-2041-a/chunk-1.jsonl"},
		CreatedAt:     now.Add(-12 * time.Minute),
		ByteLength:    16384,
	}); err != nil {
		return fmt.Errorf("seed replay manifest: %w", err)
	}

	liveStates := []domain.LiveAircraftState{
		{AircraftID: "aircraft-eagle-7", OperatorID: "operator-demo", AgentID: "agent-eagle-7", RelayID: "relay-west-1", Connected: true, LastConnectedAt: now.Add(-30 * time.Minute), LastHeartbeatAt: now.Add(-10 * time.Second), PlacementLastUpdatedAt: now.Add(-45 * time.Minute)},
		{AircraftID: "aircraft-falcon-3", OperatorID: "operator-demo", AgentID: "agent-falcon-3", RelayID: "relay-north-2", Connected: true, LastConnectedAt: now.Add(-5 * time.Hour), LastHeartbeatAt: now.Add(-12 * time.Minute), PlacementLastUpdatedAt: now.Add(-5 * time.Hour)},
	}
	for _, item := range liveStates {
		if err := live.SetLiveAircraftState(ctx, item); err != nil {
			return fmt.Errorf("seed live state %s: %w", item.AircraftID, err)
		}
	}

	return nil
}

func float64Ptr(v float64) *float64 {
	return &v
}

func timePtr(v time.Time) *time.Time {
	return &v
}
