package preflight

import (
	"context"
	"fmt"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

// Snapshot is the immutable input state for one preflight evaluation.
type Snapshot struct {
	Intent      domain.OperationalIntent
	Aircraft    domain.Aircraft
	AircraftErr error
	Volumes     []domain.OperationalVolume
	Now         time.Time
}

func (s *PreflightService) loadSnapshot(ctx context.Context, intentID string) (Snapshot, error) {
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("get operational intent: %w", err)
	}

	aircraft, aircraftErr := s.durable.GetAircraft(ctx, intent.AircraftID)
	volumes, err := s.durable.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("list operational volumes: %w", err)
	}

	return Snapshot{
		Intent:      intent,
		Aircraft:    aircraft,
		AircraftErr: aircraftErr,
		Volumes:     volumesForVersion(volumes, intent.Version),
		Now:         s.now().UTC(),
	}, nil
}

func volumesForVersion(volumes []domain.OperationalVolume, version int) []domain.OperationalVolume {
	filtered := make([]domain.OperationalVolume, 0, len(volumes))
	for _, volume := range volumes {
		if volume.IntentVersion == version {
			filtered = append(filtered, volume)
		}
	}
	return filtered
}
