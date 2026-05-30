package registry

import (
	"context"
	"fmt"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type Client interface {
	GetLiveAircraftState(ctx context.Context, aircraftID string) (*domain.LiveAircraftState, error)
	ListLiveAircraftStates(ctx context.Context) ([]domain.LiveAircraftState, error)
}

func New(ctx context.Context, mode string, address string, dialTimeout time.Duration) (Client, func() error, error) {
	switch mode {
	case "memory":
		return NewMemoryClient(), func() error { return nil }, nil
	case "grpc":
		return NewGRPCClient(ctx, address, dialTimeout)
	default:
		return nil, nil, fmt.Errorf("unsupported registry mode %q", mode)
	}
}
