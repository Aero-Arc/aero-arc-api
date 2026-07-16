package deconfliction

import (
	"context"
	"fmt"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

// LocalStoreAirspaceProvider discovers conflict candidates from the local
// durable store. It filters by intent lifecycle status, current intent
// version, time overlap, and altitude band before any geometry is parsed.
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
	peers, err := p.durable.ListOperationalIntents(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list operational intents: %w", err)
	}
	allVolumes, err := p.durable.ListOperationalVolumes(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list candidate operational volumes: %w", err)
	}
	volumesByIntent := make(map[string][]domain.OperationalVolume)
	for _, volume := range allVolumes {
		volumesByIntent[volume.IntentID] = append(volumesByIntent[volume.IntentID], volume)
	}

	candidates := make([]domain.OperationalIntentConflictCandidate, 0)
	for _, peer := range peers {
		if peer.ID == intent.ID || !candidateConflictEligible(peer.Status) {
			continue
		}
		peerVolumes := make([]domain.OperationalVolume, 0)
		for _, peerVolume := range volumesForVersion(volumesByIntent[peer.ID], peer.Version) {
			if peerVolumeCouldConflict(volumes, peerVolume) {
				peerVolumes = append(peerVolumes, peerVolume)
			}
		}
		if len(peerVolumes) == 0 {
			continue
		}
		candidates = append(candidates, domain.OperationalIntentConflictCandidate{
			Intent:  peer,
			Volumes: peerVolumes,
		})
	}
	return candidates, nil
}

// peerVolumeCouldConflict is the broad-phase 1D prefilter applied before any
// geometry parsing. It may only drop a peer volume when narrow-phase
// evaluation could not produce a finding against any target volume:
//
//   - A peer volume with unusable dimensions always fails closed to an
//     indeterminate finding downstream, so it is always kept.
//   - A peer volume with a mismatched altitude reference can still yield an
//     indeterminate finding, so altitude bands are only compared when the
//     references match.
func peerVolumeCouldConflict(targetVolumes []domain.OperationalVolume, peerVolume domain.OperationalVolume) bool {
	if !peerVolumeDimensionsUsable(peerVolume) {
		return true
	}
	for _, target := range targetVolumes {
		if !timeWindowsOverlap(target.StartsAt, target.EndsAt, peerVolume.StartsAt, peerVolume.EndsAt) {
			continue
		}
		if target.AltitudeRef != peerVolume.AltitudeRef ||
			altitudeBandsOverlap(target.MinAltitudeM, target.MaxAltitudeM, peerVolume.MinAltitudeM, peerVolume.MaxAltitudeM) {
			return true
		}
	}
	return false
}

func candidateConflictEligible(status domain.IntentStatus) bool {
	return status == domain.IntentStatusAccepted || status == domain.IntentStatusActive
}
