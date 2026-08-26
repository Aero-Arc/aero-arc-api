package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

// FindCandidates searches the authoritative operational volume rows. PostGIS
// maintains the GiST index transactionally with those rows, so every API
// replica observes one committed source of truth.
func (s *Store) FindCandidates(ctx context.Context, query durable.CandidateQuery) ([]durable.Candidate, error) {
	targetVolumes := append([]domain.OperationalVolume(nil), query.Volumes...)
	for index := range targetVolumes {
		geometry, err := geometryJSON(targetVolumes[index].GeoJSON)
		if err != nil {
			return nil, fmt.Errorf("read target volume %q geometry: %w", targetVolumes[index].ID, err)
		}
		targetVolumes[index].GeoJSON = geometry
	}
	targets, err := json.Marshal(targetVolumes)
	if err != nil {
		return nil, fmt.Errorf("encode target volumes: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		WITH targets AS (
			SELECT
				starts_at,
				ends_at,
				altitude_ref,
				min_altitude_m,
				max_altitude_m,
				COALESCE(buffer_meters, 0) AS buffer_meters,
				CASE
					WHEN NULLIF(geojson, '') IS NULL THEN NULL
					ELSE ST_SetSRID(ST_GeomFromGeoJSON(geojson), 4326)
				END AS footprint
			FROM jsonb_to_recordset($2::jsonb) AS target(
				starts_at timestamptz,
				ends_at timestamptz,
				altitude_ref text,
				min_altitude_m double precision,
				max_altitude_m double precision,
				buffer_meters double precision,
				geojson text
			)
		)
		SELECT DISTINCT volume.intent_id, volume.intent_version
		FROM operational_volumes volume
		WHERE volume.intent_id <> $1
			AND EXISTS (
				SELECT 1
				FROM targets target
				WHERE volume.starts_at < target.ends_at
					AND target.starts_at < volume.ends_at
					AND (
						volume.altitude_reference <> target.altitude_ref
						OR volume.min_altitude_m <= target.max_altitude_m
							AND target.min_altitude_m <= volume.max_altitude_m
					)
					AND (
						volume.footprint IS NULL
						OR target.footprint IS NULL
						OR ST_DWithin(
							volume.footprint::geography,
							target.footprint::geography,
							volume.buffer_meters + target.buffer_meters
						)
					)
			)
		ORDER BY volume.intent_id, volume.intent_version`, query.ExcludeIntentID, targets)
	if err != nil {
		return nil, fmt.Errorf("query PostGIS candidates: %w", err)
	}
	defer rows.Close()
	candidates := make([]durable.Candidate, 0)
	for rows.Next() {
		var candidate durable.Candidate
		if err := rows.Scan(&candidate.IntentID, &candidate.IntentVersion); err != nil {
			return nil, fmt.Errorf("scan PostGIS candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostGIS candidates: %w", err)
	}
	return candidates, nil
}

// CheckMissionCoverage uses the authoritative PostGIS footprint to evaluate
// every mission point and complete consecutive segment with ST_Covers.
//
// Parameters:
//   - ctx: controls cancellation and the PostgreSQL query.
//   - volume: identifies the exact intent-version volume row.
//   - items: supplies ordered canonical mission points.
//
// Returns:
//   - result: identifies uncovered item and segment starting sequences.
//   - error: reports missing geometry, query, or decoding failures.
func (s *Store) CheckMissionCoverage(ctx context.Context, volume domain.OperationalVolume, items []domain.MissionItem) (durable.MissionCoverageResult, error) {
	type routePoint struct {
		Sequence  int     `json:"sequence"`
		Longitude float64 `json:"longitude"`
		Latitude  float64 `json:"latitude"`
	}
	points := make([]routePoint, len(items))
	for index, item := range items {
		points[index] = routePoint{
			Sequence:  item.Sequence,
			Longitude: float64(item.LongitudeE7) / 1e7,
			Latitude:  float64(item.LatitudeE7) / 1e7,
		}
	}
	raw, err := json.Marshal(points)
	if err != nil {
		return durable.MissionCoverageResult{}, fmt.Errorf("encode mission route: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		WITH route_points AS (
			SELECT sequence, ST_SetSRID(ST_MakePoint(longitude, latitude), 4326) AS point
			FROM jsonb_to_recordset($4::jsonb) AS item(sequence integer, longitude double precision, latitude double precision)
		), route AS (
			SELECT sequence, point, LEAD(point) OVER (ORDER BY sequence) AS next_point
			FROM route_points
		), volume AS (
			SELECT footprint
			FROM operational_volumes
			WHERE intent_id = $1 AND intent_version = $2 AND id = $3
		)
		SELECT route.sequence,
		       COALESCE(ST_Covers(volume.footprint, route.point), false),
		       CASE WHEN route.next_point IS NULL THEN true
		            ELSE COALESCE(ST_Covers(volume.footprint, ST_MakeLine(route.point, route.next_point)), false)
		       END
		FROM route CROSS JOIN volume
		ORDER BY route.sequence`, volume.IntentID, volume.IntentVersion, volume.ID, raw)
	if err != nil {
		return durable.MissionCoverageResult{}, fmt.Errorf("check PostGIS mission coverage: %w", err)
	}
	defer rows.Close()
	result := durable.MissionCoverageResult{}
	count := 0
	for rows.Next() {
		var sequence int
		var itemCovered, segmentCovered bool
		if err := rows.Scan(&sequence, &itemCovered, &segmentCovered); err != nil {
			return durable.MissionCoverageResult{}, fmt.Errorf("scan PostGIS mission coverage: %w", err)
		}
		count++
		if !itemCovered {
			result.UncoveredItems = append(result.UncoveredItems, sequence)
		}
		if !segmentCovered {
			result.UncoveredSegments = append(result.UncoveredSegments, sequence)
		}
	}
	if err := rows.Err(); err != nil {
		return durable.MissionCoverageResult{}, fmt.Errorf("iterate PostGIS mission coverage: %w", err)
	}
	if count != len(items) {
		return durable.MissionCoverageResult{}, durable.ErrNotFound
	}
	return result, nil
}
