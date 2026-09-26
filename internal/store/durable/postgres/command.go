package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/aero-arc/aero-arc-protos/commanddigest"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	"github.com/aero-arc/aero-arc-protos/missiondigest"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

// AcceptCommand commits acceptance, audit events, and dispatch obligation under
// flight/aircraft/intent fences. Mission upload also reserves its existing
// deployment record in the same transaction. It performs no network dispatch.
// Exact scoped idempotency replays return the original command.
//
// Parameters: ctx bounds the transaction; c is immutable authority; deployment optionally reserves the existing mission workflow.
//
// Returns: The original command on exact replay, or the accepted record; validation, binding, idempotency, and database errors fail without dispatch.
func (s *Store) AcceptCommand(ctx context.Context, c domain.Command, deployment *domain.MissionDeployment) (domain.Command, error) {
	var cmd pb.DurableCommand
	if err := proto.Unmarshal(c.Payload, &cmd); err != nil {
		return c, err
	}
	digest, err := commanddigest.Digest(&cmd)
	if err != nil || cmd.CommandId != c.ID || digest != c.Digest || digest != cmd.CommandDigest || cmd.Definition != c.Type || cmd.OperatorId != c.OperatorID || cmd.AircraftId != c.AircraftID || cmd.Context.FlightId != c.FlightID || cmd.ExpiresAtUnixMs != c.ExpiresAt.UnixMilli() {
		return c, fmt.Errorf("invalid immutable command record")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 7))`, c.OperatorID+"/"+c.IdempotencyKey); err != nil {
		return c, err
	}
	var id, hash string
	err = tx.QueryRow(ctx, `SELECT id,request_hash FROM commands WHERE operator_id=$1 AND idempotency_key=$2`, c.OperatorID, c.IdempotencyKey).Scan(&id, &hash)
	if err == nil {
		if hash != c.RequestHash {
			return c, durable.ErrIdempotencyConflict
		}
		if err = tx.Rollback(ctx); err != nil {
			return c, err
		}
		return s.GetCommand(ctx, id)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return c, err
	}
	var flightStatus, operator, aircraft, intent string
	var version int
	if err = tx.QueryRow(ctx, `SELECT status,operator_id,aircraft_id,intent_id,intent_version FROM flight_records WHERE id=$1 FOR UPDATE`, c.FlightID).Scan(&flightStatus, &operator, &aircraft, &intent, &version); err != nil {
		return c, err
	}
	if err = lockMissionAircraftLifecycle(ctx, tx, aircraft); err != nil {
		return c, err
	}
	if err = lockIntent(ctx, tx, intent); err != nil {
		return c, err
	}
	if operator != c.OperatorID || aircraft != c.AircraftID || cmd.Context.IntentId != intent || int(cmd.Context.IntentVersion) != version {
		return c, durable.ErrVersionConflict
	}
	var agentID string
	if err = tx.QueryRow(ctx, `SELECT data->>'agent_id' FROM aircraft WHERE id=$1 FOR UPDATE`, aircraft).Scan(&agentID); err != nil {
		return c, err
	}
	if agentID != cmd.AgentId {
		return c, durable.ErrVersionConflict
	}
	var intentStatus string
	var currentVersion int
	if err = tx.QueryRow(ctx, `SELECT version,data->>'status' FROM operational_intents WHERE id=$1 ORDER BY version DESC LIMIT 1 FOR UPDATE`, intent).Scan(&currentVersion, &intentStatus); err != nil {
		return c, err
	}
	if currentVersion != version || (intentStatus != "active" && !(c.Type == "MISSION_UPLOAD" && intentStatus == "accepted")) {
		return c, durable.ErrVersionConflict
	}
	if flightStatus != "planned" && flightStatus != "active" {
		return c, durable.ErrVersionConflict
	}
	var anotherActive bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM flight_records WHERE aircraft_id=$1 AND id<>$2 AND status='active')`, aircraft, c.FlightID).Scan(&anotherActive); err != nil {
		return c, err
	}
	if anotherActive {
		return c, durable.ErrVersionConflict
	}
	var outstanding bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM commands WHERE aircraft_id=$1 AND state NOT IN ('applied','rejected','failed','timed_out'))`, aircraft).Scan(&outstanding); err != nil {
		return c, err
	}
	if outstanding {
		return c, fmt.Errorf("%w: aircraft has unresolved command", durable.ErrVersionConflict)
	}
	if deployment != nil {
		d, e := admitMissionDeployment(ctx, tx, *deployment)
		if e != nil {
			return c, e
		}
		c.DeploymentID = d.ID
	} else {
		if err = rejectOutstandingMissionDeploymentForFlight(ctx, tx, c.FlightID); err != nil {
			return c, err
		}
		if c.Type == "MISSION_START" || c.Type == "RESUME" {
			var verified bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM missions m JOIN mission_deployments d ON d.mission_id=m.id WHERE m.flight_id=$1 AND m.id=(SELECT id FROM missions WHERE flight_id=$1 ORDER BY version DESC LIMIT 1) AND d.status IN ('applied','already_applied') AND d.id=(SELECT md.id FROM mission_deployments md JOIN flight_records f ON f.id=md.flight_id WHERE f.aircraft_id=$2 ORDER BY md.creation_order DESC LIMIT 1))`, c.FlightID, aircraft).Scan(&verified)
			if err != nil {
				return c, err
			}
			if !verified {
				return c, fmt.Errorf("%w: current onboard mission is not verified", durable.ErrVersionConflict)
			}
			var currentID, currentDigest string
			var currentVersion uint32
			if err = tx.QueryRow(ctx, `SELECT id,version,mission_digest FROM missions WHERE flight_id=$1 ORDER BY version DESC LIMIT 1`, c.FlightID).Scan(&currentID, &currentVersion, &currentDigest); err != nil {
				return c, err
			}
			m := cmd.GetMavlink()
			if m == nil || m.MissionPrecondition == nil || m.MissionPreconditionId != currentID || m.MissionPreconditionVersion != currentVersion {
				return c, durable.ErrVersionConflict
			}
			digest, e := missiondigest.Digest(m.MissionPrecondition)
			if e != nil || digest != currentDigest {
				return c, durable.ErrVersionConflict
			}
		}
	}
	c.State = "accepted"
	c.ObservationState = "pending"
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&c.CreatedAt); err != nil {
		return c, err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO commands(id,operator_id,aircraft_id,flight_id,idempotency_key,request_hash,digest,payload,data,state,created_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'accepted',$10,$11)`, c.ID, c.OperatorID, c.AircraftID, c.FlightID, c.IdempotencyKey, c.RequestHash, c.Digest, c.Payload, data, c.CreatedAt, c.ExpiresAt)
	if err != nil {
		return c, err
	}
	for _, stage := range []string{"requested", "authorized", "accepted"} {
		_, err = tx.Exec(ctx, `INSERT INTO command_events(event_id,command_id,stage,occurred_at,source,message) VALUES($1,$2,$3,$4,'api',$5)`, c.ID+"/"+stage, c.ID, stage, c.CreatedAt, c.RequestedBy)
		if err != nil {
			return c, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO command_outbox(command_id) VALUES($1)`, c.ID); err != nil {
		return c, err
	}
	if err = tx.Commit(ctx); err != nil {
		return c, err
	}
	return s.GetCommand(ctx, c.ID)
}

