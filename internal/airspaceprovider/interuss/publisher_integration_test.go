//go:build integration

package interussprovider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func TestInterUSSPublisherCreateUpdateDelete(t *testing.T) {
	baseURL := os.Getenv("AERO_API_TEST_DSS_BASE_URL")
	tokenURL := os.Getenv("AERO_API_TEST_DSS_OAUTH_TOKEN_URL")
	if baseURL == "" || tokenURL == "" {
		t.Skip("DSS integration environment is not configured")
	}
	provider, err := New(Config{
		BaseURL: baseURL, OAuthTokenURL: tokenURL, OAuthAudience: "localhost",
		OAuthIssuer: "localhost", OAuthSubject: "aero_arc_write_integration",
		USSBaseURL: "http://localhost:18080", AllowInsecurePeerURLs: true,
		RequestTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Minute)
	intent := domain.OperationalIntent{ID: uuid.NewString(), Version: 1, PlannedStartAt: now, PlannedEndAt: now.Add(10 * time.Minute)}
	request := airspaceprovider.PublicationRequest{
		Intent: intent, State: domain.OperationalIntentExternalStateAccepted,
		Volumes: []domain.OperationalVolume{{
			ID: "volume", IntentID: intent.ID, IntentVersion: 1,
			GeoJSON:      `{"type":"Polygon","coordinates":[[[-97.1,32.7],[-97.0,32.7],[-97.0,32.8],[-97.1,32.8],[-97.1,32.7]]]}`,
			MinAltitudeM: 20, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceWGS84,
			StartsAt: now, EndsAt: now.Add(10 * time.Minute), VolumeType: domain.OperationalVolumeRoute,
		}},
	}
	ctx := context.Background()
	created, err := provider.CreateOperationalIntent(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	currentOVN := created.OVN
	t.Cleanup(func() {
		if currentOVN != "" {
			_, _ = provider.DeleteOperationalIntent(context.Background(), intent.ID, currentOVN)
		}
	})
	if created.Version <= 0 || created.OVN == "" || created.Manager == "" {
		t.Fatalf("create receipt = %#v", created)
	}
	request.OVN = created.OVN
	request.SubscriptionID = created.SubscriptionID
	updated, err := provider.UpdateOperationalIntent(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	currentOVN = updated.OVN
	if updated.Version <= created.Version || updated.OVN == created.OVN {
		t.Fatalf("update receipt = %#v after %#v", updated, created)
	}
	if _, err := provider.DeleteOperationalIntent(ctx, intent.ID, updated.OVN); err != nil {
		t.Fatal(err)
	}
	currentOVN = ""
}
