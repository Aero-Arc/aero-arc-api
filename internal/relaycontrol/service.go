package relaycontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	agentv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	relayv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/relay/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const defaultTimeout = 5 * time.Second

// ErrAgentNotConnected reports that Registry or Relay has no active session
// for the Agent selected by the API's durable aircraft mapping.
var ErrAgentNotConnected = errors.New("agent is not connected")

type SetRequest struct {
	AgentID       string
	FlightID      string
	IntentID      string
	IntentVersion uint32
	CommandID     string
}

type ClearRequest struct{ AgentID, FlightID, CommandID string }

// AircraftCommandRequest carries the resolved Agent route and the immediate
// aircraft command that must be delivered without retries or persistence.
type AircraftCommandRequest struct {
	AgentID    string
	AircraftID string
	Type       domain.AircraftCommandType
	CommandID  string
	IssuedAt   time.Time
}

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
	command := &agentv1.SetOperationContextCommand{CommandId: commandID, Context: &agentv1.OperationContext{FlightId: req.FlightID, IntentId: req.IntentID, IntentVersion: req.IntentVersion}}
	err = s.call(ctx, req.AgentID, func(callCtx context.Context, client relayv1.RelayControlClient) error {
		response, err := client.SetOperationContext(callCtx, &relayv1.SetOperationContextRequest{AgentId: req.AgentID, Command: command})
		if err != nil {
			return err
		}
		return validateAck(response.GetResult(), commandID)
	})
	return commandID, err
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

// SendAircraftCommand resolves the Agent's current Relay placement, delivers
// one ARM or DISARM command exactly once, and returns the Agent's correlated
// autopilot-level result. It deliberately bypasses the placement cache and
// does not retry an ambiguous Relay failure because physical commands are not
// idempotent at this boundary.
//
// Parameters:
//   - ctx: bounds Registry lookup, Relay delivery, and result propagation.
//   - req: identifies the Agent, aircraft, command type, and optional command ID.
//
// Returns:
//   - result: contains accepted, rejected, timeout, or delivery-failed status.
//   - error: reports validation, offline placement, Relay transport, correlation,
//     or unsupported-result failures.
func (s *Service) SendAircraftCommand(ctx context.Context, req AircraftCommandRequest) (domain.AircraftCommandResult, error) {
	if strings.TrimSpace(req.AgentID) == "" || strings.TrimSpace(req.AircraftID) == "" {
		return domain.AircraftCommandResult{}, fmt.Errorf("agent_id and aircraft_id are required")
	}
	var commandType agentv1.AircraftCommandType
	switch req.Type {
	case domain.AircraftCommandTypeArm:
		commandType = agentv1.AircraftCommandType_AIRCRAFT_COMMAND_TYPE_ARM
	case domain.AircraftCommandTypeDisarm:
		commandType = agentv1.AircraftCommandType_AIRCRAFT_COMMAND_TYPE_DISARM
	default:
		return domain.AircraftCommandResult{}, fmt.Errorf("unsupported aircraft command type %q", req.Type)
	}
	commandID, err := ensureCommandID(req.CommandID)
	if err != nil {
		return domain.AircraftCommandResult{}, err
	}
	issuedAt := req.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = s.now().UTC()
	}
	command := &agentv1.AircraftCommand{
		CommandId: commandID, AircraftId: req.AircraftID,
		Type: commandType, IssuedAtUnixMs: issuedAt.UnixMilli(),
	}

	placement, err := s.resolve(ctx, req.AgentID, true)
	if err != nil {
		return domain.AircraftCommandResult{}, err
	}
	client, err := s.pool.Client(ctx, placement.relayID, placement.address)
	if err != nil {
		return domain.AircraftCommandResult{}, fmt.Errorf("connect relay %s: %w", placement.relayID, err)
	}
	slog.LogAttrs(ctx, slog.LevelInfo, "command_routed",
		slog.String("command_id", commandID),
		slog.String("aircraft_id", req.AircraftID),
		slog.String("agent_id", req.AgentID),
		slog.String("relay_id", placement.relayID),
		slog.String("command_type", string(req.Type)),
	)
	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	response, err := client.SendAircraftCommand(callCtx, &relayv1.SendAircraftCommandRequest{
		AgentId: req.AgentID, Command: command,
	})
	cancel()
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return domain.AircraftCommandResult{}, fmt.Errorf("%w: %v", ErrAgentNotConnected, err)
		}
		return domain.AircraftCommandResult{}, fmt.Errorf("deliver aircraft command: %w", err)
	}
	result := response.GetResult()
	if result == nil || result.GetCommandId() != commandID || result.GetAircraftId() != req.AircraftID {
		return domain.AircraftCommandResult{}, fmt.Errorf("relay returned a missing or mismatched aircraft command result")
	}
	mapped := domain.AircraftCommandResult{CommandID: commandID, AircraftID: req.AircraftID, Message: result.GetMessage()}
	switch result.GetStatus() {
	case agentv1.AircraftCommandResult_STATUS_ACCEPTED:
		mapped.Status = domain.AircraftCommandStatusAccepted
	case agentv1.AircraftCommandResult_STATUS_REJECTED:
		mapped.Status = domain.AircraftCommandStatusRejected
	case agentv1.AircraftCommandResult_STATUS_TIMEOUT:
		mapped.Status = domain.AircraftCommandStatusTimeout
	case agentv1.AircraftCommandResult_STATUS_DELIVERY_FAILED:
		mapped.Status = domain.AircraftCommandStatusDeliveryFailed
	default:
		return domain.AircraftCommandResult{}, fmt.Errorf("agent returned unsupported command status %s", result.GetStatus())
	}
	return mapped, nil
}

func (s *Service) call(ctx context.Context, agentID string, invoke func(context.Context, relayv1.RelayControlClient) error) error {
	for attempt := 0; attempt < 2; attempt++ {
		placement, err := s.resolve(ctx, agentID, attempt > 0)
		if err != nil {
			return err
		}
		client, err := s.pool.Client(ctx, placement.relayID, placement.address)
		if err != nil {
			return fmt.Errorf("connect relay %s: %w", placement.relayID, err)
		}
		callCtx, cancel := context.WithTimeout(ctx, s.timeout)
		err = invoke(callCtx, client)
		cancel()
		if status.Code(err) != codes.Unavailable || attempt == 1 {
			if err != nil {
				return fmt.Errorf("deliver operation context: %w", err)
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
	lookupCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	response, err := s.registry.GetAgentPlacement(lookupCtx, &registryv1.GetAgentPlacementRequest{AgentId: agentID})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return cachedPlacement{}, fmt.Errorf("%w: agent %s has no relay placement", ErrAgentNotConnected, agentID)
		}
		return cachedPlacement{}, fmt.Errorf("resolve agent placement: %w", err)
	}
	placement := response.GetPlacement()
	if placement == nil || placement.GetRelayId() == "" {
		return cachedPlacement{}, fmt.Errorf("%w: agent %s has no relay placement", ErrAgentNotConnected, agentID)
	}
	relays, err := s.registry.ListRelays(lookupCtx, &registryv1.ListRelaysRequest{})
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
