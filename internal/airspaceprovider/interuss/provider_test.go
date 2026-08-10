package interussprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
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
	gets       []string
}

func TestNewBuildsConfiguredProvider(t *testing.T) {
	provider, err := New(Config{
		BaseURL:     "http://dss.example",
		StaticToken: "test-token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.ID() != "interuss_scd" {
		t.Fatalf("provider ID = %q", provider.ID())
	}
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
	c.gets = append(c.gets, key)
	if failure := c.failures[key]; failure != nil {
		return nil, failure
	}
	return c.intents[key], nil
}

func TestProviderExcludesTargetIntentReference(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	target := testReference(t, "11111111-1111-4111-8111-111111111111", 3)
	peer := testReference(t, "22222222-2222-4222-8222-222222222222", 1)
	client := &fakeClient{
		references: []scdv1.OperationalIntentReference{target, peer},
		intents: map[string]*scdv1.OperationalIntent{
			mustReferenceKey(t, peer): testSCDIntent(t, peer, testVolume(now)),
		},
	}

	records, err := (&Provider{reader: client}).FindOperationalIntents(context.Background(), airspaceprovider.Query{
		Intent:  domain.OperationalIntent{ID: "11111111-1111-4111-8111-111111111111"},
		Volumes: []domain.OperationalVolume{testVolume(now)},
	})
	if err != nil {
		t.Fatalf("FindOperationalIntents returned error: %v", err)
	}
	if len(records) != 1 || records[0].Source.ReferenceID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("records = %#v, want only peer intent", records)
	}
	peerKey := mustReferenceKey(t, peer)
	if len(client.gets) != 1 || client.gets[0] != peerKey {
		t.Fatalf("fetched references = %#v, want only %q", client.gets, peerKey)
	}
}

