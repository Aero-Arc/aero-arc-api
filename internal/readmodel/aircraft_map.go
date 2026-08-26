package readmodel

import "github.com/Aero-Arc/aero-arc-api/internal/domain"

type AircraftMapView struct {
	Aircraft           domain.Aircraft               `json:"aircraft"`
	LiveState          *domain.LiveAircraftState     `json:"live_state,omitempty"`
	LiveStateAvailable bool                          `json:"live_state_available"`
	LatestTelemetry    *domain.TelemetrySample       `json:"latest_telemetry,omitempty"`
	Telemetry          domain.AircraftTelemetryState `json:"telemetry"`
	ReplaySamples      []domain.TelemetrySample      `json:"replay_samples"`
	ActiveIntent       *domain.OperationalIntent     `json:"active_intent,omitempty"`
	OperationalVolumes []domain.OperationalVolume    `json:"operational_volumes"`
	CommandedMission   *domain.Mission               `json:"commanded_mission,omitempty"`
	ConformanceSummary *domain.ConformanceSummary    `json:"conformance_summary,omitempty"`
	ConformanceEvents  []domain.ConformanceEvent     `json:"conformance_events"`
}
