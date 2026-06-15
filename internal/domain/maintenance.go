package domain

import "time"

type MaintenanceEvent struct {
	ID                string            `json:"id"`
	OperatorID        string            `json:"operator_id,omitempty"`
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
