package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type Store struct {
	mu      sync.RWMutex
	samples []domain.TelemetrySample
}

func NewStore() *Store {
	return &Store{}
}

func (s *Store) AddSample(_ context.Context, sample domain.TelemetrySample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, sample)
	return nil
}

func (s *Store) GetLatestSample(_ context.Context, aircraftID string) (*domain.TelemetrySample, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest *domain.TelemetrySample
	for _, sample := range s.samples {
		if sample.AircraftID != aircraftID {
			continue
		}
		item := sample
		if latest == nil || item.RecordedAt.After(latest.RecordedAt) {
			latest = &item
		}
	}
	return latest, nil
}

func (s *Store) QueryFlightSamples(_ context.Context, flightID string, limit int) ([]domain.TelemetrySample, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	samples := make([]domain.TelemetrySample, 0)
	for _, sample := range s.samples {
		if sample.FlightID == flightID {
			samples = append(samples, sample)
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].RecordedAt.Before(samples[j].RecordedAt) })
	if limit > 0 && len(samples) > limit {
		samples = samples[:limit]
	}
	return samples, nil
}
