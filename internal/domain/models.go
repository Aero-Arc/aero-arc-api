package domain

import "time"

type Aircraft struct {
	ID           string    `json:"id"`
	AgentID      string    `json:"agent_id,omitempty"`
	TailNumber   string    `json:"tail_number"`
	Name         string    `json:"name"`
	Model        string    `json:"model"`
	Manufacturer string    `json:"manufacturer"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Battery struct {
	ID             string     `json:"id"`
	SerialNumber   string     `json:"serial_number"`
	Model          string     `json:"model"`
	StateOfHealth  float64    `json:"state_of_health"`
	CycleCount     int        `json:"cycle_count"`
	ManufacturedAt *time.Time `json:"manufactured_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type BatteryInstallation struct {
	ID          string     `json:"id"`
	AircraftID  string     `json:"aircraft_id"`
	BatteryID   string     `json:"battery_id"`
	InstalledAt time.Time  `json:"installed_at"`
	RemovedAt   *time.Time `json:"removed_at,omitempty"`
}

type MaintenanceEvent struct {
	ID         string     `json:"id"`
	AircraftID string     `json:"aircraft_id"`
	Severity   string     `json:"severity"`
	Title      string     `json:"title"`
	Notes      string     `json:"notes"`
	OpenedAt   time.Time  `json:"opened_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

type OperatingLimit struct {
	ID         string  `json:"id"`
	AircraftID string  `json:"aircraft_id"`
	Name       string  `json:"name"`
	Unit       string  `json:"unit"`
	Minimum    float64 `json:"minimum,omitempty"`
	Maximum    float64 `json:"maximum,omitempty"`
}

type FlightRecord struct {
	ID          string     `json:"id"`
	AircraftID  string     `json:"aircraft_id"`
	IntentID    string     `json:"intent_id,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	Origin      string     `json:"origin,omitempty"`
	Destination string     `json:"destination,omitempty"`
	Status      string     `json:"status"`
}

type OperationalIntent struct {
	ID          string    `json:"id"`
	AircraftID  string    `json:"aircraft_id"`
	Summary     string    `json:"summary"`
	Status      string    `json:"status"`
	SubmittedAt time.Time `json:"submitted_at"`
}

type ConformanceEvent struct {
	ID         string    `json:"id"`
	FlightID   string    `json:"flight_id"`
	Severity   string    `json:"severity"`
	Type       string    `json:"type"`
	Message    string    `json:"message"`
	OccurredAt time.Time `json:"occurred_at"`
}

type TelemetrySample struct {
	ID          string    `json:"id"`
	AircraftID  string    `json:"aircraft_id"`
	FlightID    string    `json:"flight_id,omitempty"`
	RecordedAt  time.Time `json:"recorded_at"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	AltitudeM   float64   `json:"altitude_m"`
	VelocityMPS float64   `json:"velocity_mps"`
	HeadingDeg  float64   `json:"heading_deg"`
	BatteryPct  float64   `json:"battery_pct"`
}

type ReplayManifest struct {
	FlightID   string    `json:"flight_id"`
	ObjectURI  string    `json:"object_uri"`
	ChunkURIs  []string  `json:"chunk_uris"`
	CreatedAt  time.Time `json:"created_at"`
	ByteLength int64     `json:"byte_length"`
}

type LiveAircraftState struct {
	AircraftID             string    `json:"aircraft_id"`
	AgentID                string    `json:"agent_id,omitempty"`
	RelayID                string    `json:"relay_id,omitempty"`
	Connected              bool      `json:"connected"`
	LastConnectedAt        time.Time `json:"last_connected_at,omitempty"`
	LastHeartbeatAt        time.Time `json:"last_heartbeat_at,omitempty"`
	PlacementLastUpdatedAt time.Time `json:"placement_last_updated_at,omitempty"`
}

type Readiness struct {
	Status  string   `json:"status"`
	Reasons []string `json:"reasons"`
}

type AircraftDashboard struct {
	Aircraft           Aircraft           `json:"aircraft"`
	ActiveBattery      *Battery           `json:"active_battery,omitempty"`
	MaintenanceEvents  []MaintenanceEvent `json:"maintenance_events"`
	LatestTelemetry    *TelemetrySample   `json:"latest_telemetry,omitempty"`
	LiveState          *LiveAircraftState `json:"live_state,omitempty"`
	LiveStateAvailable bool               `json:"live_state_available"`
	Readiness          Readiness          `json:"readiness"`
}
