package domain

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
type AltitudeReference string
type OperationalVolumeType string
type ConformanceEventCode string
type ComplianceFindingStatus string
type DeconflictionPosture string
type ConflictFindingStatus string
type ConflictFindingSourceType string

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
	IntentStatusDraft      IntentStatus = "draft"
	IntentStatusSubmitted  IntentStatus = "submitted"
	IntentStatusReview     IntentStatus = "review"
	IntentStatusAccepted   IntentStatus = "accepted"
	IntentStatusRejected   IntentStatus = "rejected"
	IntentStatusActive     IntentStatus = "active"
	IntentStatusComplete   IntentStatus = "complete"
	IntentStatusCanceled   IntentStatus = "canceled"
	IntentStatusSuperseded IntentStatus = "superseded"
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
	ConformanceStatusUnknown       ConformanceStatus = "unknown"
	ConformanceStatusConforming    ConformanceStatus = "conforming"
	ConformanceStatusNonConforming ConformanceStatus = "non_conforming"
	ConformanceStatusContingent    ConformanceStatus = "contingent"
	ConformanceStatusEmergency     ConformanceStatus = "emergency"
	ConformanceStatusComplete      ConformanceStatus = "complete"
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

const (
	AltitudeReferenceAGL      AltitudeReference = "agl"
	AltitudeReferenceMSL      AltitudeReference = "msl"
	AltitudeReferenceWGS84    AltitudeReference = "wgs84"
	AltitudeReferenceRelative AltitudeReference = "relative"
	AltitudeReferenceBaro     AltitudeReference = "barometric"
)

const (
	OperationalVolumeRoute       OperationalVolumeType = "route"
	OperationalVolumeLoiter      OperationalVolumeType = "loiter"
	OperationalVolumeContingency OperationalVolumeType = "contingency"
	OperationalVolumeEmergency   OperationalVolumeType = "emergency"
)

const (
	ConformanceEventIntentExit        ConformanceEventCode = "intent_exit"
	ConformanceEventAltitudeDeviation ConformanceEventCode = "altitude_deviation"
	ConformanceEventTelemetryLoss     ConformanceEventCode = "telemetry_loss"
	ConformanceEventC2Degraded        ConformanceEventCode = "c2_degraded"
	ConformanceEventRemoteIDDegraded  ConformanceEventCode = "remote_id_degraded"
)

const (
	ComplianceFindingPass   ComplianceFindingStatus = "pass"
	ComplianceFindingReview ComplianceFindingStatus = "review"
	ComplianceFindingFail   ComplianceFindingStatus = "fail"
	ComplianceFindingWaived ComplianceFindingStatus = "waived"
)

const (
	DeconflictionPostureClear             DeconflictionPosture = "clear"
	DeconflictionPostureConflict          DeconflictionPosture = "conflict"
	DeconflictionPosturePotentialConflict DeconflictionPosture = "potential_conflict"
	DeconflictionPostureIndeterminate     DeconflictionPosture = "indeterminate"
)

const (
	ConflictFindingStatusClear             ConflictFindingStatus = "clear"
	ConflictFindingStatusConflict          ConflictFindingStatus = "conflict"
	ConflictFindingStatusPotentialConflict ConflictFindingStatus = "potential_conflict"
	ConflictFindingStatusIndeterminate     ConflictFindingStatus = "indeterminate"
)

const (
	ConflictFindingSourceLocal    ConflictFindingSourceType = "local"
	ConflictFindingSourceExternal ConflictFindingSourceType = "external"
)
