package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/telemetry"
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

func (s *Store) GetLatestAircraftStates(_ context.Context, aircraftIDs []string) (map[string]domain.AircraftTelemetryState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	states := make(map[string]domain.AircraftTelemetryState, len(aircraftIDs))
	requested := make(map[string]struct{}, len(aircraftIDs))
	for _, aircraftID := range aircraftIDs {
		states[aircraftID] = domain.AircraftTelemetryState{}
		requested[aircraftID] = struct{}{}
	}
	for _, sample := range s.samples {
		if _, ok := requested[sample.AircraftID]; !ok {
			continue
		}
		state := states[sample.AircraftID]
		if state.Position == nil || sample.RecordedAt.After(state.Position.RecordedAt) {
			altitude := sample.AltitudeM
			groundspeed := sample.VelocityMPS
			heading := sample.HeadingDeg
			state.Position = &domain.PositionTelemetry{
				TelemetryObservation: domain.TelemetryObservation{RecordedAt: sample.RecordedAt, FrameID: sample.ID, OperatorID: sample.OperatorID, IntentID: sample.IntentID, IntentVersion: sample.IntentVersion, FlightID: sample.FlightID},
				LatitudeDeg:          sample.Latitude, LongitudeDeg: sample.Longitude,
				AltitudeMSLM: &altitude, GroundspeedMPS: &groundspeed, HeadingDeg: &heading,
			}
		}
		if sample.BatteryPct != nil && (state.Battery == nil || sample.RecordedAt.After(state.Battery.RecordedAt)) {
			remaining := *sample.BatteryPct
			state.Battery = &domain.BatteryTelemetry{
				TelemetryObservation: domain.TelemetryObservation{RecordedAt: sample.RecordedAt, FrameID: sample.ID, OperatorID: sample.OperatorID, IntentID: sample.IntentID, IntentVersion: sample.IntentVersion, FlightID: sample.FlightID},
				BatteryRemainingPct:  &remaining,
			}
		}
		states[sample.AircraftID] = state
	}
	return states, nil
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

func (s *Store) QueryAircraftSamples(_ context.Context, aircraftID string, limit int) ([]domain.TelemetrySample, error) {
	if limit <= 0 {
		limit = telemetry.DefaultSampleLimit
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	samples := make([]domain.TelemetrySample, 0)
	for _, sample := range s.samples {
		if sample.AircraftID == aircraftID {
			samples = append(samples, sample)
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].RecordedAt.Before(samples[j].RecordedAt) })
	if limit > 0 && len(samples) > limit {
		samples = samples[len(samples)-limit:]
	}
	return samples, nil
}

func (s *Store) QueryFlightSamples(_ context.Context, flightID string, limit int) ([]domain.TelemetrySample, error) {
	if limit <= 0 {
		limit = telemetry.DefaultSampleLimit
	}
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
