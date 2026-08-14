package registry

import (
	"context"
	"fmt"
	"time"

	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// NewGRPCClient constructs registry from the supplied configuration and dependencies.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - address: locates the external dependency used by the operation.
//   - dialTimeout: defines the time bound applied by the operation.
//
// Returns:
//   - result: is the registryv1.AeroRegistryClient value produced by NewGRPCClient.
//   - result: is the func() error value produced by NewGRPCClient.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func NewGRPCClient(ctx context.Context, address string, dialTimeout time.Duration) (registryv1.AeroRegistryClient, func() error, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("dial registry: %w", err)
	}

	return registryv1.NewAeroRegistryClient(conn), conn.Close, nil
}
