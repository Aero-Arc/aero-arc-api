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

// Open opens postgres and initializes the resources required for use.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - databaseURL: is the string value supplied to Open.
//
// Returns:
//   - result: is the *Store value produced by Open.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// Close releases resources owned by Store and completes any required shutdown work.
func (s *Store) Close() { s.pool.Close() }

// CreateMission atomically assigns and persists an immutable flight-local
// mission version with its ordered items. Exact idempotency retries return the
// original record; conflicting key reuse is rejected.
//
// Parameters:
//   - ctx: controls cancellation and the PostgreSQL transaction.
//   - mission: contains validated binding, hashes, ordered items, and idempotency metadata.
//
// Returns:
//   - result: is the stored mission with its assigned version.
//   - error: reports constraint, serialization, or conflicting idempotency failures.
func (s *Store) CreateMission(ctx context.Context, mission domain.Mission) (domain.Mission, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Mission{}, fmt.Errorf("begin mission create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	mission, err = createMission(ctx, tx, mission)
	if err != nil {
		return domain.Mission{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Mission{}, fmt.Errorf("commit mission create: %w", err)
	}
	return mission, nil
}

// CreateMissionForPlannedFlight creates a mission while holding the same
// PostgreSQL row lock used by flight activation.
func (s *Store) CreateMissionForPlannedFlight(ctx context.Context, mission domain.Mission) (domain.Mission, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Mission{}, fmt.Errorf("begin planned-flight mission create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 1))`, mission.IdempotencyKey); err != nil {
		return domain.Mission{}, fmt.Errorf("lock mission idempotency key: %w", err)
	}
	existing, err := getMissionByIdempotencyKey(ctx, tx, mission.IdempotencyKey)
	if err == nil {
		if existing.IdempotencyRequest != mission.IdempotencyRequest {
			return domain.Mission{}, durable.ErrIdempotencyConflict
		}
		return existing, nil
	}
	if !errors.Is(err, durable.ErrNotFound) {
		return domain.Mission{}, err
	}
	var status domain.FlightStatus
	var operatorID, aircraftID, intentID string
	var intentVersion int
	if err := tx.QueryRow(ctx, `SELECT status,operator_id,aircraft_id,intent_id,intent_version FROM flight_records WHERE id=$1 FOR UPDATE`, mission.FlightID).
		Scan(&status, &operatorID, &aircraftID, &intentID, &intentVersion); errors.Is(err, pgx.ErrNoRows) {
		return domain.Mission{}, durable.ErrNotFound
	} else if err != nil {
		return domain.Mission{}, fmt.Errorf("lock mission flight: %w", err)
	}
	if status != domain.FlightStatusPlanned || mission.OperatorID != operatorID || mission.AircraftID != aircraftID ||
		mission.IntentID != intentID || mission.IntentVersion != intentVersion {
		return domain.Mission{}, durable.ErrVersionConflict
	}
	if err := lockIntent(ctx, tx, intentID); err != nil {
		return domain.Mission{}, err
	}
	var currentIntentVersion int
	var currentIntentOperator, currentIntentAircraft, currentIntentStatus string
	if err := tx.QueryRow(ctx, `SELECT version,data->>'operator_id',aircraft_id,data->>'status' FROM operational_intents WHERE id=$1 ORDER BY version DESC LIMIT 1 FOR UPDATE`, intentID).
		Scan(&currentIntentVersion, &currentIntentOperator, &currentIntentAircraft, &currentIntentStatus); errors.Is(err, pgx.ErrNoRows) {
		return domain.Mission{}, durable.ErrNotFound
	} else if err != nil {
		return domain.Mission{}, fmt.Errorf("lock current operational intent for mission import: %w", err)
	}
	intentStatus := domain.IntentStatus(currentIntentStatus)
	if currentIntentVersion != intentVersion || currentIntentOperator != operatorID || currentIntentAircraft != aircraftID ||
		(intentStatus != domain.IntentStatusAccepted && intentStatus != domain.IntentStatusActive) {
		return domain.Mission{}, durable.ErrVersionConflict
	}
	if err := rejectOutstandingMissionDeploymentForFlight(ctx, tx, mission.FlightID); err != nil {
		return domain.Mission{}, err
	}
	mission, err = createMission(ctx, tx, mission)
	if err != nil {
		return domain.Mission{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Mission{}, fmt.Errorf("commit planned-flight mission create: %w", err)
	}
	return mission, nil
}

func createMission(ctx context.Context, tx pgx.Tx, mission domain.Mission) (domain.Mission, error) {
	var err error
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 1))`, mission.IdempotencyKey); err != nil {
		return domain.Mission{}, fmt.Errorf("lock mission idempotency key: %w", err)
	}
	existing, err := getMissionByIdempotencyKey(ctx, tx, mission.IdempotencyKey)
	switch {
	case err == nil:
		if existing.IdempotencyRequest != mission.IdempotencyRequest {
			return domain.Mission{}, durable.ErrIdempotencyConflict
		}
		return existing, nil
	case !errors.Is(err, durable.ErrNotFound):
		return domain.Mission{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 2))`, mission.FlightID); err != nil {
		return domain.Mission{}, fmt.Errorf("lock mission flight: %w", err)
	}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) + 1 FROM missions WHERE flight_id = $1`, mission.FlightID).Scan(&mission.Version); err != nil {
		return domain.Mission{}, fmt.Errorf("assign mission version: %w", err)
	}
	metadata := mission
	metadata.Items = nil
	raw, err := json.Marshal(metadata)
	if err != nil {
		return domain.Mission{}, fmt.Errorf("encode mission metadata: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO missions (
			id, operator_id, flight_id, aircraft_id, intent_id, intent_version, version,
			source_format, source_sha256, mission_digest, idempotency_key,
			idempotency_request_hash, created_at, data
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		mission.ID, mission.OperatorID, mission.FlightID, mission.AircraftID, mission.IntentID, mission.IntentVersion,
		mission.Version, mission.SourceFormat, mission.SourceSHA256, mission.MissionDigest,
		mission.IdempotencyKey, mission.IdempotencyRequest, mission.CreatedAt, raw)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.Mission{}, durable.ErrAlreadyExists
		}
		return domain.Mission{}, fmt.Errorf("insert mission: %w", err)
	}
	for _, item := range mission.Items {
		rawItem, marshalErr := json.Marshal(item)
		if marshalErr != nil {
			return domain.Mission{}, fmt.Errorf("encode mission item %d: %w", item.Sequence, marshalErr)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO mission_items (mission_id, sequence, data) VALUES ($1,$2,$3)`, mission.ID, item.Sequence, rawItem); err != nil {
			return domain.Mission{}, fmt.Errorf("insert mission item %d: %w", item.Sequence, err)
		}
	}
	return mission, nil
}

