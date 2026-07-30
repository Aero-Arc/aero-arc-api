package interussprovider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dss "github.com/Aero-Arc/dss-clients/interuss"
	"github.com/Aero-Arc/dss-clients/interuss/gen/scdv1"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type fakeClient struct {
	references []scdv1.OperationalIntentReference
	intents    map[string]*scdv1.OperationalIntent
	failures   map[string]error
	queries    int
}

func (c *fakeClient) QueryOperationalIntentReferences(context.Context, scdv1.Volume4D) ([]scdv1.OperationalIntentReference, error) {
	c.queries++
	return c.references, nil
}

func (c *fakeClient) GetOperationalIntent(_ context.Context, reference scdv1.OperationalIntentReference) (*scdv1.OperationalIntent, error) {
	key, err := referenceKey(reference)
	if err != nil {
		return nil, err
	}
	if failure := c.failures[key]; failure != nil {
		return nil, failure
	}
	return c.intents[key], nil
}

func TestProviderQueriesEachTargetAndDeduplicatesReferences(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	reference := testReference(t, "11111111-1111-4111-8111-111111111111", 3)
	client := &fakeClient{
		references: []scdv1.OperationalIntentReference{reference},
		intents: map[string]*scdv1.OperationalIntent{
			mustReferenceKey(t, reference): testSCDIntent(t, reference, testVolume(now)),
		},
	}
	target := testVolume(now)
	target.ID = "target-one"
	second := target
	second.ID = "target-two"

	records, err := New(client).FindOperationalIntents(context.Background(), airspaceprovider.Query{
		Intent:  domain.OperationalIntent{ID: "target"},
		Volumes: []domain.OperationalVolume{target, second},
	})
	if err != nil {
		t.Fatalf("FindOperationalIntents returned error: %v", err)
	}
	if client.queries != 2 {
		t.Fatalf("DSS queries = %d, want 2", client.queries)
	}
	if len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
	record := records[0]
	if record.Source.ReferenceID != "11111111-1111-4111-8111-111111111111" ||
		record.Source.Manager != "peer-uss" ||
		record.Source.USSBaseURL != "https://peer.example" ||
		record.Source.Version != 3 ||
		len(record.Volumes) != 1 {
		t.Fatalf("record = %#v", record)
	}
}

func TestProviderReturnsSuccessfulPeersWithPartialFailure(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	good := testReference(t, "11111111-1111-4111-8111-111111111111", 1)
	bad := testReference(t, "22222222-2222-4222-8222-222222222222", 1)
	client := &fakeClient{
		references: []scdv1.OperationalIntentReference{good, bad},
		intents: map[string]*scdv1.OperationalIntent{
			mustReferenceKey(t, good): testSCDIntent(t, good, testVolume(now)),
		},
		failures: map[string]error{
			mustReferenceKey(t, bad): errors.New("peer unavailable"),
		},
	}

	records, err := New(client).FindOperationalIntents(context.Background(), airspaceprovider.Query{
		Intent:  domain.OperationalIntent{ID: "target"},
		Volumes: []domain.OperationalVolume{testVolume(now)},
	})
	if err == nil || !strings.Contains(err.Error(), "peer unavailable") {
		t.Fatalf("error = %v", err)
	}
	if len(records) != 1 || records[0].Source.ReferenceID != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("records = %#v", records)
	}
}

func TestProviderRejectsUnsupportedTargetAltitudeReference(t *testing.T) {
	volume := testVolume(time.Now().UTC())
	volume.AltitudeRef = domain.AltitudeReferenceAGL
	client := &fakeClient{}

	records, err := New(client).FindOperationalIntents(context.Background(), airspaceprovider.Query{
		Intent: domain.OperationalIntent{ID: "target"}, Volumes: []domain.OperationalVolume{volume},
	})
	if err == nil || !strings.Contains(err.Error(), "not supported by SCD") {
		t.Fatalf("error = %v", err)
	}
	if len(records) != 0 || client.queries != 0 {
		t.Fatalf("records = %#v; queries = %d", records, client.queries)
	}
}

