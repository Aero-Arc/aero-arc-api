package domain

import "time"

type FlightRecord struct {
	ID            string       `json:"id"`
	OperatorID    string       `json:"operator_id,omitempty"`
	AircraftID    string       `json:"aircraft_id"`
	IntentID      string       `json:"intent_id,omitempty"`
	IntentVersion int          `json:"intent_version,omitempty"`
	StartedAt     time.Time    `json:"started_at"`
	EndedAt       *time.Time   `json:"ended_at,omitempty"`
	Origin        string       `json:"origin,omitempty"`
	Destination   string       `json:"destination,omitempty"`
	Status        FlightStatus `json:"status"`
	MissionType   string       `json:"mission_type,omitempty"`
	TelemetryURI  string       `json:"telemetry_uri,omitempty"`
	SampleCount   int          `json:"sample_count,omitempty"`
}
