package relaycontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	agentv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	relayv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/relay/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const defaultTimeout = 5 * time.Second

type SetRequest struct {
	AgentID       string
	AircraftID    string
	FlightID      string
	IntentID      string
	IntentVersion uint32
	CommandID     string
}

type ClearRequest struct{ AgentID, FlightID, CommandID string }

type Service struct {
	registry              registryClient
	pool                  clientPool
	timeout, placementTTL time.Duration
	now                   func() time.Time
	mu                    sync.Mutex
	placements            map[string]cachedPlacement
}

type cachedPlacement struct {
	relayID, address string
	expiresAt        time.Time
}

// New constructs relaycontrol from the supplied configuration and dependencies.
//
// Parameters:
//   - registry: is the registryClient value supplied to New.
//   - transportCredentials: provides authentication material for the dependency.
//   - timeout: defines the time bound applied by the operation.
//   - placementTTL: is the time.Duration value supplied to New.
//
// Returns:
//   - result: is the *Service value produced by New.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func New(registry registryClient, transportCredentials credentials.TransportCredentials, timeout, placementTTL time.Duration) (*Service, error) {
	if transportCredentials == nil {
		return nil, fmt.Errorf("relay transport credentials are required")
	}
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if placementTTL <= 0 {
		placementTTL = 30 * time.Second
	}
	return newWithPool(registry, newGRPCPool(transportCredentials), timeout, placementTTL), nil
}

func newWithPool(registry registryClient, pool clientPool, timeout, ttl time.Duration) *Service {
	return &Service{registry: registry, pool: pool, timeout: timeout, placementTTL: ttl, now: time.Now, placements: map[string]cachedPlacement{}}
}

// Close releases resources owned by Service and completes any required shutdown work.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Service) Close() error { return s.pool.Close() }

// SetOperationContext sets the selected Service state to the supplied value.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - req: contains the validated request payload.
//
// Returns:
//   - result: is the string value produced by SetOperationContext.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Service) SetOperationContext(ctx context.Context, req SetRequest) (string, error) {
	if req.AgentID == "" || req.FlightID == "" {
		return "", fmt.Errorf("agent_id and flight_id are required")
	}
	commandID, err := ensureCommandID(req.CommandID)
	if err != nil {
		return "", err
	}
	command := &agentv1.SetOperationContextCommand{CommandId: commandID, Context: &agentv1.OperationContext{FlightId: req.FlightID, IntentId: req.IntentID, IntentVersion: req.IntentVersion, AircraftId: req.AircraftID}}
	err = s.call(ctx, req.AgentID, func(callCtx context.Context, client relayv1.RelayControlClient) error {
		response, err := client.SetOperationContext(callCtx, &relayv1.SetOperationContextRequest{AgentId: req.AgentID, Command: command})
		if err != nil {
			return err
		}
		return validateSetAck(response.GetResult(), commandID, command.GetContext())
	})
	return commandID, err
}

// EnsureOperationContext delivers one stable context command and requires a
// correlated successful Agent acknowledgement before returning.
func (s *Service) EnsureOperationContext(ctx context.Context, agentID string, command *agentv1.SetOperationContextCommand) error {
	if strings.TrimSpace(agentID) == "" || command == nil || strings.TrimSpace(command.GetCommandId()) == "" || command.GetContext() == nil {
		return fmt.Errorf("agent_id and a complete operation context command are required")
	}
	operation := command.GetContext()
	if strings.TrimSpace(operation.GetAircraftId()) == "" || strings.TrimSpace(operation.GetFlightId()) == "" ||
		strings.TrimSpace(operation.GetIntentId()) == "" || operation.GetIntentVersion() == 0 {
		return fmt.Errorf("mission operation context requires aircraft_id, flight_id, intent_id, and positive intent_version")
	}
	return s.call(ctx, agentID, func(callCtx context.Context, client relayv1.RelayControlClient) error {
		response, err := client.SetOperationContext(callCtx, &relayv1.SetOperationContextRequest{AgentId: agentID, Command: command})
		if err != nil {
			return err
		}
		return validateSetAck(response.GetResult(), command.GetCommandId(), command.GetContext())
	})
}

