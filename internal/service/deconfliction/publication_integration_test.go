//go:build integration

package deconfliction

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	interussprovider "github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider/interuss"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestDSSPublicationVerticalSlice(t *testing.T) {
	baseURL := os.Getenv("AERO_API_TEST_DSS_BASE_URL")
	tokenURL := os.Getenv("AERO_API_TEST_DSS_OAUTH_TOKEN_URL")
	if baseURL == "" || tokenURL == "" {
		t.Skip("DSS integration environment is not configured")
	}
	peerNotifications := 0
	ussServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == "/uss/v1/operational_intents" {
			peerNotifications++
			response.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(response, request)
	}))
	defer ussServer.Close()
	provider, err := interussprovider.New(interussprovider.Config{
		BaseURL: baseURL, OAuthTokenURL: tokenURL, OAuthAudience: "localhost",
		OAuthIssuer: "localhost", OAuthSubject: "aero_arc_vertical_integration",
		USSBaseURL: ussServer.URL, AllowInsecurePeerURLs: true,
		RequestTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := durablememory.NewStore()
	now := time.Now().UTC()
	deconflictionService, err := NewDeconflictionServiceWithClock(store, func() time.Time { return now }, provider)
	if err != nil {
		t.Fatal(err)
	}
	intents := service.NewIntentServiceWithClock(store, func() time.Time { return now }, deconflictionService)
	intent, err := intents.CreateIntent(context.Background(), service.CreateIntentRequest{
		ID: uuid.NewString(), AircraftID: "aircraft", Name: "integration", Summary: "DSS write",
		PlannedStartAt: now.Add(2 * time.Minute), PlannedEndAt: now.Add(12 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	minimum, maximum := 20.0, 100.0
	longitude := -120.0 + float64(now.UnixNano()%4_000_000)/100_000
	geoJSON := fmt.Sprintf(`{"type":"Polygon","coordinates":[[[%[1]f,32.7],[%[2]f,32.7],[%[2]f,32.71],[%[1]f,32.71],[%[1]f,32.7]]]}`, longitude, longitude+0.01)
	_, err = intents.AddOperationalVolume(context.Background(), intent.ID, service.AddOperationalVolumeRequest{
		ID: "volume", GeoJSON: geoJSON,
		MinAltitudeM: &minimum, MaxAltitudeM: &maximum, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: now.Add(2 * time.Minute), EndsAt: now.Add(12 * time.Minute), VolumeType: domain.OperationalVolumeRoute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = intents.SubmitIntent(context.Background(), intent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = intents.AcceptIntent(context.Background(), intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := deconflictionService.ReconcileDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	publication, err := deconflictionService.GetPublication(context.Background(), intent.ID)
	if err != nil || publication.SyncStatus != domain.PublicationSyncConfirmed || publication.OVN == "" {
		t.Fatalf("publication = %#v, %v", publication, err)
	}
	cleanupOVN := publication.OVN
	t.Cleanup(func() {
		if cleanupOVN != "" {
			_, _ = provider.DeleteOperationalIntent(context.Background(), intent.ID, cleanupOVN)
		}
	})
	if err := store.RecordPreflightCheck(context.Background(), domain.PreflightCheck{
		ID: "preflight", IntentID: intent.ID, IntentVersion: intent.Version,
		AircraftID: intent.AircraftID, Status: domain.PreflightStatusClear, CapturedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	intent, err = intents.ActivateIntent(context.Background(), intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	publication, err = deconflictionService.GetPublication(context.Background(), intent.ID)
	if err != nil || intent.Status != domain.IntentStatusActive || publication.ConfirmedState != domain.OperationalIntentExternalStateActivated {
		t.Fatalf("activated publication = %#v notifications=%d err=%v", publication, peerNotifications, err)
	}
	cleanupOVN = publication.OVN
	if _, err := intents.CancelIntent(context.Background(), intent.ID); err != nil {
		t.Fatal(err)
	}
	if err := deconflictionService.ReconcileDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	publication, err = deconflictionService.GetPublication(context.Background(), intent.ID)
	if err != nil || publication.SyncStatus != domain.PublicationSyncWithdrawn {
		t.Fatalf("withdrawn publication = %#v, %v", publication, err)
	}
	cleanupOVN = ""
}
