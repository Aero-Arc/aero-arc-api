package domain

import "time"

type OperationalIntent struct {
	ID                  string             `json:"id"`
	OperatorID          string             `json:"operator_id,omitempty"`
	AircraftID          string             `json:"aircraft_id"`
	AuthorizationID     string             `json:"authorization_id,omitempty"`
	Version             int                `json:"version,omitempty"`
	Name                string             `json:"name"`
	Summary             string             `json:"summary"`
	UseCase             string             `json:"use_case,omitempty"`
	AuthorizationPath   AuthorizationPath  `json:"authorization_path"`
	PopulationCategory  PopulationCategory `json:"population_category"`
	Status              IntentStatus       `json:"status"`
	ConformanceRequired bool               `json:"conformance_required"`
	OperatingAreaID     string             `json:"operating_area_id,omitempty"`
	RouteSummary        string             `json:"route_summary,omitempty"`
	PlannedStartAt      time.Time          `json:"planned_start_at"`
	PlannedEndAt        time.Time          `json:"planned_end_at"`
	MinAltitudeFtAGL    *float64           `json:"min_altitude_ft_agl,omitempty"`
	MaxAltitudeFtAGL    *float64           `json:"max_altitude_ft_agl,omitempty"`
	SupervisorID        string             `json:"supervisor_id,omitempty"`
	FlightCoordinatorID string             `json:"flight_coordinator_id,omitempty"`
	SubmittedAt         *time.Time         `json:"submitted_at,omitempty"`
	AcceptedAt          *time.Time         `json:"accepted_at,omitempty"`
	ActivatedAt         *time.Time         `json:"activated_at,omitempty"`
	CompletedAt         *time.Time         `json:"completed_at,omitempty"`
	CanceledAt          *time.Time         `json:"canceled_at,omitempty"`
	SupersededAt        *time.Time         `json:"superseded_at,omitempty"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

type RegulatoryAuthorization struct {
	ID               string            `json:"id"`
	OperatorID       string            `json:"operator_id"`
	Path             AuthorizationPath `json:"path"`
	Reference        string            `json:"reference,omitempty"`
	Scope            string            `json:"scope,omitempty"`
	ValidFrom        time.Time         `json:"valid_from"`
	ValidUntil       *time.Time        `json:"valid_until,omitempty"`
	EvidenceRecordID string            `json:"evidence_record_id,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type OperationalVolume struct {
	ID            string                `json:"id"`
	OperatorID    string                `json:"operator_id,omitempty"`
	IntentID      string                `json:"intent_id"`
	IntentVersion int                   `json:"intent_version,omitempty"`
	Sequence      int                   `json:"sequence"`
	GeometryURI   string                `json:"geometry_uri,omitempty"`
	GeoJSON       string                `json:"geojson,omitempty"`
	MinAltitudeM  float64               `json:"min_altitude_m"`
	MaxAltitudeM  float64               `json:"max_altitude_m"`
	AltitudeRef   AltitudeReference     `json:"altitude_ref"`
	StartsAt      time.Time             `json:"starts_at"`
	EndsAt        time.Time             `json:"ends_at"`
	BufferMeters  *float64              `json:"buffer_meters,omitempty"`
	VolumeType    OperationalVolumeType `json:"volume_type,omitempty"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
}
