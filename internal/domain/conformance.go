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
	ID                  string              `json:"id"`
	OperatorID          string              `json:"operator_id,omitempty"`
	IntentID            string              `json:"intent_id"`
	IntentVersion       int                 `json:"intent_version,omitempty"`
	FlightID            string              `json:"flight_id,omitempty"`
	AircraftID          string              `json:"aircraft_id"`
	Status              ConformanceStatus   `json:"status"`
	Score               *float64            `json:"score,omitempty"`
	AlertCount          int                 `json:"alert_count"`
	ReportabilityStatus ReportabilityStatus `json:"reportability_status"`
	UpdatedAt           time.Time           `json:"updated_at"`
}
