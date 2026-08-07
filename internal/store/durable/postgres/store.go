package postgres

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

// Store persists the deconfliction vertical slice in PostgreSQL/PostGIS. The
// embedded memory store remains the explicitly temporary implementation for
// domain areas that do not have PostgreSQL tables yet.
type Store struct {
	*durablememory.Store
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("PostgreSQL database URL is required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("configure PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		pool.Close()
		return nil, fmt.Errorf("apply PostgreSQL/PostGIS schema: %w", err)
	}
	return &Store{Store: durablememory.NewStore(), pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) CreateOperationalIntent(ctx context.Context, intent domain.OperationalIntent) error {
	return upsertIntent(ctx, s.pool, intent)
}

func (s *Store) UpdateOperationalIntent(ctx context.Context, intent domain.OperationalIntent) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM operational_intents WHERE id = $1)`, intent.ID).Scan(&exists); err != nil {
		return fmt.Errorf("check operational intent: %w", err)
	}
	if !exists {
		return durable.ErrNotFound
	}
	return upsertIntent(ctx, s.pool, intent)
}

func (s *Store) GetOperationalIntent(ctx context.Context, id string) (domain.OperationalIntent, error) {
	return scanIntent(s.pool.QueryRow(ctx, `SELECT data FROM operational_intents WHERE id = $1 ORDER BY version DESC, updated_at DESC LIMIT 1`, id))
}

func (s *Store) GetOperationalIntentVersion(ctx context.Context, id string, version int) (domain.OperationalIntent, error) {
	return scanIntent(s.pool.QueryRow(ctx, `SELECT data FROM operational_intents WHERE id = $1 AND version = $2`, id, version))
}

func (s *Store) ListOperationalIntents(ctx context.Context, aircraftID string) ([]domain.OperationalIntent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (id) data
		FROM operational_intents
		WHERE $1 = '' OR aircraft_id = $1
		ORDER BY id, version DESC, updated_at DESC`, aircraftID)
	if err != nil {
		return nil, fmt.Errorf("list operational intents: %w", err)
	}
	intents, err := readIntents(rows)
	if err != nil {
		return nil, err
	}
	sort.Slice(intents, func(i, j int) bool { return intents[i].PlannedStartAt.Before(intents[j].PlannedStartAt) })
	return intents, nil
}

func (s *Store) ListOperationalIntentVersions(ctx context.Context, id string) ([]domain.OperationalIntent, error) {
	rows, err := s.pool.Query(ctx, `SELECT data FROM operational_intents WHERE id = $1 ORDER BY version, updated_at`, id)
	if err != nil {
		return nil, fmt.Errorf("list operational intent versions: %w", err)
	}
	return readIntents(rows)
}

func (s *Store) RecordOperationalVolume(ctx context.Context, volume domain.OperationalVolume) error {
	return upsertVolume(ctx, s.pool, volume)
}

func (s *Store) ReplaceOperationalVolumes(ctx context.Context, id string, version int, volumes []domain.OperationalVolume) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational volume replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := replaceVolumes(ctx, tx, id, version, volumes); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit operational volume replacement: %w", err)
	}
	return nil
}

func (s *Store) ReplaceOperationalIntent(ctx context.Context, expectedVersion int, intent domain.OperationalIntent, volumes []domain.OperationalVolume) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational intent replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A transaction-scoped advisory lock gives every replica the same lock for
	// this intent, including when a new version row is inserted.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, intent.ID); err != nil {
		return fmt.Errorf("lock operational intent: %w", err)
	}
	var currentVersion int
	err = tx.QueryRow(ctx, `SELECT version FROM operational_intents WHERE id = $1 ORDER BY version DESC LIMIT 1`, intent.ID).Scan(&currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return durable.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read current operational intent version: %w", err)
	}
	if currentVersion != expectedVersion {
		return durable.ErrVersionConflict
	}
	if err := upsertIntent(ctx, tx, intent); err != nil {
		return err
	}
	if err := replaceVolumes(ctx, tx, intent.ID, intent.Version, volumes); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit operational intent replacement: %w", err)
	}
	return nil
}

func (s *Store) ListOperationalVolumes(ctx context.Context, intentID string) ([]domain.OperationalVolume, error) {
	rows, err := s.pool.Query(ctx, `SELECT data FROM operational_volumes WHERE $1 = '' OR intent_id = $1 ORDER BY sequence, starts_at`, intentID)
	if err != nil {
		return nil, fmt.Errorf("list operational volumes: %w", err)
	}
	defer rows.Close()
	volumes := make([]domain.OperationalVolume, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan operational volume: %w", err)
		}
		var volume domain.OperationalVolume
		if err := json.Unmarshal(raw, &volume); err != nil {
			return nil, fmt.Errorf("decode operational volume: %w", err)
		}
		volumes = append(volumes, volume)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operational volumes: %w", err)
	}
	return volumes, nil
}

func (s *Store) RecordConflictFinding(ctx context.Context, finding domain.ConflictFinding) error {
	return upsertFinding(ctx, s.pool, finding)
}

