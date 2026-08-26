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
	if err = tx.Commit(ctx); err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("commit mission deployment create: %w", err)
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
	Deployment                domain.MissionDeployment `json:"deployment"`
	AgentID                   string                   `json:"agent_id"`
	OperationContextCommandID string                   `json:"operation_context_command_id"`
}

func encodeMissionDeployment(deployment domain.MissionDeployment) ([]byte, error) {
	return json.Marshal(missionDeploymentData{
		Deployment: deployment, AgentID: deployment.AgentID,
		OperationContextCommandID: deployment.OperationContextCommandID,
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
	return nil
}
