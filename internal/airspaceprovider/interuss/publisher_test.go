package interussprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func TestPublisherWriteAndPeerNotificationFlow(t *testing.T) {
	const intentID = "11111111-1111-4111-8111-111111111111"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		response.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/uss/v1/operational_intents" {
			if request.Method != http.MethodPost {
				t.Errorf("peer notification method = %s", request.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode peer notification: %v", err)
			}
			if body["operational_intent_id"] != intentID {
				t.Errorf("operational_intent_id = %#v", body["operational_intent_id"])
			}
			response.WriteHeader(http.StatusNoContent)
			return
		}
		if !strings.HasPrefix(request.URL.Path, "/dss/v1/operational_intent_references/"+intentID) {
			http.NotFound(response, request)
			return
		}
		state, version, status := "Accepted", 1, http.StatusCreated
		switch request.Method {
		case http.MethodGet:
			state, version, status = "Activated", 2, http.StatusOK
		case http.MethodDelete:
			state, version, status = "Activated", 3, http.StatusOK
		case http.MethodPut:
			var body struct {
				State string `json:"state"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode DSS request: %v", err)
			}
			state = body.State
			if strings.Count(request.URL.Path, "/") > 4 {
				version, status = 2, http.StatusOK
			}
		default:
			t.Errorf("DSS method = %s", request.Method)
		}
		response.WriteHeader(status)
		if request.Method == http.MethodGet {
			_, _ = fmt.Fprintf(response, `{"operational_intent_reference":%s}`, testReferenceJSON(intentID, state, version, server.URL))
			return
		}
		_, _ = fmt.Fprintf(response, `{"operational_intent_reference":%s,"subscribers":[{"uss_base_url":%q,"subscriptions":[{"subscription_id":"22222222-2222-4222-8222-222222222222","notification_index":1}]}]}`,
			testReferenceJSON(intentID, state, version, server.URL), server.URL)
	}))
	defer server.Close()

	provider, err := New(Config{
		BaseURL: server.URL, StaticToken: "test-token", USSBaseURL: server.URL,
		AllowInsecurePeerURLs: true, RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	request := airspaceprovider.PublicationRequest{
		Intent: domain.OperationalIntent{ID: intentID, Version: 1, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour)},
		State:  domain.OperationalIntentExternalStateAccepted,
		Volumes: []domain.OperationalVolume{{
			ID: "volume", GeoJSON: `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
			MinAltitudeM: 20, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceWGS84,
			StartsAt: now, EndsAt: now.Add(time.Hour), VolumeType: domain.OperationalVolumeRoute,
		}},
	}
	created, err := provider.CreateOperationalIntent(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Version != 1 || created.OVN == "" || len(created.Subscribers) != 1 {
		t.Fatalf("created = %#v", created)
	}
	request.State = domain.OperationalIntentExternalStateActivated
	request.Key = []string{"peer-ovn"}
	request.OVN = created.OVN
	request.SubscriptionID = created.SubscriptionID
	updated, err := provider.UpdateOperationalIntent(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != 2 || updated.State != domain.OperationalIntentExternalStateActivated {
		t.Fatalf("updated = %#v", updated)
	}
	current, err := provider.GetOperationalIntentReference(context.Background(), intentID)
	if err != nil || current.Version != 2 {
		t.Fatalf("current = %#v, %v", current, err)
	}
	payload, err := provider.BuildPeerNotification(request, updated, updated.Subscribers[0], false)
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.DeliverPeerNotification(context.Background(), server.URL, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.BuildPeerNotification(request, updated, updated.Subscribers[0], true); err != nil {
		t.Fatal(err)
	}
	deleted, err := provider.DeleteOperationalIntent(context.Background(), intentID, updated.OVN)
	if err != nil || deleted.Version != 3 {
		t.Fatalf("deleted = %#v, %v", deleted, err)
	}

	request.SubscriptionID = ""
	parameters, err := provider.publicationParameters(request)
	if err != nil || parameters.NewSubscription == nil {
		t.Fatalf("implicit subscription parameters = %#v, %v", parameters, err)
	}
}

func TestPublisherRejectsInvalidRequests(t *testing.T) {
	provider := &Provider{}
	request := airspaceprovider.PublicationRequest{Intent: domain.OperationalIntent{ID: "not-a-uuid"}}
	if _, err := provider.CreateOperationalIntent(context.Background(), request); err == nil {
		t.Fatal("CreateOperationalIntent accepted an unconfigured provider")
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	configured, err := New(Config{
		BaseURL: server.URL, StaticToken: "test-token", USSBaseURL: "https://uss.example", RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configured.CreateOperationalIntent(context.Background(), request); err == nil {
		t.Fatal("CreateOperationalIntent accepted an invalid intent UUID")
	}
	if _, err := configured.UpdateOperationalIntent(context.Background(), request); err == nil {
		t.Fatal("UpdateOperationalIntent accepted an invalid intent UUID")
	}
	if _, err := configured.DeleteOperationalIntent(context.Background(), request.Intent.ID, "ovn"); err == nil {
		t.Fatal("DeleteOperationalIntent accepted an invalid intent UUID")
	}
	if _, err := configured.GetOperationalIntentReference(context.Background(), request.Intent.ID); err == nil {
		t.Fatal("GetOperationalIntentReference accepted an invalid intent UUID")
	}
}

func testReferenceJSON(intentID, state string, version int, baseURL string) string {
	return fmt.Sprintf(`{"id":%q,"manager":"aero-arc","ovn":%q,"state":%q,"subscription_id":"33333333-3333-4333-8333-333333333333","uss_availability":"Normal","uss_base_url":%q,"version":%d,"time_start":{"format":"RFC3339","value":"2026-08-10T18:00:00Z"},"time_end":{"format":"RFC3339","value":"2026-08-10T19:00:00Z"}}`,
		intentID, fmt.Sprintf("ovn-%d", version), state, baseURL, version)
}
