package preflight

import (
	"context"
	"fmt"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type IntentVolumeChecker struct{}

func (IntentVolumeChecker) Name() string { return "intent_volume" }

func (IntentVolumeChecker) Evaluate(_ context.Context, snapshot Snapshot, builder *Builder) {
	if snapshot.Intent.PlannedStartAt.Before(snapshot.Intent.PlannedEndAt) {
		builder.Clear(domain.PreflightCheckAirspace, "intent_time_window", "intent_service", "INTENT-WINDOW", "intent planned time window is valid")
	} else {
		builder.Block(domain.PreflightCheckAirspace, "intent_time_window", "intent_service", "INTENT-WINDOW", "intent planned start must be before planned end", "set planned_start_at before planned_end_at")
	}

	if len(snapshot.Volumes) == 0 {
		builder.Block(domain.PreflightCheckAirspace, "operational_volume_exists", "intent_service", "VOLUME-EXISTS", "at least one operational volume is required", "add an operational volume")
	} else {
		builder.Clear(domain.PreflightCheckAirspace, "operational_volume_exists", "intent_service", "VOLUME-EXISTS", "operational volume exists")
	}
	for _, volume := range snapshot.Volumes {
		prefix := fmt.Sprintf("volume_%s", volume.ID)
		if volume.StartsAt.Before(volume.EndsAt) {
			builder.Clear(domain.PreflightCheckAirspace, prefix+"_time_window", "intent_service", "VOLUME-WINDOW", "operational volume time window is valid")
		} else {
			builder.Block(domain.PreflightCheckAirspace, prefix+"_time_window", "intent_service", "VOLUME-WINDOW", "operational volume start must be before end", "set volume starts_at before ends_at")
		}
		if volume.MinAltitudeM <= volume.MaxAltitudeM {
			builder.Clear(domain.PreflightCheckAirspace, prefix+"_altitude_range", "intent_service", "VOLUME-ALTITUDE", "operational volume altitude range is valid")
		} else {
			builder.Block(domain.PreflightCheckAirspace, prefix+"_altitude_range", "intent_service", "VOLUME-ALTITUDE", "operational volume minimum altitude exceeds maximum altitude", "set min_altitude_m <= max_altitude_m")
		}
		if !volume.StartsAt.Before(snapshot.Intent.PlannedStartAt) && !volume.EndsAt.After(snapshot.Intent.PlannedEndAt) {
			builder.Clear(domain.PreflightCheckAirspace, prefix+"_inside_intent_window", "intent_service", "VOLUME-IN-INTENT", "operational volume is inside planned intent window")
		} else {
			builder.Block(domain.PreflightCheckAirspace, prefix+"_inside_intent_window", "intent_service", "VOLUME-IN-INTENT", "operational volume must be inside planned intent window", "adjust volume or planned intent time window")
		}
		if volume.GeoJSON != "" {
			builder.Clear(domain.PreflightCheckAirspace, prefix+"_inline_geojson", "intent_service", "VOLUME-GEOJSON", "operational volume has inline GeoJSON evaluable by this server")
		} else {
			builder.Block(domain.PreflightCheckAirspace, prefix+"_inline_geojson", "intent_service", "VOLUME-GEOJSON", "operational volume requires inline GeoJSON for local conformance evaluation", "provide inline GeoJSON; geometry_uri resolution is not implemented")
		}
	}
}