func TestProviderRejectsMismatchedPeerReference(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	reference := testReference(t, "11111111-1111-4111-8111-111111111111", 3)
	details := testSCDIntent(t, reference, testVolume(now))
	details.Reference.Version++
	client := &fakeClient{
		references: []scdv1.OperationalIntentReference{reference},
		intents: map[string]*scdv1.OperationalIntent{
			mustReferenceKey(t, reference): details,
		},
	}

	records, err := New(client).FindOperationalIntents(context.Background(), airspaceprovider.Query{
		Intent:  domain.OperationalIntent{ID: "target"},
		Volumes: []domain.OperationalVolume{testVolume(now)},
	})
	if err == nil || !strings.Contains(err.Error(), "mismatched reference") || len(records) != 0 {
		t.Fatalf("records = %#v, error = %v", records, err)
	}
}

func TestProviderRejectsMissingStateRequiredVolumes(t *testing.T) {
	reference := testReference(t, "11111111-1111-4111-8111-111111111111", 3)
	client := &fakeClient{
		references: []scdv1.OperationalIntentReference{reference},
		intents: map[string]*scdv1.OperationalIntent{
			mustReferenceKey(t, reference): {Reference: reference},
		},
	}

	records, err := New(client).FindOperationalIntents(context.Background(), airspaceprovider.Query{
		Intent:  domain.OperationalIntent{ID: "target"},
		Volumes: []domain.OperationalVolume{testVolume(time.Now().UTC())},
	})
	if err == nil || !strings.Contains(err.Error(), "requires nominal volumes") || len(records) != 0 {
		t.Fatalf("records = %#v, error = %v", records, err)
	}
}

func TestPeerURLPolicy(t *testing.T) {
	for _, raw := range []string{
		"http://peer.example",
		"https://localhost:8080",
		"https://127.0.0.1",
		"https://169.254.169.254/latest",
		"https://user:password@peer.example",
	} {
		if err := validatePeerURL(raw, false); err == nil {
			t.Errorf("validatePeerURL(%q) unexpectedly succeeded", raw)
		}
	}
	if err := validatePeerURL("https://peer.example/uss", false); err != nil {
		t.Fatalf("public HTTPS peer rejected: %v", err)
	}
	if err := validatePeerURL("http://localhost:8080", true); err != nil {
		t.Fatalf("explicit local development peer rejected: %v", err)
	}
}

func testReference(t *testing.T, id string, version int32) scdv1.OperationalIntentReference {
	t.Helper()
	entityID, err := dss.SCDEntityID(id)
	if err != nil {
		t.Fatal(err)
	}
	baseURL := scdv1.OperationalIntentUssBaseURL{}
	if err := baseURL.FromUssBaseURL("https://peer.example"); err != nil {
		t.Fatal(err)
	}
	return scdv1.OperationalIntentReference{
		Id:         entityID,
		Manager:    "peer-uss",
		State:      scdv1.Accepted,
		UssBaseUrl: baseURL,
		Version:    version,
	}
}

func testSCDIntent(t *testing.T, reference scdv1.OperationalIntentReference, volume domain.OperationalVolume) *scdv1.OperationalIntent {
	t.Helper()
	scdVolume, err := toSCDVolume(volume)
	if err != nil {
		t.Fatal(err)
	}
	return &scdv1.OperationalIntent{
		Reference: reference,
		Details: scdv1.OperationalIntentDetails{
			Volumes: &[]scdv1.Volume4D{scdVolume},
		},
	}
}

func testVolume(now time.Time) domain.OperationalVolume {
	return domain.OperationalVolume{
		ID:            "target-volume",
		IntentID:      "target",
		IntentVersion: 1,
		MinAltitudeM:  20,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceWGS84,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		GeoJSON:       `{"type":"Polygon","coordinates":[[[-97,32],[-96,32],[-96,33],[-97,33],[-97,32]]]}`,
	}
}

func mustReferenceKey(t *testing.T, reference scdv1.OperationalIntentReference) string {
	t.Helper()
	key, err := referenceKey(reference)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
