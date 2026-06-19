package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

const deconflictionRuleVersion = "local-dss-shaped-v1"

type DeconflictionService struct {
	durable durable.Store
	now     func() time.Time
}

func NewDeconflictionService(durableStore durable.Store) *DeconflictionService {
	return NewDeconflictionServiceWithClock(durableStore, nil)
}

func NewDeconflictionServiceWithClock(durableStore durable.Store, now func() time.Time) *DeconflictionService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &DeconflictionService{durable: durableStore, now: now}
}

func (s *DeconflictionService) CheckIntent(ctx context.Context, intentID string) (domain.DeconflictionResult, error) {
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return domain.DeconflictionResult{}, fmt.Errorf("get operational intent: %w", err)
	}

	checkedAt := s.now().UTC()
	result := domain.DeconflictionResult{
		Intent:      intent,
		Posture:     domain.DeconflictionPostureClear,
		Findings:    make([]domain.ConflictFinding, 0),
		CheckedAt:   checkedAt,
		RuleVersion: deconflictionRuleVersion,
	}

	volumes, err := s.durable.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		return domain.DeconflictionResult{}, fmt.Errorf("list operational volumes: %w", err)
	}
	volumes = volumesForVersion(volumes, intent.Version)
	if len(volumes) == 0 {
		finding := s.finding(intent, domain.ConflictFindingStatusIndeterminate, "", "", 0, "", "intent has no operational volumes to check", checkedAt)
		result.Findings = append(result.Findings, finding)
		result.Posture = maxPosture(result.Posture, finding.Status)
		if err := s.durable.ReplaceConflictFindings(ctx, intent.ID, intent.Version, deconflictionRuleVersion, result.Findings); err != nil {
			return domain.DeconflictionResult{}, fmt.Errorf("replace conflict findings: %w", err)
		}
		return result, nil
	}

	intents, err := s.durable.ListOperationalIntents(ctx, "")
	if err != nil {
		return domain.DeconflictionResult{}, fmt.Errorf("list operational intents: %w", err)
	}
	allVolumes, err := s.durable.ListOperationalVolumes(ctx, "")
	if err != nil {
		return domain.DeconflictionResult{}, fmt.Errorf("list candidate operational volumes: %w", err)
	}
	volumesByIntent := make(map[string][]domain.OperationalVolume)
	for _, volume := range allVolumes {
		volumesByIntent[volume.IntentID] = append(volumesByIntent[volume.IntentID], volume)
	}

	for _, volume := range volumes {
		ownBounds, finding, ok := s.evaluableVolume(intent, volume, checkedAt)
		if !ok {
			result.Findings = append(result.Findings, finding)
			result.Posture = maxPosture(result.Posture, finding.Status)
			continue
		}

		for _, candidate := range intents {
			if candidate.ID == intent.ID || !candidateConflictEligible(candidate.Status) {
				continue
			}
			for _, candidateVolume := range volumesForVersion(volumesByIntent[candidate.ID], candidate.Version) {
				peerBounds, peerFinding, ok := s.evaluableVolumeForPeer(intent, volume.ID, candidate, candidateVolume, checkedAt)
				if !ok {
					if peerFinding.Status == domain.ConflictFindingStatusIndeterminate && !peerVolumeDimensionsUsable(candidateVolume) {
						result.Findings = append(result.Findings, peerFinding)
						result.Posture = maxPosture(result.Posture, peerFinding.Status)
						continue
					}
					if timeWindowsOverlap(volume.StartsAt, volume.EndsAt, candidateVolume.StartsAt, candidateVolume.EndsAt) &&
						altitudeBandsOverlap(volume.MinAltitudeM, volume.MaxAltitudeM, candidateVolume.MinAltitudeM, candidateVolume.MaxAltitudeM) {
						result.Findings = append(result.Findings, peerFinding)
						result.Posture = maxPosture(result.Posture, peerFinding.Status)
					}
					continue
				}
				if !timeWindowsOverlap(volume.StartsAt, volume.EndsAt, candidateVolume.StartsAt, candidateVolume.EndsAt) ||
					!ownBounds.overlaps(peerBounds) {
					continue
				}
				if volume.AltitudeRef != candidateVolume.AltitudeRef {
					finding := s.finding(intent, domain.ConflictFindingStatusIndeterminate, volume.ID, candidate.ID, candidate.Version, candidateVolume.ID, "operational volume altitude references differ and cannot be compared locally", checkedAt)
					result.Findings = append(result.Findings, finding)
					result.Posture = maxPosture(result.Posture, finding.Status)
					continue
				}
				if !altitudeBandsOverlap(volume.MinAltitudeM, volume.MaxAltitudeM, candidateVolume.MinAltitudeM, candidateVolume.MaxAltitudeM) {
					continue
				}

				start, end := timeOverlap(volume.StartsAt, volume.EndsAt, candidateVolume.StartsAt, candidateVolume.EndsAt)
				minAlt, maxAlt := altitudeOverlap(volume.MinAltitudeM, volume.MaxAltitudeM, candidateVolume.MinAltitudeM, candidateVolume.MaxAltitudeM)
				finding := s.finding(intent, domain.ConflictFindingStatusPotentialConflict, volume.ID, candidate.ID, candidate.Version, candidateVolume.ID, "local 4D operational volume bounding boxes overlap; exact polygon intersection is not evaluated in v1", checkedAt)
				finding.TimeOverlapStart = &start
				finding.TimeOverlapEnd = &end
				finding.AltitudeOverlapMin = &minAlt
				finding.AltitudeOverlapMax = &maxAlt
				result.Findings = append(result.Findings, finding)
				result.Posture = maxPosture(result.Posture, finding.Status)
			}
		}
	}

	if len(result.Findings) == 0 {
		finding := s.finding(intent, domain.ConflictFindingStatusClear, "", "", 0, "", "no local accepted/active operational volume overlap detected", checkedAt)
		result.Findings = append(result.Findings, finding)
	}
	if err := s.durable.ReplaceConflictFindings(ctx, intent.ID, intent.Version, deconflictionRuleVersion, result.Findings); err != nil {
		return domain.DeconflictionResult{}, fmt.Errorf("replace conflict findings: %w", err)
	}
	return result, nil
}

