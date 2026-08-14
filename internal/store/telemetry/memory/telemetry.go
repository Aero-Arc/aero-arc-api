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

// NewStore constructs memory from the supplied configuration and dependencies.
//
// Returns:
//   - result: is the *Store value produced by NewStore.
func NewStore() *Store {
	return &Store{}
}

// AddSample adds the supplied value to Store.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - sample: is the domain.TelemetrySample value supplied to AddSample.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) AddSample(_ context.Context, sample domain.TelemetrySample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, sample)
	return nil
}

// GetLatestAircraftStates returns the latest independently sampled telemetry groups for each requested aircraft.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - aircraftIDs: identifies the target aircraft.
//
// Returns:
//   - result: is the map[string]domain.AircraftTelemetryState value produced by GetLatestAircraftStates.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// GetLatestSample returns the newest legacy telemetry sample for one aircraft.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the *domain.TelemetrySample value produced by GetLatestSample.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// QueryAircraftSamples queries Store with the supplied statement and parameters.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - aircraftID: identifies the target aircraft.
//   - limit: caps the number of records claimed or returned in one call.
//
// Returns:
//   - result: is the []domain.TelemetrySample value produced by QueryAircraftSamples.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// QueryFlightSamples queries Store with the supplied statement and parameters.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - flightID: identifies the target flight.
//   - limit: caps the number of records claimed or returned in one call.
//
// Returns:
//   - result: is the []domain.TelemetrySample value produced by QueryFlightSamples.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
