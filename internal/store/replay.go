package store

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type ReplayStore interface {
	GetReplayManifest(ctx context.Context, flightID string) (*domain.ReplayManifest, error)
}
