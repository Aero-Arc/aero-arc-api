package preflight

import (
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func TestIntentVolumeCheckerBlocksInvalidWindowAndMissingVolume(t *testing.T) {
	now := timeNow()
	snapshot := testSnapshot(now)
	snapshot.Intent.PlannedStartAt = now.Add(time.Hour)
	snapshot.Intent.PlannedEndAt = now
	builder := evaluateChecker(t, IntentVolumeChecker{}, snapshot)
	requireCheck(t, builder, "intent_time_window", "INTENT-WINDOW", "intent_service", true)
	requireCheck(t, builder, "operational_volume_exists", "VOLUME-EXISTS", "intent_service", true)
}

func TestIntentVolumeCheckerClearsCurrentVersionVolume(t *testing.T) {
	now := timeNow()
	snapshot := testSnapshot(now)
	snapshot.Intent.PlannedStartAt = now
	snapshot.Intent.PlannedEndAt = now.Add(time.Hour)
	snapshot.Volumes = []domain.OperationalVolume{{
		ID:           "volume-1",
		GeoJSON:      `{"type":"Polygon"}`,
		MinAltitudeM: 10,
		MaxAltitudeM: 120,
		StartsAt:     now,
		EndsAt:       now.Add(time.Hour),
	}}
	builder := evaluateChecker(t, IntentVolumeChecker{}, snapshot)
	requireCheck(t, builder, "intent_time_window", "INTENT-WINDOW", "intent_service", false)
	requireCheck(t, builder, "operational_volume_exists", "VOLUME-EXISTS", "intent_service", false)
	requireCheck(t, builder, "volume_volume-1_time_window", "VOLUME-WINDOW", "intent_service", false)
	requireCheck(t, builder, "volume_volume-1_altitude_range", "VOLUME-ALTITUDE", "intent_service", false)
	requireCheck(t, builder, "volume_volume-1_inside_intent_window", "VOLUME-IN-INTENT", "intent_service", false)
	requireCheck(t, builder, "volume_volume-1_inline_geojson", "VOLUME-GEOJSON", "intent_service", false)
}

func TestIntentVolumeCheckerBlocksURIOnlyGeometry(t *testing.T) {
	now := timeNow()
	snapshot := testSnapshot(now)
	snapshot.Intent.PlannedStartAt = now
	snapshot.Intent.PlannedEndAt = now.Add(time.Hour)
	snapshot.Volumes = []domain.OperationalVolume{{
		ID:           "volume-uri",
		GeometryURI:  "s3://demo/volume.geojson",
		MinAltitudeM: 10,
		MaxAltitudeM: 120,
		StartsAt:     now,
		EndsAt:       now.Add(time.Hour),
	}}
	builder := evaluateChecker(t, IntentVolumeChecker{}, snapshot)
	requireCheck(t, builder, "volume_volume-uri_inline_geojson", "VOLUME-GEOJSON", "intent_service", true)
}
