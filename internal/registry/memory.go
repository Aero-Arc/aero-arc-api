package registry

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type MemoryClient struct {
	mu         sync.RWMutex
	relays     map[string]*registryv1.Relay
	agents     map[string]*registryv1.Agent
	placements map[string]*registryv1.AgentPlacement
}

// NewMemoryClient constructs registry from the supplied configuration and dependencies.
//
// Returns:
//   - result: is the *MemoryClient value produced by NewMemoryClient.
func NewMemoryClient() *MemoryClient {
	return &MemoryClient{
		relays:     make(map[string]*registryv1.Relay),
		agents:     make(map[string]*registryv1.Agent),
		placements: make(map[string]*registryv1.AgentPlacement),
	}
}

var _ registryv1.AeroRegistryClient = (*MemoryClient)(nil)

// SetLiveAircraftState sets the selected MemoryClient state to the supplied value.
//
// Parameters:
//   - value: is the context.Context value supplied to SetLiveAircraftState.
//   - state: is the domain.LiveAircraftState value supplied to SetLiveAircraftState.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *MemoryClient) SetLiveAircraftState(_ context.Context, state domain.LiveAircraftState) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	agentID := state.AgentID
	if agentID == "" {
		return status.Error(codes.InvalidArgument, "agent_id is required")
	}
	heartbeatAt := state.LastHeartbeatAt
	if heartbeatAt.IsZero() && state.Connected {
		heartbeatAt = time.Now().UTC()
	}
	c.agents[agentID] = &registryv1.Agent{
		AgentId:             agentID,
		LastHeartbeatUnixMs: unixMillis(heartbeatAt),
	}
	if state.RelayID != "" {
		placementAt := state.PlacementLastUpdatedAt
		if placementAt.IsZero() {
			placementAt = heartbeatAt
		}
		c.placements[agentID] = &registryv1.AgentPlacement{
			AgentId:           agentID,
			RelayId:           state.RelayID,
			LastUpdatedUnixMs: unixMillis(placementAt),
		}
	} else {
		delete(c.placements, agentID)
	}
	return nil
}

// RegisterRelay registers the supplied MemoryClient identity or handler.
//
// Parameters:
//   - value: is the context.Context value supplied to RegisterRelay.
//   - req: contains the validated request payload.
//   - value: is the ...grpc.CallOption value supplied to RegisterRelay.
//
// Returns:
//   - result: is the *registryv1.RegisterRelayResponse value produced by RegisterRelay.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *MemoryClient) RegisterRelay(_ context.Context, req *registryv1.RegisterRelayRequest, _ ...grpc.CallOption) (*registryv1.RegisterRelayResponse, error) {
	relay := req.GetRelay()
	if relay == nil || relay.GetRelayId() == "" {
		return nil, status.Error(codes.InvalidArgument, "relay_id is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.relays[relay.GetRelayId()] = cloneRelay(relay)
	return &registryv1.RegisterRelayResponse{}, nil
}

// HeartbeatRelay renews liveness for the supplied MemoryClient identity without changing ownership.
//
// Parameters:
//   - value: is the context.Context value supplied to HeartbeatRelay.
//   - req: contains the validated request payload.
//   - value: is the ...grpc.CallOption value supplied to HeartbeatRelay.
//
// Returns:
//   - result: is the *registryv1.HeartbeatRelayResponse value produced by HeartbeatRelay.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *MemoryClient) HeartbeatRelay(_ context.Context, req *registryv1.HeartbeatRelayRequest, _ ...grpc.CallOption) (*registryv1.HeartbeatRelayResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	relay, ok := c.relays[req.GetRelayId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "relay not found")
	}
	relay.LastHeartbeatUnixMs = req.GetTimestampUnixMs()
	return &registryv1.HeartbeatRelayResponse{}, nil
}

// ListRelays returns MemoryClient records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListRelays.
//   - value: is the *registryv1.ListRelaysRequest value supplied to ListRelays.
//   - value: is the ...grpc.CallOption value supplied to ListRelays.
//
// Returns:
//   - result: is the *registryv1.ListRelaysResponse value produced by ListRelays.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *MemoryClient) ListRelays(context.Context, *registryv1.ListRelaysRequest, ...grpc.CallOption) (*registryv1.ListRelaysResponse, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	relayIDs := make([]string, 0, len(c.relays))
	for relayID := range c.relays {
		relayIDs = append(relayIDs, relayID)
	}
	sort.Strings(relayIDs)

	relays := make([]*registryv1.Relay, 0, len(relayIDs))
	for _, relayID := range relayIDs {
		relays = append(relays, cloneRelay(c.relays[relayID]))
	}

	return &registryv1.ListRelaysResponse{Relays: relays}, nil
}

