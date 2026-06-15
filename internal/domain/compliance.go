package domain

import "time"

type ComplianceFinding struct {
	ID               string                  `json:"id"`
	OperatorID       string                  `json:"operator_id,omitempty"`
	IntentID         string                  `json:"intent_id,omitempty"`
	IntentVersion    int                     `json:"intent_version,omitempty"`
	SubjectType      string                  `json:"subject_type"`
	SubjectID        string                  `json:"subject_id"`
	RequirementCode  string                  `json:"requirement_code"`
	Status           ComplianceFindingStatus `json:"status"`
	Severity         Severity                `json:"severity"`
	Blocking         bool                    `json:"blocking"`
	RuleVersion      string                  `json:"rule_version,omitempty"`
	Remediation      string                  `json:"remediation,omitempty"`
	Message          string                  `json:"message"`
	EvidenceRecordID string                  `json:"evidence_record_id,omitempty"`
	EvaluatedAt      time.Time               `json:"evaluated_at"`
}