// GetCommand loads authoritative state and ordered immutable execution evidence.
//
// Parameters: ctx bounds reads; id is the stable command identity.
//
// Returns: The persisted projection and immutable events, ErrNotFound for missing identity, or a database error.
func (s *Store) GetCommand(ctx context.Context, id string) (domain.Command, error) {
	var c domain.Command
	var data []byte
	err := s.pool.QueryRow(ctx, `SELECT data,payload,state,observation_state FROM commands WHERE id=$1`, id).Scan(&data, &c.Payload, &c.State, &c.ObservationState)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, durable.ErrNotFound
	}
	if err != nil {
		return c, err
	}
	payload, state, obs := c.Payload, c.State, c.ObservationState
	if err = json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	c.Payload = payload
	c.State = state
	c.ObservationState = obs
	rows, err := s.pool.Query(ctx, `SELECT event_id,stage,occurred_at,received_at,source,message FROM command_events WHERE command_id=$1 ORDER BY occurred_at,event_id`, id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	c.Events = []domain.CommandEvent{}
	for rows.Next() {
		var e domain.CommandEvent
		if err = rows.Scan(&e.ID, &e.Stage, &e.OccurredAt, &e.ReceivedAt, &e.Source, &e.Message); err != nil {
			return c, err
		}
		c.Events = append(c.Events, e)
	}
	if err = rows.Err(); err != nil {
		return c, err
	}
	err = s.pool.QueryRow(ctx, `SELECT attempts FROM command_outbox WHERE command_id=$1`, id).Scan(&c.Attempts)
	return c, err
}

