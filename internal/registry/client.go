package registry

import (
	"context"
	"fmt"
	"time"

	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
)

// New constructs registry from the supplied configuration and dependencies.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - mode: is the string value supplied to New.
//   - address: locates the external dependency used by the operation.
//   - dialTimeout: defines the time bound applied by the operation.
//
// Returns:
//   - result: is the registryv1.AeroRegistryClient value produced by New.
//   - result: is the func() error value produced by New.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
