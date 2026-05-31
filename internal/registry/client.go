package registry

import (
	"context"
	"fmt"
	"time"

	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
)

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