func (s *DeconflictionService) ListConflictFindings(ctx context.Context, intentID string) ([]domain.ConflictFinding, error) {
	if strings.TrimSpace(intentID) == "" {
		return nil, fmt.Errorf("%w: intent_id is required", ErrValidation)
	}
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return nil, fmt.Errorf("get operational intent: %w", err)
	}
	findings, err := s.durable.ListConflictFindings(ctx, intent.ID, intent.Version)
	if err != nil {
		return nil, fmt.Errorf("list conflict findings: %w", err)
	}
	return findings, nil
}

func (s *DeconflictionService) evaluableVolume(intent domain.OperationalIntent, volume domain.OperationalVolume, checkedAt time.Time) (geoBounds, domain.ConflictFinding, bool) {
	status, message := validateVolume(volume)
	if status != domain.ConflictFindingStatusClear {
		finding := s.finding(intent, status, volume.ID, "", 0, "", message, checkedAt)
		return geoBounds{}, finding, false
	}
	bounds, err := geoJSONBounds(volume.GeoJSON)
	if err != nil {
		finding := s.finding(intent, domain.ConflictFindingStatusIndeterminate, volume.ID, "", 0, "", err.Error(), checkedAt)
		return geoBounds{}, finding, false
	}
	return bounds, domain.ConflictFinding{}, true
}

func (s *DeconflictionService) evaluableVolumeForPeer(target domain.OperationalIntent, volumeID string, peer domain.OperationalIntent, peerVolume domain.OperationalVolume, checkedAt time.Time) (geoBounds, domain.ConflictFinding, bool) {
	status, message := validateVolume(peerVolume)
	if status != domain.ConflictFindingStatusClear {
		finding := s.finding(target, status, volumeID, peer.ID, peer.Version, peerVolume.ID, message, checkedAt)
		return geoBounds{}, finding, false
	}
	bounds, err := geoJSONBounds(peerVolume.GeoJSON)
	if err != nil {
		finding := s.finding(target, domain.ConflictFindingStatusIndeterminate, volumeID, peer.ID, peer.Version, peerVolume.ID, err.Error(), checkedAt)
		return geoBounds{}, finding, false
	}
	return bounds, domain.ConflictFinding{}, true
}

func (s *DeconflictionService) finding(intent domain.OperationalIntent, status domain.ConflictFindingStatus, volumeID, conflictingIntentID string, conflictingVersion int, conflictingVolumeID, message string, checkedAt time.Time) domain.ConflictFinding {
	id := strings.Join([]string{"conflict", intent.ID, fmt.Sprintf("v%d", intent.Version), string(status), emptyID(volumeID), emptyID(conflictingIntentID), emptyID(conflictingVolumeID)}, "-")
	return domain.ConflictFinding{
		ID:                  id,
		OperatorID:          intent.OperatorID,
		IntentID:            intent.ID,
		IntentVersion:       intent.Version,
		AircraftID:          intent.AircraftID,
		VolumeID:            volumeID,
		ConflictingIntentID: conflictingIntentID,
		ConflictingVersion:  conflictingVersion,
		ConflictingVolumeID: conflictingVolumeID,
		SourceType:          domain.ConflictFindingSourceLocal,
		SourceID:            "local_durable_store",
		Status:              status,
		Severity:            conflictSeverity(status),
		Blocking:            status != domain.ConflictFindingStatusClear,
		Message:             message,
		RuleVersion:         deconflictionRuleVersion,
		Provenance:          "local_operational_volumes",
		EvaluatedAt:         checkedAt,
	}
}

