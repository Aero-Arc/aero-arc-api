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
	"google.golang.org/grpc/status"
)

const defaultTimeout = 5 * time.Second

type SetRequest struct {
	AgentID       string
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

func New(registry registryClient, timeout, placementTTL time.Duration) *Service {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	if placementTTL <= 0 {
		placementTTL = 30 * time.Second
	}
	return newWithPool(registry, newGRPCPool(), timeout, placementTTL)
}

func newWithPool(registry registryClient, pool clientPool, timeout, ttl time.Duration) *Service {
	return &Service{registry: registry, pool: pool, timeout: timeout, placementTTL: ttl, now: time.Now, placements: map[string]cachedPlacement{}}
}

func (s *Service) Close() error { return s.pool.Close() }

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
		return cachedPlacement{}, fmt.Errorf("resolve agent placement: %w", err)
	}
	placement := response.GetPlacement()
	if placement == nil || placement.GetRelayId() == "" {
		return cachedPlacement{}, fmt.Errorf("agent %s has no relay placement", agentID)
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
