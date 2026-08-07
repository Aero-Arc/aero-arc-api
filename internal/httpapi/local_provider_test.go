package httpapi

import (
	"context"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	localprovider "github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider/local"
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

// durableCandidates is a test-only broad-phase finder.
type durableCandidates struct {
	store durable.Store
}

func (f *durableCandidates) FindCandidates(
	ctx context.Context,
	query durable.CandidateQuery,
) ([]durable.Candidate, error) {
	volumes, err := f.store.ListOperationalVolumes(ctx, "")
	if err != nil {
		return nil, err
	}
	unique := make(map[durable.Candidate]struct{})
	for _, volume := range volumes {
		if volume.IntentID != query.ExcludeIntentID {
			unique[durable.Candidate{
				IntentID:      volume.IntentID,
				IntentVersion: volume.IntentVersion,
			}] = struct{}{}
		}
	}
	candidates := make([]durable.Candidate, 0, len(unique))
	for candidate := range unique {
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func newTestLocalProvider(store durable.Store) airspaceprovider.Provider {
	return localprovider.New(store, &durableCandidates{store: store})
}

func newTestDeconflictionService(t *testing.T, store durable.Store) *deconfliction.DeconflictionService {
	t.Helper()
	service, err := deconfliction.NewDeconflictionService(store, newTestLocalProvider(store))
	if err != nil {
		t.Fatal(err)
	}
	return service
}
