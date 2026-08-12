// Package telemetry owns queryable time-series samples used for current status
// and flight playback views.
//
// Implementations should be optimized for time-window and latest-sample
// queries, for example InfluxDB or another time-series backend. They should not
// own durable fleet metadata, maintenance records, or replay blob storage.
package telemetry

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

// DefaultSampleLimit is the first-tier bounded query size used when callers
// pass zero rather than an explicit positive limit.
const DefaultSampleLimit = 1000

// Store defines the telemetry read model used by the API.
type Store interface {
	// GetLatestAircraftStates returns independently sampled latest telemetry
	// message groups. Every requested aircraft ID is present in the result,
	// including aircraft with no observations.
	GetLatestAircraftStates(ctx context.Context, aircraftIDs []string) (map[string]domain.AircraftTelemetryState, error)
	GetLatestSample(ctx context.Context, aircraftID string) (*domain.TelemetrySample, error)
	// QueryAircraftSamples selects the latest limited window and returns it
	// chronologically from oldest to newest. A zero limit uses DefaultSampleLimit.
	QueryAircraftSamples(ctx context.Context, aircraftID string, limit int) ([]domain.TelemetrySample, error)
	// QueryFlightSamples selects the earliest limited window and returns it
	// chronologically from oldest to newest. A zero limit uses DefaultSampleLimit.
	QueryFlightSamples(ctx context.Context, flightID string, limit int) ([]domain.TelemetrySample, error)
}
