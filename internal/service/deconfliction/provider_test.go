package deconfliction_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

type discoveryProvider struct {
	id      string
	records []airspaceprovider.OperationalIntent
	err     error
	calls   int
}

func TestServiceRequiresProvider(t *testing.T) {
	if _, err := deconfliction.NewDeconflictionService(durablememory.NewStore()); err == nil {
		t.Fatal("NewDeconflictionService did not reject an empty provider list")
	}
}

func newProviderService(
	t *testing.T,
	store durable.Store,
	now func() time.Time,
	providers ...airspaceprovider.Provider,
) *deconfliction.DeconflictionService {
	t.Helper()
	service, err := deconfliction.NewDeconflictionServiceWithClock(store, now, providers...)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func (p *discoveryProvider) ID() string {
	return p.id
}

func (p *discoveryProvider) FindOperationalIntents(context.Context, airspaceprovider.Query) ([]airspaceprovider.OperationalIntent, error) {
	p.calls++
	return p.records, p.err
}

func TestServiceDiscoversEvaluatesAndPersistsProviderIntents(t *testing.T) {
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
	first := &discoveryProvider{id: "dss-one", records: []airspaceprovider.OperationalIntent{{
		Source: airspaceprovider.Source{ReferenceID: "remote-ref", Version: 2, Manager: "uss-one"},
		Intent: domain.OperationalIntent{ID: "remote-flight", Version: 2},
		Volumes: []domain.OperationalVolume{{
			ID: "remote-volume", IntentID: "remote-flight", IntentVersion: 2,
			MinAltitudeM: 10, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceWGS84,
			StartsAt: now, EndsAt: now.Add(time.Hour),
			GeoJSON: `{"type":"Polygon","coordinates":[[[-97,32],[-96,32],[-96,33],[-97,33],[-97,32]]]}`,
		}},
	}}}
	second := &discoveryProvider{id: "dss-two"}

	service := newProviderService(t,
		store, func() time.Time { return now }, first, second,
	)
	result, err := service.CheckIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("provider calls = %d, %d; want one each", first.calls, second.calls)
	}
	if result.Posture != domain.DeconflictionPosturePotentialConflict || len(result.Findings) != 1 {
		t.Fatalf("result = %#v", result)
	}
	finding := result.Findings[0]
	if finding.SourceID != "dss-one:remote-ref" || finding.SourceType != domain.ConflictFindingSourceExternal || !finding.Blocking {
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

func TestProviderFailureProducesIndeterminateFinding(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	intent := domain.OperationalIntent{ID: "target", Version: 1}
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
	provider := &discoveryProvider{id: "unavailable-dss", err: errors.New("network unavailable")}

	service := newProviderService(t,
		store, func() time.Time { return now }, provider,
	)
	result, err := service.CheckIntent(ctx, intent.ID)
	if err != nil {
		t.Fatalf("CheckIntent returned error: %v", err)
	}
	if result.Posture != domain.DeconflictionPostureIndeterminate ||
		len(result.Findings) != 1 ||
		result.Findings[0].SourceID != provider.id ||
		!result.Findings[0].Blocking {
		t.Fatalf("result = %#v", result)
	}
}

func TestProviderPartialFailureStillEvaluatesReturnedCandidates(t *testing.T) {
	ctx := context.Background()
	store := durablememory.NewStore()
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	intent := domain.OperationalIntent{ID: "target", Version: 1}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	targetVolume := domain.OperationalVolume{
		ID: "target-volume", IntentID: intent.ID, IntentVersion: intent.Version,
		MinAltitudeM: 10, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: now, EndsAt: now.Add(time.Hour),
		GeoJSON: `{"type":"Polygon","coordinates":[[[-97,32],[-96,32],[-96,33],[-97,33],[-97,32]]]}`,
	}
	if err := store.RecordOperationalVolume(ctx, targetVolume); err != nil {
		t.Fatal(err)
	}
	provider := &discoveryProvider{
		id:  "partially-available-dss",
		err: errors.New("one peer unavailable"),
		records: []airspaceprovider.OperationalIntent{{
			Source: airspaceprovider.Source{ReferenceID: "available-peer", Version: 1},
			Intent: domain.OperationalIntent{ID: "available-peer", Version: 1},
			Volumes: []domain.OperationalVolume{{
				ID: "peer-volume", IntentID: "available-peer", IntentVersion: 1,
				MinAltitudeM: 10, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceWGS84,
				StartsAt: now, EndsAt: now.Add(time.Hour), GeoJSON: targetVolume.GeoJSON,
			}},
		}},
	}

	service := newProviderService(t,
		store, func() time.Time { return now }, provider,
	)
	result, err := service.CheckIntent(ctx, intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Posture != domain.DeconflictionPostureIndeterminate || len(result.Findings) != 2 {
		t.Fatalf("result = %#v", result)
	}
	var sawPeer bool
	for _, finding := range result.Findings {
		if finding.ConflictingIntentID == "available-peer" {
			sawPeer = true
		}
	}
	if !sawPeer {
		t.Fatalf("successful peer was not evaluated: %#v", result.Findings)
	}
}
