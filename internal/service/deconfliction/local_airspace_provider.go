package deconfliction

import (
	"context"
	"fmt"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

// LocalStoreAirspaceProvider discovers conflict candidates from the local
// durable store via indexed QueryConflictCandidates rather than scanning all
// intents and volumes in application memory.
type LocalStoreAirspaceProvider struct {
	durable durable.Store
}

func NewLocalStoreAirspaceProvider(durableStore durable.Store) *LocalStoreAirspaceProvider {
	return &LocalStoreAirspaceProvider{durable: durableStore}
}

func (p *LocalStoreAirspaceProvider) QueryConflictCandidates(
	ctx context.Context,
	intent domain.OperationalIntent,
	volumes []domain.OperationalVolume,
) ([]domain.OperationalIntentConflictCandidate, error) {
	candidates, err := p.durable.QueryConflictCandidates(ctx, durable.ConflictCandidateQuery{
		ExcludeIntentID: intent.ID,
		TargetVolumes:   volumes,
	})
	if err != nil {
		return nil, fmt.Errorf("query conflict candidates: %w", err)
	}
	return candidates, nil
}
