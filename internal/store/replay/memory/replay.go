package memory

import (
	"context"
	"sync"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type Store struct {
	mu        sync.RWMutex
	manifests map[string]domain.ReplayManifest
}

// NewStore constructs memory from the supplied configuration and dependencies.
//
// Returns:
//   - result: is the *Store value produced by NewStore.
func NewStore() *Store {
	return &Store{manifests: make(map[string]domain.ReplayManifest)}
}

// PutReplayManifest stores or replaces a replay manifest by flight identity.
//
// Parameters:
//   - value: is the context.Context value supplied to PutReplayManifest.
//   - manifest: is the domain.ReplayManifest value supplied to PutReplayManifest.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) PutReplayManifest(_ context.Context, manifest domain.ReplayManifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manifests[manifest.FlightID] = manifest
	return nil
}

// GetReplayManifest returns the replay manifest associated with one flight.
//
// Parameters:
//   - value: is the context.Context value supplied to GetReplayManifest.
//   - flightID: identifies the target flight.
//
// Returns:
//   - result: is the *domain.ReplayManifest value produced by GetReplayManifest.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetReplayManifest(_ context.Context, flightID string) (*domain.ReplayManifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	manifest, ok := s.manifests[flightID]
	if !ok {
		return nil, nil
	}
	return &manifest, nil
}
