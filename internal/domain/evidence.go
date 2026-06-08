package domain

import "time"

type EvidenceRecord struct {
	ID             string             `json:"id"`
	OperatorID     string             `json:"operator_id,omitempty"`
	Type           EvidenceRecordType `json:"type"`
	IntentID       string             `json:"intent_id,omitempty"`
	IntentVersion  int                `json:"intent_version,omitempty"`
	FlightID       string             `json:"flight_id,omitempty"`
	AircraftID     string             `json:"aircraft_id,omitempty"`
	Status         EvidenceStatus     `json:"status"`
	Title          string             `json:"title"`
	Summary        string             `json:"summary,omitempty"`
	ObjectURI      string             `json:"object_uri,omitempty"`
	Hash           string             `json:"hash,omitempty"`
	HashAlgorithm  string             `json:"hash_algorithm,omitempty"`
	SchemaVersion  string             `json:"schema_version,omitempty"`
	GeneratedBy    string             `json:"generated_by,omitempty"`
	SourceSystem   string             `json:"source_system,omitempty"`
	RetentionUntil *time.Time         `json:"retention_until,omitempty"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

type ReportabilityReview struct {
	ID               string              `json:"id"`
	OperatorID       string              `json:"operator_id,omitempty"`
	IntentID         string              `json:"intent_id,omitempty"`
	IntentVersion    int                 `json:"intent_version,omitempty"`
	FlightID         string              `json:"flight_id,omitempty"`
	AircraftID       string              `json:"aircraft_id,omitempty"`
	Trigger          string              `json:"trigger"`
	Status           ReportabilityStatus `json:"status"`
	Decision         string              `json:"decision,omitempty"`
	EvidenceRecordID string              `json:"evidence_record_id,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	ResolvedAt       *time.Time          `json:"resolved_at,omitempty"`
}
