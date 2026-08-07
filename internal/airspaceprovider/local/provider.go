package localprovider

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

// Provider uses the durable store's spatial query for candidate discovery and
// hydrates every result from that same authoritative store.
type Provider struct {
	store store
}

type store interface {
	GetOperationalIntentVersion(context.Context, string, int) (domain.OperationalIntent, error)
	ListOperationalVolumes(context.Context, string) ([]domain.OperationalVolume, error)
	FindCandidates(context.Context, durable.CandidateQuery) ([]durable.Candidate, error)
}

func New(store store) *Provider {
	return &Provider{store: store}
}

func (p *Provider) ID() string {
	return airspaceprovider.ProviderLocal
}

func (p *Provider) FindOperationalIntents(
	ctx context.Context,
	query airspaceprovider.Query,
) ([]airspaceprovider.OperationalIntent, error) {
	if p.store == nil {
		return nil, fmt.Errorf("local spatial provider is not configured")
	}
	candidates, err := p.store.FindCandidates(ctx, durable.CandidateQuery{
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
		intent, err := p.store.GetOperationalIntentVersion(ctx, candidate.IntentID, candidate.IntentVersion)
		if errors.Is(err, durable.ErrNotFound) {
			continue // The candidate may have been removed after the spatial query.
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
	records := make([]airspaceprovider.OperationalIntent, 0, len(ids))
	for _, id := range ids {
		intent := selected[id]
		volumes, err := p.store.ListOperationalVolumes(ctx, id)
		if err != nil {
			readErrors = append(readErrors, fmt.Errorf("list candidate %s volumes: %w", id, err))
			continue
		}
		record := airspaceprovider.OperationalIntent{
			Source: airspaceprovider.Source{
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

func candidateConflictEligible(status domain.IntentStatus) bool {
	return status == domain.IntentStatusAccepted || status == domain.IntentStatusActive
}

func volumesForVersion(volumes []domain.OperationalVolume, version int) []domain.OperationalVolume {
	filtered := make([]domain.OperationalVolume, 0, len(volumes))
	for _, volume := range volumes {
		if volume.IntentVersion == version {
			filtered = append(filtered, volume)
		}
	}
	return filtered
}
