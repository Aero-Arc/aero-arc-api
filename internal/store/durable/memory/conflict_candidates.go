package memory

import (
	"context"
	"sort"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func (s *Store) indexOperationalIntent(intent domain.OperationalIntent) {
	latest, ok := s.latestOperationalIntent(intent.ID)
	if ok {
		s.latestIntentByID[intent.ID] = latest
	}
	s.refreshEligibleConflictPeer(intent.ID)
}

func (s *Store) refreshEligibleConflictPeer(intentID string) {
	peer, ok := s.latestIntentByID[intentID]
	if !ok || !durable.CandidateConflictEligible(peer.Status) {
		delete(s.eligibleConflictPeers, intentID)
		return
	}
	s.eligibleConflictPeers[intentID] = peer
}

func (s *Store) indexOperationalVolume(volume domain.OperationalVolume) {
	key := operationalVolumeKey(volume)
	s.operationalVolumes[key] = volume

	volumes := s.volumesByIntentID[volume.IntentID]
	replaced := false
	for i, existing := range volumes {
		if operationalVolumeKey(existing) == key {
			volumes[i] = volume
			replaced = true
			break
		}
	}
	if !replaced {
		s.volumesByIntentID[volume.IntentID] = append(volumes, volume)
	}
}

func (s *Store) removeOperationalVolumes(intentID string, intentVersion int) {
	remaining := make([]domain.OperationalVolume, 0)
	for _, volume := range s.volumesByIntentID[intentID] {
		if volume.IntentVersion == intentVersion {
			delete(s.operationalVolumes, operationalVolumeKey(volume))
			continue
		}
		remaining = append(remaining, volume)
	}
	if len(remaining) == 0 {
		delete(s.volumesByIntentID, intentID)
		return
	}
	s.volumesByIntentID[intentID] = remaining
}

func (s *Store) QueryConflictCandidates(_ context.Context, query durable.ConflictCandidateQuery) ([]domain.OperationalIntentConflictCandidate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	candidates := make([]domain.OperationalIntentConflictCandidate, 0)
	for intentID, peer := range s.eligibleConflictPeers {
		if intentID == query.ExcludeIntentID {
			continue
		}
		peerVolumes := durable.VolumesForVersion(s.volumesByIntentID[intentID], peer.Version)
		filtered := durable.FilterConflictCandidateVolumes(query.TargetVolumes, peerVolumes)
		if len(filtered) == 0 {
			continue
		}
		candidates = append(candidates, domain.OperationalIntentConflictCandidate{
			Intent:  peer,
			Volumes: filtered,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Intent.PlannedStartAt.Before(candidates[j].Intent.PlannedStartAt)
	})
	return candidates, nil
}
