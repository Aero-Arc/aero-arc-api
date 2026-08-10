package interussprovider

import (
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func TestPublishedOperationalIntentUsesStoredReferenceAndVolumes(t *testing.T) {
	start := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)
	reference := []byte(`{
		"id":"11111111-1111-4111-8111-111111111111",
		"manager":"aero-arc","ovn":"owned-ovn","state":"Accepted",
		"subscription_id":"22222222-2222-4222-8222-222222222222",
		"uss_availability":"Normal","uss_base_url":"https://uss.example","version":1,
		"time_start":{"format":"RFC3339","value":"2026-08-10T18:00:00Z"},
		"time_end":{"format":"RFC3339","value":"2026-08-10T19:00:00Z"}
	}`)
	intent, err := PublishedOperationalIntent(reference, []domain.OperationalVolume{{
		ID: "volume", GeoJSON: `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
		MinAltitudeM: 20, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceWGS84,
		StartsAt: start, EndsAt: start.Add(time.Hour), VolumeType: domain.OperationalVolumeRoute,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if intent.Reference.Manager != "aero-arc" || intent.Reference.Ovn == nil || intent.Details.Volumes == nil || len(*intent.Details.Volumes) != 1 {
		t.Fatalf("published intent = %#v", intent)
	}
}
