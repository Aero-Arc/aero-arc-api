package postgis

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
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
		return nil, fmt.Errorf("apply PostGIS schema: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) ID() string {
	return "local_postgis"
}

func (s *Store) CreateOperationalIntent(ctx context.Context, intent domain.OperationalIntent) error {
	document, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("encode operational intent: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO operational_intents (id, version, aircraft_id, status, document, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		intent.ID, intent.Version, intent.AircraftID, intent.Status, document, intent.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert operational intent: %w", err)
	}
	return nil
}

func (s *Store) UpdateOperationalIntent(ctx context.Context, intent domain.OperationalIntent) error {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM operational_intents WHERE id = $1)`,
		intent.ID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check operational intent: %w", err)
	}
	if !exists {
		return durable.ErrNotFound
	}
	document, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("encode operational intent: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO operational_intents (id, version, aircraft_id, status, document, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id, version) DO UPDATE SET
			aircraft_id = EXCLUDED.aircraft_id,
			status = EXCLUDED.status,
			document = EXCLUDED.document,
			updated_at = EXCLUDED.updated_at`,
		intent.ID, intent.Version, intent.AircraftID, intent.Status, document, intent.UpdatedAt)
	if err != nil {
		return fmt.Errorf("update operational intent: %w", err)
	}
	return nil
}

func (s *Store) GetOperationalIntent(ctx context.Context, id string) (domain.OperationalIntent, error) {
	return scanIntent(s.pool.QueryRow(ctx, `
		SELECT document
		FROM operational_intents
		WHERE id = $1
		ORDER BY version DESC
		LIMIT 1`, id))
}

func (s *Store) GetOperationalIntentVersion(ctx context.Context, id string, version int) (domain.OperationalIntent, error) {
	return scanIntent(s.pool.QueryRow(ctx, `
		SELECT document
		FROM operational_intents
		WHERE id = $1 AND version = $2`, id, version))
}

func (s *Store) ListOperationalIntents(ctx context.Context, aircraftID string) ([]domain.OperationalIntent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (id) document
		FROM operational_intents
		WHERE ($1 = '' OR aircraft_id = $1)
		ORDER BY id, version DESC`, aircraftID)
	if err != nil {
		return nil, fmt.Errorf("list operational intents: %w", err)
	}
	defer rows.Close()
	return scanIntents(rows)
}

func (s *Store) ListOperationalIntentVersions(ctx context.Context, id string) ([]domain.OperationalIntent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT document
		FROM operational_intents
		WHERE id = $1
		ORDER BY version`, id)
	if err != nil {
		return nil, fmt.Errorf("list operational intent versions: %w", err)
	}
	defer rows.Close()
	return scanIntents(rows)
}

func (s *Store) RecordOperationalVolume(ctx context.Context, volume domain.OperationalVolume) error {
	return recordVolume(ctx, s.pool, volume)
}

func (s *Store) ReplaceOperationalVolumes(ctx context.Context, id string, version int, volumes []domain.OperationalVolume) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational volume replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`DELETE FROM operational_volumes WHERE intent_id = $1 AND intent_version = $2`,
		id, version,
	); err != nil {
		return fmt.Errorf("clear operational volumes: %w", err)
	}
	for _, volume := range volumes {
		if err := recordVolume(ctx, tx, volume); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit operational volume replacement: %w", err)
	}
	return nil
}

func (s *Store) ReplaceOperationalIntent(
	ctx context.Context,
	expectedVersion int,
	intent domain.OperationalIntent,
	volumes []domain.OperationalVolume,
) error {
	document, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("encode operational intent: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational intent replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, intent.ID); err != nil {
		return fmt.Errorf("lock operational intent: %w", err)
	}
	var currentVersion int
	if err := tx.QueryRow(ctx, `
		SELECT version
		FROM operational_intents
		WHERE id = $1
		ORDER BY version DESC
		LIMIT 1`, intent.ID).Scan(&currentVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return durable.ErrNotFound
		}
		return fmt.Errorf("read current operational intent version: %w", err)
	}
	if currentVersion != expectedVersion {
		return fmt.Errorf("%w: got %d, want %d", durable.ErrVersionConflict, currentVersion, expectedVersion)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO operational_intents (id, version, aircraft_id, status, document, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id, version) DO UPDATE SET
			aircraft_id = EXCLUDED.aircraft_id,
			status = EXCLUDED.status,
			document = EXCLUDED.document,
			updated_at = EXCLUDED.updated_at`,
		intent.ID, intent.Version, intent.AircraftID, intent.Status, document, intent.UpdatedAt,
	); err != nil {
		return fmt.Errorf("replace operational intent: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM operational_volumes WHERE intent_id = $1 AND intent_version = $2`,
		intent.ID, intent.Version,
	); err != nil {
		return fmt.Errorf("clear replacement operational volumes: %w", err)
	}
	for _, volume := range volumes {
		if err := recordVolume(ctx, tx, volume); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit operational intent replacement: %w", err)
	}
	return nil
}

