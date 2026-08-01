package airspaceprovider

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestLocalStoreProviderCheckIntentReturnsCandidates(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	target := domain.OperationalIntent{ID: "target", Version: 1, Status: domain.IntentStatusDraft}
	peer := domain.OperationalIntent{ID: "peer", Version: 1, Status: domain.IntentStatusAccepted}
	if err := store.CreateOperationalIntent(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOperationalIntent(ctx, peer); err != nil {
		t.Fatal(err)
	}
	targetVolume := testVolume("target-volume", "target", now)
	peerVolume := testVolume("peer-volume", "peer", now)
	if err := store.RecordOperationalVolume(ctx, targetVolume); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperationalVolume(ctx, peerVolume); err != nil {
		t.Fatal(err)
	}

	provider := NewLocalStoreAirspaceProvider(store)
	records, err := provider.FindOperationalIntents(ctx, Query{Intent: target, Volumes: []domain.OperationalVolume{targetVolume}})
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if provider.ID() != "local" {
		t.Fatalf("provider ID = %q", provider.ID())
	}
	if len(records) != 1 || records[0].Intent.ID != peer.ID ||
		records[0].Source.ReferenceID != peer.ID {
		t.Fatalf("records = %#v", records)
	}
}

func TestLocalStoreProviderExcludesIneligibleIntents(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	target := domain.OperationalIntent{ID: "target", Version: 1, Status: domain.IntentStatusDraft}
	draft := domain.OperationalIntent{ID: "draft-peer", Version: 1, Status: domain.IntentStatusDraft}
	if err := store.CreateOperationalIntent(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOperationalIntent(ctx, draft); err != nil {
		t.Fatal(err)
	}
	targetVolume := testVolume("target-volume", "target", now)
	if err := store.RecordOperationalVolume(ctx, testVolume("draft-volume", "draft-peer", now)); err != nil {
		t.Fatal(err)
	}

	records, err := NewLocalStoreAirspaceProvider(store).FindOperationalIntents(ctx, Query{Intent: target, Volumes: []domain.OperationalVolume{targetVolume}})
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records = %#v, want none", records)
	}
}

func testVolume(id, intentID string, now time.Time) domain.OperationalVolume {
	return domain.OperationalVolume{
		ID:            id,
		IntentID:      intentID,
		IntentVersion: 1,
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceWGS84,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		GeoJSON:       `{"type":"Polygon","coordinates":[[[-97,32],[-96,32],[-96,33],[-97,33],[-97,32]]]}`,
	}
}
