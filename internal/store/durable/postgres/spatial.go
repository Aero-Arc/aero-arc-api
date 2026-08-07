package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
)

// FindCandidates searches the authoritative operational volume rows. PostGIS
// maintains the GiST index transactionally with those rows, so every API
// replica observes one committed source of truth.
func (s *Store) FindCandidates(ctx context.Context, query spatialindex.Query) ([]spatialindex.Candidate, error) {
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
	candidates := make([]spatialindex.Candidate, 0)
	for rows.Next() {
		var candidate spatialindex.Candidate
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
