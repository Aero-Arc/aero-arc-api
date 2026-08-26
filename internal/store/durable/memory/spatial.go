package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

// CheckMissionCoverage evaluates route points and complete consecutive
// segments against one GeoJSON Polygon using the same covered/not-covered
// semantics as the PostGIS implementation.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - volume: supplies the exact authorized footprint.
//   - items: supplies ordered canonical mission points.
//
// Returns:
//   - result: identifies uncovered item and segment starting sequences.
//   - error: reports malformed or unsupported geometry.
func (s *Store) CheckMissionCoverage(_ context.Context, volume domain.OperationalVolume, items []domain.MissionItem) (durable.MissionCoverageResult, error) {
	polygon, err := decodeMissionPolygon(volume.GeoJSON)
	if err != nil {
		return durable.MissionCoverageResult{}, err
	}
	result := durable.MissionCoverageResult{}
	for index, item := range items {
		point := missionPoint{x: float64(item.LongitudeE7) / 1e7, y: float64(item.LatitudeE7) / 1e7}
		if !missionPolygonCoversPoint(polygon, point) {
			result.UncoveredItems = append(result.UncoveredItems, item.Sequence)
		}
		if index+1 == len(items) {
			continue
		}
		next := items[index+1]
		end := missionPoint{x: float64(next.LongitudeE7) / 1e7, y: float64(next.LatitudeE7) / 1e7}
		if !missionPolygonCoversSegment(polygon, point, end) {
			result.UncoveredSegments = append(result.UncoveredSegments, item.Sequence)
		}
	}
	return result, nil
}

type missionPoint struct{ x, y float64 }

