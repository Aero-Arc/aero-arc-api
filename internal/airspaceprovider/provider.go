package airspaceprovider

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

const (
	ProviderLocal    = "local"
	ProviderInterUSS = "interuss"
)

type Query struct {
	Intent  domain.OperationalIntent
	Volumes []domain.OperationalVolume
}

type Source struct {
	ProviderID  string
	ReferenceID string
	Manager     string
	USSBaseURL  string
	Version     int
	Local       bool
}

type OperationalIntent struct {
	Source            Source
	Intent            domain.OperationalIntent
	Volumes           []domain.OperationalVolume
	OffNominalVolumes []domain.OperationalVolume
}

// Provider discovers normalized operational intents from one airspace source.
type Provider interface {
	ID() string
	FindOperationalIntents(ctx context.Context, query Query) ([]OperationalIntent, error)
}
