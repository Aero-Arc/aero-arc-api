package memory

import (
	"context"
	"sort"
	"strconv"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

// FindCandidates is the development-store equivalent of the PostGIS query. It
// scans in memory and deliberately over-selects because final conflict checks
// always hydrate and evaluate the authoritative records.
func (s *Store) FindCandidates(_ context.Context, query durable.CandidateQuery) ([]durable.Candidate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	unique := make(map[string]durable.Candidate)
	for _, volume := range s.operationalVolumes {
		if volume.IntentID == query.ExcludeIntentID || !couldOverlap(query.Volumes, volume) {
			continue
		}
		candidate := durable.Candidate{IntentID: volume.IntentID, IntentVersion: volume.IntentVersion}
		unique[candidateKey(candidate)] = candidate
	}
	candidates := make([]durable.Candidate, 0, len(unique))
	for _, candidate := range unique {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].IntentID == candidates[b].IntentID {
			return candidates[a].IntentVersion < candidates[b].IntentVersion
		}
		return candidates[a].IntentID < candidates[b].IntentID
	})
	return candidates, nil
}

func couldOverlap(targets []domain.OperationalVolume, candidate domain.OperationalVolume) bool {
	if len(targets) == 0 || !dimensionsUsable(candidate) {
		return true
	}
	for _, target := range targets {
		if !dimensionsUsable(target) {
			return true
		}
		if target.StartsAt.Before(candidate.EndsAt) && candidate.StartsAt.Before(target.EndsAt) &&
			(target.AltitudeRef != candidate.AltitudeRef ||
				target.MinAltitudeM <= candidate.MaxAltitudeM && candidate.MinAltitudeM <= target.MaxAltitudeM) {
			return true
		}
	}
	return false
}

func dimensionsUsable(volume domain.OperationalVolume) bool {
	return !volume.StartsAt.IsZero() && !volume.EndsAt.IsZero() &&
		volume.StartsAt.Before(volume.EndsAt) &&
		volume.MinAltitudeM >= 0 && volume.MaxAltitudeM > 0 &&
		volume.MinAltitudeM <= volume.MaxAltitudeM &&
		volume.AltitudeRef != ""
}

func candidateKey(candidate durable.Candidate) string {
	return candidate.IntentID + "\x00" + strconv.Itoa(candidate.IntentVersion)
}
