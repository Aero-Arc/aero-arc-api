//go:build integration

package postgis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestOperationalIntentReadCheckSlice(t *testing.T) {
	databaseURL := os.Getenv("AERO_API_TEST_POSTGIS_URL")
	if databaseURL == "" {
		t.Skip("AERO_API_TEST_POSTGIS_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.pool.Exec(ctx, `
		TRUNCATE conflict_findings, operational_volumes, operational_intents CASCADE`); err != nil {
		t.Fatalf("reset integration database: %v", err)
	}

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	now := time.Now().UTC().Add(time.Minute)
	target := domain.OperationalIntent{
		ID: "target-" + suffix, Version: 1, Status: domain.IntentStatusDraft,
		PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}
	overlap := domain.OperationalIntent{
		ID: "overlap-" + suffix, Version: 1, Status: domain.IntentStatusAccepted,
		PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}
	distant := domain.OperationalIntent{
		ID: "distant-" + suffix, Version: 1, Status: domain.IntentStatusAccepted,
		PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}
	for _, intent := range []domain.OperationalIntent{target, overlap, distant} {
		if err := store.CreateOperationalIntent(ctx, intent); err != nil {
			t.Fatal(err)
		}
	}
	targetVolume := integrationVolume("target-volume", target.ID, now,
		`{"type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[-97,32],[-96,32],[-96,33],[-97,33],[-97,32]]]}}`)
	overlapVolume := integrationVolume("overlap-volume", overlap.ID, now,
		`{"type":"Polygon","coordinates":[[[-96.5,32.5],[-95.5,32.5],[-95.5,33.5],[-96.5,33.5],[-96.5,32.5]]]}`)
	distantVolume := integrationVolume("distant-volume", distant.ID, now,
		`{"type":"Polygon","coordinates":[[[-80,20],[-79,20],[-79,21],[-80,21],[-80,20]]]}`)
	for _, volume := range []domain.OperationalVolume{targetVolume, overlapVolume, distantVolume} {
		if err := store.RecordOperationalVolume(ctx, volume); err != nil {
			t.Fatal(err)
		}
	}
	invalidVolume := integrationVolume("invalid-volume", target.ID, now, targetVolume.GeoJSON)
	invalidVolume.EndsAt = invalidVolume.StartsAt
	if err := store.RecordOperationalVolume(ctx, invalidVolume); err == nil {
		t.Fatal("expected PostGIS to reject an invalid time window")
	}

	records, err := store.FindOperationalIntents(ctx, airspaceprovider.Query{
		Intent: target, Volumes: []domain.OperationalVolume{targetVolume},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Intent.ID != overlap.ID ||
		!records[0].Source.Local || len(records[0].Volumes) != 1 {
		t.Fatalf("records = %#v", records)
	}

	combined := durable.UseOperationalStore(durablememory.NewStore(), store)
	modified, err := service.NewIntentService(combined).ModifyIntent(ctx, overlap.ID, service.ModifyIntentRequest{
		ExpectedVersion: overlap.Version,
		Intent:          service.ModifyIntentFields{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if modified.Intent.Version != 2 || modified.Intent.Status != domain.IntentStatusDraft {
		t.Fatalf("modified intent = %#v", modified.Intent)
	}
	failedIntent := modified.Intent
	failedIntent.Version = 3
	failedVolume := integrationVolume("failed-volume", failedIntent.ID, now, overlapVolume.GeoJSON)
	failedVolume.IntentVersion = failedIntent.Version
	failedVolume.EndsAt = failedVolume.StartsAt
	if err := store.ReplaceOperationalIntent(ctx, 2, failedIntent, []domain.OperationalVolume{failedVolume}); err == nil {
		t.Fatal("expected atomic replacement to reject invalid volume")
	}
	latest, err := store.GetOperationalIntent(ctx, failedIntent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != 2 {
		t.Fatalf("failed replacement persisted intent v%d", latest.Version)
	}
	if err := store.ReplaceOperationalIntent(ctx, 1, failedIntent, nil); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("version conflict error = %v", err)
	}
	records, err = store.FindOperationalIntents(ctx, airspaceprovider.Query{
		Intent: target, Volumes: []domain.OperationalVolume{targetVolume},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Intent.ID != overlap.ID || records[0].Intent.Version != 1 {
		t.Fatalf("accepted v1 was hidden by draft v2: %#v", records)
	}

	result, err := deconfliction.NewDeconflictionService(combined, store).CheckIntent(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Posture != domain.DeconflictionPosturePotentialConflict ||
		len(result.Findings) != 1 ||
		result.Findings[0].ConflictingIntentID != overlap.ID {
		t.Fatalf("deconfliction result = %#v", result)
	}
	stored, err := store.ListConflictFindings(ctx, target.ID, target.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].ID != result.Findings[0].ID {
		t.Fatalf("stored findings = %#v", stored)
	}
}

func integrationVolume(id, intentID string, now time.Time, geoJSON string) domain.OperationalVolume {
	return domain.OperationalVolume{
		ID: id, IntentID: intentID, IntentVersion: 1,
		MinAltitudeM: 20, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: now, EndsAt: now.Add(time.Hour), GeoJSON: geoJSON,
		CreatedAt: now, UpdatedAt: now,
	}
}
