package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/relaycontrol"
)

var (
	// ErrAircraftNotConnected reports that an aircraft has no mapped Agent or
	// that its mapped Agent has no current Relay session.
	ErrAircraftNotConnected = errors.New("aircraft is not connected")
	// ErrAircraftCommandDelivery reports a Registry or Relay failure before a
	// correlated Agent result could be returned.
	ErrAircraftCommandDelivery = errors.New("aircraft command delivery failed")
)

type aircraftCommandStore interface {
	GetAircraft(context.Context, string) (domain.Aircraft, error)
}

type aircraftCommandTransport interface {
	SendAircraftCommand(context.Context, relaycontrol.AircraftCommandRequest) (domain.AircraftCommandResult, error)
}

// AircraftCommandService resolves API aircraft identities to their durable
// Agent mappings before invoking the ephemeral Relay control path.
type AircraftCommandService struct {
	store     aircraftCommandStore
	transport aircraftCommandTransport
}

// NewAircraftCommandService constructs the immediate command workflow.
//
// Parameters:
//   - store: supplies the authoritative aircraft-to-Agent mapping.
//   - transport: resolves Registry placement and invokes the owning Relay.
//
// Returns:
//   - service: is ready to issue non-durable ARM and DISARM commands.
func NewAircraftCommandService(store aircraftCommandStore, transport aircraftCommandTransport) *AircraftCommandService {
	return &AircraftCommandService{store: store, transport: transport}
}

// SendAircraftCommand resolves aircraftID through the durable record and sends
// one immediate ARM or DISARM command to its currently connected Agent.
//
// Parameters:
//   - ctx: bounds durable lookup, Registry routing, Relay delivery, and result propagation.
//   - aircraftID: identifies the Aero Arc aircraft requested by the caller.
//   - commandType: must be arm or disarm.
//
// Returns:
//   - result: is the Agent's correlated autopilot-level terminal outcome.
//   - error: reports validation, missing aircraft records, disconnected aircraft,
//     or control-path delivery failures.
func (s *AircraftCommandService) SendAircraftCommand(ctx context.Context, aircraftID string, commandType domain.AircraftCommandType) (domain.AircraftCommandResult, error) {
	aircraftID = strings.TrimSpace(aircraftID)
	if aircraftID == "" {
		return domain.AircraftCommandResult{}, fmt.Errorf("%w: aircraft_id is required", ErrValidation)
	}
	if commandType != domain.AircraftCommandTypeArm && commandType != domain.AircraftCommandTypeDisarm {
		return domain.AircraftCommandResult{}, fmt.Errorf("%w: unsupported aircraft command %q", ErrValidation, commandType)
	}
	aircraft, err := s.store.GetAircraft(ctx, aircraftID)
	if err != nil {
		return domain.AircraftCommandResult{}, err
	}
	agentID := strings.TrimSpace(aircraft.AgentID)
	if agentID == "" {
		return domain.AircraftCommandResult{}, fmt.Errorf("%w: aircraft %s has no agent mapping", ErrAircraftNotConnected, aircraftID)
	}
	slog.LogAttrs(ctx, slog.LevelInfo, "command_requested",
		slog.String("aircraft_id", aircraftID),
		slog.String("agent_id", agentID),
		slog.String("command_type", string(commandType)),
	)
	result, err := s.transport.SendAircraftCommand(ctx, relaycontrol.AircraftCommandRequest{
		AgentID: agentID, AircraftID: aircraftID, Type: commandType,
	})
	if err != nil {
		if errors.Is(err, relaycontrol.ErrAgentNotConnected) {
			return domain.AircraftCommandResult{}, fmt.Errorf("%w: %w", ErrAircraftNotConnected, err)
		}
		return domain.AircraftCommandResult{}, fmt.Errorf("%w: %w", ErrAircraftCommandDelivery, err)
	}
	return result, nil
}
