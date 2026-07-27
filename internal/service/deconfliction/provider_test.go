package deconfliction_test

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

type assessmentProvider struct {
	id       string
	findings []domain.ConflictFinding
	calls    int
}

func (p *assessmentProvider) CheckIntent(context.Context, domain.OperationalIntent, []domain.OperationalVolume) (airspaceprovider.Assessment, error) {
	p.calls++
	return airspaceprovider.Assessment{ProviderID: p.id, Findings: p.findings}, nil
}

func TestServiceTrustsAggregatesAndPersistsProviderFindings(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	intent := domain.OperationalIntent{ID: "target", Version: 1, OperatorID: "operator", AircraftID: "aircraft"}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID: "target-volume", IntentID: intent.ID, IntentVersion: intent.Version,
		MinAltitudeM: 10, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: now, EndsAt: now.Add(time.Hour),
		GeoJSON: `{"type":"Polygon","coordinates":[[[-97,32],[-96,32],[-96,33],[-97,33],[-97,32]]]}`,
	}); err != nil {
		t.Fatal(err)
	}
	first := &assessmentProvider{id: "dss-one", findings: []domain.ConflictFinding{{
		Status: domain.ConflictFindingStatusConflict, Message: "provider-defined conflict",
		ConflictingIntentID: "remote-flight",
	}}}
	second := &assessmentProvider{id: "dss-two"}

	result, err := deconfliction.NewDeconflictionServiceWithClock(
		store, func() time.Time { return now }, first, second,
	).CheckIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("provider calls = %d, %d; want one each", first.calls, second.calls)
	}
	if result.Posture != domain.DeconflictionPostureConflict || len(result.Findings) != 1 {
		t.Fatalf("result = %#v", result)
	}
	finding := result.Findings[0]
	if finding.SourceID != "dss-one" || finding.SourceType != domain.ConflictFindingSourceExternal || !finding.Blocking {
		t.Fatalf("normalized finding = %#v", finding)
	}
	stored, err := store.ListConflictFindings(ctx, intent.ID, intent.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].ConflictingIntentID != "remote-flight" {
		t.Fatalf("stored findings = %#v", stored)
	}
}