// GetMissionByIdempotencyKey returns the immutable mission stored for a request key.
//
// Parameters:
//   - ctx: controls cancellation and the PostgreSQL read.
//   - key: identifies the original import request.
//
// Returns:
//   - result: is the complete mission including ordered items.
//   - error: is durable.ErrNotFound when the key has not been used.
func (s *Store) GetMissionByIdempotencyKey(ctx context.Context, key string) (domain.Mission, error) {
	return getMissionByIdempotencyKey(ctx, s.pool, key)
}

// GetMission returns one immutable mission by identity.
func (s *Store) GetMission(ctx context.Context, missionID string) (domain.Mission, error) {
	return getMission(ctx, s.pool, `WHERE id = $1`, missionID)
}

// GetCurrentMissionForFlight returns the highest immutable mission version for one flight.
//
// Parameters:
//   - ctx: controls cancellation and the PostgreSQL read.
//   - flightID: identifies the flight binding.
//
// Returns:
//   - result: is the complete current mission.
//   - error: is durable.ErrNotFound when the flight has no mission.
func (s *Store) GetCurrentMissionForFlight(ctx context.Context, flightID string) (domain.Mission, error) {
	return getMission(ctx, s.pool, `WHERE flight_id = $1 ORDER BY version DESC LIMIT 1`, flightID)
}

// GetCurrentMissionForIntent returns the newest mission exactly bound to an aircraft and intent version.
//
// Parameters:
//   - ctx: controls cancellation and the PostgreSQL read.
//   - aircraftID: identifies the bound aircraft.
//   - intentID: identifies the operational intent.
//   - intentVersion: identifies its exact immutable version.
//
// Returns:
//   - result: is the complete newest matching mission.
//   - error: is durable.ErrNotFound when no mission matches.
func (s *Store) GetCurrentMissionForIntent(ctx context.Context, aircraftID string, intentID string, intentVersion int) (domain.Mission, error) {
	return getMission(ctx, s.pool, `WHERE aircraft_id = $1 AND intent_id = $2 AND intent_version = $3 ORDER BY created_at DESC, version DESC LIMIT 1`, aircraftID, intentID, intentVersion)
}

