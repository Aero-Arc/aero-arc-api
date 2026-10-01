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
	"github.com/aero-arc/aero-arc-protos/flightcompletion"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	"github.com/aero-arc/aero-arc-protos/missiondigest"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

// AdmitFlightCompletion validates aircraft evidence against immutable API authority
// and commits an idempotent inbox obligation under the exact flight row lock.
//
// Parameters:
//   - ctx: bounds validation reads and the admission/retirement transaction.
//   - e: carries authenticated Agent evidence. Its cited mission start must have
//     durable applied state/evidence; dispatch permission alone is insufficient.
//
// Returns: nil after first admission or exact replay; ErrIdempotencyConflict for
// changed delivery content, ErrVersionConflict for binding/start/lifecycle conflicts,
// or validation, lookup, encoding, and storage errors without a partial commit.
// Undispatched commands are rejected and their delivery leases revoked atomically;
// previously started work remains uncertain for post-expiry evidence recovery.
// An unadmitted Relay notification stays pending until start evidence catches up.
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
	var startAuthorized bool
	if err = tx.QueryRow(ctx, `SELECT payload,state='applied' OR EXISTS(SELECT 1 FROM command_events WHERE command_id=commands.id AND stage='applied') FROM commands WHERE id=$1 AND flight_id=$2`, e.StartCommandId, flight.ID).Scan(&commandRaw, &startAuthorized); err != nil {
		return err
	}
	if !startAuthorized {
		return fmt.Errorf("%w: completion cites a mission start without durable applied evidence", durable.ErrVersionConflict)
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
	if err = validateCompletionStart(ctx, tx, e); err != nil {
		return err
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
	if err = retireUndispatchedFlightCommands(ctx, tx, flight.ID, databaseNow); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func retireUndispatchedFlightCommands(ctx context.Context, tx pgx.Tx, flightID string, at time.Time) error {
	const message = "flight completion admitted before command delivery began"
	rows, err := tx.Query(ctx, `UPDATE command_outbox o SET done=true,generation=generation+1,lease_until=NULL FROM commands c WHERE o.command_id=c.id AND c.flight_id=$1 AND NOT c.dispatch_started AND c.state NOT IN ('applied','rejected','failed','timed_out') RETURNING o.command_id`, flightID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.Exec(ctx, `UPDATE commands SET state='rejected',observation_state='unavailable' WHERE id=$1`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO command_events(event_id,command_id,stage,occurred_at,source,message) VALUES($1,$2,'rejected',$3,'api_completion',$4) ON CONFLICT DO NOTHING`, id+"/rejected", id, at, message); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE command_attempts SET finished_at=$2,result=$3 WHERE command_id=$1 AND finished_at IS NULL`, id, at, message); err != nil {
			return err
		}
	}
	// Preserve the existing mission-deployment read model and fence direct retries.
	rows, err = tx.Query(ctx, `SELECT id,data FROM mission_deployments WHERE flight_id=$1 AND status IN ('pending','temporary_error','outcome_unknown') AND NOT COALESCE((data->>'dispatch_started')::boolean,false) FOR UPDATE`, flightID)
	if err != nil {
		return err
	}
	var deployments []domain.MissionDeployment
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var d domain.MissionDeployment
		if err = decodeMissionDeployment(raw, &d); err != nil {
			rows.Close()
			return err
		}
		d.ID = id
		deployments = append(deployments, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range deployments {
		d.Status = domain.MissionDeploymentRejected
		d.Message = message
		d.CompletedAt = &at
		d.UpdatedAt = at
		raw, err := encodeMissionDeployment(d)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE mission_deployments SET status=$2,data=$3,updated_at=$4,revision=revision+1 WHERE id=$1`, d.ID, d.Status, raw, at); err != nil {
			return err
		}
	}
	return nil
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
//
// Parameters: ctx bounds storage reads; flightID selects the exact durable flight.
// Returns: immutable evidence and current finalization state, ErrNotFound when no
// evidence has been admitted, or a database/protobuf decoding error. A pending
// record does not imply flight/intent cleanup or archive publication is complete.
func (s *Store) GetFlightCompletion(ctx context.Context, flightID string) (domain.FlightCompletion, error) {
	c, err := scanCompletion(s.pool.QueryRow(ctx, `SELECT payload,state,attempts,generation,last_error FROM flight_completions WHERE flight_id=$1`, flightID))
	if errors.Is(err, pgx.ErrNoRows) {
		err = durable.ErrNotFound
	}
	return c, err
}

// ClaimFlightCompletion takes one due obligation under a database-clock lease.
//
// Parameters: ctx bounds the atomic database claim.
// Returns: evidence with incremented attempt and fencing generation, ErrNotFound
// when no due unleased obligation exists, or a storage/decoding error. Only that
// unexpired generation may finish or reschedule; the claim itself closes nothing.
func (s *Store) ClaimFlightCompletion(ctx context.Context) (domain.FlightCompletion, error) {
	c, err := scanCompletion(s.pool.QueryRow(ctx, `WITH due AS (SELECT event_id FROM flight_completions WHERE state<>'complete' AND available_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<=clock_timestamp()) ORDER BY available_at,event_id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE flight_completions c SET state='finalizing',attempts=attempts+1,generation=generation+1,lease_until=clock_timestamp()+interval '90 seconds' FROM due WHERE c.event_id=due.event_id RETURNING c.payload,c.state,c.attempts,c.generation,c.last_error`))
	if errors.Is(err, pgx.ErrNoRows) {
		err = durable.ErrNotFound
	}
	return c, err
}

// RetryFlightCompletion retains failed cleanup under the current unexpired lease.
//
// Parameters: ctx bounds persistence; c holds claimed event identity/generation;
// cause is the nonnil cleanup failure whose bounded message is retained.
// Returns: nil after releasing the lease and scheduling retry, ErrVersionConflict
// for expired/superseded ownership or wrong state, or a storage error. It preserves
// immutable evidence and never marks partial external cleanup as completion.
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

// CompleteFlight atomically closes the flight, completes an active intent while
// preserving a canceled intent, requests optional DSS withdrawal, and creates one
// archive obligation. External cleanup must have succeeded before this commit.
//
// Parameters:
//   - ctx: bounds the flight/aircraft/intent transaction.
//   - c: supplies immutable completion identity and the current finalizing lease.
//   - publication: optionally supplies withdrawal for the exact intent version.
//
// Returns: nil after the atomic lifecycle/outbox commit; ErrVersionConflict for
// expired/replaced leases, wrong lifecycle/publication binding, or unresolved
// commands; lookup/encoding/storage errors otherwise. Errors roll back all changes.
// The archive outbox records an obligation, not proof of uploaded archive coverage.
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
	if err = validateCompletionStart(ctx, tx, e); err != nil {
		return err
	}
	var revision int64
	if err = tx.QueryRow(ctx, `SELECT data,revision FROM operational_intents WHERE id=$1 AND version=$2 FOR UPDATE`, f.IntentID, f.IntentVersion).Scan(&raw, &revision); err != nil {
		return err
	}
	var intent domain.OperationalIntent
	if err = json.Unmarshal(raw, &intent); err != nil {
		return err
	}
	if intent.Status != domain.IntentStatusActive && intent.Status != domain.IntentStatusCanceled {
		return durable.ErrVersionConflict
	}
	ended := time.Unix(0, max(e.LandedAtUnixNs, e.DisarmedAtUnixNs)).UTC()
	// Cancellation describes operator intent, not whether the aircraft landed.
	// Preserve that terminal decision while still closing the evidenced flight.
	if intent.Status == domain.IntentStatusActive {
		intent.Status = domain.IntentStatusComplete
		intent.CompletedAt = &ended
		if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&intent.UpdatedAt); err != nil {
			return err
		}
		if err = updateOperationalIntentTx(ctx, tx, intent, revision); err != nil {
			return err
		}
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

// validateCompletionStart runs under the flight row lock shared by command
// admission, dispatch and evidence writes. A flight watch binds one immutable
// start. Any other applied start, or one still able to have taken effect, makes
// that watch's completion ambiguous. Unsent starts may be retired atomically.
func validateCompletionStart(ctx context.Context, tx pgx.Tx, e *pb.FlightCompletionEvidence) error {
	rows, err := tx.Query(ctx, `SELECT payload,state='applied', (SELECT max(occurred_at) FROM command_events WHERE command_id=c.id AND stage='applied') FROM commands c WHERE flight_id=$1 AND id<>$2 AND (state='applied' OR EXISTS(SELECT 1 FROM command_events WHERE command_id=c.id AND stage='applied') OR (dispatch_started AND state NOT IN ('rejected','failed','timed_out')))`, e.Context.FlightId, e.StartCommandId)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var applied bool
		var appliedAt *time.Time
		if err = rows.Scan(&raw, &applied, &appliedAt); err != nil {
			return err
		}
		command := new(pb.DurableCommand)
		if err = proto.Unmarshal(raw, command); err != nil {
			return err
		}
		projection := domain.Command{Type: command.Definition}
		if applied {
			projection.State = "applied"
		}
		if appliedAt != nil {
			projection.Events = []domain.CommandEvent{{Stage: "applied", OccurredAt: *appliedAt}}
		}
		if e.LandedAtUnixNs > 0 && e.DisarmedAtUnixNs > 0 && projection.InvalidatesGroundEvidence(time.Unix(0, min(e.LandedAtUnixNs, e.DisarmedAtUnixNs))) {
			return fmt.Errorf("%w: command %s invalidates ground evidence; explicit reconciliation required", durable.ErrVersionConflict, command.CommandId)
		}
		if command.Definition == "MISSION_START" {
			return fmt.Errorf("%w: competing mission start %s requires reconciliation", durable.ErrVersionConflict, command.CommandId)
		}
	}
	return rows.Err()
}

// CheckFlightFinalizationUpgrade rejects cutover with active legacy flights that
// have no applied durable mission-start authority. It never fabricates evidence.
//
// Parameters: ctx bounds the read-only preflight; callers must stop old producers
// before checking, and must not start admission/dispatch/finalization on failure.
// Returns: nil when every active flight has applied MISSION_START authority;
// otherwise an actionable error listing affected flight IDs, or a storage error.
func (s *Store) CheckFlightFinalizationUpgrade(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT f.id FROM flight_records f WHERE f.status='active' AND NOT EXISTS(SELECT 1 FROM commands c WHERE c.flight_id=f.id AND c.data->>'type'='MISSION_START' AND (c.state='applied' OR EXISTS(SELECT 1 FROM command_events e WHERE e.command_id=c.id AND e.stage='applied'))) ORDER BY f.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(ids) > 0 {
		return fmt.Errorf("finalization upgrade blocked: active legacy flights lack applied MISSION_START authority: %s; preserve existing stores and reconcile legacy records before cutover (docs/flight-finalization.md)", strings.Join(ids, ", "))
	}
	return nil
}
