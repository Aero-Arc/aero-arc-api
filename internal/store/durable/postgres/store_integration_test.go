//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
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
			ID: id, Version: 1, AircraftID: id + "-aircraft", PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
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
	candidates, err := observer.FindCandidates(ctx, durable.CandidateQuery{
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
	candidates, err = observer.FindCandidates(ctx, durable.CandidateQuery{
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

func TestConcurrentIntentUpdatesUseOptimisticRevision(t *testing.T) {
	ctx, first, second := integrationStores(t)
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	intent := integrationIntent("concurrent-intent", now)
	if err := first.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	one, err := first.GetOperationalIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	two, err := second.GetOperationalIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	one.Status = domain.IntentStatusSubmitted
	one.UpdatedAt = now.Add(time.Second)
	two.Status = domain.IntentStatusCanceled
	two.UpdatedAt = now.Add(2 * time.Second)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for _, update := range []struct {
		store  *Store
		intent domain.OperationalIntent
	}{{first, one}, {second, two}} {
		go func() {
			ready.Done()
			<-start
			errs <- update.store.UpdateOperationalIntent(ctx, update.intent, update.intent.Revision)
		}()
	}
	ready.Wait()
	close(start)
	var succeeded, conflicted int
	for range 2 {
		err := <-errs
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, durable.ErrVersionConflict):
			conflicted++
		default:
			t.Fatalf("concurrent update error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("successful updates = %d, conflicts = %d", succeeded, conflicted)
	}
	stored, err := first.GetOperationalIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Revision != 1 {
		t.Fatalf("stored revision = %d, want 1", stored.Revision)
	}
}

func TestUpdateOperationalIntentRejectsSupersededVersion(t *testing.T) {
	ctx, store, _ := integrationStores(t)
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	v1 := integrationIntent("superseded-intent", now)
	v1.Status = domain.IntentStatusAccepted
	if err := store.CreateOperationalIntent(ctx, v1); err != nil {
		t.Fatal(err)
	}
	v2 := v1
	v2.Version = 2
	v2.Status = domain.IntentStatusDraft
	if err := store.ReplaceOperationalIntent(ctx, v1.Version, v1.Revision, v2, nil); err != nil {
		t.Fatal(err)
	}

	stale := v1
	stale.Status = domain.IntentStatusActive
	stale.UpdatedAt = now.Add(time.Minute)
	if err := store.UpdateOperationalIntent(ctx, stale, v1.Revision); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("superseded update error = %v, want ErrVersionConflict", err)
	}
	stored, err := store.GetOperationalIntentVersion(ctx, v1.ID, v1.Version)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.IntentStatusAccepted || stored.Revision != 0 {
		t.Fatalf("superseded version changed: %#v", stored)
	}
}

func TestConcurrentFindingReplacementsDoNotMerge(t *testing.T) {
	ctx, first, second := integrationStores(t)
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	intent := integrationIntent("finding-intent", now)
	if err := first.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	sets := [][]domain.ConflictFinding{
		{{ID: "first-a", IntentID: intent.ID, IntentVersion: 1, RuleVersion: "rule", EvaluatedAt: now}, {ID: "first-b", IntentID: intent.ID, IntentVersion: 1, RuleVersion: "rule", EvaluatedAt: now}},
		{{ID: "second-a", IntentID: intent.ID, IntentVersion: 1, RuleVersion: "rule", EvaluatedAt: now}, {ID: "second-b", IntentID: intent.ID, IntentVersion: 1, RuleVersion: "rule", EvaluatedAt: now}},
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	for index, store := range []*Store{first, second} {
		index, store := index, store
		go func() {
			<-start
			errs <- store.ReplaceConflictFindings(ctx, intent.ID, 1, "rule", sets[index])
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	findings, err := first.ListConflictFindings(ctx, intent.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %#v, want exactly one two-finding replacement set", findings)
	}
	if findings[0].ID[:5] != findings[1].ID[:5] {
		t.Fatalf("replacement sets merged: %#v", findings)
	}
}

func TestPostgresIntegrityAndReplacementScope(t *testing.T) {
	ctx, store, _ := integrationStores(t)
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	intent := integrationIntent("integrity-intent", now)
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOperationalIntent(ctx, intent); !errors.Is(err, durable.ErrAlreadyExists) {
		t.Fatalf("duplicate create error = %v, want ErrAlreadyExists", err)
	}
	original := integrationVolume("original", intent.ID, 1, now, "")
	if err := store.RecordOperationalVolume(ctx, original); err != nil {
		t.Fatal(err)
	}
	wrong := original
	wrong.ID = "wrong"
	wrong.IntentID = "another-intent"
	if err := store.ReplaceOperationalVolumes(ctx, intent.ID, 1, []domain.OperationalVolume{wrong}); err == nil {
		t.Fatal("expected volume replacement scope error")
	}
	volumes, err := store.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 1 || volumes[0].ID != original.ID {
		t.Fatalf("volumes after rejected replacement = %#v", volumes)
	}
	orphan := domain.ConflictFinding{ID: "orphan", IntentID: "missing", IntentVersion: 1, RuleVersion: "rule", EvaluatedAt: now}
	if err := store.RecordConflictFinding(ctx, orphan); err == nil {
		t.Fatal("expected foreign key error for orphan finding")
	}
}

func integrationStores(t *testing.T) (context.Context, *Store, *Store) {
	t.Helper()
	databaseURL := os.Getenv("AERO_API_TEST_POSTGIS_URL")
	if databaseURL == "" {
		t.Skip("AERO_API_TEST_POSTGIS_URL is not set")
	}
	ctx := context.Background()
	first, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(first.Close)
	second, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	if _, err := first.pool.Exec(ctx, `TRUNCATE conflict_findings, operational_volumes, operational_intents`); err != nil {
		t.Fatal(err)
	}
	return ctx, first, second
}

func integrationIntent(id string, now time.Time) domain.OperationalIntent {
	return domain.OperationalIntent{
		ID: id, Version: 1, AircraftID: id + "-aircraft", Status: domain.IntentStatusDraft,
		PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}
}

func integrationVolume(id, intentID string, version int, now time.Time, geoJSON string) domain.OperationalVolume {
	return domain.OperationalVolume{
		ID: id, IntentID: intentID, IntentVersion: version,
		MinAltitudeM: 20, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: now, EndsAt: now.Add(time.Hour), GeoJSON: geoJSON,
	}
}
