package domain

import "time"

type Aircraft struct {
	ID               string           `json:"id"`
	OperatorID       string           `json:"operator_id,omitempty"`
	AgentID          string           `json:"agent_id,omitempty"`
	TailNumber       string           `json:"tail_number"`
	Registration     string           `json:"registration,omitempty"`
	SerialNumber     string           `json:"serial_number,omitempty"`
	Name             string           `json:"name"`
	Model            string           `json:"model"`
	Manufacturer     string           `json:"manufacturer"`
	Status           AircraftStatus   `json:"status"`
	AcceptanceStatus AcceptanceStatus `json:"acceptance_status"`
	RemoteIDSerial   string           `json:"remote_id_serial,omitempty"`
	RemoteIDStatus   RemoteIDStatus   `json:"remote_id_status"`
	ConfigVersion    string           `json:"config_version,omitempty"`
	SoftwareVersion  string           `json:"software_version,omitempty"`
	HardwareVersion  string           `json:"hardware_version,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at"`
}

type Battery struct {
	ID             string            `json:"id"`
	OperatorID     string            `json:"operator_id,omitempty"`
	SerialNumber   string            `json:"serial_number"`
	Model          string            `json:"model"`
	StateOfHealth  *float64          `json:"state_of_health,omitempty"`
	CycleCount     int               `json:"cycle_count"`
	Status         MaintenanceStatus `json:"status"`
	ManufacturedAt *time.Time        `json:"manufactured_at,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type BatteryInstallation struct {
	ID          string     `json:"id"`
	OperatorID  string     `json:"operator_id,omitempty"`
	AircraftID  string     `json:"aircraft_id"`
	BatteryID   string     `json:"battery_id"`
	InstalledAt time.Time  `json:"installed_at"`
	RemovedAt   *time.Time `json:"removed_at,omitempty"`
}

type OperatingLimit struct {
	ID         string   `json:"id"`
	OperatorID string   `json:"operator_id,omitempty"`
	AircraftID string   `json:"aircraft_id"`
	Name       string   `json:"name"`
	Unit       string   `json:"unit"`
	Minimum    *float64 `json:"minimum,omitempty"`
	Maximum    *float64 `json:"maximum,omitempty"`
}

type AircraftOperatingProfile struct {
	AircraftID            string    `json:"aircraft_id"`
	OperatorID            string    `json:"operator_id,omitempty"`
	MaxGroundspeedKt      *float64  `json:"max_groundspeed_kt,omitempty"`
	MaxTakeoffWeightLb    *float64  `json:"max_takeoff_weight_lb,omitempty"`
	MaxAltitudeFtAGL      *float64  `json:"max_altitude_ft_agl,omitempty"`
	WeatherEnvelope       string    `json:"weather_envelope,omitempty"`
	DAACapability         string    `json:"daa_capability,omitempty"`
	LightingStatus        string    `json:"lighting_status,omitempty"`
	PNTIntegrity          string    `json:"pnt_integrity,omitempty"`
	ManufacturerLimitsURI string    `json:"manufacturer_limits_uri,omitempty"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type Readiness struct {
	Status  ReadinessStatus `json:"status"`
	Reasons []string        `json:"reasons"`
}
