package domain

import "time"

type TelemetrySample struct {
	ID            string    `json:"id"`
	OperatorID    string    `json:"operator_id,omitempty"`
	AircraftID    string    `json:"aircraft_id"`
	IntentID      string    `json:"intent_id,omitempty"`
	IntentVersion int       `json:"intent_version,omitempty"`
	FlightID      string    `json:"flight_id,omitempty"`
	RecordedAt    time.Time `json:"recorded_at"`
	Latitude      float64   `json:"latitude"`
	Longitude     float64   `json:"longitude"`
	AltitudeM     float64   `json:"altitude_m"`
	VelocityMPS   float64   `json:"velocity_mps"`
	HeadingDeg    float64   `json:"heading_deg"`
	BatteryPct    *float64  `json:"battery_pct,omitempty"`
}

type ReplayManifest struct {
	OperatorID    string    `json:"operator_id,omitempty"`
	FlightID      string    `json:"flight_id"`
	IntentID      string    `json:"intent_id,omitempty"`
	IntentVersion int       `json:"intent_version,omitempty"`
	ObjectURI     string    `json:"object_uri"`
	ChunkURIs     []string  `json:"chunk_uris"`
	CreatedAt     time.Time `json:"created_at"`
	ByteLength    int64     `json:"byte_length"`
}

type LiveAircraftState struct {
	AircraftID             string    `json:"aircraft_id"`
	OperatorID             string    `json:"operator_id,omitempty"`
	AgentID                string    `json:"agent_id,omitempty"`
	RelayID                string    `json:"relay_id,omitempty"`
	Connected              bool      `json:"connected"`
	LastConnectedAt        time.Time `json:"last_connected_at,omitempty"`
	LastHeartbeatAt        time.Time `json:"last_heartbeat_at,omitempty"`
	PlacementLastUpdatedAt time.Time `json:"placement_last_updated_at,omitempty"`
}