// ClearOperationContextForReconciliation conditionally clears the old flight
// context and proves that exact binding is no longer active before recovery.
func (s *Service) ClearOperationContextForReconciliation(ctx context.Context, agentID string, command *agentv1.ClearOperationContextCommand, old *agentv1.OperationContext) error {
	if strings.TrimSpace(agentID) == "" || command == nil || strings.TrimSpace(command.GetCommandId()) == "" ||
		strings.TrimSpace(command.GetFlightId()) == "" || command.GetAuthoritative() || old == nil {
		return fmt.Errorf("agent_id, conditional clear command, and old operation context are required")
	}
	return s.call(ctx, agentID, func(callCtx context.Context, client relayv1.RelayControlClient) error {
		response, err := client.ClearOperationContext(callCtx, &relayv1.ClearOperationContextRequest{AgentId: agentID, Command: command})
		if err != nil {
			return err
		}
		ack := response.GetResult()
		if err := validateAck(ack, command.GetCommandId()); err != nil {
			return err
		}
		if operationContextsEqual(ack.GetActiveContext(), old) {
			return fmt.Errorf("agent acknowledgement still reports the old operation context active")
		}
		return nil
	})
}

// ClearOperationContext clears the selected Service state without changing unrelated records.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - req: contains the validated request payload.
//
// Returns:
//   - result: is the string value produced by ClearOperationContext.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Service) ClearOperationContext(ctx context.Context, req ClearRequest) (string, error) {
	if req.AgentID == "" || req.FlightID == "" {
		return "", fmt.Errorf("agent_id and flight_id are required")
	}
	commandID, err := ensureCommandID(req.CommandID)
	if err != nil {
		return "", err
	}
	command := &agentv1.ClearOperationContextCommand{CommandId: commandID, FlightId: req.FlightID}
	err = s.call(ctx, req.AgentID, func(callCtx context.Context, client relayv1.RelayControlClient) error {
		response, err := client.ClearOperationContext(callCtx, &relayv1.ClearOperationContextRequest{AgentId: req.AgentID, Command: command})
		if err != nil {
			return err
		}
		return validateAck(response.GetResult(), commandID)
	})
	return commandID, err
}

// DeployMission routes one API-authoritative command through the Agent's
// Registry placement and returns only a correlated Agent result.
func (s *Service) DeployMission(ctx context.Context, agentID string, command *agentv1.DeployMissionCommand) (*agentv1.MissionDeploymentResult, error) {
	if strings.TrimSpace(agentID) == "" || command == nil || strings.TrimSpace(command.GetCommandId()) == "" {
		return nil, fmt.Errorf("agent_id and a command with command_id are required")
	}
	var result *agentv1.MissionDeploymentResult
	err := s.call(ctx, agentID, func(callCtx context.Context, client relayv1.RelayControlClient) error {
		response, err := client.DeployMission(callCtx, &relayv1.DeployMissionRequest{AgentId: agentID, Command: command})
		if err != nil {
			return err
		}
		if response.GetResult() == nil {
			return fmt.Errorf("relay returned no mission deployment result")
		}
		result = response.GetResult()
		return nil
	})
	return result, err
}

func (s *Service) call(ctx context.Context, agentID string, invoke func(context.Context, relayv1.RelayControlClient) error) error {
	phaseCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		placement, err := s.resolve(phaseCtx, agentID, attempt > 0)
		if err != nil {
			return err
		}
		client, err := s.pool.Client(phaseCtx, placement.relayID, placement.address)
		if err != nil {
			return fmt.Errorf("connect relay %s: %w", placement.relayID, err)
		}
		err = invoke(phaseCtx, client)
		if status.Code(err) != codes.Unavailable || attempt == 1 {
			if err != nil {
				return fmt.Errorf("deliver relay control command: %w", err)
			}
			return nil
		}
		s.invalidate(agentID, placement.relayID)
	}
	return nil
}

