package domain

import "time"

type ConflictFinding struct {
	ID                  string                    `json:"id"`
	OperatorID          string                    `json:"operator_id,omitempty"`
	IntentID            string                    `json:"intent_id"`
	IntentVersion       int                       `json:"intent_version,omitempty"`
	AircraftID          string                    `json:"aircraft_id,omitempty"`
	VolumeID            string                    `json:"volume_id,omitempty"`
	ConflictingIntentID string                    `json:"conflicting_intent_id,omitempty"`
	ConflictingVersion  int                       `json:"conflicting_version,omitempty"`
	ConflictingVolumeID string                    `json:"conflicting_volume_id,omitempty"`
	SourceType          ConflictFindingSourceType `json:"source_type"`
	SourceID            string                    `json:"source_id,omitempty"`
	Status              ConflictFindingStatus     `json:"status"`
	Severity            Severity                  `json:"severity"`
	Blocking            bool                      `json:"blocking"`
	Message             string                    `json:"message"`
	TimeOverlapStart    *time.Time                `json:"time_overlap_start,omitempty"`
	TimeOverlapEnd      *time.Time                `json:"time_overlap_end,omitempty"`
	AltitudeOverlapMin  *float64                  `json:"altitude_overlap_min,omitempty"`
	AltitudeOverlapMax  *float64                  `json:"altitude_overlap_max,omitempty"`
	RuleVersion         string                    `json:"rule_version,omitempty"`
	Provenance          string                    `json:"provenance,omitempty"`
	EvaluatedAt         time.Time                 `json:"evaluated_at"`
}

type DeconflictionResult struct {
	Intent      OperationalIntent    `json:"intent"`
	Posture     DeconflictionPosture `json:"posture"`
	Findings    []ConflictFinding    `json:"findings"`
	CheckedAt   time.Time            `json:"checked_at"`
	RuleVersion string               `json:"rule_version,omitempty"`
}
