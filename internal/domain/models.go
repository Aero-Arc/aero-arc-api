package domain

import "time"

type AircraftStatus string
type AcceptanceStatus string
type RemoteIDStatus string
type MaintenanceStatus string
type Severity string
type ReadinessStatus string
type FlightStatus string
type IntentStatus string
type AuthorizationPath string
type PopulationCategory string
type PreflightStatus string
type PreflightCheckCategory string
type EvidenceRecordType string
type EvidenceStatus string
type ConformanceStatus string
type ReportabilityStatus string
type PersonnelRole string
type SecurityAssessmentStatus string

const (
	AircraftStatusActive      AircraftStatus = "active"
	AircraftStatusInactive    AircraftStatus = "inactive"
	AircraftStatusMaintenance AircraftStatus = "maintenance"
	AircraftStatusReview      AircraftStatus = "review"
)

const (
	AcceptanceStatusDraft    AcceptanceStatus = "draft"
	AcceptanceStatusReview   AcceptanceStatus = "review"
	AcceptanceStatusAccepted AcceptanceStatus = "accepted"
	AcceptanceStatusRejected AcceptanceStatus = "rejected"
	AcceptanceStatusExpired  AcceptanceStatus = "expired"
)

const (
	RemoteIDStatusUnknown      RemoteIDStatus = "unknown"
	RemoteIDStatusBroadcasting RemoteIDStatus = "broadcasting"
	RemoteIDStatusOffline      RemoteIDStatus = "offline"
	RemoteIDStatusDegraded     RemoteIDStatus = "degraded"
)

const (
	MaintenanceStatusCurrent MaintenanceStatus = "current"
	MaintenanceStatusDueSoon MaintenanceStatus = "due_soon"
	MaintenanceStatusOpen    MaintenanceStatus = "open"
	MaintenanceStatusOverdue MaintenanceStatus = "overdue"
	MaintenanceStatusClosed  MaintenanceStatus = "closed"
)

