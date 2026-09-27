//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	"github.com/aero-arc/aero-arc-protos/missiondigest"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func TestFlightFinalizationIsAtomicIdempotentAndLeaseFenced(t *testing.T) {
	for _, mode := range []string{"queued", "claimed", "dispatching"} {
		t.Run(mode, func(t *testing.T) {
			for _, canceled := range []bool{false, true} {
				t.Run(fmt.Sprint(canceled), func(t *testing.T) {
					ctx := context.Background()
					s, err := Open(ctx, integrationDatabaseURL)
					if err != nil {
						t.Fatal(err)
					}
					defer s.Close()
					if _, err = s.pool.Exec(ctx, `TRUNCATE command_attempts,command_outbox,command_events,commands`); err != nil {
						t.Fatal(err)
					}
					id := uuid.NewString()
					now := time.Now().UTC().Add(-time.Minute)
					aircraft := domain.Aircraft{ID: id, OperatorID: id, AgentID: id, CreatedAt: now, UpdatedAt: now}
					if err = s.CreateAircraft(ctx, aircraft); err != nil {
						t.Fatal(err)
					}
					intent := domain.OperationalIntent{ID: id, OperatorID: id, AircraftID: id, Version: 1, Status: domain.IntentStatusActive, PlannedStartAt: now.Add(-time.Minute), PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
					if err = s.CreateOperationalIntent(ctx, intent); err != nil {
						t.Fatal(err)
					}
					flight := domain.FlightRecord{ID: id, OperatorID: id, AircraftID: id, IntentID: id, IntentVersion: 1, Status: domain.FlightStatusActive, StartedAt: now}
					if err = s.CreateFlightRecord(ctx, flight); err != nil {
						t.Fatal(err)
					}
					plan := &pb.MissionPlan{SchemaVersion: 1, Items: []*pb.MissionItem{{Command: 21, Param4: 1, Autocontinue: true}}}
					digest, err := missiondigest.Digest(plan)
					if err != nil {
						t.Fatal(err)
					}
					command := &pb.DurableCommand{CommandId: id, AgentId: id, Definition: "MISSION_START", Context: &pb.OperationContext{AircraftId: id, FlightId: id, IntentId: id, IntentVersion: 1}, Execution: &pb.DurableCommand_Mavlink{Mavlink: &pb.MavlinkExecution{MissionPreconditionId: id, MissionPrecondition: plan}}}
					raw, err := proto.Marshal(command)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.pool.Exec(ctx, `INSERT INTO commands(id,operator_id,aircraft_id,flight_id,digest,idempotency_key,request_hash,payload,data,state,expires_at) VALUES($1,$1,$1,$1,'digest',$1,'hash',$2,'{}','applied',clock_timestamp())`, id, raw); err != nil {
						t.Fatal(err)
					}
					e := &pb.FlightCompletionEvidence{EventId: id, AgentId: id, Context: command.Context, MissionId: id, MissionDigest: digest, StartCommandId: id, Outcome: "mission_completed", AirborneAtUnixNs: now.Add(time.Second).UnixNano(), TerminalAtUnixNs: now.Add(2 * time.Second).UnixNano(), LandedAtUnixNs: now.Add(3 * time.Second).UnixNano(), DisarmedAtUnixNs: now.Add(4 * time.Second).UnixNano(), ObservationEpoch: id}
					queuedID := id + "-queued"
					queued := domain.Command{ID: queuedID, OperatorID: id, AircraftID: id, FlightID: id, Type: "ARM", Digest: "queued-digest", Payload: raw, Lease: 1, Attempts: 1, ExpiresAt: time.Now().Add(time.Minute)}
					data, err := json.Marshal(queued)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.pool.Exec(ctx, `INSERT INTO commands(id,operator_id,aircraft_id,flight_id,digest,idempotency_key,request_hash,payload,data,state,expires_at) VALUES($1,$2,$2,$2,'queued-digest',$1,'hash',$3,$4,'accepted',$5)`, queuedID, id, raw, data, queued.ExpiresAt); err != nil {
						t.Fatal(err)
					}
					if _, err = s.pool.Exec(ctx, `INSERT INTO command_outbox(command_id,generation,attempts,lease_until) VALUES($1,1,1,CASE WHEN $2 THEN NULL ELSE clock_timestamp()+interval '150 seconds' END)`, queuedID, mode == "queued"); err != nil {
						t.Fatal(err)
					}
					if _, err = s.pool.Exec(ctx, `INSERT INTO command_attempts(command_id,attempt) VALUES($1,1)`, queuedID); err != nil {
						t.Fatal(err)
					}
					if mode == "dispatching" {
						if err = s.BeginCommandDispatch(ctx, queued); err != nil {
							t.Fatal(err)
						}
					}
					for i := 0; i < 2; i++ {
						if err = s.AdmitFlightCompletion(ctx, e); err != nil {
							t.Fatal(err)
						}
					}
					storedCommand, err := s.GetCommand(ctx, queuedID)
					if err != nil {
						t.Fatal(err)
					}
					if err = s.BeginCommandDispatch(ctx, queued); !errors.Is(err, durable.ErrVersionConflict) {
						t.Fatalf("completion allowed delivery: %v", err)
					}
					if mode == "dispatching" {
						if storedCommand.State != "accepted" {
							t.Fatalf("possible delivery falsely retired: %s", storedCommand.State)
						}
						// Advance only the test database clock boundary to simulate original expiry.
						if _, err = s.pool.Exec(ctx, `UPDATE commands SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, queuedID); err != nil {
							t.Fatal(err)
						}
						if err = s.BeginCommandDispatch(ctx, queued); err != nil {
							t.Fatalf("post-expiry recovery blocked: %v", err)
						}
						if err = s.FinishCommandAttempt(ctx, queued, []domain.CommandEvent{{ID: queuedID + "/rejected", Stage: "rejected", OccurredAt: time.Now(), Source: "agent", Message: "expired before first effect"}}, "expired recovery"); err != nil {
							t.Fatal(err)
						}
					} else {
						if storedCommand.State != "rejected" || len(storedCommand.Events) != 1 || storedCommand.Events[0].Source != "api_completion" {
							t.Fatalf("queued command not retired: %+v", storedCommand)
						}
						if err = s.FinishCommandAttempt(ctx, queued, []domain.CommandEvent{{ID: queuedID + "/applied", Stage: "applied", OccurredAt: time.Now(), Source: "stale-worker"}}, "late"); err == nil {
							t.Fatal("retired worker overwrote completion fence")
						}
						if err = s.RequeueCommand(ctx, queuedID); err != nil {
							t.Fatal(err)
						}
						if claimed, err := s.ClaimCommand(ctx); !errors.Is(err, pgx.ErrNoRows) {
							t.Fatalf("retired command was reclaimable: %+v %v", claimed, err)
						}
					}
					changed := proto.Clone(e).(*pb.FlightCompletionEvidence)
					changed.Outcome = "ended_early"
					if err = s.AdmitFlightCompletion(ctx, changed); !errors.Is(err, durable.ErrIdempotencyConflict) {
						t.Fatalf("changed evidence admitted: %v", err)
					}
					first, err := s.ClaimFlightCompletion(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.pool.Exec(ctx, `UPDATE flight_completions SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1`, id); err != nil {
						t.Fatal(err)
					}
					if err = s.CompleteFlight(ctx, first, nil); !errors.Is(err, durable.ErrVersionConflict) {
						t.Fatalf("expired worker committed: %v", err)
					}
					stored, err := s.GetFlightRecord(ctx, id)
					if err != nil || stored.Status != domain.FlightStatusActive {
						t.Fatalf("partial flight closure: %+v %v", stored, err)
					}
					current, err := s.GetOperationalIntent(ctx, id)
					if err != nil || current.Status != domain.IntentStatusActive {
						t.Fatalf("partial intent closure: %+v %v", current, err)
					}
					second, err := s.ClaimFlightCompletion(ctx)
					if err != nil || second.Generation <= first.Generation {
						t.Fatalf("takeover: %+v %v", second, err)
					}
					if canceled {
						current.Status = domain.IntentStatusCanceled
						if err = s.UpdateOperationalIntent(ctx, current, current.Revision); err != nil {
							t.Fatal(err)
						}
					}
					if err = s.CompleteFlight(ctx, second, nil); err != nil {
						t.Fatal(err)
					}
					stored, err = s.GetFlightRecord(ctx, id)
					if err != nil || stored.Status != domain.FlightStatusComplete || stored.CompletionEventID != id {
						t.Fatalf("flight closure: %+v %v", stored, err)
					}
					current, err = s.GetOperationalIntent(ctx, id)
					wantStatus := domain.IntentStatusComplete
					if canceled {
						wantStatus = domain.IntentStatusCanceled
					}
					if err != nil || current.Status != wantStatus {
						t.Fatalf("intent closure: %+v %v", current, err)
					}
					var count int
					if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM flight_finalized_outbox WHERE event_id=$1`, id).Scan(&count); err != nil || count != 1 {
						t.Fatalf("archive obligation=%d %v", count, err)
					}
					if err = s.AdmitFlightCompletion(ctx, e); err != nil {
						t.Fatalf("delayed exact duplicate: %v", err)
					}

				})
			}
		})
	}
}
