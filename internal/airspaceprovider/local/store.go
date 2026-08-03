package localprovider

import (
	"context"
	"fmt"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

// NewFromStore is the in-process fallback used by tests and callers that do
// not configure a separate spatial index. Production startup calls New with
// an explicit spatial index.
func NewFromStore(durableStore durable.Store) *Provider {
	return New(durableStore, &durableVolumeFinder{durable: durableStore})
}

type durableVolumeFinder struct {
	durable durable.Store
}

func (f *durableVolumeFinder) FindCandidates(
	ctx context.Context,
	query spatialindex.Query,
) ([]spatialindex.Candidate, error) {
	allVolumes, err := f.durable.ListOperationalVolumes(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("list candidate operational volumes: %w", err)
	}
	unique := make(map[string]spatialindex.Candidate)
	for _, volume := range allVolumes {
		if volume.IntentID == query.ExcludeIntentID || !peerVolumeCouldConflict(query.Volumes, volume) {
			continue
		}
		candidate := spatialindex.Candidate{IntentID: volume.IntentID, IntentVersion: volume.IntentVersion}
		unique[fmt.Sprintf("%s:%d", candidate.IntentID, candidate.IntentVersion)] = candidate
	}
	candidates := make([]spatialindex.Candidate, 0, len(unique))
	for _, candidate := range unique {
		candidates = append(candidates, candidate)
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
