//go:build integration

package postgres

import (
	"context"
	"errors"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	"github.com/aero-arc/aero-arc-protos/missiondigest"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

func TestFlightFinalizationIsAtomicIdempotentAndLeaseFenced(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
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
	for i := 0; i < 2; i++ {
		if err = s.AdmitFlightCompletion(ctx, e); err != nil {
			t.Fatal(err)
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
	if err = s.CompleteFlight(ctx, second, nil); err != nil {
		t.Fatal(err)
	}
	stored, err = s.GetFlightRecord(ctx, id)
	if err != nil || stored.Status != domain.FlightStatusComplete || stored.CompletionEventID != id {
		t.Fatalf("flight closure: %+v %v", stored, err)
	}
	current, err = s.GetOperationalIntent(ctx, id)
	if err != nil || current.Status != domain.IntentStatusComplete {
		t.Fatalf("intent closure: %+v %v", current, err)
	}
	var count int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM flight_finalized_outbox WHERE event_id=$1`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("archive obligation=%d %v", count, err)
	}
	if err = s.AdmitFlightCompletion(ctx, e); err != nil {
		t.Fatalf("delayed exact duplicate: %v", err)
	}
}
