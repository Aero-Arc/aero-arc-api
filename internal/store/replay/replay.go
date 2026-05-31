// Package replay owns replay manifests and references to raw replay artifacts.
//
// Implementations should point at object or file storage, such as S3, for
// replay chunks/logs. They should not own durable flight records or queryable
// telemetry samples.
package replay

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

// Store defines replay manifest lookup operations used by the API.
type Store interface {
	GetReplayManifest(ctx context.Context, flightID string) (*domain.ReplayManifest, error)
}
