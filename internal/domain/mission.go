package domain

import "time"

// MissionSourceFormat identifies a supported immutable mission source encoding.
type MissionSourceFormat string

const (
	// MissionSourceFormatQGCWPL110 is the QGroundControl/Mission Planner WPL 110 text format.
	MissionSourceFormatQGCWPL110 MissionSourceFormat = "qgc_wpl_110"
)

// MissionValidationFinding records one parser or mission-policy result.
type MissionValidationFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Sequence *int   `json:"sequence,omitempty"`
}

// MissionItem is one validated MAVLink mission item in immutable sequence order.
type MissionItem struct {
	Sequence     int     `json:"sequence"`
	Current      bool    `json:"current"`
	Frame        int     `json:"frame"`
	Command      int     `json:"command"`
	Param1       float64 `json:"param1"`
	Param2       float64 `json:"param2"`
	Param3       float64 `json:"param3"`
	Param4       float64 `json:"param4"`
	LatitudeE7   int32   `json:"latitude_e7"`
	LongitudeE7  int32   `json:"longitude_e7"`
	AltitudeM    float64 `json:"altitude_m"`
	Autocontinue bool    `json:"autocontinue"`
}

// Mission is one immutable, flight-bound mission import version.
type Mission struct {
	ID                 string                     `json:"id"`
	Version            int                        `json:"version"`
	OperatorID         string                     `json:"operator_id"`
	FlightID           string                     `json:"flight_id"`
	AircraftID         string                     `json:"aircraft_id"`
	IntentID           string                     `json:"intent_id"`
	IntentVersion      int                        `json:"intent_version"`
	SourceFormat       MissionSourceFormat        `json:"source_format"`
	SourceSHA256       string                     `json:"source_sha256"`
	MissionDigest      string                     `json:"mission_digest"`
	IdempotencyKey     string                     `json:"-"`
	IdempotencyRequest string                     `json:"-"`
	ValidationFindings []MissionValidationFinding `json:"validation_findings"`
	Items              []MissionItem              `json:"items"`
	CreatedAt          time.Time                  `json:"created_at"`
}

// MissionDeploymentStatus is the API's durable view of one exact Agent command.
type MissionDeploymentStatus string

const (
	MissionDeploymentPending                MissionDeploymentStatus = "pending"
	MissionDeploymentApplied                MissionDeploymentStatus = "applied"
	MissionDeploymentAlreadyApplied         MissionDeploymentStatus = "already_applied"
	MissionDeploymentRejected               MissionDeploymentStatus = "rejected"
	MissionDeploymentTemporaryError         MissionDeploymentStatus = "temporary_error"
	MissionDeploymentOutcomeUnknown         MissionDeploymentStatus = "outcome_unknown"
	MissionDeploymentBindingMismatch        MissionDeploymentStatus = "binding_mismatch"
	MissionDeploymentOnboardMissionMismatch MissionDeploymentStatus = "onboard_mission_mismatch"
)

// MissionDeployment records the durable request and observed result for one
// immutable mission upload command. Routing is derived server-side.
type MissionDeployment struct {
	ID                        string                  `json:"id"`
	Revision                  int64                   `json:"-"`
	OperatorID                string                  `json:"operator_id"`
	FlightID                  string                  `json:"flight_id"`
	AircraftID                string                  `json:"aircraft_id"`
	AgentID                   string                  `json:"-"`
	IntentID                  string                  `json:"intent_id"`
	IntentVersion             int                     `json:"intent_version"`
	MissionID                 string                  `json:"mission_id"`
	MissionVersion            int                     `json:"mission_version"`
	MissionDigest             string                  `json:"mission_digest"`
	CommandID                 string                  `json:"command_id"`
	OperationContextCommandID string                  `json:"-"`
	IdempotencyKey            string                  `json:"-"`
	IdempotencyRequest        string                  `json:"-"`
	Status                    MissionDeploymentStatus `json:"status"`
	Message                   string                  `json:"message,omitempty"`
	UploadedItemCount         uint32                  `json:"uploaded_item_count,omitempty"`
	OnboardMissionDigest      string                  `json:"onboard_mission_digest,omitempty"`
	MAVLinkMissionAckType     *uint32                 `json:"mavlink_mission_ack_type,omitempty"`
	IssuedAt                  time.Time               `json:"issued_at"`
	ExpiresAt                 time.Time               `json:"expires_at"`
	CompletedAt               *time.Time              `json:"completed_at,omitempty"`
	AttemptCount              int                     `json:"attempt_count"`
	CreatedAt                 time.Time               `json:"created_at"`
	UpdatedAt                 time.Time               `json:"updated_at"`
}
