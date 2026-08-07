package spatialindex

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type Query struct {
	ExcludeIntentID string
	Volumes         []domain.OperationalVolume
}

type Candidate struct {
	IntentID      string
	IntentVersion int
}

type CandidateFinder interface {
	FindCandidates(context.Context, Query) ([]Candidate, error)
}
