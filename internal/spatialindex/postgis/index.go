package postgis

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

type Index struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Index, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("PostGIS database URL is required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("configure PostGIS pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostGIS: %w", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply PostGIS spatial schema: %w", err)
	}
	return &Index{pool: pool}, nil
}

func (i *Index) ID() string {
	return "postgis"
}

func (i *Index) Close() {
	i.pool.Close()
}

func (i *Index) Rebuild(ctx context.Context, volumes []domain.OperationalVolume) error {
	tx, err := i.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin PostGIS rebuild: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM spatial_operational_volumes`); err != nil {
		return fmt.Errorf("clear PostGIS spatial projection: %w", err)
	}
	for _, volume := range volumes {
		if err := recordVolume(ctx, tx, volume); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit PostGIS rebuild: %w", err)
	}
	return nil
}

func (i *Index) RecordVolume(ctx context.Context, volume domain.OperationalVolume) error {
	return recordVolume(ctx, i.pool, volume)
}

func (i *Index) ReplaceVolumes(ctx context.Context, id string, version int, volumes []domain.OperationalVolume) error {
	tx, err := i.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin PostGIS volume replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`DELETE FROM spatial_operational_volumes WHERE intent_id = $1 AND intent_version = $2`,
		id, version,
	); err != nil {
		return fmt.Errorf("clear PostGIS volumes: %w", err)
	}
	for _, volume := range volumes {
		if err := recordVolume(ctx, tx, volume); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit PostGIS volume replacement: %w", err)
	}
	return nil
}

func (i *Index) FindCandidates(ctx context.Context, query spatialindex.Query) ([]spatialindex.Candidate, error) {
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
	rows, err := i.pool.Query(ctx, `
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
		FROM spatial_operational_volumes volume
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
	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].IntentID == candidates[b].IntentID {
			return candidates[a].IntentVersion < candidates[b].IntentVersion
		}
		return candidates[a].IntentID < candidates[b].IntentID
	})
	return candidates, nil
}

type querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func recordVolume(ctx context.Context, db querier, volume domain.OperationalVolume) error {
	geometry, err := geometryJSON(volume.GeoJSON)
	if err != nil {
		return fmt.Errorf("read operational volume geometry: %w", err)
	}
	buffer := 0.0
	if volume.BufferMeters != nil {
		buffer = *volume.BufferMeters
	}
	_, err = db.Exec(ctx, `
		INSERT INTO spatial_operational_volumes (
			intent_id, intent_version, id, altitude_reference,
			min_altitude_m, max_altitude_m, starts_at, ends_at,
			buffer_meters, footprint
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			CASE
				WHEN NULLIF($10, '') IS NULL THEN NULL
				ELSE ST_SetSRID(ST_GeomFromGeoJSON($10), 4326)
			END
		)
		ON CONFLICT (intent_id, intent_version, id) DO UPDATE SET
			altitude_reference = EXCLUDED.altitude_reference,
			min_altitude_m = EXCLUDED.min_altitude_m,
			max_altitude_m = EXCLUDED.max_altitude_m,
			starts_at = EXCLUDED.starts_at,
			ends_at = EXCLUDED.ends_at,
			buffer_meters = EXCLUDED.buffer_meters,
			footprint = EXCLUDED.footprint`,
		volume.IntentID, volume.IntentVersion, volume.ID, volume.AltitudeRef,
		volume.MinAltitudeM, volume.MaxAltitudeM, volume.StartsAt, volume.EndsAt,
		buffer, geometry)
	if err != nil {
		return fmt.Errorf("write PostGIS spatial volume: %w", err)
	}
	return nil
}

func geometryJSON(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	var value struct {
		Type     string          `json:"type"`
		Geometry json.RawMessage `json:"geometry"`
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return "", fmt.Errorf("decode GeoJSON: %w", err)
	}
	if value.Type == "Feature" {
		if len(value.Geometry) == 0 || string(value.Geometry) == "null" {
			return "", fmt.Errorf("GeoJSON feature has no geometry")
		}
		raw = string(value.Geometry)
		if err := json.Unmarshal(value.Geometry, &value); err != nil {
			return "", fmt.Errorf("decode GeoJSON feature geometry: %w", err)
		}
	}
	if value.Type != "Polygon" {
		return "", fmt.Errorf("GeoJSON type %q is not supported", value.Type)
	}
	return raw, nil
}
