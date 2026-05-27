package registry

import (
	"context"
	"fmt"
	"time"

	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn grpc.ClientConnInterface
	api  registryv1.AeroRegistryClient
}

func New(ctx context.Context, address string, dialTimeout time.Duration) (*Client, func() error, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("dial registry: %w", err)
	}

	c := &Client{
		conn: conn,
		api:  registryv1.NewAeroRegistryClient(conn),
	}

	return c, conn.Close, nil
}

func (c *Client) ListRelays(ctx context.Context) (*registryv1.ListRelaysResponse, error) {
	return c.api.ListRelays(ctx, &registryv1.ListRelaysRequest{})
}

func (c *Client) ListAgents(ctx context.Context) (*registryv1.ListAgentsResponse, error) {
	return c.api.ListAgents(ctx, &registryv1.ListAgentsRequest{})
}

func (c *Client) GetAgentPlacement(ctx context.Context, agentID string) (*registryv1.GetAgentPlacementResponse, error) {
	return c.api.GetAgentPlacement(ctx, &registryv1.GetAgentPlacementRequest{AgentId: agentID})
}
