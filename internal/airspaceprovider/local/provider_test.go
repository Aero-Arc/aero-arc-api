package localprovider

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestProviderHydratesAuthoritativeCandidate(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	store := durablememory.NewStore()
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

	provider := New(store)
	records, err := provider.FindOperationalIntents(ctx, airspaceprovider.Query{Intent: target, Volumes: []domain.OperationalVolume{targetVolume}})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Intent.Version != 1 || records[0].Source.Manager != "operator" {
		t.Fatalf("records = %#v", records)
	}
}

func TestProviderIgnoresAcceptedVersionAfterReplacementIsAccepted(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	store := &candidateStore{
		Store:      durablememory.NewStore(),
		candidates: []durable.Candidate{{IntentID: "peer", IntentVersion: 1}},
	}
	target := domain.OperationalIntent{ID: "target", Version: 1, Status: domain.IntentStatusDraft}
	v1 := domain.OperationalIntent{ID: "peer", Version: 1, Status: domain.IntentStatusAccepted}
	v2 := v1
	v2.Version = 2
	v2.Status = domain.IntentStatusSubmitted
	for _, intent := range []domain.OperationalIntent{target, v1, v2} {
		if err := store.CreateOperationalIntent(ctx, intent); err != nil {
			t.Fatal(err)
		}
	}
	targetVolume := testVolume("target-volume", target.ID, now)
	v1Volume := testVolume("peer-v1-volume", v1.ID, now)
	v2Volume := testVolume("peer-v2-volume", v2.ID, now)
	v2Volume.IntentVersion = 2
	v2Volume.GeoJSON = `{"type":"Polygon","coordinates":[[[-80,20],[-79,20],[-79,21],[-80,21],[-80,20]]]}`
	for _, volume := range []domain.OperationalVolume{targetVolume, v1Volume, v2Volume} {
		if err := store.RecordOperationalVolume(ctx, volume); err != nil {
			t.Fatal(err)
		}
	}

	provider := New(store)
	query := airspaceprovider.Query{Intent: target, Volumes: []domain.OperationalVolume{targetVolume}}
	records, err := provider.FindOperationalIntents(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Intent.Version != 1 {
		t.Fatalf("records while replacement is pending = %#v, want accepted v1", records)
	}

	acceptedAt := now.Add(time.Minute)
	v2.Status = domain.IntentStatusAccepted
	v2.AcceptedAt = &acceptedAt
	v2.UpdatedAt = acceptedAt
	if err := store.AcceptOperationalIntent(ctx, v2, v2.Revision); err != nil {
		t.Fatal(err)
	}
	records, err = provider.FindOperationalIntents(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Fatalf("records after moved replacement is accepted = %#v, want none", records)
	}
}

func TestProviderReturnsCandidateQueryError(t *testing.T) {
	ctx := context.Background()
	provider := New(&failingStore{Store: durablememory.NewStore()})
	if _, err := provider.FindOperationalIntents(ctx, airspaceprovider.Query{}); err == nil {
		t.Fatal("expected candidate finder error")
	}
}

type failingStore struct {
	*durablememory.Store
}

func (*failingStore) FindCandidates(context.Context, durable.CandidateQuery) ([]durable.Candidate, error) {
	return nil, context.Canceled
}

type candidateStore struct {
	*durablememory.Store
	candidates []durable.Candidate
}

func (s *candidateStore) FindCandidates(context.Context, durable.CandidateQuery) ([]durable.Candidate, error) {
	return s.candidates, nil
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
