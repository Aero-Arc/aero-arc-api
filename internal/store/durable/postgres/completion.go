package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/aero-arc/aero-arc-protos/flightcompletion"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	"github.com/aero-arc/aero-arc-protos/missiondigest"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

// AdmitFlightCompletion validates aircraft evidence against immutable API authority
// and commits an idempotent inbox obligation. Parameters: ctx bounds persistence;
// e carries authenticated Agent evidence. Returns: binding/conflict/storage errors.
func (s *Store) AdmitFlightCompletion(ctx context.Context, e *pb.FlightCompletionEvidence) error {
	raw, digest, err := flightcompletion.Encode(e)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var flightRaw, commandRaw []byte
	if err = tx.QueryRow(ctx, `SELECT data FROM flight_records WHERE id=$1 FOR UPDATE`, e.Context.FlightId).Scan(&flightRaw); err != nil {
		return err
	}
	var flight domain.FlightRecord
	if err = json.Unmarshal(flightRaw, &flight); err != nil {
		return err
	}
	if flight.AircraftID != e.Context.AircraftId || flight.IntentID != e.Context.IntentId || flight.IntentVersion != int(e.Context.IntentVersion) {
		return durable.ErrVersionConflict
	}
	var oldID, oldDigest string
	err = tx.QueryRow(ctx, `SELECT event_id,digest FROM flight_completions WHERE flight_id=$1`, flight.ID).Scan(&oldID, &oldDigest)
	if err == nil {
		if oldID != e.EventId || oldDigest != digest {
			return durable.ErrIdempotencyConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if flight.Status != domain.FlightStatusActive {
		return durable.ErrVersionConflict
	}
	if err = tx.QueryRow(ctx, `SELECT payload FROM commands WHERE id=$1 AND flight_id=$2`, e.StartCommandId, flight.ID).Scan(&commandRaw); err != nil {
		return err
	}
	c := new(pb.DurableCommand)
	if err = proto.Unmarshal(commandRaw, c); err != nil {
		return err
	}
	if c.Definition != "MISSION_START" || c.AgentId != e.AgentId || !proto.Equal(c.Context, e.Context) || c.GetMavlink().GetMissionPreconditionId() != e.MissionId {
		return durable.ErrVersionConflict
	}
	missionHash, err := missiondigest.Digest(c.GetMavlink().GetMissionPrecondition())
	if err != nil || missionHash != e.MissionDigest {
		return durable.ErrVersionConflict
	}
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return err
	}
	if e.AirborneAtUnixNs < flight.StartedAt.UnixNano() || max(e.LandedAtUnixNs, e.DisarmedAtUnixNs) > databaseNow.Add(5*time.Second).UnixNano() {
		return fmt.Errorf("completion evidence outside flight timeline")
	}
	_, err = tx.Exec(ctx, `INSERT INTO flight_completions(event_id,flight_id,digest,payload) VALUES($1,$2,$3,$4)`, e.EventId, flight.ID, digest, raw)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanCompletion(row pgx.Row) (domain.FlightCompletion, error) {
	var c domain.FlightCompletion
	var raw []byte
	if err := row.Scan(&raw, &c.State, &c.Attempts, &c.Generation, &c.Error); err != nil {
		return c, err
	}
	c.Evidence = new(pb.FlightCompletionEvidence)
	err := proto.Unmarshal(raw, c.Evidence)
	return c, err
}

// GetFlightCompletion reads evidence and finalization progress for a flight.
func (s *Store) GetFlightCompletion(ctx context.Context, flightID string) (domain.FlightCompletion, error) {
	c, err := scanCompletion(s.pool.QueryRow(ctx, `SELECT payload,state,attempts,generation,last_error FROM flight_completions WHERE flight_id=$1`, flightID))
	if errors.Is(err, pgx.ErrNoRows) {
		err = durable.ErrNotFound
	}
	return c, err
}

// ClaimFlightCompletion takes one due obligation under a database-clock lease.
func (s *Store) ClaimFlightCompletion(ctx context.Context) (domain.FlightCompletion, error) {
	c, err := scanCompletion(s.pool.QueryRow(ctx, `WITH due AS (SELECT event_id FROM flight_completions WHERE state<>'complete' AND available_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<=clock_timestamp()) ORDER BY available_at,event_id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE flight_completions c SET state='finalizing',attempts=attempts+1,generation=generation+1,lease_until=clock_timestamp()+interval '90 seconds' FROM due WHERE c.event_id=due.event_id RETURNING c.payload,c.state,c.attempts,c.generation,c.last_error`))
	if errors.Is(err, pgx.ErrNoRows) {
		err = durable.ErrNotFound
	}
	return c, err
}

// RetryFlightCompletion retains failed cleanup under the current unexpired lease.
func (s *Store) RetryFlightCompletion(ctx context.Context, c domain.FlightCompletion, cause error) error {
	message := cause.Error()
	if len(message) > 2048 {
		message = message[:2048]
	}
	tag, err := s.pool.Exec(ctx, `UPDATE flight_completions SET state='retrying',last_error=$3,lease_until=NULL,available_at=clock_timestamp()+interval '5 seconds' WHERE event_id=$1 AND generation=$2 AND lease_until>clock_timestamp() AND state='finalizing'`, c.Evidence.EventId, c.Generation, message)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return durable.ErrVersionConflict
	}
	return nil
}

// CompleteFlight atomically closes flight and intent, requests DSS withdrawal,
// and publishes the archive obligation. Parameters: ctx bounds the transaction;
// c holds the live lease; publication is optional configured DSS withdrawal.
// Returns: conflicts and failures without partial lifecycle transitions.
func (s *Store) CompleteFlight(ctx context.Context, c domain.FlightCompletion, publication *domain.OperationalIntentPublication) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	e := c.Evidence
	var raw []byte
	if err = tx.QueryRow(ctx, `SELECT data FROM flight_records WHERE id=$1 FOR UPDATE`, e.Context.FlightId).Scan(&raw); err != nil {
		return err
	}
	var f domain.FlightRecord
	if err = json.Unmarshal(raw, &f); err != nil {
		return err
	}
	if f.Status != domain.FlightStatusActive {
		return durable.ErrVersionConflict
	}
	if err = lockMissionAircraftLifecycle(ctx, tx, f.AircraftID); err != nil {
		return err
	}
	if err = lockIntent(ctx, tx, f.IntentID); err != nil {
		return err
	}
	var unresolved bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM commands WHERE flight_id=$1 AND state NOT IN ('applied','rejected','failed','timed_out'))`, f.ID).Scan(&unresolved); err != nil {
		return err
	}
	if unresolved {
		return fmt.Errorf("%w: outstanding command evidence must be reconciled", durable.ErrVersionConflict)
	}
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT data,revision FROM operational_intents WHERE id=$1 AND version=$2 FOR UPDATE`, f.IntentID, f.IntentVersion).Scan(&raw, &revision); err != nil {
		return err
	}
	var intent domain.OperationalIntent
	if err = json.Unmarshal(raw, &intent); err != nil {
		return err
	}
	if intent.Status != domain.IntentStatusActive {
		return durable.ErrVersionConflict
	}
	ended := time.Unix(0, max(e.LandedAtUnixNs, e.DisarmedAtUnixNs)).UTC()
	intent.Status = domain.IntentStatusComplete
	intent.CompletedAt = &ended
	intent.UpdatedAt = ended
	if err = updateOperationalIntentTx(ctx, tx, intent, revision); err != nil {
		return err
	}
	if publication != nil {
		if publication.IntentID != intent.ID || publication.DesiredIntentVersion != intent.Version {
			return durable.ErrVersionConflict
		}
		if err = requestPublicationTx(ctx, tx, *publication); err != nil {
			return err
		}
	}
	f.Status = domain.FlightStatusComplete
	f.EndedAt = &ended
	f.CompletionOutcome = e.Outcome
	f.CompletionEventID = e.EventId
	raw, err = json.Marshal(f)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE flight_records SET status=$2,data=$3 WHERE id=$1`, f.ID, f.Status, raw); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE flight_completions SET state='complete',last_error='',lease_until=NULL WHERE event_id=$1 AND generation=$2 AND lease_until>clock_timestamp() AND state='finalizing'`, e.EventId, c.Generation)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return durable.ErrVersionConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO flight_finalized_outbox(event_id,flight_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, e.EventId, f.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