func (s *Store) ListConflictFindings(ctx context.Context, intentID string, intentVersion int) ([]domain.ConflictFinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT data FROM conflict_findings
		WHERE ($1 = '' OR intent_id = $1) AND ($2 = 0 OR intent_version = $2)
		ORDER BY evaluated_at DESC, id`, intentID, intentVersion)
	if err != nil {
		return nil, fmt.Errorf("list conflict findings: %w", err)
	}
	defer rows.Close()
	findings := make([]domain.ConflictFinding, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan conflict finding: %w", err)
		}
		var finding domain.ConflictFinding
		if err := json.Unmarshal(raw, &finding); err != nil {
			return nil, fmt.Errorf("decode conflict finding: %w", err)
		}
		findings = append(findings, finding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conflict findings: %w", err)
	}
	return findings, nil
}

func (s *Store) ReplaceConflictFindings(ctx context.Context, intentID string, intentVersion int, ruleVersion string, findings []domain.ConflictFinding) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin conflict finding replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM conflict_findings WHERE intent_id = $1 AND intent_version = $2 AND rule_version = $3`, intentID, intentVersion, ruleVersion); err != nil {
		return fmt.Errorf("clear conflict findings: %w", err)
	}
	for _, finding := range findings {
		if err := upsertFinding(ctx, tx, finding); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit conflict finding replacement: %w", err)
	}
	return nil
}

type querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func upsertIntent(ctx context.Context, db querier, intent domain.OperationalIntent) error {
	raw, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("encode operational intent: %w", err)
	}
	_, err = db.Exec(ctx, `
		INSERT INTO operational_intents (id, version, aircraft_id, planned_start_at, updated_at, data)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id, version) DO UPDATE SET
			aircraft_id = EXCLUDED.aircraft_id,
			planned_start_at = EXCLUDED.planned_start_at,
			updated_at = EXCLUDED.updated_at,
			data = EXCLUDED.data`, intent.ID, intent.Version, intent.AircraftID, intent.PlannedStartAt, intent.UpdatedAt, raw)
	if err != nil {
		return fmt.Errorf("write operational intent: %w", err)
	}
	return nil
}

func replaceVolumes(ctx context.Context, tx pgx.Tx, id string, version int, volumes []domain.OperationalVolume) error {
	if _, err := tx.Exec(ctx, `DELETE FROM operational_volumes WHERE intent_id = $1 AND intent_version = $2`, id, version); err != nil {
		return fmt.Errorf("clear operational volumes: %w", err)
	}
	for _, volume := range volumes {
		if err := upsertVolume(ctx, tx, volume); err != nil {
			return err
		}
	}
	return nil
}

func upsertVolume(ctx context.Context, db querier, volume domain.OperationalVolume) error {
	geometry, err := geometryJSON(volume.GeoJSON)
	if err != nil {
		return fmt.Errorf("read operational volume geometry: %w", err)
	}
	raw, err := json.Marshal(volume)
	if err != nil {
		return fmt.Errorf("encode operational volume: %w", err)
	}
	buffer := 0.0
	if volume.BufferMeters != nil {
		buffer = *volume.BufferMeters
	}
	_, err = db.Exec(ctx, `
		INSERT INTO operational_volumes (
			intent_id, intent_version, id, sequence, altitude_reference,
			min_altitude_m, max_altitude_m, starts_at, ends_at,
			buffer_meters, footprint, data
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			CASE WHEN NULLIF($11, '') IS NULL THEN NULL ELSE ST_SetSRID(ST_GeomFromGeoJSON($11), 4326) END,
			$12
		)
		ON CONFLICT (intent_id, intent_version, id) DO UPDATE SET
			sequence = EXCLUDED.sequence,
			altitude_reference = EXCLUDED.altitude_reference,
			min_altitude_m = EXCLUDED.min_altitude_m,
			max_altitude_m = EXCLUDED.max_altitude_m,
			starts_at = EXCLUDED.starts_at,
			ends_at = EXCLUDED.ends_at,
			buffer_meters = EXCLUDED.buffer_meters,
			footprint = EXCLUDED.footprint,
			data = EXCLUDED.data`,
		volume.IntentID, volume.IntentVersion, volume.ID, volume.Sequence, volume.AltitudeRef,
		volume.MinAltitudeM, volume.MaxAltitudeM, volume.StartsAt, volume.EndsAt,
		buffer, geometry, raw)
	if err != nil {
		return fmt.Errorf("write operational volume: %w", err)
	}
	return nil
}

func upsertFinding(ctx context.Context, db querier, finding domain.ConflictFinding) error {
	raw, err := json.Marshal(finding)
	if err != nil {
		return fmt.Errorf("encode conflict finding: %w", err)
	}
	_, err = db.Exec(ctx, `
		INSERT INTO conflict_findings (id, intent_id, intent_version, rule_version, evaluated_at, data)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			intent_id = EXCLUDED.intent_id,
			intent_version = EXCLUDED.intent_version,
			rule_version = EXCLUDED.rule_version,
			evaluated_at = EXCLUDED.evaluated_at,
			data = EXCLUDED.data`, finding.ID, finding.IntentID, finding.IntentVersion, finding.RuleVersion, finding.EvaluatedAt, raw)
	if err != nil {
		return fmt.Errorf("write conflict finding: %w", err)
	}
	return nil
}

func scanIntent(row pgx.Row) (domain.OperationalIntent, error) {
	var raw []byte
	if err := row.Scan(&raw); errors.Is(err, pgx.ErrNoRows) {
		return domain.OperationalIntent{}, durable.ErrNotFound
	} else if err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("read operational intent: %w", err)
	}
	var intent domain.OperationalIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("decode operational intent: %w", err)
	}
	return intent, nil
}

func readIntents(rows pgx.Rows) ([]domain.OperationalIntent, error) {
	defer rows.Close()
	intents := make([]domain.OperationalIntent, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan operational intent: %w", err)
		}
		var intent domain.OperationalIntent
		if err := json.Unmarshal(raw, &intent); err != nil {
			return nil, fmt.Errorf("decode operational intent: %w", err)
		}
		intents = append(intents, intent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operational intents: %w", err)
	}
	return intents, nil
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
