package deconfliction

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func evaluateConflicts(intent domain.OperationalIntent, volumes []domain.OperationalVolume, records []airspaceprovider.OperationalIntent) []domain.ConflictFinding {
	findings := make([]domain.ConflictFinding, 0)
	for _, volume := range volumes {
		ownBounds, status, message := evaluateVolume(volume)
		if status != domain.ConflictFindingStatusClear {
			findings = append(findings, evaluatedFinding(intent, airspaceprovider.Source{
				ProviderID: "deconfliction_service",
			}, status, volume.ID, "", 0, "", message))
			continue
		}
		for _, record := range records {
			for _, peerVolume := range append(record.Volumes, record.OffNominalVolumes...) {
				peerBounds, peerStatus, peerMessage := evaluateVolume(peerVolume)
				if peerStatus != domain.ConflictFindingStatusClear {
					if peerStatus == domain.ConflictFindingStatusIndeterminate && !volumeDimensionsUsable(peerVolume) {
						findings = append(findings, evaluatedFinding(intent, record.Source, peerStatus, volume.ID, record.Intent.ID, record.Intent.Version, peerVolume.ID, peerMessage))
						continue
					}
					if timeWindowsOverlap(volume.StartsAt, volume.EndsAt, peerVolume.StartsAt, peerVolume.EndsAt) &&
						altitudeBandsOverlap(volume.MinAltitudeM, volume.MaxAltitudeM, peerVolume.MinAltitudeM, peerVolume.MaxAltitudeM) {
						findings = append(findings, evaluatedFinding(intent, record.Source, peerStatus, volume.ID, record.Intent.ID, record.Intent.Version, peerVolume.ID, peerMessage))
					}
					continue
				}
				if !timeWindowsOverlap(volume.StartsAt, volume.EndsAt, peerVolume.StartsAt, peerVolume.EndsAt) || !ownBounds.overlaps(peerBounds) {
					continue
				}
				if volume.AltitudeRef != peerVolume.AltitudeRef {
					findings = append(findings, evaluatedFinding(intent, record.Source, domain.ConflictFindingStatusIndeterminate, volume.ID, record.Intent.ID, record.Intent.Version, peerVolume.ID, "operational volume altitude references differ and cannot be compared locally"))
					continue
				}
				if !altitudeBandsOverlap(volume.MinAltitudeM, volume.MaxAltitudeM, peerVolume.MinAltitudeM, peerVolume.MaxAltitudeM) {
					continue
				}
				finding := evaluatedFinding(intent, record.Source, domain.ConflictFindingStatusPotentialConflict, volume.ID, record.Intent.ID, record.Intent.Version, peerVolume.ID, "4D operational volume bounding boxes overlap; exact polygon intersection is not evaluated in v1")
				start, end := timeOverlap(volume.StartsAt, volume.EndsAt, peerVolume.StartsAt, peerVolume.EndsAt)
				minAltitude, maxAltitude := altitudeOverlap(volume.MinAltitudeM, volume.MaxAltitudeM, peerVolume.MinAltitudeM, peerVolume.MaxAltitudeM)
				finding.TimeOverlapStart, finding.TimeOverlapEnd = &start, &end
				finding.AltitudeOverlapMin, finding.AltitudeOverlapMax = &minAltitude, &maxAltitude
				finding.OwnBounds, finding.ConflictingBounds = ownBounds.domainBounds(), peerBounds.domainBounds()
				findings = append(findings, finding)
			}
		}
	}
	return findings
}

func evaluatedFinding(intent domain.OperationalIntent, source airspaceprovider.Source, status domain.ConflictFindingStatus, volumeID, peerIntentID string, peerVersion int, peerVolumeID, message string) domain.ConflictFinding {
	sourceID := source.ProviderID
	if source.ReferenceID != "" {
		sourceID += ":" + source.ReferenceID
	}
	sourceType := domain.ConflictFindingSourceExternal
	if source.ProviderID == "local_durable_store" || source.ProviderID == "deconfliction_service" {
		sourceType = domain.ConflictFindingSourceLocal
	}
	return domain.ConflictFinding{
		OperatorID: intent.OperatorID, IntentID: intent.ID, IntentVersion: intent.Version,
		AircraftID: intent.AircraftID, VolumeID: volumeID, ConflictingIntentID: peerIntentID,
		ConflictingVersion: peerVersion, ConflictingVolumeID: peerVolumeID,
		SourceType: sourceType, SourceID: sourceID,
		Status: status, Message: message,
		Provenance: strings.Join([]string{source.ProviderID, source.Manager, source.USSBaseURL}, "|"),
	}
}

func evaluateVolume(volume domain.OperationalVolume) (geoBounds, domain.ConflictFindingStatus, string) {
	if message := volumeDimensionError(volume); message != "" {
		return geoBounds{}, domain.ConflictFindingStatusIndeterminate, message
	}
	if strings.TrimSpace(volume.GeoJSON) == "" {
		if strings.TrimSpace(volume.GeometryURI) != "" {
			return geoBounds{}, domain.ConflictFindingStatusPotentialConflict, "operational volume references external geometry that is not resolved locally"
		}
		return geoBounds{}, domain.ConflictFindingStatusIndeterminate, "operational volume has no inline GeoJSON geometry"
	}
	bounds, err := geoJSONBounds(volume.GeoJSON)
	if err != nil {
		return geoBounds{}, domain.ConflictFindingStatusIndeterminate, err.Error()
	}
	return bounds, domain.ConflictFindingStatusClear, ""
}

