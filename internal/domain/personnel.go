package domain

import "time"

type OperationsPersonnel struct {
	ID                       string                   `json:"id"`
	OperatorID               string                   `json:"operator_id,omitempty"`
	Name                     string                   `json:"name"`
	Role                     PersonnelRole            `json:"role"`
	QualificationStatus      ReadinessStatus          `json:"qualification_status"`
	SecurityAssessmentStatus SecurityAssessmentStatus `json:"security_assessment_status"`
	RecentExperienceHours    *float64                 `json:"recent_experience_hours,omitempty"`
	DutyStartedAt            *time.Time               `json:"duty_started_at,omitempty"`
	RestedAt                 *time.Time               `json:"rested_at,omitempty"`
	UpdatedAt                time.Time                `json:"updated_at"`
}

type PersonnelAssignment struct {
	ID            string        `json:"id"`
	OperatorID    string        `json:"operator_id,omitempty"`
	IntentID      string        `json:"intent_id"`
	IntentVersion int           `json:"intent_version,omitempty"`
	PersonnelID   string        `json:"personnel_id"`
	Role          PersonnelRole `json:"role"`
	AssignedAt    time.Time     `json:"assigned_at"`
	ReleasedAt    *time.Time    `json:"released_at,omitempty"`
}
