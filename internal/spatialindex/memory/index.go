package memory

import (
	"context"
	"sort"
	"strconv"
	"sync"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
)

type Index struct {
	mu      sync.RWMutex
	volumes map[string]domain.OperationalVolume
}

func New() *Index {
	return &Index{volumes: make(map[string]domain.OperationalVolume)}
}

func (i *Index) ID() string {
	return "memory"
}

func (i *Index) Close() {}

func (i *Index) Rebuild(_ context.Context, volumes []domain.OperationalVolume) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.volumes = make(map[string]domain.OperationalVolume, len(volumes))
	for _, volume := range volumes {
		i.volumes[volumeKey(volume)] = volume
	}
	return nil
}

func (i *Index) RecordVolume(_ context.Context, volume domain.OperationalVolume) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.volumes[volumeKey(volume)] = volume
	return nil
}

func (i *Index) ReplaceVolumes(_ context.Context, id string, version int, volumes []domain.OperationalVolume) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	for key, volume := range i.volumes {
		if volume.IntentID == id && volume.IntentVersion == version {
			delete(i.volumes, key)
		}
	}
	for _, volume := range volumes {
		i.volumes[volumeKey(volume)] = volume
	}
	return nil
}

func (i *Index) FindCandidates(_ context.Context, query spatialindex.Query) ([]spatialindex.Candidate, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	unique := make(map[string]spatialindex.Candidate)
	for _, volume := range i.volumes {
		if volume.IntentID == query.ExcludeIntentID || !couldOverlap(query.Volumes, volume) {
			continue
		}
		candidate := spatialindex.Candidate{IntentID: volume.IntentID, IntentVersion: volume.IntentVersion}
		unique[candidateKey(candidate)] = candidate
	}
	candidates := make([]spatialindex.Candidate, 0, len(unique))
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

func volumeKey(volume domain.OperationalVolume) string {
	return volume.IntentID + "\x00" + strconv.Itoa(volume.IntentVersion) + "\x00" + volume.ID
}

func candidateKey(candidate spatialindex.Candidate) string {
	return candidate.IntentID + "\x00" + strconv.Itoa(candidate.IntentVersion)
}
