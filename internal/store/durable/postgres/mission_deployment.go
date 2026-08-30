package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CreateMissionDeployment atomically stores one exact command or returns an
// existing exact idempotency replay.
func (s *Store) CreateMissionDeployment(ctx context.Context, deployment domain.MissionDeployment) (domain.MissionDeployment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("begin mission deployment create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	deployment, err = createMissionDeployment(ctx, tx, deployment)
	if err != nil {
		return domain.MissionDeployment{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("commit mission deployment create: %w", err)
	}
	return deployment, nil
}

// CreateMissionDeploymentForPlannedFlight records a deployment under the
// flight row lock shared with mission import and StartFlight.
func (s *Store) CreateMissionDeploymentForPlannedFlight(ctx context.Context, deployment domain.MissionDeployment) (domain.MissionDeployment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("begin planned-flight mission deployment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 3))`, deployment.IdempotencyKey); err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("lock mission deployment idempotency key: %w", err)
	}
	var replay *domain.MissionDeployment
	existing, err := getMissionDeploymentByIdempotencyKey(ctx, tx, deployment.IdempotencyKey)
	if err == nil {
		if existing.IdempotencyRequest != deployment.IdempotencyRequest {
			return domain.MissionDeployment{}, durable.ErrIdempotencyConflict
		}
		if !missionDeploymentOutstanding(existing.Status) {
			return existing, nil
		}
		replay = &existing
	}
	if err != nil && !errors.Is(err, durable.ErrNotFound) {
		return domain.MissionDeployment{}, err
	}
	var flightStatus domain.FlightStatus
	var operatorID, aircraftID, intentID string
	var intentVersion int
	if err := tx.QueryRow(ctx, `SELECT status,operator_id,aircraft_id,intent_id,intent_version FROM flight_records WHERE id=$1 FOR UPDATE`, deployment.FlightID).
		Scan(&flightStatus, &operatorID, &aircraftID, &intentID, &intentVersion); errors.Is(err, pgx.ErrNoRows) {
		return domain.MissionDeployment{}, durable.ErrNotFound
	} else if err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("lock deployment flight: %w", err)
	}
	if flightStatus != domain.FlightStatusPlanned || deployment.OperatorID != operatorID || deployment.AircraftID != aircraftID ||
		deployment.IntentID != intentID || deployment.IntentVersion != intentVersion {
		return domain.MissionDeployment{}, durable.ErrVersionConflict
	}
	if err := lockMissionAircraftLifecycle(ctx, tx, aircraftID); err != nil {
		return domain.MissionDeployment{}, err
	}
	if err := lockIntent(ctx, tx, intentID); err != nil {
		return domain.MissionDeployment{}, err
	}
	var currentIntentVersion int
	var currentIntentOperator, currentIntentAircraft, currentIntentStatus string
	if err := tx.QueryRow(ctx, `SELECT version,data->>'operator_id',aircraft_id,data->>'status' FROM operational_intents WHERE id=$1 ORDER BY version DESC LIMIT 1 FOR UPDATE`, intentID).
		Scan(&currentIntentVersion, &currentIntentOperator, &currentIntentAircraft, &currentIntentStatus); errors.Is(err, pgx.ErrNoRows) {
		return domain.MissionDeployment{}, durable.ErrNotFound
	} else if err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("lock current operational intent for mission deployment: %w", err)
	}
	intentStatus := domain.IntentStatus(currentIntentStatus)
	if currentIntentVersion != intentVersion || currentIntentOperator != operatorID || currentIntentAircraft != aircraftID ||
		(intentStatus != domain.IntentStatusAccepted && intentStatus != domain.IntentStatusActive) {
		return domain.MissionDeployment{}, durable.ErrVersionConflict
	}
	var activeFlight bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM flight_records WHERE aircraft_id=$1 AND status=$2)`, aircraftID, domain.FlightStatusActive).Scan(&activeFlight); err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("check active aircraft flight before deployment: %w", err)
	}
	if activeFlight {
		return domain.MissionDeployment{}, durable.ErrVersionConflict
	}
	var currentMissionID, currentMissionDigest string
	if err := tx.QueryRow(ctx, `SELECT id,mission_digest FROM missions WHERE flight_id=$1 ORDER BY version DESC LIMIT 1`, deployment.FlightID).
		Scan(&currentMissionID, &currentMissionDigest); errors.Is(err, pgx.ErrNoRows) {
		return domain.MissionDeployment{}, durable.ErrVersionConflict
	} else if err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("get current mission for deployment: %w", err)
	}
	if currentMissionID != deployment.MissionID || currentMissionDigest != deployment.MissionDigest {
		return domain.MissionDeployment{}, durable.ErrVersionConflict
	}
	replayID := ""
	if replay != nil {
		replayID = replay.ID
	}
	var outstanding bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM mission_deployments AS deployment
			JOIN flight_records AS flight ON flight.id=deployment.flight_id
			WHERE flight.aircraft_id=$1
			  AND flight.status=$2
			  AND deployment.id<>$3
			  AND deployment.status IN ($4,$5,$6)
			  AND deployment.mission_id=(SELECT id FROM missions WHERE flight_id=flight.id ORDER BY version DESC LIMIT 1)
		)`, aircraftID, domain.FlightStatusPlanned, replayID,
		domain.MissionDeploymentPending, domain.MissionDeploymentTemporaryError, domain.MissionDeploymentOutcomeUnknown).Scan(&outstanding); err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("check outstanding aircraft mission deployment: %w", err)
	}
	if outstanding {
		return domain.MissionDeployment{}, durable.ErrVersionConflict
	}
	if replay != nil {
		return *replay, nil
	}
	deployment, err = createMissionDeployment(ctx, tx, deployment)
	if err != nil {
		return domain.MissionDeployment{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("commit planned-flight mission deployment: %w", err)
	}
	return deployment, nil
}

