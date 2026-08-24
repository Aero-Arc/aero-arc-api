package domain

import "time"

type ConformanceEvent struct {
	ID               string               `json:"id"`
	OperatorID       string               `json:"operator_id,omitempty"`
	IntentID         string               `json:"intent_id,omitempty"`
	IntentVersion    int                  `json:"intent_version,omitempty"`
	FlightID         string               `json:"flight_id,omitempty"`
	AircraftID       string               `json:"aircraft_id,omitempty"`
	Severity         Severity             `json:"severity"`
	EventCode        ConformanceEventCode `json:"event_code"`
	ExpectedVolumeID string               `json:"expected_volume_id,omitempty"`
	Message          string               `json:"message"`
	Latitude         *float64             `json:"latitude,omitempty"`
	Longitude        *float64             `json:"longitude,omitempty"`
	AltitudeM        *float64             `json:"altitude_m,omitempty"`
	AltitudeRef      AltitudeReference    `json:"altitude_ref,omitempty"`
	ObservedValue    *float64             `json:"observed_value,omitempty"`
	ThresholdValue   *float64             `json:"threshold_value,omitempty"`
	DeviationMeters  *float64             `json:"deviation_meters,omitempty"`
	DeviationSeconds *float64             `json:"deviation_seconds,omitempty"`
	OccurredAt       time.Time            `json:"occurred_at"`
}

type ConformanceSummary struct {
	ID                   string                        `json:"id"`
	OperatorID           string                        `json:"operator_id,omitempty"`
	IntentID             string                        `json:"intent_id"`
	IntentVersion        int                           `json:"intent_version,omitempty"`
	FlightID             string                        `json:"flight_id,omitempty"`
	AircraftID           string                        `json:"aircraft_id"`
	Status               ConformanceStatus             `json:"status"`
	Score                *float64                      `json:"score,omitempty"`
	AlertCount           int                           `json:"alert_count"`
	ReportabilityStatus  ReportabilityStatus           `json:"reportability_status"`
	UpdatedAt            time.Time                     `json:"updated_at"`
	AssignmentID         string                        `json:"assignment_id,omitempty"`
	AssignmentGeneration uint64                        `json:"assignment_generation,omitempty"`
	EvaluationRevision   uint64                        `json:"evaluation_revision,omitempty"`
	EvaluationID         string                        `json:"evaluation_id,omitempty"`
	Condition            string                        `json:"condition,omitempty"`
	MonitoringStatus     string                        `json:"monitoring_status,omitempty"`
	RecordingStatus      string                        `json:"recording_status,omitempty"`
	ObservedAt           *time.Time                    `json:"observed_at,omitempty"`
	FrameID              string                        `json:"frame_id,omitempty"`
	Violations           []ConformanceViolationSummary `json:"violations,omitempty"`
}

// ConformanceViolationSummary describes one live Registry violation without
// replacing the durable incident history represented by ConformanceEvent.
type ConformanceViolationSummary struct {
	ViolationType   string     `json:"violation_type"`
	Phase           string     `json:"phase"`
	OpeningFrameID  string     `json:"opening_frame_id,omitempty"`
	OpenedAt        *time.Time `json:"opened_at,omitempty"`
	LastObservedAt  *time.Time `json:"last_observed_at,omitempty"`
	WorstDeviationM float64    `json:"worst_deviation_m"`
}
