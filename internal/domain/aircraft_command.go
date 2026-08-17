package domain

// AircraftCommandType identifies the immediate vehicle operation requested by
// an API caller. The first control slice intentionally supports only ARM and
// DISARM.
type AircraftCommandType string

const (
	AircraftCommandTypeArm    AircraftCommandType = "arm"
	AircraftCommandTypeDisarm AircraftCommandType = "disarm"
)

// AircraftCommandStatus is the terminal autopilot-level outcome of an
// immediate aircraft command.
type AircraftCommandStatus string

const (
	AircraftCommandStatusAccepted       AircraftCommandStatus = "accepted"
	AircraftCommandStatusRejected       AircraftCommandStatus = "rejected"
	AircraftCommandStatusTimeout        AircraftCommandStatus = "timeout"
	AircraftCommandStatusDeliveryFailed AircraftCommandStatus = "delivery_failed"
)

// AircraftCommandResult reports the correlated terminal result returned by
// the Agent after attempting an immediate vehicle command.
type AircraftCommandResult struct {
	CommandID  string                `json:"command_id"`
	AircraftID string                `json:"aircraft_id"`
	Status     AircraftCommandStatus `json:"status"`
	Message    string                `json:"message,omitempty"`
}
