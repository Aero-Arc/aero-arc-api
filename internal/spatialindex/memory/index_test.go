package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
)

func TestIndexFindsBroadPhaseCandidates(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	index := New()
	if err := index.Rebuild(ctx, []domain.OperationalVolume{
		testVolume("target-volume", "target", 1, now),
		testVolume("peer-volume", "peer", 2, now),
		testVolume("late-volume", "late", 1, now.Add(2*time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	candidates, err := index.FindCandidates(ctx, spatialindex.Query{
		ExcludeIntentID: "target",
		Volumes:         []domain.OperationalVolume{testVolume("target-volume", "target", 1, now)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].IntentID != "peer" || candidates[0].IntentVersion != 2 {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestIndexReplacesOneIntentVersion(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	index := New()
	if err := index.RecordVolume(ctx, testVolume("v1", "peer", 1, now)); err != nil {
		t.Fatal(err)
	}
	if err := index.RecordVolume(ctx, testVolume("v2-old", "peer", 2, now)); err != nil {
		t.Fatal(err)
	}
	if err := index.ReplaceVolumes(ctx, "peer", 2, []domain.OperationalVolume{
		testVolume("v2-new", "peer", 2, now.Add(2*time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	candidates, err := index.FindCandidates(ctx, spatialindex.Query{
		ExcludeIntentID: "target",
		Volumes:         []domain.OperationalVolume{testVolume("target", "target", 1, now)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].IntentVersion != 1 {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func testVolume(id, intentID string, version int, start time.Time) domain.OperationalVolume {
	return domain.OperationalVolume{
		ID: id, IntentID: intentID, IntentVersion: version,
		MinAltitudeM: 10, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: start, EndsAt: start.Add(time.Hour),
	}
}
