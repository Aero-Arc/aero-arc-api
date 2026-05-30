package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type GRPCClient struct {
	api registryv1.AeroRegistryClient
}

func NewGRPCClient(ctx context.Context, address string, dialTimeout time.Duration) (*GRPCClient, func() error, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("dial registry: %w", err)
	}

	return &GRPCClient{api: registryv1.NewAeroRegistryClient(conn)}, conn.Close, nil
}

func (c *GRPCClient) GetLiveAircraftState(ctx context.Context, aircraftID string) (*domain.LiveAircraftState, error) {
	agents, err := c.api.ListAgents(ctx, &registryv1.ListAgentsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list registry agents: %w", err)
	}

	var agent *registryv1.Agent
	for _, item := range agents.Agents {
		if item.AgentId == aircraftID {
			agent = item
			break
		}
	}
	if agent == nil {
		return nil, nil
	}

	state := domain.LiveAircraftState{
		AircraftID:      aircraftID,
		AgentID:         agent.AgentId,
		Connected:       true,
		LastHeartbeatAt: unixMillis(agent.LastHeartbeatUnixMs),
		LastConnectedAt: unixMillis(agent.LastHeartbeatUnixMs),
	}

	placement, err := c.api.GetAgentPlacement(ctx, &registryv1.GetAgentPlacementRequest{AgentId: agent.AgentId})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return &state, nil
		}
		return nil, fmt.Errorf("get registry agent placement: %w", err)
	}
	if placement.Placement != nil {
		state.RelayID = placement.Placement.RelayId
		state.PlacementLastUpdatedAt = unixMillis(placement.Placement.LastUpdatedUnixMs)
	}

	return &state, nil
}

func (c *GRPCClient) ListLiveAircraftStates(ctx context.Context) ([]domain.LiveAircraftState, error) {
	agents, err := c.api.ListAgents(ctx, &registryv1.ListAgentsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list registry agents: %w", err)
	}

	states := make([]domain.LiveAircraftState, 0, len(agents.Agents))
	for _, agent := range agents.Agents {
		state := domain.LiveAircraftState{
			AircraftID:      agent.AgentId,
			AgentID:         agent.AgentId,
			Connected:       true,
			LastHeartbeatAt: unixMillis(agent.LastHeartbeatUnixMs),
			LastConnectedAt: unixMillis(agent.LastHeartbeatUnixMs),
		}

		placement, err := c.api.GetAgentPlacement(ctx, &registryv1.GetAgentPlacementRequest{AgentId: agent.AgentId})
		if err != nil {
			if status.Code(err) != codes.NotFound {
				return nil, fmt.Errorf("get registry agent placement: %w", err)
			}
			states = append(states, state)
			continue
		}
		if placement.Placement != nil {
			state.RelayID = placement.Placement.RelayId
			state.PlacementLastUpdatedAt = unixMillis(placement.Placement.LastUpdatedUnixMs)
		}
		states = append(states, state)
	}

	return states, nil
}

func unixMillis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