func volumeDimensionsUsable(volume domain.OperationalVolume) bool {
	return volumeDimensionError(volume) == ""
}

func volumeDimensionError(volume domain.OperationalVolume) string {
	switch {
	case volume.StartsAt.IsZero() || volume.EndsAt.IsZero() || !volume.StartsAt.Before(volume.EndsAt):
		return "operational volume has an invalid time window"
	case volume.MinAltitudeM < 0 || volume.MaxAltitudeM <= 0:
		return "operational volume has a missing or invalid altitude band"
	case volume.MinAltitudeM > volume.MaxAltitudeM:
		return "operational volume has an invalid altitude band"
	case volume.AltitudeRef == "":
		return "operational volume has no altitude reference"
	default:
		return ""
	}
}

type geoBounds struct {
	minLat, maxLat float64
	minLon, maxLon float64
}

func (bounds geoBounds) overlaps(other geoBounds) bool {
	return bounds.minLat <= other.maxLat && other.minLat <= bounds.maxLat &&
		bounds.minLon <= other.maxLon && other.minLon <= bounds.maxLon
}

func (bounds geoBounds) domainBounds() *domain.GeoBounds {
	return &domain.GeoBounds{MinLat: bounds.minLat, MinLon: bounds.minLon, MaxLat: bounds.maxLat, MaxLon: bounds.maxLon}
}

func geoJSONBounds(raw string) (geoBounds, error) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return geoBounds{}, fmt.Errorf("operational volume GeoJSON is malformed: %w", err)
	}
	if decoded["type"] == "Feature" {
		geometry, ok := decoded["geometry"].(map[string]any)
		if !ok {
			return geoBounds{}, fmt.Errorf("operational volume GeoJSON feature has no geometry")
		}
		decoded = geometry
	}
	if decoded["type"] != "Polygon" {
		return geoBounds{}, fmt.Errorf("operational volume GeoJSON type %q is not supported for local deconfliction", decoded["type"])
	}
	coordinates, ok := decoded["coordinates"].([]any)
	if !ok || len(coordinates) == 0 {
		return geoBounds{}, fmt.Errorf("operational volume GeoJSON polygon has no coordinates")
	}
	exterior, ok := coordinates[0].([]any)
	if !ok || len(exterior) < 4 {
		return geoBounds{}, fmt.Errorf("operational volume GeoJSON polygon exterior ring is invalid")
	}
	bounds := geoBounds{minLat: math.MaxFloat64, minLon: math.MaxFloat64, maxLat: -math.MaxFloat64, maxLon: -math.MaxFloat64}
	var firstLon, firstLat, lastLon, lastLat float64
	for index, item := range exterior {
		lon, lat, err := geoJSONPosition(item)
		if err != nil {
			return geoBounds{}, err
		}
		if index == 0 {
			firstLon, firstLat = lon, lat
		}
		lastLon, lastLat = lon, lat
		bounds.minLat, bounds.maxLat = math.Min(bounds.minLat, lat), math.Max(bounds.maxLat, lat)
		bounds.minLon, bounds.maxLon = math.Min(bounds.minLon, lon), math.Max(bounds.maxLon, lon)
	}
	if firstLon != lastLon || firstLat != lastLat {
		return geoBounds{}, fmt.Errorf("operational volume GeoJSON polygon exterior ring is not closed")
	}
	return bounds, nil
}

func geoJSONPosition(value any) (float64, float64, error) {
	pair, ok := value.([]any)
	if !ok || len(pair) < 2 {
		return 0, 0, fmt.Errorf("operational volume GeoJSON polygon contains an invalid coordinate")
	}
	lon, lonOK := pair[0].(float64)
	lat, latOK := pair[1].(float64)
	if !latOK || !lonOK || math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) {
		return 0, 0, fmt.Errorf("operational volume GeoJSON polygon contains a non-finite coordinate")
	}
	if lon < -180 || lon > 180 || lat < -90 || lat > 90 {
		return 0, 0, fmt.Errorf("operational volume GeoJSON polygon coordinate is outside valid longitude/latitude range")
	}
	return lon, lat, nil
}

func timeWindowsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

func timeOverlap(aStart, aEnd, bStart, bEnd time.Time) (time.Time, time.Time) {
	start, end := aStart, aEnd
	if bStart.After(start) {
		start = bStart
	}
	if bEnd.Before(end) {
		end = bEnd
	}
	return start, end
}

func altitudeBandsOverlap(aMin, aMax, bMin, bMax float64) bool {
	return aMin <= bMax && bMin <= aMax
}

func altitudeOverlap(aMin, aMax, bMin, bMax float64) (float64, float64) {
	return math.Max(aMin, bMin), math.Min(aMax, bMax)
}