func lockMissionAircraftLifecycle(ctx context.Context, tx pgx.Tx, aircraftID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 4))`, aircraftID); err != nil {
		return fmt.Errorf("lock aircraft mission lifecycle %q: %w", aircraftID, err)
	}
	return nil
}

func missionDeploymentOutstanding(status domain.MissionDeploymentStatus) bool {
	return status == domain.MissionDeploymentPending || status == domain.MissionDeploymentTemporaryError || status == domain.MissionDeploymentOutcomeUnknown
}

func rejectOutstandingMissionDeploymentForIntent(ctx context.Context, tx pgx.Tx, intentID string) error {
	var outstanding bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM mission_deployments AS deployment
			JOIN flight_records AS flight ON flight.id=deployment.flight_id
			WHERE flight.intent_id=$1
			  AND deployment.status IN ($2,$3,$4)
			  AND deployment.mission_id=(SELECT id FROM missions WHERE flight_id=flight.id ORDER BY version DESC LIMIT 1)
		)`, intentID, domain.MissionDeploymentPending, domain.MissionDeploymentTemporaryError, domain.MissionDeploymentOutcomeUnknown).Scan(&outstanding); err != nil {
		return fmt.Errorf("check operational intent mission deployment fence: %w", err)
	}
	if outstanding {
		return durable.ErrVersionConflict
	}
	return nil
}

func rejectOutstandingMissionDeploymentForFlight(ctx context.Context, tx pgx.Tx, flightID string) error {
	var outstanding bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM mission_deployments
			WHERE flight_id=$1
			  AND status IN ($2,$3,$4)
			  AND mission_id=(SELECT id FROM missions WHERE flight_id=$1 ORDER BY version DESC LIMIT 1)
		)`, flightID, domain.MissionDeploymentPending, domain.MissionDeploymentTemporaryError, domain.MissionDeploymentOutcomeUnknown).Scan(&outstanding); err != nil {
		return fmt.Errorf("check flight mission deployment fence: %w", err)
	}
	if outstanding {
		return durable.ErrVersionConflict
	}
	return nil
}

func createMissionDeployment(ctx context.Context, tx pgx.Tx, deployment domain.MissionDeployment) (domain.MissionDeployment, error) {
	var err error
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 3))`, deployment.IdempotencyKey); err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("lock mission deployment idempotency key: %w", err)
	}
	existing, err := getMissionDeploymentByIdempotencyKey(ctx, tx, deployment.IdempotencyKey)
	switch {
	case err == nil:
		if existing.IdempotencyRequest != deployment.IdempotencyRequest {
			return domain.MissionDeployment{}, durable.ErrIdempotencyConflict
		}
		return existing, nil
	case !errors.Is(err, durable.ErrNotFound):
		return domain.MissionDeployment{}, err
	}
	deployment.Revision = 0
	raw, err := encodeMissionDeployment(deployment)
	if err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("encode mission deployment: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO mission_deployments
		(id, flight_id, mission_id, idempotency_key, idempotency_request_hash, revision, status, created_at, updated_at, data)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		deployment.ID, deployment.FlightID, deployment.MissionID, deployment.IdempotencyKey,
		deployment.IdempotencyRequest, deployment.Revision, deployment.Status,
		deployment.CreatedAt, deployment.UpdatedAt, raw)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.MissionDeployment{}, durable.ErrAlreadyExists
		}
		return domain.MissionDeployment{}, fmt.Errorf("insert mission deployment: %w", err)
	}
	return deployment, nil
}

// GetMissionDeployment returns one durable deployment by identity.
func (s *Store) GetMissionDeployment(ctx context.Context, deploymentID string) (domain.MissionDeployment, error) {
	return getMissionDeployment(ctx, s.pool, `WHERE id = $1`, deploymentID)
}