const (
	SeverityInfo     Severity = "info"
	SeverityAdvisory Severity = "advisory"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

const (
	ReadinessStatusReady   ReadinessStatus = "ready"
	ReadinessStatusReview  ReadinessStatus = "review"
	ReadinessStatusWarning ReadinessStatus = "warning"
	ReadinessStatusBlocked ReadinessStatus = "blocked"
	ReadinessStatusUnknown ReadinessStatus = "unknown"
)

const (
	FlightStatusPlanned     FlightStatus = "planned"
	FlightStatusActive      FlightStatus = "active"
	FlightStatusComplete    FlightStatus = "complete"
	FlightStatusAborted     FlightStatus = "aborted"
	FlightStatusInterrupted FlightStatus = "interrupted"
)

const (
	IntentStatusDraft    IntentStatus = "draft"
	IntentStatusReview   IntentStatus = "review"
	IntentStatusReady    IntentStatus = "ready"
	IntentStatusActive   IntentStatus = "active"
	IntentStatusBlocked  IntentStatus = "blocked"
	IntentStatusComplete IntentStatus = "complete"
	IntentStatusCanceled IntentStatus = "canceled"
)

const (
	AuthorizationPathPermit      AuthorizationPath = "permit"
	AuthorizationPathCertificate AuthorizationPath = "certificate"
	AuthorizationPathWaiver      AuthorizationPath = "waiver"
	AuthorizationPathExemption   AuthorizationPath = "exemption"
	AuthorizationPathDemo        AuthorizationPath = "demo"
	AuthorizationPathUnknown     AuthorizationPath = "unknown"
)

const (
	PopulationCategoryOne     PopulationCategory = "cat_1"
	PopulationCategoryTwo     PopulationCategory = "cat_2"
	PopulationCategoryThree   PopulationCategory = "cat_3"
	PopulationCategoryFour    PopulationCategory = "cat_4"
	PopulationCategoryFive    PopulationCategory = "cat_5"
	PopulationCategoryUnknown PopulationCategory = "unknown"
)

const (
	PreflightStatusClear   PreflightStatus = "clear"
	PreflightStatusReview  PreflightStatus = "review"
	PreflightStatusAction  PreflightStatus = "action"
	PreflightStatusBlocked PreflightStatus = "blocked"
)

const (
	PreflightCheckWeather       PreflightCheckCategory = "weather"
	PreflightCheckNOTAM         PreflightCheckCategory = "notam"
	PreflightCheckAirspace      PreflightCheckCategory = "airspace"
	PreflightCheckPopulation    PreflightCheckCategory = "population"
	PreflightCheckObstacle      PreflightCheckCategory = "obstacle"
	PreflightCheckBattery       PreflightCheckCategory = "battery"
	PreflightCheckMaintenance   PreflightCheckCategory = "maintenance"
	PreflightCheckRemoteID      PreflightCheckCategory = "remote_id"
	PreflightCheckPersonnel     PreflightCheckCategory = "personnel"
	PreflightCheckCybersecurity PreflightCheckCategory = "cybersecurity"
)

const (
	EvidenceRecordFlight        EvidenceRecordType = "flight_record"
	EvidenceRecordPreflight     EvidenceRecordType = "preflight_package"
	EvidenceRecordMaintenance   EvidenceRecordType = "maintenance_release"
	EvidenceRecordConformance   EvidenceRecordType = "conformance_record"
	EvidenceRecordReportability EvidenceRecordType = "reportability_review"
	EvidenceRecordSecurity      EvidenceRecordType = "security_report"
	EvidenceRecordMonthlyExport EvidenceRecordType = "monthly_export"
)

const (
	EvidenceStatusOpen     EvidenceStatus = "open"
	EvidenceStatusReview   EvidenceStatus = "review"
	EvidenceStatusComplete EvidenceStatus = "complete"
	EvidenceStatusExported EvidenceStatus = "exported"
)

const (
	ConformanceStatusNotRequired ConformanceStatus = "not_required"
	ConformanceStatusRequired    ConformanceStatus = "required"
	ConformanceStatusActive      ConformanceStatus = "active"
	ConformanceStatusComplete    ConformanceStatus = "complete"
	ConformanceStatusDegraded    ConformanceStatus = "degraded"
	ConformanceStatusBlocked     ConformanceStatus = "blocked"
)

const (
	ReportabilityStatusNo         ReportabilityStatus = "no"
	ReportabilityStatusReview     ReportabilityStatus = "review"
	ReportabilityStatusReportable ReportabilityStatus = "reportable"
	ReportabilityStatusClosed     ReportabilityStatus = "closed"
)

const (
	PersonnelRoleSupervisor        PersonnelRole = "operations_supervisor"
	PersonnelRoleFlightCoordinator PersonnelRole = "flight_coordinator"
	PersonnelRoleMaintenance       PersonnelRole = "maintenance"
	PersonnelRoleGroundHandler     PersonnelRole = "ground_handler"
	PersonnelRoleSecurity          PersonnelRole = "security"
)

const (
	SecurityAssessmentUnknown SecurityAssessmentStatus = "unknown"
	SecurityAssessmentPending SecurityAssessmentStatus = "pending"
	SecurityAssessmentCleared SecurityAssessmentStatus = "cleared"
	SecurityAssessmentExpired SecurityAssessmentStatus = "expired"
	SecurityAssessmentDenied  SecurityAssessmentStatus = "denied"
)

type Aircraft struct {
	ID               string           `json:"id"`
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
	SerialNumber   string            `json:"serial_number"`
	Model          string            `json:"model"`
	StateOfHealth  float64           `json:"state_of_health"`
	CycleCount     int               `json:"cycle_count"`
	Status         MaintenanceStatus `json:"status"`
	ManufacturedAt *time.Time        `json:"manufactured_at,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type BatteryInstallation struct {
	ID          string     `json:"id"`
	AircraftID  string     `json:"aircraft_id"`
	BatteryID   string     `json:"battery_id"`
	InstalledAt time.Time  `json:"installed_at"`
	RemovedAt   *time.Time `json:"removed_at,omitempty"`
}

type MaintenanceEvent struct {
	ID                string            `json:"id"`
	AircraftID        string            `json:"aircraft_id"`
	IntentID          string            `json:"intent_id,omitempty"`
	EventType         string            `json:"event_type,omitempty"`
	Severity          Severity          `json:"severity"`
	Status            MaintenanceStatus `json:"status"`
	Title             string            `json:"title"`
	Notes             string            `json:"notes"`
	Owner             string            `json:"owner,omitempty"`
	DueAt             *time.Time        `json:"due_at,omitempty"`
	CorrectiveAction  string            `json:"corrective_action,omitempty"`
	OpenedAt          time.Time         `json:"opened_at"`
	ResolvedAt        *time.Time        `json:"resolved_at,omitempty"`
	ReturnToServiceAt *time.Time        `json:"return_to_service_at,omitempty"`
	ReturnToServiceBy string            `json:"return_to_service_by,omitempty"`
}

type OperatingLimit struct {
	ID         string  `json:"id"`
	AircraftID string  `json:"aircraft_id"`
	Name       string  `json:"name"`
	Unit       string  `json:"unit"`
	Minimum    float64 `json:"minimum,omitempty"`
	Maximum    float64 `json:"maximum,omitempty"`
}

type AircraftOperatingProfile struct {
	AircraftID            string    `json:"aircraft_id"`
	MaxGroundspeedKt      float64   `json:"max_groundspeed_kt,omitempty"`
	MaxTakeoffWeightLb    float64   `json:"max_takeoff_weight_lb,omitempty"`
	MaxAltitudeFtAGL      float64   `json:"max_altitude_ft_agl,omitempty"`
	WeatherEnvelope       string    `json:"weather_envelope,omitempty"`
	DAACapability         string    `json:"daa_capability,omitempty"`
	LightingStatus        string    `json:"lighting_status,omitempty"`
	PNTIntegrity          string    `json:"pnt_integrity,omitempty"`
	ManufacturerLimitsURI string    `json:"manufacturer_limits_uri,omitempty"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type FlightRecord struct {
	ID           string       `json:"id"`
	AircraftID   string       `json:"aircraft_id"`
	IntentID     string       `json:"intent_id,omitempty"`
	StartedAt    time.Time    `json:"started_at"`
	EndedAt      *time.Time   `json:"ended_at,omitempty"`
	Origin       string       `json:"origin,omitempty"`
	Destination  string       `json:"destination,omitempty"`
	Status       FlightStatus `json:"status"`
	MissionType  string       `json:"mission_type,omitempty"`
	TelemetryURI string       `json:"telemetry_uri,omitempty"`
	SampleCount  int          `json:"sample_count,omitempty"`
}

type OperationalIntent struct {
	ID                  string             `json:"id"`
	AircraftID          string             `json:"aircraft_id"`
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
	MinAltitudeFtAGL    float64            `json:"min_altitude_ft_agl,omitempty"`
	MaxAltitudeFtAGL    float64            `json:"max_altitude_ft_agl,omitempty"`
	SupervisorID        string             `json:"supervisor_id,omitempty"`
	FlightCoordinatorID string             `json:"flight_coordinator_id,omitempty"`
	SubmittedAt         time.Time          `json:"submitted_at"`
	UpdatedAt           time.Time          `json:"updated_at"`
}

type ConformanceEvent struct {
	ID         string    `json:"id"`
	IntentID   string    `json:"intent_id,omitempty"`
	FlightID   string    `json:"flight_id"`
	AircraftID string    `json:"aircraft_id,omitempty"`
	Severity   Severity  `json:"severity"`
	Type       string    `json:"type"`
	Message    string    `json:"message"`
	Latitude   float64   `json:"latitude,omitempty"`
	Longitude  float64   `json:"longitude,omitempty"`
	AltitudeFt float64   `json:"altitude_ft,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

type ConformanceSummary struct {
	ID                  string              `json:"id"`
	IntentID            string              `json:"intent_id"`
	FlightID            string              `json:"flight_id,omitempty"`
	AircraftID          string              `json:"aircraft_id"`
	Status              ConformanceStatus   `json:"status"`
	Score               float64             `json:"score"`
	AlertCount          int                 `json:"alert_count"`
	ReportabilityStatus ReportabilityStatus `json:"reportability_status"`
	UpdatedAt           time.Time           `json:"updated_at"`
}

type PreflightCheck struct {
	ID               string                 `json:"id"`
	IntentID         string                 `json:"intent_id"`
	AircraftID       string                 `json:"aircraft_id,omitempty"`
	Category         PreflightCheckCategory `json:"category"`
	Source           string                 `json:"source"`
	Status           PreflightStatus        `json:"status"`
	Summary          string                 `json:"summary"`
	EvidenceRecordID string                 `json:"evidence_record_id,omitempty"`
	CapturedAt       time.Time              `json:"captured_at"`
}

type EvidenceRecord struct {
	ID         string             `json:"id"`
	Type       EvidenceRecordType `json:"type"`
	IntentID   string             `json:"intent_id,omitempty"`
	FlightID   string             `json:"flight_id,omitempty"`
	AircraftID string             `json:"aircraft_id,omitempty"`
	Status     EvidenceStatus     `json:"status"`
	Title      string             `json:"title"`
	Summary    string             `json:"summary,omitempty"`
	ObjectURI  string             `json:"object_uri,omitempty"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

type ReportabilityReview struct {
	ID               string              `json:"id"`
	IntentID         string              `json:"intent_id,omitempty"`
	FlightID         string              `json:"flight_id,omitempty"`
	AircraftID       string              `json:"aircraft_id,omitempty"`
	Trigger          string              `json:"trigger"`
	Status           ReportabilityStatus `json:"status"`
	Decision         string              `json:"decision,omitempty"`
	EvidenceRecordID string              `json:"evidence_record_id,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	ResolvedAt       *time.Time          `json:"resolved_at,omitempty"`
}

type OperationsPersonnel struct {
	ID                       string                   `json:"id"`
	Name                     string                   `json:"name"`
	Role                     PersonnelRole            `json:"role"`
	QualificationStatus      ReadinessStatus          `json:"qualification_status"`
	SecurityAssessmentStatus SecurityAssessmentStatus `json:"security_assessment_status"`
	RecentExperienceHours    float64                  `json:"recent_experience_hours,omitempty"`
	DutyStartedAt            *time.Time               `json:"duty_started_at,omitempty"`
	RestedAt                 *time.Time               `json:"rested_at,omitempty"`
	UpdatedAt                time.Time                `json:"updated_at"`
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
	Status  ReadinessStatus `json:"status"`
	Reasons []string        `json:"reasons"`
}

type AircraftDashboard struct {
	Aircraft           Aircraft                  `json:"aircraft"`
	OperatingProfile   *AircraftOperatingProfile `json:"operating_profile,omitempty"`
	ActiveBattery      *Battery                  `json:"active_battery,omitempty"`
	MaintenanceEvents  []MaintenanceEvent        `json:"maintenance_events"`
	LatestTelemetry    *TelemetrySample          `json:"latest_telemetry,omitempty"`
	LiveState          *LiveAircraftState        `json:"live_state,omitempty"`
	LiveStateAvailable bool                      `json:"live_state_available"`
	Readiness          Readiness                 `json:"readiness"`
}

type DashboardMetric struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status,omitempty"`
}

type OverviewDashboard struct {
	Metrics             []DashboardMetric     `json:"metrics"`
	Aircraft            []AircraftDashboard   `json:"aircraft"`
	OperationalIntents  []OperationalIntent   `json:"operational_intents"`
	EvidenceRecords     []EvidenceRecord      `json:"evidence_records"`
	ReportabilityReview []ReportabilityReview `json:"reportability_reviews"`
}

type OperationsDashboard struct {
	Metrics            []DashboardMetric    `json:"metrics"`
	OperationalIntents []OperationalIntent  `json:"operational_intents"`
	Conformance        []ConformanceSummary `json:"conformance"`
}

type PreflightDashboard struct {
	Metrics []DashboardMetric `json:"metrics"`
	Checks  []PreflightCheck  `json:"checks"`
}

type ConformanceDashboard struct {
	Metrics   []DashboardMetric    `json:"metrics"`
	Summaries []ConformanceSummary `json:"summaries"`
	Events    []ConformanceEvent   `json:"events"`
}

type MaintenanceDashboard struct {
	Metrics   []DashboardMetric  `json:"metrics"`
	Events    []MaintenanceEvent `json:"events"`
	Batteries []Battery          `json:"batteries"`
}

type RecordsDashboard struct {
	Metrics             []DashboardMetric     `json:"metrics"`
	EvidenceRecords     []EvidenceRecord      `json:"evidence_records"`
	ReportabilityReview []ReportabilityReview `json:"reportability_reviews"`
}
