package durable

import (
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

// ConflictCandidateQuery describes the target operational intent snapshot used
// for broad-phase conflict candidate discovery. Implementations should return
// peer intents whose current-version volumes could plausibly conflict with the
// target volumes; narrow-phase geometry evaluation happens outside the store.
type ConflictCandidateQuery struct {
	ExcludeIntentID string
	TargetVolumes   []domain.OperationalVolume
}

func CandidateConflictEligible(status domain.IntentStatus) bool {
	return status == domain.IntentStatusAccepted || status == domain.IntentStatusActive
}

func VolumesForVersion(volumes []domain.OperationalVolume, version int) []domain.OperationalVolume {
	filtered := make([]domain.OperationalVolume, 0, len(volumes))
	for _, volume := range volumes {
		if volume.IntentVersion == version {
			filtered = append(filtered, volume)
		}
	}
	return filtered
}

func VolumeDimensionsUsable(volume domain.OperationalVolume) bool {
	return !volume.StartsAt.IsZero() &&
		!volume.EndsAt.IsZero() &&
		volume.StartsAt.Before(volume.EndsAt) &&
		volume.MinAltitudeM >= 0 &&
		volume.MaxAltitudeM > 0 &&
		volume.MinAltitudeM <= volume.MaxAltitudeM &&
		volume.AltitudeRef != ""
}

func TimeWindowsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	// Operational volume windows are treated as half-open intervals: [start, end).
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

func AltitudeBandsOverlap(aMin, aMax, bMin, bMax float64) bool {
	return aMin <= bMax && bMin <= aMax
}

// TargetTimeEnvelope returns the union time window covering all target volumes.
// It is a conservative broad-phase bound: any volume outside this envelope
// cannot overlap any target volume.
func TargetTimeEnvelope(volumes []domain.OperationalVolume) (start, end time.Time, ok bool) {
	for _, volume := range volumes {
		if volume.StartsAt.IsZero() || volume.EndsAt.IsZero() {
			continue
		}
		if !ok {
			start, end = volume.StartsAt, volume.EndsAt
			ok = true
			continue
		}
		if volume.StartsAt.Before(start) {
			start = volume.StartsAt
		}
		if volume.EndsAt.After(end) {
			end = volume.EndsAt
		}
	}
	return start, end, ok
}

// PeerVolumeCouldConflict is the broad-phase 1D prefilter applied before any
// geometry parsing. It may only drop a peer volume when narrow-phase
// evaluation could not produce a finding against any target volume.
func PeerVolumeCouldConflict(targetVolumes []domain.OperationalVolume, peerVolume domain.OperationalVolume) bool {
	if !VolumeDimensionsUsable(peerVolume) {
		return true
	}
	for _, target := range targetVolumes {
		if !TimeWindowsOverlap(target.StartsAt, target.EndsAt, peerVolume.StartsAt, peerVolume.EndsAt) {
			continue
		}
		if target.AltitudeRef != peerVolume.AltitudeRef ||
			AltitudeBandsOverlap(target.MinAltitudeM, target.MaxAltitudeM, peerVolume.MinAltitudeM, peerVolume.MaxAltitudeM) {
			return true
		}
	}
	return false
}

func FilterConflictCandidateVolumes(targetVolumes []domain.OperationalVolume, peerVolumes []domain.OperationalVolume) []domain.OperationalVolume {
	if len(peerVolumes) == 0 {
		return nil
	}
	envelopeStart, envelopeEnd, hasEnvelope := TargetTimeEnvelope(targetVolumes)
	filtered := make([]domain.OperationalVolume, 0, len(peerVolumes))
	for _, peerVolume := range peerVolumes {
		if !VolumeDimensionsUsable(peerVolume) {
			filtered = append(filtered, peerVolume)
			continue
		}
		if hasEnvelope && !TimeWindowsOverlap(envelopeStart, envelopeEnd, peerVolume.StartsAt, peerVolume.EndsAt) {
			continue
		}
		if PeerVolumeCouldConflict(targetVolumes, peerVolume) {
			filtered = append(filtered, peerVolume)
		}
	}
	return filtered
}
