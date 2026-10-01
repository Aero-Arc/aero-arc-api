package domain

import "time"

// Command is durable API acceptance and its independent execution evidence.
type Command struct {
	Replayed         bool           `json:"-"` // transient submission metadata, never persisted
	ID               string         `json:"id"`
	OperatorID       string         `json:"operator_id"`
	AircraftID       string         `json:"aircraft_id"`
	FlightID         string         `json:"flight_id"`
	Type             string         `json:"type"`
	Digest           string         `json:"digest"`
	RequestedBy      string         `json:"requested_by"`
	State            string         `json:"state"`
	ObservationState string         `json:"observation_state"`
	CreatedAt        time.Time      `json:"created_at"`
	ExpiresAt        time.Time      `json:"expires_at"`
	Events           []CommandEvent `json:"events"`
	Attempts         int            `json:"attempts"`
	IdempotencyKey   string         `json:"-"`
	RequestHash      string         `json:"-"`
	Payload          []byte         `json:"-"`
	DeploymentID     string         `json:"deployment_id,omitempty"`
	Lease            int64          `json:"-"`
}

// CommandEvent retains immutable source time and independently assigned receipt time.
type CommandEvent struct {
	ID         string    `json:"id"`
	Stage      string    `json:"stage"`
	OccurredAt time.Time `json:"occurred_at"`
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source"`
	Message    string    `json:"message"`
}

// InvalidatesGroundEvidence reports whether applied motion authority is not
// proven earlier than the landed/disarmed evidence boundary.
//
// Parameters: boundary is the earlier ground observation timestamp. Command
// events have millisecond precision; the same millisecond is conservatively ambiguous.
// Returns: true for ARM/RESUME/MISSION_START with late or missing application
// time, including applied evidence behind a conflicting projection. Other command
// types do not independently authorize leaving the disarmed ground state.
func (c Command) InvalidatesGroundEvidence(boundary time.Time) bool {
	if c.Type != "ARM" && c.Type != "RESUME" && c.Type != "MISSION_START" {
		return false
	}
	found := false
	for _, event := range c.Events {
		if event.Stage != "applied" {
			continue
		}
		found = true
		if event.OccurredAt.IsZero() || !event.OccurredAt.Before(boundary.Truncate(time.Millisecond)) {
			return true
		}
	}
	return c.State == "applied" && !found
}
