package airspaceprovider

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

// LocalSpatialProvider uses a spatial projection for candidate discovery and
// hydrates every result from the authoritative durable store.
type LocalSpatialProvider struct {
	durable operationalReader
	spatial spatialindex.CandidateFinder
}

type operationalReader interface {
	GetOperationalIntentVersion(context.Context, string, int) (domain.OperationalIntent, error)
	ListOperationalVolumes(context.Context, string) ([]domain.OperationalVolume, error)
}

func NewLocalSpatialProvider(
	durableStore operationalReader,
	spatialIndex spatialindex.CandidateFinder,
) *LocalSpatialProvider {
	return &LocalSpatialProvider{durable: durableStore, spatial: spatialIndex}
}

func (p *LocalSpatialProvider) ID() string {
	return "local"
}

func (p *LocalSpatialProvider) FindOperationalIntents(
	ctx context.Context,
	query Query,
) ([]OperationalIntent, error) {
	if p.durable == nil || p.spatial == nil {
		return nil, fmt.Errorf("local spatial provider is not configured")
	}
	candidates, err := p.spatial.FindCandidates(ctx, spatialindex.Query{
		ExcludeIntentID: query.Intent.ID,
		Volumes:         query.Volumes,
	})
	if err != nil {
		return nil, fmt.Errorf("find local spatial candidates: %w", err)
	}

	// A newer draft may coexist with the still-effective accepted version. Pick
	// the highest conflict-eligible candidate version, not simply the latest.
	selected := make(map[string]domain.OperationalIntent)
	var readErrors []error
	for _, candidate := range candidates {
		intent, err := p.durable.GetOperationalIntentVersion(ctx, candidate.IntentID, candidate.IntentVersion)
		if errors.Is(err, durable.ErrNotFound) {
			continue // A stale projection row can only create a false positive.
		}
		if err != nil {
			readErrors = append(readErrors, fmt.Errorf("get candidate %s v%d: %w", candidate.IntentID, candidate.IntentVersion, err))
			continue
		}
		if !candidateConflictEligible(intent.Status) {
			continue
		}
		current, exists := selected[intent.ID]
		if !exists || intent.Version > current.Version {
			selected[intent.ID] = intent
		}
	}

	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	records := make([]OperationalIntent, 0, len(ids))
	for _, id := range ids {
		intent := selected[id]
		volumes, err := p.durable.ListOperationalVolumes(ctx, id)
		if err != nil {
			readErrors = append(readErrors, fmt.Errorf("list candidate %s volumes: %w", id, err))
			continue
		}
		record := OperationalIntent{
			Source: Source{
				ProviderID: p.ID(), ReferenceID: intent.ID, Manager: intent.OperatorID,
				Version: intent.Version, Local: true,
			},
			Intent: intent,
		}
		for _, volume := range volumesForVersion(volumes, intent.Version) {
			if volume.VolumeType == domain.OperationalVolumeContingency ||
				volume.VolumeType == domain.OperationalVolumeEmergency {
				record.OffNominalVolumes = append(record.OffNominalVolumes, volume)
			} else {
				record.Volumes = append(record.Volumes, volume)
			}
		}
		if len(record.Volumes)+len(record.OffNominalVolumes) > 0 {
			records = append(records, record)
		}
	}
	return records, errors.Join(readErrors...)
}
