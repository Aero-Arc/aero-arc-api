package registry

import (
	"context"
	"fmt"
	"time"

	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
)

// New constructs the configured in-memory or gRPC Registry client and returns
// the cleanup function that owns any underlying client connection.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - mode: is the string value supplied to New.
//   - address: locates the external dependency used by the operation.
//   - dialTimeout: defines the time bound applied by the operation.
//
// Returns:
//   - client: implements Registry liveness and placement reads for the API.
//   - closeClient: closes the gRPC connection; memory mode returns a no-op closer.
//   - error: reports an unsupported mode or gRPC client construction failure.
func New(ctx context.Context, mode string, address string, dialTimeout time.Duration) (registryv1.AeroRegistryClient, func() error, error) {
	switch mode {
	case "memory":
		return NewMemoryClient(), func() error { return nil }, nil
	case "grpc":
		return NewGRPCClient(ctx, address, dialTimeout)
	default:
		return nil, nil, fmt.Errorf("unsupported registry mode %q", mode)
	}
}
