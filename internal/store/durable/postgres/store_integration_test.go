//go:build integration

package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
)

func TestAuthoritativeSpatialReadCheckSlice(t *testing.T) {
	databaseURL := os.Getenv("AERO_API_TEST_POSTGIS_URL")
	if databaseURL == "" {
		t.Skip("AERO_API_TEST_POSTGIS_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	observer, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observer.Close)
	if _, err := store.pool.Exec(ctx, `TRUNCATE conflict_findings, operational_volumes, operational_intents`); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	target := integrationVolume("target-volume", "target", 1, now,
		`{"type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[-97,32],[-96,32],[-96,33],[-97,33],[-97,32]]]}}`)
	overlap := integrationVolume("overlap-volume", "overlap", 1, now,
		`{"type":"Polygon","coordinates":[[[-96.5,32.5],[-95.5,32.5],[-95.5,33.5],[-96.5,33.5],[-96.5,32.5]]]}`)
	distant := integrationVolume("distant-volume", "distant", 1, now,
		`{"type":"Polygon","coordinates":[[[-80,20],[-79,20],[-79,21],[-80,21],[-80,20]]]}`)
	for _, id := range []string{target.IntentID, overlap.IntentID, distant.IntentID} {
		if err := store.CreateOperationalIntent(ctx, domain.OperationalIntent{
			ID: id, Version: 1, AircraftID: id + "-aircraft", PlannedStartAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, volume := range []domain.OperationalVolume{target, overlap, distant} {
		if err := store.RecordOperationalVolume(ctx, volume); err != nil {
			t.Fatal(err)
		}
	}

	// Query through a second store instance to model another API replica.
	candidates, err := observer.FindCandidates(ctx, spatialindex.Query{
		ExcludeIntentID: target.IntentID,
		Volumes:         []domain.OperationalVolume{target},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].IntentID != overlap.IntentID {
		t.Fatalf("candidates = %#v", candidates)
	}

	replacement := distant
	replacement.ID = "overlap-moved"
	replacement.IntentID = overlap.IntentID
	if err := store.ReplaceOperationalVolumes(ctx, overlap.IntentID, 1, []domain.OperationalVolume{replacement}); err != nil {
		t.Fatal(err)
	}
	candidates, err = observer.FindCandidates(ctx, spatialindex.Query{
		ExcludeIntentID: target.IntentID,
		Volumes:         []domain.OperationalVolume{target},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates after replacement = %#v", candidates)
	}

	invalid := target
	invalid.ID = "invalid"
	invalid.EndsAt = invalid.StartsAt
	if err := store.RecordOperationalVolume(ctx, invalid); err == nil {
		t.Fatal("expected PostGIS to reject invalid time window")
	}
}

func integrationVolume(id, intentID string, version int, now time.Time, geoJSON string) domain.OperationalVolume {
	return domain.OperationalVolume{
		ID: id, IntentID: intentID, IntentVersion: version,
		MinAltitudeM: 20, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: now, EndsAt: now.Add(time.Hour), GeoJSON: geoJSON,
	}
}
