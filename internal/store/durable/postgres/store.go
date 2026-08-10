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

var _ durable.OperationalStore = (*Store)(nil)

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
	intent.Revision = 0
	raw, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("encode operational intent: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO operational_intents (
			id, version, revision, aircraft_id, planned_start_at, planned_end_at, updated_at, data
		) VALUES ($1, $2, 0, $3, $4, $5, $6, $7)`,
		intent.ID, intent.Version, intent.AircraftID, intent.PlannedStartAt, intent.PlannedEndAt, intent.UpdatedAt, raw)
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return durable.ErrAlreadyExists
	}
	return fmt.Errorf("create operational intent: %w", err)
}

func (s *Store) UpdateOperationalIntent(ctx context.Context, intent domain.OperationalIntent, expectedRevision int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational intent update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, intent.ID); err != nil {
		return err
	}
	if err := updateOperationalIntentTx(ctx, tx, intent, expectedRevision); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit operational intent update: %w", err)
	}
	return nil
}

func updateOperationalIntentTx(ctx context.Context, tx pgx.Tx, intent domain.OperationalIntent, expectedRevision int64) error {
	raw, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("encode operational intent: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE operational_intents SET
			revision = revision + 1,
			aircraft_id = $3,
			planned_start_at = $4,
			planned_end_at = $5,
			updated_at = $6,
			data = $7
		WHERE id = $1 AND version = $2 AND revision = $8
			AND NOT EXISTS (
				SELECT 1 FROM operational_intents newer
				WHERE newer.id = $1 AND newer.version > $2
			)`,
		intent.ID, intent.Version, intent.AircraftID, intent.PlannedStartAt, intent.PlannedEndAt,
		intent.UpdatedAt, raw, expectedRevision)
	if err != nil {
		return fmt.Errorf("update operational intent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM operational_intents WHERE id = $1)`, intent.ID).Scan(&exists); err != nil {
			return fmt.Errorf("check operational intent after update conflict: %w", err)
		}
		if !exists {
			return durable.ErrNotFound
		}
		return durable.ErrVersionConflict
	}
	return nil
}

func (s *Store) AcceptOperationalIntent(ctx context.Context, intent domain.OperationalIntent, expectedRevision int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational intent acceptance: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, intent.ID); err != nil {
		return err
	}
	if err := acceptOperationalIntentTx(ctx, tx, intent, expectedRevision); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit operational intent acceptance: %w", err)
	}
	return nil
}

func acceptOperationalIntentTx(ctx context.Context, tx pgx.Tx, intent domain.OperationalIntent, expectedRevision int64) error {
	var currentVersion int
	var currentRevision int64
	err := tx.QueryRow(ctx, `SELECT version, revision FROM operational_intents WHERE id = $1 ORDER BY version DESC LIMIT 1`, intent.ID).Scan(&currentVersion, &currentRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return durable.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read current operational intent before acceptance: %w", err)
	}
	if currentVersion != intent.Version || currentRevision != expectedRevision {
		return durable.ErrVersionConflict
	}
	if err := upsertIntent(ctx, tx, intent, expectedRevision+1); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT data, revision
		FROM operational_intents
		WHERE id = $1 AND version < $2 AND data->>'status' = $3
		ORDER BY version`, intent.ID, intent.Version, domain.IntentStatusAccepted)
	if err != nil {
		return fmt.Errorf("list accepted operational intent versions: %w", err)
	}
	priorIntents, err := readIntents(rows)
	if err != nil {
		return err
	}
	for _, prior := range priorIntents {
		prior.Status = domain.IntentStatusSuperseded
		prior.SupersededAt = intent.AcceptedAt
		prior.UpdatedAt = intent.UpdatedAt
		if err := upsertIntent(ctx, tx, prior, prior.Revision+1); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetOperationalIntent(ctx context.Context, id string) (domain.OperationalIntent, error) {
	return scanIntent(s.pool.QueryRow(ctx, `SELECT data, revision FROM operational_intents WHERE id = $1 ORDER BY version DESC, updated_at DESC LIMIT 1`, id))
}

func (s *Store) GetOperationalIntentVersion(ctx context.Context, id string, version int) (domain.OperationalIntent, error) {
	return scanIntent(s.pool.QueryRow(ctx, `SELECT data, revision FROM operational_intents WHERE id = $1 AND version = $2`, id, version))
}

func (s *Store) ListOperationalIntents(ctx context.Context, aircraftID string) ([]domain.OperationalIntent, error) {
	rows, err := s.pool.Query(ctx, `
			SELECT DISTINCT ON (id) data, revision
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
	rows, err := s.pool.Query(ctx, `SELECT data, revision FROM operational_intents WHERE id = $1 ORDER BY version, updated_at`, id)
	if err != nil {
		return nil, fmt.Errorf("list operational intent versions: %w", err)
	}
	return readIntents(rows)
}

func (s *Store) RecordOperationalVolume(ctx context.Context, volume domain.OperationalVolume) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational volume write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, volume.IntentID); err != nil {
		return err
	}
	if err := upsertVolume(ctx, tx, volume); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit operational volume write: %w", err)
	}
	return nil
}

