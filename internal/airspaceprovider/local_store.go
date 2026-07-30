package airspaceprovider

import (
	"context"
	"fmt"
	"time"

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

func (p *LocalStoreAirspaceProvider) ID() string {
	return "local_durable_store"
}

func (p *LocalStoreAirspaceProvider) FindOperationalIntents(
	ctx context.Context,
	query Query,
) ([]OperationalIntent, error) {
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

	records := make([]OperationalIntent, 0)
	for _, peer := range peers {
		if peer.ID == query.Intent.ID || !candidateConflictEligible(peer.Status) {
			continue
		}
		peerVolumes := make([]domain.OperationalVolume, 0)
		for _, peerVolume := range volumesForVersion(volumesByIntent[peer.ID], peer.Version) {
			if peerVolumeCouldConflict(query.Volumes, peerVolume) {
				peerVolumes = append(peerVolumes, peerVolume)
			}
		}
		if len(peerVolumes) == 0 {
			continue
		}
		record := OperationalIntent{
			Source: Source{
				ProviderID: p.ID(), ReferenceID: peer.ID, Manager: peer.OperatorID,
				Version: peer.Version, Local: true,
			},
			Intent: peer,
		}
		for _, volume := range peerVolumes {
			if volume.VolumeType == domain.OperationalVolumeContingency ||
				volume.VolumeType == domain.OperationalVolumeEmergency {
				record.OffNominalVolumes = append(record.OffNominalVolumes, volume)
			} else {
				record.Volumes = append(record.Volumes, volume)
			}
		}
		records = append(records, record)
	}
	return records, nil
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

func volumesForVersion(volumes []domain.OperationalVolume, version int) []domain.OperationalVolume {
	filtered := make([]domain.OperationalVolume, 0, len(volumes))
	for _, volume := range volumes {
		if volume.IntentVersion == version {
			filtered = append(filtered, volume)
		}
	}
	return filtered
}

func peerVolumeDimensionsUsable(volume domain.OperationalVolume) bool {
	return !volume.StartsAt.IsZero() &&
		!volume.EndsAt.IsZero() &&
		volume.StartsAt.Before(volume.EndsAt) &&
		volume.MinAltitudeM >= 0 &&
		volume.MaxAltitudeM > 0 &&
		volume.MinAltitudeM <= volume.MaxAltitudeM &&
		volume.AltitudeRef != ""
}

func timeWindowsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

func altitudeBandsOverlap(aMin, aMax, bMin, bMax float64) bool {
	return aMin <= bMax && bMin <= aMax
}
