package durable_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestSpatialProjectionFailureLeavesDurableDataAndFailsReadsClosed(t *testing.T) {
	ctx := context.Background()
	index := &controlledIndex{}
	projection := spatialindex.NewProjection(index)
	if err := projection.Rebuild(ctx, nil); err != nil {
		t.Fatal(err)
	}
	base := durablememory.NewStore()
	store := durable.UseSpatialIndex(base, projection)
	volume := domain.OperationalVolume{
		ID: "volume", IntentID: "intent", IntentVersion: 1,
		MinAltitudeM: 10, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: time.Now().UTC(), EndsAt: time.Now().UTC().Add(time.Hour),
	}
	index.writeErr = errors.New("index unavailable")
	if err := store.RecordOperationalVolume(ctx, volume); err == nil {
		t.Fatal("expected projection write error")
	}
	stored, err := base.ListOperationalVolumes(ctx, volume.IntentID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("durable volumes = %#v, error = %v", stored, err)
	}
	if _, err := projection.FindCandidates(ctx, spatialindex.Query{}); err == nil {
		t.Fatal("expected candidate reads to fail closed")
	}
}

func TestSpatiallyIndexedStoreProjectsVolumeReplacements(t *testing.T) {
	ctx := context.Background()
	index := &controlledIndex{}
	projection := spatialindex.NewProjection(index)
	if err := projection.Rebuild(ctx, nil); err != nil {
		t.Fatal(err)
	}
	base := durablememory.NewStore()
	store := durable.UseSpatialIndex(base, projection)
	v1 := domain.OperationalIntent{ID: "intent", Version: 1, Status: domain.IntentStatusAccepted}
	if err := store.CreateOperationalIntent(ctx, v1); err != nil {
		t.Fatal(err)
	}
	volume := domain.OperationalVolume{ID: "volume", IntentID: v1.ID, IntentVersion: 1}
	if err := store.ReplaceOperationalVolumes(ctx, v1.ID, 1, []domain.OperationalVolume{volume}); err != nil {
		t.Fatal(err)
	}
	v2 := v1
	v2.Version = 2
	v2.Status = domain.IntentStatusDraft
	volume.IntentVersion = 2
	if err := store.ReplaceOperationalIntent(ctx, 1, v2, []domain.OperationalVolume{volume}); err != nil {
		t.Fatal(err)
	}
	if index.replacements != 2 {
		t.Fatalf("spatial replacements = %d", index.replacements)
	}
}

type controlledIndex struct {
	writeErr     error
	replacements int
}

func (*controlledIndex) ID() string { return "controlled" }
func (*controlledIndex) Close()     {}
func (*controlledIndex) Rebuild(context.Context, []domain.OperationalVolume) error {
	return nil
}
func (i *controlledIndex) RecordVolume(context.Context, domain.OperationalVolume) error {
	return i.writeErr
}

func (i *controlledIndex) ReplaceVolumes(context.Context, string, int, []domain.OperationalVolume) error {
	i.replacements++
	return i.writeErr
}
func (*controlledIndex) FindCandidates(context.Context, spatialindex.Query) ([]spatialindex.Candidate, error) {
	return nil, nil
}