// GetDeployedMissionForActiveFlight returns the current mission for the exact
// active flight only when that mission has a verified terminal deployment.
//
// Parameters:
//   - ctx: controls cancellation and the PostgreSQL read.
//   - aircraftID: identifies the active flight's aircraft.
//   - intentID: identifies the active flight's operational intent.
//   - intentVersion: identifies the exact active intent version.
//
// Returns:
//   - result: is the verified mission commanded for the active flight.
//   - error: is durable.ErrNotFound when no exact active flight or verified current mission exists.
func (s *Store) GetDeployedMissionForActiveFlight(ctx context.Context, aircraftID string, intentID string, intentVersion int) (domain.Mission, error) {
	var activeFlightCount int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM flight_records
		WHERE aircraft_id=$1 AND intent_id=$2 AND intent_version=$3 AND status=$4`,
		aircraftID, intentID, intentVersion, domain.FlightStatusActive).Scan(&activeFlightCount); err != nil {
		return domain.Mission{}, fmt.Errorf("count active flights for commanded mission: %w", err)
	}
	if activeFlightCount == 0 {
		return domain.Mission{}, durable.ErrNotFound
	}
	if activeFlightCount > 1 {
		return domain.Mission{}, durable.ErrVersionConflict
	}
	return getMission(ctx, s.pool, `
		WHERE id = (
			SELECT mission.id
			FROM missions AS mission
			JOIN flight_records AS flight ON flight.id = mission.flight_id
			WHERE flight.aircraft_id=$1
			  AND flight.intent_id=$2
			  AND flight.intent_version=$3
			  AND flight.status=$4
			  AND mission.version=(
				SELECT MAX(current.version)
				FROM missions AS current
				WHERE current.flight_id=flight.id
			  )
			  AND EXISTS (
				SELECT 1
				FROM mission_deployments AS deployment
				WHERE deployment.flight_id=flight.id
				  AND deployment.mission_id=mission.id
				  AND deployment.status IN ($5,$6)
				  AND deployment.data->'deployment'->>'mission_digest'=mission.mission_digest
			  )
			LIMIT 1
		)`, aircraftID, intentID, intentVersion, domain.FlightStatusActive,
		domain.MissionDeploymentApplied, domain.MissionDeploymentAlreadyApplied)
}

type missionQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func getMissionByIdempotencyKey(ctx context.Context, query missionQuerier, key string) (domain.Mission, error) {
	return getMission(ctx, query, `WHERE idempotency_key = $1`, key)
}

func getMission(ctx context.Context, query missionQuerier, clause string, args ...any) (domain.Mission, error) {
	var mission domain.Mission
	var raw []byte
	err := query.QueryRow(ctx, `
		SELECT id, operator_id, flight_id, aircraft_id, intent_id, intent_version, version,
		       source_format, source_sha256, mission_digest, idempotency_key,
		       idempotency_request_hash, created_at, data
		FROM missions `+clause, args...).Scan(
		&mission.ID, &mission.OperatorID, &mission.FlightID, &mission.AircraftID, &mission.IntentID,
		&mission.IntentVersion, &mission.Version, &mission.SourceFormat,
		&mission.SourceSHA256, &mission.MissionDigest, &mission.IdempotencyKey,
		&mission.IdempotencyRequest, &mission.CreatedAt, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Mission{}, durable.ErrNotFound
	}
	if err != nil {
		return domain.Mission{}, fmt.Errorf("get mission: %w", err)
	}
	var metadata domain.Mission
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return domain.Mission{}, fmt.Errorf("decode mission metadata: %w", err)
	}
	mission.ValidationFindings = metadata.ValidationFindings
	rows, err := query.Query(ctx, `SELECT data FROM mission_items WHERE mission_id = $1 ORDER BY sequence`, mission.ID)
	if err != nil {
		return domain.Mission{}, fmt.Errorf("list mission items: %w", err)
	}
	defer rows.Close()
	mission.Items = make([]domain.MissionItem, 0)
	for rows.Next() {
		var rawItem []byte
		if err := rows.Scan(&rawItem); err != nil {
			return domain.Mission{}, fmt.Errorf("scan mission item: %w", err)
		}
		var item domain.MissionItem
		if err := json.Unmarshal(rawItem, &item); err != nil {
			return domain.Mission{}, fmt.Errorf("decode mission item: %w", err)
		}
		mission.Items = append(mission.Items, item)
	}
	if err := rows.Err(); err != nil {
		return domain.Mission{}, fmt.Errorf("iterate mission items: %w", err)
	}
	return mission, nil
}

// CreateOperationalIntent creates and stores the supplied Store record.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - intent: is the domain.OperationalIntent value supplied to CreateOperationalIntent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) CreateOperationalIntent(ctx context.Context, intent domain.OperationalIntent) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational intent create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, intent.ID); err != nil {
		return err
	}
	if err := rejectOutstandingMissionDeploymentForIntent(ctx, tx, intent.ID); err != nil {
		return err
	}
	intent.Revision = 0
	raw, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("encode operational intent: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO operational_intents (
			id, version, revision, aircraft_id, planned_start_at, planned_end_at, updated_at, data
		) VALUES ($1, $2, 0, $3, $4, $5, $6, $7)`,
		intent.ID, intent.Version, intent.AircraftID, intent.PlannedStartAt, intent.PlannedEndAt, intent.UpdatedAt, raw)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return durable.ErrAlreadyExists
		}
		return fmt.Errorf("create operational intent: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit operational intent create: %w", err)
	}
	return nil
}