func (s *Store) ListOperationalVolumes(ctx context.Context, id string) ([]domain.OperationalVolume, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT document
		FROM operational_volumes
		WHERE ($1 = '' OR intent_id = $1)
		ORDER BY intent_id, intent_version, id`, id)
	if err != nil {
		return nil, fmt.Errorf("list operational volumes: %w", err)
	}
	defer rows.Close()

	volumes := make([]domain.OperationalVolume, 0)
	for rows.Next() {
		var document []byte
		if err := rows.Scan(&document); err != nil {
			return nil, fmt.Errorf("scan operational volume: %w", err)
		}
		var volume domain.OperationalVolume
		if err := json.Unmarshal(document, &volume); err != nil {
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
	return recordFinding(ctx, s.pool, finding)
}

func (s *Store) ListConflictFindings(ctx context.Context, id string, version int) ([]domain.ConflictFinding, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT document
		FROM conflict_findings
		WHERE intent_id = $1 AND ($2 = 0 OR intent_version = $2)
		ORDER BY evaluated_at, id`, id, version)
	if err != nil {
		return nil, fmt.Errorf("list conflict findings: %w", err)
	}
	defer rows.Close()
	findings := make([]domain.ConflictFinding, 0)
	for rows.Next() {
		var document []byte
		if err := rows.Scan(&document); err != nil {
			return nil, fmt.Errorf("scan conflict finding: %w", err)
		}
		var finding domain.ConflictFinding
		if err := json.Unmarshal(document, &finding); err != nil {
			return nil, fmt.Errorf("decode conflict finding: %w", err)
		}
		findings = append(findings, finding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conflict findings: %w", err)
	}
	return findings, nil
}

func (s *Store) ReplaceConflictFindings(ctx context.Context, id string, version int, ruleVersion string, findings []domain.ConflictFinding) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin conflict finding replacement: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		DELETE FROM conflict_findings
		WHERE intent_id = $1 AND intent_version = $2 AND rule_version = $3`,
		id, version, ruleVersion,
	); err != nil {
		return fmt.Errorf("clear conflict findings: %w", err)
	}
	for _, finding := range findings {
		if err := recordFinding(ctx, tx, finding); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit conflict finding replacement: %w", err)
	}
	return nil
}

func (s *Store) FindOperationalIntents(ctx context.Context, query airspaceprovider.Query) ([]airspaceprovider.OperationalIntent, error) {
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
		WITH latest_intents AS (
			SELECT DISTINCT ON (id) id, version, document, status
			FROM operational_intents
			WHERE status IN ('accepted', 'active')
			ORDER BY id, version DESC
		),
		targets AS (
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
		SELECT intent.document, volume.document
		FROM latest_intents intent
		JOIN operational_volumes volume
			ON volume.intent_id = intent.id AND volume.intent_version = intent.version
		WHERE intent.id <> $1
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
		ORDER BY intent.id, volume.id`, query.Intent.ID, targets)
	if err != nil {
		return nil, fmt.Errorf("query PostGIS candidates: %w", err)
	}
	defer rows.Close()

	byIntent := make(map[string]*airspaceprovider.OperationalIntent)
	order := make([]string, 0)
	for rows.Next() {
		var intentDocument, volumeDocument []byte
		if err := rows.Scan(&intentDocument, &volumeDocument); err != nil {
			return nil, fmt.Errorf("scan PostGIS candidate: %w", err)
		}
		var intent domain.OperationalIntent
		var volume domain.OperationalVolume
		if err := json.Unmarshal(intentDocument, &intent); err != nil {
			return nil, fmt.Errorf("decode PostGIS candidate intent: %w", err)
		}
		if err := json.Unmarshal(volumeDocument, &volume); err != nil {
			return nil, fmt.Errorf("decode PostGIS candidate volume: %w", err)
		}
		record := byIntent[intent.ID]
		if record == nil {
			record = &airspaceprovider.OperationalIntent{
				Source: airspaceprovider.Source{
					ReferenceID: intent.ID,
					Manager:     intent.OperatorID,
					Version:     intent.Version,
					Local:       true,
				},
				Intent: intent,
			}
			byIntent[intent.ID] = record
			order = append(order, intent.ID)
		}
		if volume.VolumeType == domain.OperationalVolumeContingency ||
			volume.VolumeType == domain.OperationalVolumeEmergency {
			record.OffNominalVolumes = append(record.OffNominalVolumes, volume)
		} else {
			record.Volumes = append(record.Volumes, volume)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate PostGIS candidates: %w", err)
	}
	sort.Strings(order)
	records := make([]airspaceprovider.OperationalIntent, 0, len(order))
	for _, id := range order {
		records = append(records, *byIntent[id])
	}
	return records, nil
}

type querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func recordVolume(ctx context.Context, db querier, volume domain.OperationalVolume) error {
	document, err := json.Marshal(volume)
	if err != nil {
		return fmt.Errorf("encode operational volume: %w", err)
	}
	geometry, err := geometryJSON(volume.GeoJSON)
	if err != nil {
		return fmt.Errorf("read operational volume geometry: %w", err)
	}
	buffer := 0.0
	if volume.BufferMeters != nil {
		buffer = *volume.BufferMeters
	}
	_, err = db.Exec(ctx, `
		INSERT INTO operational_volumes (
			intent_id, intent_version, id, altitude_reference,
			min_altitude_m, max_altitude_m, starts_at, ends_at,
			buffer_meters, footprint, document
		)
		VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9,
			CASE
				WHEN NULLIF($10, '') IS NULL THEN NULL
				ELSE ST_SetSRID(ST_GeomFromGeoJSON($10), 4326)
			END,
			$11
		)
		ON CONFLICT (intent_id, intent_version, id) DO UPDATE SET
			altitude_reference = EXCLUDED.altitude_reference,
			min_altitude_m = EXCLUDED.min_altitude_m,
			max_altitude_m = EXCLUDED.max_altitude_m,
			starts_at = EXCLUDED.starts_at,
			ends_at = EXCLUDED.ends_at,
			buffer_meters = EXCLUDED.buffer_meters,
			footprint = EXCLUDED.footprint,
			document = EXCLUDED.document`,
		volume.IntentID, volume.IntentVersion, volume.ID, volume.AltitudeRef,
		volume.MinAltitudeM, volume.MaxAltitudeM, volume.StartsAt, volume.EndsAt,
		buffer, geometry, document)
	if err != nil {
		return fmt.Errorf("write operational volume: %w", err)
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

func recordFinding(ctx context.Context, db querier, finding domain.ConflictFinding) error {
	document, err := json.Marshal(finding)
	if err != nil {
		return fmt.Errorf("encode conflict finding: %w", err)
	}
	_, err = db.Exec(ctx, `
		INSERT INTO conflict_findings (
			id, intent_id, intent_version, rule_version, evaluated_at, document
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (intent_id, intent_version, rule_version, id) DO UPDATE SET
			evaluated_at = EXCLUDED.evaluated_at,
			document = EXCLUDED.document`,
		finding.ID, finding.IntentID, finding.IntentVersion,
		finding.RuleVersion, finding.EvaluatedAt, document)
	if err != nil {
		return fmt.Errorf("write conflict finding: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanIntent(row rowScanner) (domain.OperationalIntent, error) {
	var document []byte
	if err := row.Scan(&document); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.OperationalIntent{}, durable.ErrNotFound
		}
		return domain.OperationalIntent{}, fmt.Errorf("scan operational intent: %w", err)
	}
	var intent domain.OperationalIntent
	if err := json.Unmarshal(document, &intent); err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("decode operational intent: %w", err)
	}
	return intent, nil
}

func scanIntents(rows pgx.Rows) ([]domain.OperationalIntent, error) {
	intents := make([]domain.OperationalIntent, 0)
	for rows.Next() {
		intent, err := scanIntent(rows)
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operational intents: %w", err)
	}
	return intents, nil
}