func validateVolume(volume domain.OperationalVolume) (domain.ConflictFindingStatus, string) {
	if volume.StartsAt.IsZero() || volume.EndsAt.IsZero() || !volume.StartsAt.Before(volume.EndsAt) {
		return domain.ConflictFindingStatusIndeterminate, "operational volume has an invalid time window"
	}
	if volume.MinAltitudeM < 0 || volume.MaxAltitudeM <= 0 {
		return domain.ConflictFindingStatusIndeterminate, "operational volume has a missing or invalid altitude band"
	}
	if volume.MinAltitudeM > volume.MaxAltitudeM {
		return domain.ConflictFindingStatusIndeterminate, "operational volume has an invalid altitude band"
	}
	if volume.AltitudeRef == "" {
		return domain.ConflictFindingStatusIndeterminate, "operational volume has no altitude reference"
	}
	if strings.TrimSpace(volume.GeoJSON) == "" {
		if strings.TrimSpace(volume.GeometryURI) != "" {
			return domain.ConflictFindingStatusPotentialConflict, "operational volume references external geometry that is not resolved locally"
		}
		return domain.ConflictFindingStatusIndeterminate, "operational volume has no inline GeoJSON geometry"
	}
	return domain.ConflictFindingStatusClear, ""
}

func peerVolumeDimensionsUsable(volume domain.OperationalVolume) bool {
	return !volume.StartsAt.IsZero() &&
		!volume.EndsAt.IsZero() &&
		volume.StartsAt.Before(volume.EndsAt) &&
		volume.MinAltitudeM >= 0 &&
		volume.MaxAltitudeM > 0 &&
		volume.MinAltitudeM <= volume.MaxAltitudeM &&
		volume.AltitudeRef != ""
}

func candidateConflictEligible(status domain.IntentStatus) bool {
	return status == domain.IntentStatusAccepted || status == domain.IntentStatusActive
}

func volumesForVersion(volumes []domain.OperationalVolume, version int) []domain.OperationalVolume {
	filtered := make([]domain.OperationalVolume, 0, len(volumes))
	for _, volume := range volumes {
		if volume.IntentVersion == version {
			filtered = append(filtered, volume)
		}
	}
	return filtered
}

func conflictSeverity(status domain.ConflictFindingStatus) domain.Severity {
	switch status {
	case domain.ConflictFindingStatusConflict:
		return domain.SeverityCritical
	case domain.ConflictFindingStatusPotentialConflict, domain.ConflictFindingStatusIndeterminate:
		return domain.SeverityWarning
	default:
		return domain.SeverityInfo
	}
}

func maxPosture(current domain.DeconflictionPosture, status domain.ConflictFindingStatus) domain.DeconflictionPosture {
	next := postureForFinding(status)
	if postureRank(next) > postureRank(current) {
		return next
	}
	return current
}

func postureForFinding(status domain.ConflictFindingStatus) domain.DeconflictionPosture {
	switch status {
	case domain.ConflictFindingStatusConflict:
		return domain.DeconflictionPostureConflict
	case domain.ConflictFindingStatusPotentialConflict:
		return domain.DeconflictionPosturePotentialConflict
	case domain.ConflictFindingStatusIndeterminate:
		return domain.DeconflictionPostureIndeterminate
	default:
		return domain.DeconflictionPostureClear
	}
}

func postureRank(posture domain.DeconflictionPosture) int {
	switch posture {
	case domain.DeconflictionPostureConflict:
		return 4
	case domain.DeconflictionPostureIndeterminate:
		return 3
	case domain.DeconflictionPosturePotentialConflict:
		return 2
	default:
		return 1
	}
}

func emptyID(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func timeWindowsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	// Operational volume windows are treated as half-open intervals: [start, end).
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

func timeOverlap(aStart, aEnd, bStart, bEnd time.Time) (time.Time, time.Time) {
	start := aStart
	if bStart.After(start) {
		start = bStart
	}
	end := aEnd
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

type geoBounds struct {
	minLat float64
	maxLat float64
	minLon float64
	maxLon float64
}

func (b geoBounds) overlaps(other geoBounds) bool {
	return b.minLat <= other.maxLat && other.minLat <= b.maxLat && b.minLon <= other.maxLon && other.minLon <= b.maxLon
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
	points := 0
	var firstLon, firstLat, lastLon, lastLat float64
	for i, item := range exterior {
		lon, lat, err := geoJSONPosition(item)
		if err != nil {
			return geoBounds{}, err
		}
		if i == 0 {
			firstLon, firstLat = lon, lat
		}
		lastLon, lastLat = lon, lat
		bounds.minLat = math.Min(bounds.minLat, lat)
		bounds.maxLat = math.Max(bounds.maxLat, lat)
		bounds.minLon = math.Min(bounds.minLon, lon)
		bounds.maxLon = math.Max(bounds.maxLon, lon)
		points++
	}
	if points < 3 {
		return geoBounds{}, fmt.Errorf("operational volume GeoJSON polygon exterior ring has too few valid points")
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