// UpdateOperationalIntent updates the selected Store state while enforcing its consistency checks.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - intent: is the domain.OperationalIntent value supplied to UpdateOperationalIntent.
//   - expectedRevision: is the int64 value supplied to UpdateOperationalIntent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ActivateOperationalIntent atomically activates the supplied intent only
// when its aircraft has no other active operational intent. An aircraft-scoped
// transaction lock serializes competing activations across API replicas, and a
// partial unique index remains the final storage-level backstop.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the transaction.
//   - intent: is the accepted intent to transition to active.
//   - expectedRevision: fences the target intent against concurrent mutation.
//
// Returns:
//   - error: reports stale target state or durable.ErrActiveIntent when another
//     intent already owns the aircraft's active-flight lifecycle.
func (s *Store) ActivateOperationalIntent(ctx context.Context, intent domain.OperationalIntent, expectedRevision int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational intent activation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, intent.AircraftID); err != nil {
		return fmt.Errorf("lock aircraft operational lifecycle: %w", err)
	}
	if err = lockIntent(ctx, tx, intent.ID); err != nil {
		return err
	}
	var activeIntentID string
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM operational_intents
		WHERE aircraft_id = $1 AND (id <> $2 OR version <> $3) AND data->>'status' = $4
		LIMIT 1`, intent.AircraftID, intent.ID, intent.Version, domain.IntentStatusActive).Scan(&activeIntentID)
	switch {
	case err == nil:
		return durable.ErrActiveIntent
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("check active aircraft operational intent: %w", err)
	}
	if err = updateOperationalIntentTx(ctx, tx, intent, expectedRevision); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "operational_intents_one_active_aircraft_idx" {
			return durable.ErrActiveIntent
		}
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit operational intent activation: %w", err)
	}
	return nil
}

func updateOperationalIntentTx(ctx context.Context, tx pgx.Tx, intent domain.OperationalIntent, expectedRevision int64) error {
	if err := rejectOutstandingMissionDeploymentForIntent(ctx, tx, intent.ID); err != nil {
		return err
	}
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
	if intent.Status == domain.IntentStatusCanceled || intent.Status == domain.IntentStatusComplete {
		if err := retirePriorAcceptedIntentsTx(ctx, tx, intent); err != nil {
			return err
		}
	}
	return nil
}

func retirePriorAcceptedIntentsTx(ctx context.Context, tx pgx.Tx, terminal domain.OperationalIntent) error {
	rows, err := tx.Query(ctx, `
		SELECT data, revision
		FROM operational_intents
		WHERE id = $1 AND version < $2 AND data->>'status' = $3
		ORDER BY version`, terminal.ID, terminal.Version, domain.IntentStatusAccepted)
	if err != nil {
		return fmt.Errorf("list accepted operational intent versions for terminal transition: %w", err)
	}
	priorIntents, err := readIntents(rows)
	if err != nil {
		return err
	}
	for _, prior := range priorIntents {
		prior.Status = terminal.Status
		prior.CanceledAt = terminal.CanceledAt
		prior.CompletedAt = terminal.CompletedAt
		prior.UpdatedAt = terminal.UpdatedAt
		if err := upsertIntent(ctx, tx, prior, prior.Revision+1); err != nil {
			return err
		}
	}
	return nil
}

// AcceptOperationalIntent accepts the selected Store state after validating its current revision.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - intent: is the domain.OperationalIntent value supplied to AcceptOperationalIntent.
//   - expectedRevision: is the int64 value supplied to AcceptOperationalIntent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
	if err := rejectOutstandingMissionDeploymentForIntent(ctx, tx, intent.ID); err != nil {
		return err
	}
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

// GetOperationalIntent returns the current durable version of one operational intent.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - id: identifies the target record.
//
// Returns:
//   - result: is the domain.OperationalIntent value produced by GetOperationalIntent.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetOperationalIntent(ctx context.Context, id string) (domain.OperationalIntent, error) {
	return scanIntent(s.pool.QueryRow(ctx, `SELECT data, revision FROM operational_intents WHERE id = $1 ORDER BY version DESC, updated_at DESC LIMIT 1`, id))
}

// GetOperationalIntentVersion returns one immutable historical intent version.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - id: identifies the target record.
//   - version: is the int value supplied to GetOperationalIntentVersion.
//
// Returns:
//   - result: is the domain.OperationalIntent value produced by GetOperationalIntentVersion.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) GetOperationalIntentVersion(ctx context.Context, id string, version int) (domain.OperationalIntent, error) {
	return scanIntent(s.pool.QueryRow(ctx, `SELECT data, revision FROM operational_intents WHERE id = $1 AND version = $2`, id, version))
}

// ListOperationalIntents returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - aircraftID: identifies the target aircraft.
//
// Returns:
//   - result: is the []domain.OperationalIntent value produced by ListOperationalIntents.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListOperationalIntentVersions returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - id: identifies the target record.
//
// Returns:
//   - result: is the []domain.OperationalIntent value produced by ListOperationalIntentVersions.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) ListOperationalIntentVersions(ctx context.Context, id string) ([]domain.OperationalIntent, error) {
	rows, err := s.pool.Query(ctx, `SELECT data, revision FROM operational_intents WHERE id = $1 ORDER BY version, updated_at`, id)
	if err != nil {
		return nil, fmt.Errorf("list operational intent versions: %w", err)
	}
	return readIntents(rows)
}

// RecordOperationalVolume durably records the supplied Store data.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - volume: is the domain.OperationalVolume value supplied to RecordOperationalVolume.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (s *Store) RecordOperationalVolume(ctx context.Context, volume domain.OperationalVolume) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin operational volume write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIntent(ctx, tx, volume.IntentID); err != nil {
		return err
	}
	if err := rejectOutstandingMissionDeploymentForIntent(ctx, tx, volume.IntentID); err != nil {
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

// ReplaceOperationalVolumes atomically replaces the selected Store records.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - id: identifies the target record.
//   - version: is the int value supplied to ReplaceOperationalVolumes.
//   - volumes: is the []domain.OperationalVolume value supplied to ReplaceOperationalVolumes.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
	if err := rejectOutstandingMissionDeploymentForIntent(ctx, tx, id); err != nil {
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

// ReplaceOperationalIntent atomically replaces the selected Store records.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - expectedVersion: is the int value supplied to ReplaceOperationalIntent.
//   - expectedRevision: is the int64 value supplied to ReplaceOperationalIntent.
//   - intent: is the domain.OperationalIntent value supplied to ReplaceOperationalIntent.
//   - volumes: is the []domain.OperationalVolume value supplied to ReplaceOperationalIntent.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
	if err := rejectOutstandingMissionDeploymentForIntent(ctx, tx, intent.ID); err != nil {
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

// ListOperationalVolumes returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - intentID: identifies the target intent.
//
// Returns:
//   - result: is the []domain.OperationalVolume value produced by ListOperationalVolumes.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// RecordConflictFinding durably records the supplied Store data.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - finding: is the domain.ConflictFinding value supplied to RecordConflictFinding.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ListConflictFindings returns Store records matching the supplied scope and filters.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - intentID: identifies the target intent.
//   - intentVersion: is the int value supplied to ListConflictFindings.
//
// Returns:
//   - result: is the []domain.ConflictFinding value produced by ListConflictFindings.
//   - error: reports validation, dependency, cancellation, or persistence failures.
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

// ReplaceConflictFindings atomically replaces the selected Store records.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the operation.
//   - intentID: identifies the target intent.
//   - intentVersion: is the int value supplied to ReplaceConflictFindings.
//   - ruleVersion: is the string value supplied to ReplaceConflictFindings.
//   - findings: is the []domain.ConflictFinding value supplied to ReplaceConflictFindings.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
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