func decodeMissionPolygon(raw string) ([][]missionPoint, error) {
	var payload struct {
		Type        string          `json:"type"`
		Geometry    json.RawMessage `json:"geometry"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("decode mission volume GeoJSON: %w", err)
	}
	coordinates := payload.Coordinates
	if payload.Type == "Feature" {
		var geometry struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		}
		if err := json.Unmarshal(payload.Geometry, &geometry); err != nil {
			return nil, fmt.Errorf("decode mission volume feature geometry: %w", err)
		}
		if geometry.Type != "Polygon" {
			return nil, fmt.Errorf("mission coverage requires Polygon geometry")
		}
		coordinates = geometry.Coordinates
	} else if payload.Type != "Polygon" {
		return nil, fmt.Errorf("mission coverage requires Polygon geometry")
	}
	var decoded [][][]float64
	if err := json.Unmarshal(coordinates, &decoded); err != nil || len(decoded) == 0 {
		return nil, fmt.Errorf("mission coverage requires valid Polygon coordinates")
	}
	polygon := make([][]missionPoint, len(decoded))
	for ringIndex, ring := range decoded {
		if len(ring) < 4 {
			return nil, fmt.Errorf("mission volume polygon ring %d is not closed", ringIndex)
		}
		polygon[ringIndex] = make([]missionPoint, len(ring))
		for pointIndex, coordinates := range ring {
			if len(coordinates) < 2 || math.IsNaN(coordinates[0]) || math.IsNaN(coordinates[1]) || math.IsInf(coordinates[0], 0) || math.IsInf(coordinates[1], 0) {
				return nil, fmt.Errorf("mission volume polygon contains invalid coordinates")
			}
			polygon[ringIndex][pointIndex] = missionPoint{x: coordinates[0], y: coordinates[1]}
		}
		first, last := polygon[ringIndex][0], polygon[ringIndex][len(polygon[ringIndex])-1]
		if !missionPointsEqual(first, last) {
			return nil, fmt.Errorf("mission volume polygon ring %d is not closed", ringIndex)
		}
	}
	return polygon, nil
}

func missionPolygonCoversPoint(polygon [][]missionPoint, point missionPoint) bool {
	if len(polygon) == 0 || !missionRingCoversPoint(polygon[0], point) {
		return false
	}
	for _, hole := range polygon[1:] {
		if missionPointOnRing(hole, point) {
			return true
		}
		if missionPointStrictlyInRing(hole, point) {
			return false
		}
	}
	return true
}

func missionRingCoversPoint(ring []missionPoint, point missionPoint) bool {
	return missionPointOnRing(ring, point) || missionPointStrictlyInRing(ring, point)
}

func missionPointOnRing(ring []missionPoint, point missionPoint) bool {
	for index := 1; index < len(ring); index++ {
		if missionPointOnSegment(point, ring[index-1], ring[index]) {
			return true
		}
	}
	return false
}

func missionPointStrictlyInRing(ring []missionPoint, point missionPoint) bool {
	inside := false
	for i, j := 0, len(ring)-1; i < len(ring); j, i = i, i+1 {
		pi, pj := ring[i], ring[j]
		if (pi.y > point.y) != (pj.y > point.y) && point.x < (pj.x-pi.x)*(point.y-pi.y)/(pj.y-pi.y)+pi.x {
			inside = !inside
		}
	}
	return inside
}

func missionPolygonCoversSegment(polygon [][]missionPoint, start, end missionPoint) bool {
	if !missionPolygonCoversPoint(polygon, start) || !missionPolygonCoversPoint(polygon, end) {
		return false
	}
	breaks := []float64{0, 1}
	for _, ring := range polygon {
		for index := 1; index < len(ring); index++ {
			breaks = append(breaks, missionSegmentIntersectionParameters(start, end, ring[index-1], ring[index])...)
		}
	}
	sort.Float64s(breaks)
	for index := 1; index < len(breaks); index++ {
		middle := (breaks[index-1] + breaks[index]) / 2
		point := missionPoint{x: start.x + (end.x-start.x)*middle, y: start.y + (end.y-start.y)*middle}
		if !missionPolygonCoversPoint(polygon, point) {
			return false
		}
	}
	return true
}

func missionSegmentIntersectionParameters(a, b, c, d missionPoint) []float64 {
	r := missionPoint{x: b.x - a.x, y: b.y - a.y}
	s := missionPoint{x: d.x - c.x, y: d.y - c.y}
	denominator := r.x*s.y - r.y*s.x
	numerator := (c.x-a.x)*r.y - (c.y-a.y)*r.x
	if math.Abs(denominator) < 1e-12 {
		if math.Abs(numerator) >= 1e-12 {
			return nil
		}
		lengthSquared := r.x*r.x + r.y*r.y
		if lengthSquared < 1e-24 {
			return nil
		}
		values := []float64{
			((c.x-a.x)*r.x + (c.y-a.y)*r.y) / lengthSquared,
			((d.x-a.x)*r.x + (d.y-a.y)*r.y) / lengthSquared,
		}
		result := make([]float64, 0, 2)
		for _, value := range values {
			if value >= 0 && value <= 1 {
				result = append(result, value)
			}
		}
		return result
	}
	t := ((c.x-a.x)*s.y - (c.y-a.y)*s.x) / denominator
	u := ((c.x-a.x)*r.y - (c.y-a.y)*r.x) / denominator
	if t >= 0 && t <= 1 && u >= 0 && u <= 1 {
		return []float64{t}
	}
	return nil
}

func missionPointOnSegment(point, start, end missionPoint) bool {
	cross := (point.x-start.x)*(end.y-start.y) - (point.y-start.y)*(end.x-start.x)
	if math.Abs(cross) > 1e-10 {
		return false
	}
	dot := (point.x-start.x)*(end.x-start.x) + (point.y-start.y)*(end.y-start.y)
	if dot < -1e-10 {
		return false
	}
	lengthSquared := (end.x-start.x)*(end.x-start.x) + (end.y-start.y)*(end.y-start.y)
	return dot <= lengthSquared+1e-10
}

func missionPointsEqual(a, b missionPoint) bool {
	return math.Abs(a.x-b.x) < 1e-12 && math.Abs(a.y-b.y) < 1e-12
}

// FindCandidates is the development-store equivalent of the PostGIS query. It
// scans in memory and deliberately over-selects because final conflict checks
// always hydrate and evaluate the authoritative records.
func (s *Store) FindCandidates(_ context.Context, query durable.CandidateQuery) ([]durable.Candidate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	unique := make(map[string]durable.Candidate)
	for _, volume := range s.operationalVolumes {
		if volume.IntentID == query.ExcludeIntentID || !couldOverlap(query.Volumes, volume) {
			continue
		}
		candidate := durable.Candidate{IntentID: volume.IntentID, IntentVersion: volume.IntentVersion}
		unique[candidateKey(candidate)] = candidate
	}
	candidates := make([]durable.Candidate, 0, len(unique))
	for _, candidate := range unique {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].IntentID == candidates[b].IntentID {
			return candidates[a].IntentVersion < candidates[b].IntentVersion
		}
		return candidates[a].IntentID < candidates[b].IntentID
	})
	return candidates, nil
}

func couldOverlap(targets []domain.OperationalVolume, candidate domain.OperationalVolume) bool {
	if len(targets) == 0 || !dimensionsUsable(candidate) {
		return true
	}
	for _, target := range targets {
		if !dimensionsUsable(target) {
			return true
		}
		if target.StartsAt.Before(candidate.EndsAt) && candidate.StartsAt.Before(target.EndsAt) &&
			(target.AltitudeRef != candidate.AltitudeRef ||
				target.MinAltitudeM <= candidate.MaxAltitudeM && candidate.MinAltitudeM <= target.MaxAltitudeM) {
			return true
		}
	}
	return false
}

func dimensionsUsable(volume domain.OperationalVolume) bool {
	return !volume.StartsAt.IsZero() && !volume.EndsAt.IsZero() &&
		volume.StartsAt.Before(volume.EndsAt) &&
		volume.MinAltitudeM >= 0 && volume.MaxAltitudeM > 0 &&
		volume.MinAltitudeM <= volume.MaxAltitudeM &&
		volume.AltitudeRef != ""
}

func candidateKey(candidate durable.Candidate) string {
	return candidate.IntentID + "\x00" + strconv.Itoa(candidate.IntentVersion)
}