// GetMissionDeploymentByIdempotencyKey returns one deployment request replay.
func (s *Store) GetMissionDeploymentByIdempotencyKey(ctx context.Context, key string) (domain.MissionDeployment, error) {
	return getMissionDeploymentByIdempotencyKey(ctx, s.pool, key)
}

// GetCurrentMissionDeploymentForFlight returns the authoritative deployment
// for the flight's current mission, preferring an unresolved retryable command
// over newer terminal history.
//
// Parameters:
//   - ctx: controls cancellation and the PostgreSQL read.
//   - flightID: identifies the flight whose current mission is being restored.
//
// Returns:
//   - result: is the outstanding or latest deployment for the current mission.
//   - error: is durable.ErrNotFound when the flight has no mission deployment.
func (s *Store) GetCurrentMissionDeploymentForFlight(ctx context.Context, flightID string) (domain.MissionDeployment, error) {
	return getMissionDeployment(ctx, s.pool, `
		WHERE flight_id=$1
		  AND mission_id=(SELECT id FROM missions WHERE flight_id=$1 ORDER BY version DESC LIMIT 1)
		ORDER BY
		  CASE WHEN status IN ($2,$3,$4) THEN 0 ELSE 1 END,
		  creation_order DESC
		LIMIT 1`, flightID, domain.MissionDeploymentPending, domain.MissionDeploymentTemporaryError, domain.MissionDeploymentOutcomeUnknown)
}

// UpdateMissionDeployment persists an observed result with optimistic concurrency.
func (s *Store) UpdateMissionDeployment(ctx context.Context, deployment domain.MissionDeployment, expectedRevision int64) error {
	deployment.Revision = expectedRevision + 1
	raw, err := encodeMissionDeployment(deployment)
	if err != nil {
		return fmt.Errorf("encode mission deployment: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE mission_deployments
		SET revision=$1, status=$2, updated_at=$3, data=$4
		WHERE id=$5 AND revision=$6 AND idempotency_key=$7 AND idempotency_request_hash=$8 AND mission_id=$9`,
		deployment.Revision, deployment.Status, deployment.UpdatedAt, raw, deployment.ID, expectedRevision,
		deployment.IdempotencyKey, deployment.IdempotencyRequest, deployment.MissionID)
	if err != nil {
		return fmt.Errorf("update mission deployment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, getErr := s.GetMissionDeployment(ctx, deployment.ID); errors.Is(getErr, durable.ErrNotFound) {
			return durable.ErrNotFound
		}
		return durable.ErrVersionConflict
	}
	return nil
}

type deploymentQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getMissionDeploymentByIdempotencyKey(ctx context.Context, query deploymentQuerier, key string) (domain.MissionDeployment, error) {
	return getMissionDeployment(ctx, query, `WHERE idempotency_key = $1`, key)
}

func getMissionDeployment(ctx context.Context, query deploymentQuerier, clause string, args ...any) (domain.MissionDeployment, error) {
	var deployment domain.MissionDeployment
	var raw []byte
	err := query.QueryRow(ctx, `
		SELECT idempotency_key, idempotency_request_hash, revision, data
		FROM mission_deployments `+clause, args...).Scan(
		&deployment.IdempotencyKey, &deployment.IdempotencyRequest, &deployment.Revision, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.MissionDeployment{}, durable.ErrNotFound
	}
	if err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("get mission deployment: %w", err)
	}
	key, requestHash, revision := deployment.IdempotencyKey, deployment.IdempotencyRequest, deployment.Revision
	if err := decodeMissionDeployment(raw, &deployment); err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("decode mission deployment: %w", err)
	}
	deployment.IdempotencyKey, deployment.IdempotencyRequest, deployment.Revision = key, requestHash, revision
	return deployment, nil
}

type missionDeploymentData struct {
	Deployment                   domain.MissionDeployment `json:"deployment"`
	AgentID                      string                   `json:"agent_id"`
	OperationContextCommandID    string                   `json:"operation_context_command_id"`
	ReconciliationClearCommandID string                   `json:"reconciliation_clear_command_id"`
}

func encodeMissionDeployment(deployment domain.MissionDeployment) ([]byte, error) {
	return json.Marshal(missionDeploymentData{
		Deployment: deployment, AgentID: deployment.AgentID,
		OperationContextCommandID:    deployment.OperationContextCommandID,
		ReconciliationClearCommandID: deployment.ReconciliationClearCommandID,
	})
}

func decodeMissionDeployment(raw []byte, deployment *domain.MissionDeployment) error {
	var data missionDeploymentData
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	*deployment = data.Deployment
	deployment.AgentID = data.AgentID
	deployment.OperationContextCommandID = data.OperationContextCommandID
	deployment.ReconciliationClearCommandID = data.ReconciliationClearCommandID
	return nil
}
