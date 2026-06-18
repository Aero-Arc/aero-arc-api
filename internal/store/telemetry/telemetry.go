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

// Store defines the telemetry read model used by the API.
type Store interface {
	GetLatestSample(ctx context.Context, aircraftID string) (*domain.TelemetrySample, error)
	QueryAircraftSamples(ctx context.Context, aircraftID string, limit int) ([]domain.TelemetrySample, error)
	QueryFlightSamples(ctx context.Context, flightID string, limit int) ([]domain.TelemetrySample, error)
}