func TestProviderUsesPeerDetailsOVNForAirspaceKey(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	reference := testReference(t, "11111111-1111-4111-8111-111111111111", 2)
	peerReference := reference
	peerReference.Ovn = &scdv1.OperationalIntentReference_Ovn{}
	if err := peerReference.Ovn.FromEntityOVN("peer-ovn"); err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{
		references: []scdv1.OperationalIntentReference{reference},
		intents: map[string]*scdv1.OperationalIntent{
			mustReferenceKey(t, reference): testSCDIntent(t, peerReference, testVolume(now)),
		},
	}
	records, err := (&Provider{reader: client}).FindOperationalIntents(context.Background(), airspaceprovider.Query{
		Intent:  domain.OperationalIntent{ID: "22222222-2222-4222-8222-222222222222"},
		Volumes: []domain.OperationalVolume{testVolume(now)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Source.OVN != "peer-ovn" {
		t.Fatalf("records = %#v", records)
	}
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

	records, err := (&Provider{reader: client}).FindOperationalIntents(context.Background(), airspaceprovider.Query{
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

	records, err := (&Provider{reader: client}).FindOperationalIntents(context.Background(), airspaceprovider.Query{
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

	records, err := (&Provider{reader: client}).FindOperationalIntents(context.Background(), airspaceprovider.Query{
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

	records, err := (&Provider{reader: client}).FindOperationalIntents(context.Background(), airspaceprovider.Query{
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

	records, err := (&Provider{reader: client}).FindOperationalIntents(context.Background(), airspaceprovider.Query{
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

func TestSCDClientUsesDSSAndPeerEndpoints(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	reference := testReference(t, "11111111-1111-4111-8111-111111111111", 3)
	if err := reference.UssBaseUrl.FromUssBaseURL("http://peer.example"); err != nil {
		t.Fatal(err)
	}
	intent := testSCDIntent(t, reference, testVolume(now))
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/dss/v1/operational_intent_references/query":
			return jsonResponse(t, http.StatusOK, scdv1.QueryOperationalIntentReferenceResponse{
				OperationalIntentReferences: []scdv1.OperationalIntentReference{reference},
			}), nil
		case "/uss/v1/operational_intents/11111111-1111-4111-8111-111111111111":
			if version := request.URL.Query().Get("version"); version != "3" {
				t.Errorf("peer request version = %q", version)
			}
			return jsonResponse(t, http.StatusOK, scdv1.GetOperationalIntentDetailsResponse{
				OperationalIntent: *intent,
			}), nil
		default:
			t.Fatalf("unexpected request path %q", request.URL.Path)
			return nil, nil
		}
	})
	client := testDSSClient(t, transport)
	scd := &scdClient{dssClient: client, peerUSSClient: client, allowInsecurePeerURLs: true}
	area, err := toSCDVolume(testVolume(now))
	if err != nil {
		t.Fatal(err)
	}
	references, err := scd.QueryOperationalIntentReferences(context.Background(), area)
	if err != nil || len(references) != 1 {
		t.Fatalf("references = %#v, error = %v", references, err)
	}
	details, err := scd.GetOperationalIntent(context.Background(), references[0])
	if err != nil || details.Reference.Version != 3 {
		t.Fatalf("details = %#v, error = %v", details, err)
	}
}

func TestSCDClientReportsDSSResponseFailures(t *testing.T) {
	area, err := toSCDVolume(testVolume(time.Now().UTC()))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		response   *http.Response
		wantStatus int
		wantText   string
	}{
		{
			name: "status",
			response: &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Status:     "503 Service Unavailable",
				Body:       io.NopCloser(strings.NewReader("unavailable")),
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "missing JSON",
			response: &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("{}")),
			},
			wantText: "without a JSON body",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := testDSSClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				copy := *test.response
				return &copy, nil
			}))
			scd := &scdClient{dssClient: client, peerUSSClient: client, allowInsecurePeerURLs: true}
			_, err := scd.QueryOperationalIntentReferences(context.Background(), area)
			if err == nil {
				t.Fatal("expected query error")
			}
			if test.wantStatus != 0 {
				var responseErr *dss.SCDResponseError
				if !errors.As(err, &responseErr) || responseErr.StatusCode != test.wantStatus {
					t.Fatalf("error = %v", err)
				}
			}
			if test.wantText != "" && !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPeerHTTPClientEnforcesRedirectAndAddressPolicy(t *testing.T) {
	secure := newPeerHTTPClient(time.Second, false)
	private, err := http.NewRequest(http.MethodGet, "https://127.0.0.1/intent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := secure.CheckRedirect(private, nil); err == nil {
		t.Fatal("private redirect unexpectedly accepted")
	}
	via := make([]*http.Request, 10)
	if err := secure.CheckRedirect(private, via); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("redirect limit error = %v", err)
	}

	insecure := newPeerHTTPClient(time.Second, true)
	local, err := http.NewRequest(http.MethodGet, "http://localhost:8080/intent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := insecure.CheckRedirect(local, nil); err != nil {
		t.Fatalf("local development redirect rejected: %v", err)
	}
	for _, raw := range []string{
		"0.0.0.0",
		"127.0.0.1",
		"100.64.0.1",
		"192.0.2.1",
		"198.18.0.1",
		"198.51.100.1",
		"203.0.113.1",
		"2001:db8::1",
		"3fff::1",
		"5f00::1",
		"4000::1",
		"::ffff:127.0.0.1",
	} {
		if publicPeerIP(net.ParseIP(raw)) {
			t.Errorf("special-use peer address %q unexpectedly accepted", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicPeerIP(net.ParseIP(raw)) {
			t.Errorf("public peer address %q unexpectedly rejected", raw)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testDSSClient(t *testing.T, transport http.RoundTripper) *dss.Client {
	t.Helper()
	client, err := dss.NewClient(dss.Config{
		BaseURL:    "http://dss.example",
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func jsonResponse(t *testing.T, status int, value any) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
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
