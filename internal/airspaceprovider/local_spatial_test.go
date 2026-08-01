package airspaceprovider

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
	spatialmemory "github.com/Aero-Arc/aero-arc-api/internal/spatialindex/memory"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestLocalSpatialProviderHydratesAuthoritativeCandidate(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	base := durablememory.NewStore()
	projection := spatialindex.NewProjection(spatialmemory.New())
	store := durable.UseSpatialIndex(base, projection)
	target := domain.OperationalIntent{ID: "target", Version: 1, Status: domain.IntentStatusDraft}
	accepted := domain.OperationalIntent{ID: "peer", Version: 1, Status: domain.IntentStatusAccepted, OperatorID: "operator"}
	draft := accepted
	draft.Version = 2
	draft.Status = domain.IntentStatusDraft
	for _, intent := range []domain.OperationalIntent{target, accepted, draft} {
		if err := store.CreateOperationalIntent(ctx, intent); err != nil {
			t.Fatal(err)
		}
	}
	targetVolume := testVolume("target-volume", target.ID, now)
	acceptedVolume := testVolume("accepted-volume", accepted.ID, now)
	draftVolume := testVolume("draft-volume", draft.ID, now)
	draftVolume.IntentVersion = 2
	for _, volume := range []domain.OperationalVolume{targetVolume, acceptedVolume, draftVolume} {
		if err := store.RecordOperationalVolume(ctx, volume); err != nil {
			t.Fatal(err)
		}
	}

	provider := NewLocalSpatialProvider(store, projection)
	records, err := provider.FindOperationalIntents(ctx, Query{Intent: target, Volumes: []domain.OperationalVolume{targetVolume}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Intent.Version != 1 || records[0].Source.Manager != "operator" {
		t.Fatalf("records = %#v", records)
	}
}

func TestLocalSpatialProviderFailsClosedWhenProjectionIsOutOfSync(t *testing.T) {
	ctx := context.Background()
	index := &failingSpatialIndex{}
	projection := spatialindex.NewProjection(index)
	if err := projection.Rebuild(ctx, nil); err == nil {
		t.Fatal("expected rebuild failure")
	}
	provider := NewLocalSpatialProvider(durablememory.NewStore(), projection)
	if _, err := provider.FindOperationalIntents(ctx, Query{}); err == nil {
		t.Fatal("expected out-of-sync projection error")
	}
}

type failingSpatialIndex struct{}

func (*failingSpatialIndex) ID() string { return "failing" }
func (*failingSpatialIndex) Close()     {}
func (*failingSpatialIndex) Rebuild(context.Context, []domain.OperationalVolume) error {
	return context.Canceled
}
func (*failingSpatialIndex) RecordVolume(context.Context, domain.OperationalVolume) error {
	return context.Canceled
}
func (*failingSpatialIndex) ReplaceVolumes(context.Context, string, int, []domain.OperationalVolume) error {
	return context.Canceled
}
func (*failingSpatialIndex) FindCandidates(context.Context, spatialindex.Query) ([]spatialindex.Candidate, error) {
	return nil, nil
}
