package memory

import (
	"context"
	"sync"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type ReplayStore struct {
	mu        sync.RWMutex
	manifests map[string]domain.ReplayManifest
}

func NewReplayStore() *ReplayStore {
	return &ReplayStore{manifests: make(map[string]domain.ReplayManifest)}
}

func (s *ReplayStore) PutReplayManifest(_ context.Context, manifest domain.ReplayManifest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.manifests[manifest.FlightID] = manifest
	return nil
}

func (s *ReplayStore) GetReplayManifest(_ context.Context, flightID string) (*domain.ReplayManifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	manifest, ok := s.manifests[flightID]
	if !ok {
		return nil, nil
	}
	return &manifest, nil
}
