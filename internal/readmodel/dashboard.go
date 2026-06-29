package readmodel

import "github.com/Aero-Arc/aero-arc-api/internal/domain"

type AircraftDashboard struct {
	Aircraft           domain.Aircraft                  `json:"aircraft"`
	OperatingProfile   *domain.AircraftOperatingProfile `json:"operating_profile,omitempty"`
	ActiveBattery      *domain.Battery                  `json:"active_battery,omitempty"`
	MaintenanceEvents  []domain.MaintenanceEvent        `json:"maintenance_events"`
	LatestTelemetry    *domain.TelemetrySample          `json:"latest_telemetry,omitempty"`
	LiveState          *domain.LiveAircraftState        `json:"live_state,omitempty"`
	LiveStateAvailable bool                             `json:"live_state_available"`
	Readiness          domain.Readiness                 `json:"readiness"`
	CurrentIntent      *domain.OperationalIntent        `json:"current_intent,omitempty"`
}

type DashboardMetric struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status,omitempty"`
}

type OverviewDashboard struct {
	Metrics             []DashboardMetric            `json:"metrics"`
	Aircraft            []AircraftDashboard          `json:"aircraft"`
	OperationalIntents  []domain.OperationalIntent   `json:"operational_intents"`
	EvidenceRecords     []domain.EvidenceRecord      `json:"evidence_records"`
	ReportabilityReview []domain.ReportabilityReview `json:"reportability_reviews"`
}

type OperationsDashboard struct {
	Metrics            []DashboardMetric           `json:"metrics"`
	OperationalIntents []domain.OperationalIntent  `json:"operational_intents"`
	Conformance        []domain.ConformanceSummary `json:"conformance"`
}

type PreflightDashboard struct {
	Metrics []DashboardMetric       `json:"metrics"`
	Checks  []domain.PreflightCheck `json:"checks"`
}

type ConformanceDashboard struct {
	Metrics   []DashboardMetric           `json:"metrics"`
	Summaries []domain.ConformanceSummary `json:"summaries"`
	Events    []domain.ConformanceEvent   `json:"events"`
}

type MaintenanceDashboard struct {
	Metrics   []DashboardMetric         `json:"metrics"`
	Events    []domain.MaintenanceEvent `json:"events"`
	Batteries []domain.Battery          `json:"batteries"`
}

type RecordsDashboard struct {
	Metrics             []DashboardMetric            `json:"metrics"`
	EvidenceRecords     []domain.EvidenceRecord      `json:"evidence_records"`
	ReportabilityReview []domain.ReportabilityReview `json:"reportability_reviews"`
}
