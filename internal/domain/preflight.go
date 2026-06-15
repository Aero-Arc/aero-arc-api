package domain

import "time"

type PreflightCheck struct {
	ID               string                 `json:"id"`
	OperatorID       string                 `json:"operator_id,omitempty"`
	IntentID         string                 `json:"intent_id"`
	IntentVersion    int                    `json:"intent_version,omitempty"`
	AircraftID       string                 `json:"aircraft_id,omitempty"`
	Category         PreflightCheckCategory `json:"category"`
	Source           string                 `json:"source"`
	Status           PreflightStatus        `json:"status"`
	Summary          string                 `json:"summary"`
	RequirementCode  string                 `json:"requirement_code,omitempty"`
	RuleVersion      string                 `json:"rule_version,omitempty"`
	Blocking         bool                   `json:"blocking"`
	ValidUntil       *time.Time             `json:"valid_until,omitempty"`
	RawDataURI       string                 `json:"raw_data_uri,omitempty"`
	EvidenceRecordID string                 `json:"evidence_record_id,omitempty"`
	CapturedAt       time.Time              `json:"captured_at"`
}
