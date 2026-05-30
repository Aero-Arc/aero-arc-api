package store

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type TelemetryStore interface {
	GetLatestSample(ctx context.Context, aircraftID string) (*domain.TelemetrySample, error)
	QueryFlightSamples(ctx context.Context, flightID string, limit int) ([]domain.TelemetrySample, error)
}