// FindCommand restores an organization-scoped submission after response loss.
//
// Parameters: ctx bounds reads; operator and key form the submission namespace.
//
// Returns: The original command and request hash, ErrNotFound, or a database error.
func (s *Store) FindCommand(ctx context.Context, operator, key string) (domain.Command, error) {
	var id, hash string
	err := s.pool.QueryRow(ctx, `SELECT id,request_hash FROM commands WHERE operator_id=$1 AND idempotency_key=$2`, operator, key).Scan(&id, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Command{}, durable.ErrNotFound
	}
	if err != nil {
		return domain.Command{}, err
	}
	c, err := s.GetCommand(ctx, id)
	c.RequestHash = hash
	return c, err
}

// ListCommands returns complete flight command history, newest first, for Ops/replay.
//
// Parameters: ctx bounds reads; flight is the exact durable flight identity.
//
// Returns: Complete command records in reverse acceptance order, or a database error.
func (s *Store) ListCommands(ctx context.Context, flight string) ([]domain.Command, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM commands WHERE flight_id=$1 ORDER BY created_at DESC,id`, flight)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []domain.Command{}
	for _, id := range ids {
		c, e := s.GetCommand(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, nil
}

// ClaimCommand leases one outbox entry with database time and a new fencing generation.
// Expired leases are recoverable; the immutable command remains unchanged.
//
// Parameters: ctx bounds a database transaction.
//
// Returns: A command with attempt number and fencing generation; pgx.ErrNoRows when no work is available, or a database error.
func (s *Store) ClaimCommand(ctx context.Context) (domain.Command, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Command{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	var lease int64
	var attempt int
	err = tx.QueryRow(ctx, `WITH next AS (SELECT command_id FROM command_outbox WHERE NOT done AND available_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY available_at,command_id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE command_outbox o SET lease_until=clock_timestamp()+interval '150 seconds',generation=generation+1,attempts=attempts+1 FROM next WHERE o.command_id=next.command_id RETURNING o.command_id,o.generation,o.attempts`).Scan(&id, &lease, &attempt)
	if err != nil {
		return domain.Command{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO command_attempts(command_id,attempt) VALUES($1,$2)`, id, attempt); err != nil {
		return domain.Command{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Command{}, err
	}
	c, err := s.GetCommand(ctx, id)
	c.Lease = lease
	c.Attempts = attempt
	return c, err
}

// FinishCommandAttempt atomically records immutable evidence and advances its
// projection only behind an unexpired lease. Ambiguous outcomes remain blockers.
//
// Parameters: ctx bounds persistence; c carries the claimed generation; events are immutable evidence; result describes the delivery attempt.
//
// Returns: Nil after atomic projection and outbox update; stale leases, contradictory evidence, and database errors roll back all updates.
func (s *Store) FinishCommandAttempt(ctx context.Context, c domain.Command, events []domain.CommandEvent, result string) error {
	return s.recordCommandEvidence(ctx, c, events, result, true)
}

// RecordCommandProgress persists immutable evidence and updates the projection
// while retaining the delivery lease for subsequent streamed progress.
//
// Parameters: ctx bounds persistence; c carries the claimed lease generation;
// events contain immutable source evidence.
// Returns nil after commit, or a lease, evidence conflict, or database error.
func (s *Store) RecordCommandProgress(ctx context.Context, c domain.Command, events []domain.CommandEvent) error {
	return s.recordCommandEvidence(ctx, c, events, "", false)
}

func (s *Store) recordCommandEvidence(ctx context.Context, c domain.Command, events []domain.CommandEvent, result string, finish bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var generation int64
	if err = tx.QueryRow(ctx, `SELECT generation FROM command_outbox WHERE command_id=$1 AND lease_until>clock_timestamp() FOR UPDATE`, c.ID).Scan(&generation); err != nil {
		return err
	}
	if generation != c.Lease {
		return durable.ErrVersionConflict
	}
	for _, e := range events {
		tag, er := tx.Exec(ctx, `INSERT INTO command_events(event_id,command_id,stage,occurred_at,source,message) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, e.ID, c.ID, e.Stage, e.OccurredAt, e.Source, e.Message)
		if er != nil {
			return er
		}
		if tag.RowsAffected() == 0 {
			var equal bool
			er = tx.QueryRow(ctx, `SELECT command_id=$2 AND stage=$3 AND occurred_at=$4 AND source=$5 AND message=$6 FROM command_events WHERE event_id=$1`, e.ID, c.ID, e.Stage, e.OccurredAt, e.Source, e.Message).Scan(&equal)
			if er != nil {
				return er
			}
			if !equal {
				return fmt.Errorf("immutable command evidence conflict")
			}
		}
	}
	var contradictory bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage='applied') AND EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage IN ('rejected','timed_out'))`, c.ID).Scan(&contradictory); err != nil {
		return err
	}
	if contradictory {
		return fmt.Errorf("conflicting terminal command evidence")
	}
	_, err = tx.Exec(ctx, `UPDATE commands SET state=CASE WHEN EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage='applied') THEN 'applied' WHEN EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage='rejected') THEN 'rejected' WHEN EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage='timed_out') THEN 'timed_out' WHEN EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage='outcome_unknown') THEN 'outcome_unknown' WHEN EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage='acknowledged') THEN 'acknowledged' WHEN EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage='delivery_unknown') THEN 'outcome_unknown' WHEN EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage='dispatched') THEN 'dispatched' ELSE state END, observation_state=CASE WHEN EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage='observed') THEN 'observed' WHEN EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage='observation_unavailable') THEN 'unavailable' WHEN EXISTS(SELECT 1 FROM command_events WHERE command_id=$1 AND stage='observation_superseded') THEN 'superseded' ELSE observation_state END WHERE id=$1`, c.ID)
	if err != nil {
		return err
	}
	if c.Type == "MISSION_START" {
		var appliedAt time.Time
		err = tx.QueryRow(ctx, `SELECT occurred_at FROM command_events WHERE command_id=$1 AND stage='applied'`, c.ID).Scan(&appliedAt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			var raw []byte
			if err = tx.QueryRow(ctx, `SELECT data FROM flight_records WHERE id=$1 FOR UPDATE`, c.FlightID).Scan(&raw); err != nil {
				return err
			}
			var flight domain.FlightRecord
			if err = json.Unmarshal(raw, &flight); err != nil {
				return err
			}
			if err = lockMissionAircraftLifecycle(ctx, tx, c.AircraftID); err != nil {
				return err
			}
			if flight.Status == domain.FlightStatusPlanned {
				flight.Status = domain.FlightStatusActive
				flight.StartedAt = appliedAt
				raw, err = json.Marshal(flight)
				if err != nil {
					return err
				}
				if _, err = tx.Exec(ctx, `UPDATE flight_records SET status='active',started_at=$2,data=$3 WHERE id=$1`, flight.ID, appliedAt, raw); err != nil {
					return err
				}
			}
		}
	}
	if !finish {
		return tx.Commit(ctx)
	}
	var envelope pb.DurableCommand
	if err = proto.Unmarshal(c.Payload, &envelope); err != nil {
		return err
	}
	relayID := ""
	for _, e := range events {
		if strings.HasPrefix(e.Source, "relay:") {
			relayID = strings.TrimPrefix(e.Source, "relay:")
		}
	}
	_, err = tx.Exec(ctx, `UPDATE command_attempts SET finished_at=clock_timestamp(),result=$3,agent_id=$4,relay_id=$5 WHERE command_id=$1 AND attempt=$2`, c.ID, c.Attempts, result, envelope.AgentId, relayID)
	if err != nil {
		return err
	}
	delay := time.Duration(1<<min(c.Attempts, 6)) * time.Second
	tag, err := tx.Exec(ctx, `UPDATE command_outbox SET lease_until=NULL,available_at=clock_timestamp()+$2::interval,done=(SELECT state IN ('rejected','timed_out') OR (state='applied' AND observation_state IN ('observed','unavailable','superseded')) OR expires_at+interval '15 minutes'<clock_timestamp() FROM commands WHERE id=$1) WHERE command_id=$1 AND generation=$3 AND lease_until>clock_timestamp()`, c.ID, fmt.Sprintf("%f seconds", delay.Seconds()), c.Lease)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return durable.ErrVersionConflict
	}
	return tx.Commit(ctx)
}

func rejectOutstandingC2ForAircraft(ctx context.Context, tx pgx.Tx, aircraftID, exceptID string) error {
	var outstanding bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM commands WHERE aircraft_id=$1 AND id<>$2 AND state NOT IN ('applied','rejected','failed','timed_out'))`, aircraftID, exceptID).Scan(&outstanding); err != nil {
		return err
	}
	if outstanding {
		return durable.ErrVersionConflict
	}
	return nil
}

// RequeueCommand schedules an exact command for evidence recovery without changing
// its payload, expiry, or any active lease. It never authorizes a replacement effect.
//
// Parameters: ctx bounds the write; id selects existing immutable authority.
//
// Returns: Nil when scheduled, ErrNotFound for an unknown command, or a database error.
func (s *Store) RequeueCommand(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE command_outbox SET done=false,available_at=clock_timestamp() WHERE command_id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return durable.ErrNotFound
	}
	return nil
}
