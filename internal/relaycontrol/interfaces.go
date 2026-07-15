package relaycontrol

import (
	"context"

	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	relayv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/relay/v1"
	"google.golang.org/grpc"
)

type registryClient interface {
	GetAgentPlacement(context.Context, *registryv1.GetAgentPlacementRequest, ...grpc.CallOption) (*registryv1.GetAgentPlacementResponse, error)
	ListRelays(context.Context, *registryv1.ListRelaysRequest, ...grpc.CallOption) (*registryv1.ListRelaysResponse, error)
}

type clientPool interface {
	Client(context.Context, string, string) (relayv1.RelayControlClient, error)
	Invalidate(string)
	Close() error
}
