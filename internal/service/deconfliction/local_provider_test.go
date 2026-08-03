package deconfliction_test

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	localprovider "github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider/local"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

// durableCandidates is a test-only broad-phase finder. Production always
// supplies the configured spatial index to the local provider.
type durableCandidates struct {
	store durable.Store
}

func (f *durableCandidates) FindCandidates(
	ctx context.Context,
	query spatialindex.Query,
) ([]spatialindex.Candidate, error) {
	volumes, err := f.store.ListOperationalVolumes(ctx, "")
	if err != nil {
		return nil, err
	}
	unique := make(map[spatialindex.Candidate]struct{})
	for _, volume := range volumes {
		if volume.IntentID != query.ExcludeIntentID {
			unique[spatialindex.Candidate{
				IntentID:      volume.IntentID,
				IntentVersion: volume.IntentVersion,
			}] = struct{}{}
		}
	}
	candidates := make([]spatialindex.Candidate, 0, len(unique))
	for candidate := range unique {
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func newTestLocalProvider(store durable.Store) airspaceprovider.Provider {
	return localprovider.New(store, &durableCandidates{store: store})
}