func (s *Service) resolve(ctx context.Context, agentID string, refresh bool) (cachedPlacement, error) {
	s.mu.Lock()
	cached, ok := s.placements[agentID]
	s.mu.Unlock()
	if !refresh && ok && s.now().Before(cached.expiresAt) {
		return cached, nil
	}
	response, err := s.registry.GetAgentPlacement(ctx, &registryv1.GetAgentPlacementRequest{AgentId: agentID})
	if err != nil {
		return cachedPlacement{}, fmt.Errorf("resolve agent placement: %w", err)
	}
	placement := response.GetPlacement()
	if placement == nil || placement.GetRelayId() == "" {
		return cachedPlacement{}, fmt.Errorf("agent %s has no relay placement", agentID)
	}
	relays, err := s.registry.ListRelays(ctx, &registryv1.ListRelaysRequest{})
	if err != nil {
		return cachedPlacement{}, fmt.Errorf("list relays: %w", err)
	}
	for _, relay := range relays.GetRelays() {
		if relay.GetRelayId() == placement.GetRelayId() {
			address := relayAddress(relay)
			if address == "" {
				break
			}
			resolved := cachedPlacement{relayID: relay.GetRelayId(), address: address, expiresAt: s.now().Add(s.placementTTL)}
			s.mu.Lock()
			s.placements[agentID] = resolved
			s.mu.Unlock()
			return resolved, nil
		}
	}
	return cachedPlacement{}, fmt.Errorf("relay %s has no routable address", placement.GetRelayId())
}

func (s *Service) invalidate(agentID, relayID string) {
	s.mu.Lock()
	delete(s.placements, agentID)
	s.mu.Unlock()
	s.pool.Invalidate(relayID)
}

func validateAck(ack *agentv1.OperationContextCommandAck, commandID string) error {
	if ack == nil {
		return fmt.Errorf("relay returned no agent acknowledgement")
	}
	if ack.GetCommandId() != commandID {
		return fmt.Errorf("agent acknowledgement command_id %q does not match %q", ack.GetCommandId(), commandID)
	}
	switch ack.GetStatus() {
	case agentv1.OperationContextCommandAck_STATUS_APPLIED, agentv1.OperationContextCommandAck_STATUS_ALREADY_APPLIED:
		return nil
	default:
		return fmt.Errorf("agent rejected operation context: %s: %s", ack.GetStatus(), ack.GetError())
	}
}

func validateSetAck(ack *agentv1.OperationContextCommandAck, commandID string, expected *agentv1.OperationContext) error {
	if err := validateAck(ack, commandID); err != nil {
		return err
	}
	active := ack.GetActiveContext()
	if !operationContextsEqual(active, expected) {
		return fmt.Errorf("agent acknowledgement active_context does not exactly match the requested aircraft/flight/intent/version")
	}
	return nil
}

func operationContextsEqual(left, right *agentv1.OperationContext) bool {
	return left != nil && right != nil && left.GetAircraftId() == right.GetAircraftId() &&
		left.GetFlightId() == right.GetFlightId() && left.GetIntentId() == right.GetIntentId() &&
		left.GetIntentVersion() == right.GetIntentVersion()
}

func ensureCommandID(id string) (string, error) {
	if id != "" {
		return id, nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate command id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func relayAddress(relay *registryv1.Relay) string {
	address := strings.TrimSpace(relay.GetAddress())
	if address == "" {
		return ""
	}
	if relay.GetGrpcPort() > 0 {
		if _, _, err := net.SplitHostPort(address); err != nil {
			return net.JoinHostPort(address, strconv.Itoa(int(relay.GetGrpcPort())))
		}
	}
	return address
}