// RegisterAgent registers the supplied MemoryClient identity or handler.
//
// Parameters:
//   - value: is the context.Context value supplied to RegisterAgent.
//   - req: contains the validated request payload.
//   - value: is the ...grpc.CallOption value supplied to RegisterAgent.
//
// Returns:
//   - result: is the *registryv1.RegisterAgentResponse value produced by RegisterAgent.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *MemoryClient) RegisterAgent(_ context.Context, req *registryv1.RegisterAgentRequest, _ ...grpc.CallOption) (*registryv1.RegisterAgentResponse, error) {
	agent := req.GetAgent()
	if agent == nil || agent.GetAgentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.agents[agent.GetAgentId()] = cloneAgent(agent)
	if req.GetRelayId() != "" {
		c.placements[agent.GetAgentId()] = &registryv1.AgentPlacement{
			AgentId:           agent.GetAgentId(),
			RelayId:           req.GetRelayId(),
			LastUpdatedUnixMs: agent.GetLastHeartbeatUnixMs(),
		}
	}
	return &registryv1.RegisterAgentResponse{}, nil
}

// HeartbeatAgent renews liveness for the supplied MemoryClient identity without changing ownership.
//
// Parameters:
//   - value: is the context.Context value supplied to HeartbeatAgent.
//   - req: contains the validated request payload.
//   - value: is the ...grpc.CallOption value supplied to HeartbeatAgent.
//
// Returns:
//   - result: is the *registryv1.HeartbeatAgentResponse value produced by HeartbeatAgent.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *MemoryClient) HeartbeatAgent(_ context.Context, req *registryv1.HeartbeatAgentRequest, _ ...grpc.CallOption) (*registryv1.HeartbeatAgentResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	agent, ok := c.agents[req.GetAgentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "agent not found")
	}
	agent.LastHeartbeatUnixMs = req.GetTimestampUnixMs()
	return &registryv1.HeartbeatAgentResponse{}, nil
}

// ListAgents returns MemoryClient records matching the supplied scope and filters.
//
// Parameters:
//   - value: is the context.Context value supplied to ListAgents.
//   - value: is the *registryv1.ListAgentsRequest value supplied to ListAgents.
//   - value: is the ...grpc.CallOption value supplied to ListAgents.
//
// Returns:
//   - result: is the *registryv1.ListAgentsResponse value produced by ListAgents.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *MemoryClient) ListAgents(context.Context, *registryv1.ListAgentsRequest, ...grpc.CallOption) (*registryv1.ListAgentsResponse, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	agentIDs := make([]string, 0, len(c.agents))
	for agentID := range c.agents {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)

	agents := make([]*registryv1.Agent, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		agents = append(agents, cloneAgent(c.agents[agentID]))
	}

	return &registryv1.ListAgentsResponse{Agents: agents}, nil
}

// GetAgentPlacement returns an Agent's current in-process Relay placement.
//
// Parameters:
//   - value: is the context.Context value supplied to GetAgentPlacement.
//   - req: contains the validated request payload.
//   - value: is the ...grpc.CallOption value supplied to GetAgentPlacement.
//
// Returns:
//   - result: is the *registryv1.GetAgentPlacementResponse value produced by GetAgentPlacement.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *MemoryClient) GetAgentPlacement(_ context.Context, req *registryv1.GetAgentPlacementRequest, _ ...grpc.CallOption) (*registryv1.GetAgentPlacementResponse, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	placement, ok := c.placements[req.GetAgentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "agent placement not found")
	}
	return &registryv1.GetAgentPlacementResponse{
		Placement: clonePlacement(placement),
	}, nil
}

func cloneRelay(relay *registryv1.Relay) *registryv1.Relay {
	return &registryv1.Relay{
		RelayId:             relay.GetRelayId(),
		Address:             relay.GetAddress(),
		GrpcPort:            relay.GetGrpcPort(),
		LastHeartbeatUnixMs: relay.GetLastHeartbeatUnixMs(),
	}
}

func cloneAgent(agent *registryv1.Agent) *registryv1.Agent {
	return &registryv1.Agent{
		AgentId:             agent.GetAgentId(),
		LastHeartbeatUnixMs: agent.GetLastHeartbeatUnixMs(),
	}
}

func clonePlacement(placement *registryv1.AgentPlacement) *registryv1.AgentPlacement {
	return &registryv1.AgentPlacement{
		AgentId:           placement.GetAgentId(),
		RelayId:           placement.GetRelayId(),
		LastUpdatedUnixMs: placement.GetLastUpdatedUnixMs(),
	}
}

func unixMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
