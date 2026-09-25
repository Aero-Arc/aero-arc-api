package domain

import "time"

// Command is durable API acceptance and its independent execution evidence.
type Command struct {
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