func (s *Store) ReplaceOperationalVolumes(ctx context.Context, id string, version int, volumes []domain.OperationalVolume) error {
	if err := validateVolumeScope(id, version, volumes); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational volume replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, id); err != nil {
		return err
	}
	if err := replaceVolumes(ctx, tx, id, version, volumes); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit operational volume replacement: %w", err)
	}
	return nil
}

func (s *Store) ReplaceOperationalIntent(ctx context.Context, expectedVersion int, expectedRevision int64, intent domain.OperationalIntent, volumes []domain.OperationalVolume) error {
	if intent.Version != expectedVersion && intent.Version != expectedVersion+1 {
		return durable.ErrVersionConflict
	}
	if err := validateVolumeScope(intent.ID, intent.Version, volumes); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational intent replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A transaction-scoped advisory lock gives every replica the same lock for
	// this intent, including when a new version row is inserted.
	if err := lockIntent(ctx, tx, intent.ID); err != nil {
		return err
	}
	var currentVersion int
	var currentRevision int64
	err = tx.QueryRow(ctx, `SELECT version, revision FROM operational_intents WHERE id = $1 ORDER BY version DESC LIMIT 1`, intent.ID).Scan(&currentVersion, &currentRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return durable.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read current operational intent version: %w", err)
	}
	if currentVersion != expectedVersion || currentRevision != expectedRevision {
		return durable.ErrVersionConflict
	}
	nextRevision := int64(0)
	if intent.Version == currentVersion {
		nextRevision = currentRevision + 1
	}
	if err := upsertIntent(ctx, tx, intent, nextRevision); err != nil {
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin conflict finding write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, finding.IntentID); err != nil {
		return err
	}
	if err := upsertFinding(ctx, tx, finding); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit conflict finding write: %w", err)
	}
	return nil
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
	if err := validateFindingScope(intentID, intentVersion, ruleVersion, findings); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin conflict finding replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, intentID); err != nil {
		return err
	}
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

func upsertIntent(ctx context.Context, db querier, intent domain.OperationalIntent, revision int64) error {
	raw, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("encode operational intent: %w", err)
	}
	_, err = db.Exec(ctx, `
			INSERT INTO operational_intents (id, version, revision, aircraft_id, planned_start_at, planned_end_at, updated_at, data)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (id, version) DO UPDATE SET
				revision = EXCLUDED.revision,
				aircraft_id = EXCLUDED.aircraft_id,
				planned_start_at = EXCLUDED.planned_start_at,
				planned_end_at = EXCLUDED.planned_end_at,
				updated_at = EXCLUDED.updated_at,
				data = EXCLUDED.data`, intent.ID, intent.Version, revision, intent.AircraftID, intent.PlannedStartAt, intent.PlannedEndAt, intent.UpdatedAt, raw)
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
	var revision int64
	if err := row.Scan(&raw, &revision); errors.Is(err, pgx.ErrNoRows) {
		return domain.OperationalIntent{}, durable.ErrNotFound
	} else if err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("read operational intent: %w", err)
	}
	var intent domain.OperationalIntent
	if err := json.Unmarshal(raw, &intent); err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("decode operational intent: %w", err)
	}
	intent.Revision = revision
	return intent, nil
}

func readIntents(rows pgx.Rows) ([]domain.OperationalIntent, error) {
	defer rows.Close()
	intents := make([]domain.OperationalIntent, 0)
	for rows.Next() {
		var raw []byte
		var revision int64
		if err := rows.Scan(&raw, &revision); err != nil {
			return nil, fmt.Errorf("scan operational intent: %w", err)
		}
		var intent domain.OperationalIntent
		if err := json.Unmarshal(raw, &intent); err != nil {
			return nil, fmt.Errorf("decode operational intent: %w", err)
		}
		intent.Revision = revision
		intents = append(intents, intent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operational intents: %w", err)
	}
	return intents, nil
}

func lockIntent(ctx context.Context, tx pgx.Tx, intentID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, intentID); err != nil {
		return fmt.Errorf("lock operational intent %q: %w", intentID, err)
	}
	return nil
}

func validateVolumeScope(intentID string, intentVersion int, volumes []domain.OperationalVolume) error {
	for _, volume := range volumes {
		if volume.IntentID != intentID || volume.IntentVersion != intentVersion {
			return fmt.Errorf("operational volume %q belongs to intent %q version %d, not intent %q version %d",
				volume.ID, volume.IntentID, volume.IntentVersion, intentID, intentVersion)
		}
	}
	return nil
}

func validateFindingScope(intentID string, intentVersion int, ruleVersion string, findings []domain.ConflictFinding) error {
	for _, finding := range findings {
		if finding.IntentID != intentID || finding.IntentVersion != intentVersion || finding.RuleVersion != ruleVersion {
			return fmt.Errorf("conflict finding %q is outside intent %q version %d rule %q replacement scope",
				finding.ID, intentID, intentVersion, ruleVersion)
		}
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
