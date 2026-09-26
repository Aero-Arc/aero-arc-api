//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/httpapi"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/aero-arc/aero-arc-protos/commanddigest"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
)

func TestCommandAcceptanceRestartLeaseAndEvidence(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.pool.Exec(ctx, `TRUNCATE command_attempts, command_outbox, command_events, commands`); err != nil {
		t.Fatal(err)
	}
	prefix := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	a := domain.Aircraft{ID: prefix + "-aircraft", OperatorID: prefix, AgentID: prefix + "-agent", CreatedAt: now, UpdatedAt: now}
	if err = s.CreateAircraft(ctx, a); err != nil {
		t.Fatal(err)
	}
	intent := domain.OperationalIntent{ID: prefix + "-intent", OperatorID: prefix, AircraftID: a.ID, Version: 1, Status: domain.IntentStatusActive, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now}
	if err = s.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	f := domain.FlightRecord{ID: prefix + "-flight", OperatorID: prefix, AircraftID: a.ID, IntentID: intent.ID, IntentVersion: 1, Status: domain.FlightStatusPlanned}
	if err = s.CreateFlightRecord(ctx, f); err != nil {
		t.Fatal(err)
	}
	control := &noDispatchTransport{t: t}
	fleet := service.NewFleetService(s, nil, nil, nil).WithCommandControl(control, func(_ context.Context, principal string, _ domain.FlightRecord, action string) error {
		if principal != "mission-control-service" {
			t.Fatal("missing authenticated principal")
		}
		return nil
	})
	api := httpapi.New(fleet, time.Second).WithMissionDeploymentControl(time.Second, "test-control-token")
	submit := func(body string, key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/flights/"+f.ID+"/commands", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer test-control-token")
		request.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		api.Handler().ServeHTTP(response, request)
		return response
	}
	response := submit(`{"type":"ARM"}`, prefix+"-http")
	if response.Code != http.StatusAccepted {
		t.Fatalf("HTTP acceptance: %d %s", response.Code, response.Body.String())
	}
	var acceptedHTTP domain.Command
	if err = json.Unmarshal(response.Body.Bytes(), &acceptedHTTP); err != nil {
		t.Fatal(err)
	}
	duplicate := submit(`{"type":"ARM"}`, prefix+"-http")
	if duplicate.Code != http.StatusAccepted {
		t.Fatalf("HTTP replay: %s", duplicate.Body.String())
	}
	conflictResponse := submit(`{"type":"DISARM"}`, prefix+"-http")
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("HTTP conflicting retry: %d %s", conflictResponse.Code, conflictResponse.Body.String())
	}
	// Finish this fixture's first request explicitly; no worker or Relay has run.
	if _, err = s.pool.Exec(ctx, `UPDATE commands SET state='rejected' WHERE id=$1`, acceptedHTTP.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE command_outbox SET done=true WHERE command_id=$1`, acceptedHTTP.ID); err != nil {
		t.Fatal(err)
	}
	envelope := &pb.DurableCommand{CommandId: uuid.NewString(), OperatorId: prefix, AircraftId: a.ID, AgentId: a.AgentID, Context: &pb.OperationContext{AircraftId: a.ID, FlightId: f.ID, IntentId: intent.ID, IntentVersion: 1}, Definition: "ARM", DefinitionVersion: 1, Capability: "mavlink_command_v1", IssuedAtUnixMs: now.UnixMilli(), ExpiresAtUnixMs: now.Add(30 * time.Second).UnixMilli(), RecoveryPolicy: "no_repeat_effect_v1", Execution: &pb.DurableCommand_Mavlink{Mavlink: &pb.MavlinkExecution{Command: 400, Parameters: []float32{1, 0, 0, 0, 0, 0, 0}, Observation: "armed"}}}
	envelope.CommandDigest, err = commanddigest.Digest(envelope)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := proto.Marshal(envelope)
	c := domain.Command{ID: envelope.CommandId, OperatorID: prefix, AircraftID: a.ID, FlightID: f.ID, Type: "ARM", Digest: envelope.CommandDigest, RequestedBy: "test", IdempotencyKey: prefix, RequestHash: "request-1", Payload: payload, ExpiresAt: now.Add(30 * time.Second)}
	accepted, err := s.AcceptCommand(ctx, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.State != "accepted" || len(accepted.Events) != 3 {
		t.Fatalf("acceptance=%+v", accepted)
	}
	other, err := Open(ctx, integrationDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	replay, err := other.AcceptCommand(ctx, c, nil)
	if err != nil || replay.ID != accepted.ID {
		t.Fatalf("restart replay=%+v %v", replay, err)
	}
	conflict := c
	conflict.RequestHash = "changed"
	if _, err = other.AcceptCommand(ctx, conflict, nil); !errors.Is(err, durable.ErrIdempotencyConflict) {
		t.Fatalf("conflicting key: %v", err)
	}
	first, err := s.ClaimCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE command_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE command_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	second, err := other.ClaimCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Lease <= first.Lease {
		t.Fatal("lease generation did not advance")
	}
	evidence := []domain.CommandEvent{{ID: c.ID + "/applied", Stage: "applied", OccurredAt: now, Source: "agent", Message: "accepted by autopilot"}}
	if err = s.FinishCommandAttempt(ctx, first, evidence, "old worker"); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("stale worker: %v", err)
	}

	if err = s.RecordCommandProgress(ctx, first, evidence); !errors.Is(err, durable.ErrVersionConflict) {
		t.Fatalf("stale progress: %v", err)
	}
	if err = other.RecordCommandProgress(ctx, second, evidence); err != nil {
		t.Fatal(err)
	}
	progress, err := s.GetCommand(ctx, c.ID)
	if err != nil || progress.State != "applied" || progress.ObservationState != "pending" {
		t.Fatalf("uncommitted progress: %+v %v", progress, err)
	}
	if _, err = s.ClaimCommand(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("progress released delivery lease: %v", err)
	}
	altered := append([]domain.CommandEvent(nil), evidence...)
	altered[0].Message = "changed"
	if err = other.RecordCommandProgress(ctx, second, altered); err == nil {
		t.Fatal("mutable progress accepted")
	}
	if err = other.FinishCommandAttempt(ctx, second, evidence, "applied"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCommand(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "applied" || got.ObservationState != "pending" {
		t.Fatalf("applied collapsed with observed: %+v", got)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE command_outbox SET available_at=clock_timestamp() WHERE command_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	third, err := s.ClaimCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	contradiction := []domain.CommandEvent{{ID: c.ID + "/rejected", Stage: "rejected", OccurredAt: now, Source: "agent", Message: "contradiction"}}
	if err = s.FinishCommandAttempt(ctx, third, contradiction, "rejected"); err == nil {
		t.Fatal("contradictory terminal evidence was committed")
	}
	evidence = append(evidence, domain.CommandEvent{ID: c.ID + "/observed", Stage: "observed", OccurredAt: now.Add(time.Second), Source: "heartbeat", Message: "armed"})
	if err = s.FinishCommandAttempt(ctx, third, evidence, "observed"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetCommand(ctx, c.ID)
	if err != nil || got.State != "applied" || got.ObservationState != "observed" {
		t.Fatalf("observed projection=%+v %v", got, err)
	}
	// Exercise mission upload through the worker adapter and then activation via
	// the same generic lifecycle. Acceptance itself cannot call either transport.
	volume := domain.OperationalVolume{ID: prefix + "-volume", IntentID: intent.ID, IntentVersion: 1,
		GeoJSON:      `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
		MinAltitudeM: 0, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceMSL,
		StartsAt: now, EndsAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}
	if err = s.RecordOperationalVolume(ctx, volume); err != nil {
		t.Fatal(err)
	}
	mission, err := fleet.ImportMission(ctx, f.ID, prefix+"-import", service.ImportMissionRequest{
		AircraftID: a.ID, IntentID: intent.ID, IntentVersion: 1, SourceFormat: domain.MissionSourceFormatQGCWPL110,
		Source: "QGC WPL 110\n0\t1\t0\t16\t0\t0\t0\t0\t35.2\t-97.2\t120\t1\n1\t0\t0\t16\t0\t0\t0\t0\t35.21\t-97.21\t100\t1\n"})
	if err != nil {
		t.Fatal(err)
	}
	upload, err := fleet.SubmitCommand(ctx, f.ID, "mission-control-service", prefix+"-upload", service.CommandRequest{Type: "MISSION_UPLOAD", MissionID: mission.Mission.ID, MissionDigest: mission.Mission.MissionDigest})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := s.GetMissionDeployment(ctx, upload.DeploymentID)
	if err != nil || deployment.DispatchStarted || deployment.CommandID != upload.ID {
		t.Fatalf("atomic upload reservation: %+v %v", deployment, err)
	}
	transport := &appliedCommandTransport{progress: func(id string) error {
		value, err := s.GetCommand(ctx, id)
		if err != nil {
			return err
		}
		if value.State != "acknowledged" || value.Attempts != 1 {
			return fmt.Errorf("stream progress not persisted on first delivery: %+v", value)
		}
		if _, err = s.ClaimCommand(ctx); !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("stream released worker lease: %v", err)
		}
		return nil
	}}
	fleet.WithMissionDeployer(transport).WithCommandControl(transport, func(context.Context, string, domain.FlightRecord, string) error { return nil })
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); fleet.RunCommandWorker(workerCtx) }()
	defer func() { stop(); <-done }()
	waitApplied := func(id string) domain.Command {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			value, e := s.GetCommand(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			if value.State == "applied" {
				return value
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("worker did not apply command")
		return domain.Command{}
	}
	if value := waitApplied(upload.ID); value.ObservationState != "observed" {
		t.Fatalf("verified mission observation: %+v", value)
	}
	start, err := fleet.SubmitCommand(ctx, f.ID, "mission-control-service", prefix+"-start", service.CommandRequest{Type: "MISSION_START"})
	if err != nil {
		t.Fatal(err)
	}
	if value := waitApplied(start.ID); value.ObservationState != "pending" {
		t.Fatalf("start application invented observation: %+v", value)
	}
	active, err := s.GetFlightRecord(ctx, f.ID)
	if err != nil || active.Status != domain.FlightStatusActive || active.StartedAt.IsZero() {
		t.Fatalf("start activation: %+v %v", active, err)
	}

}

// noDispatchTransport makes accidental HTTP-lifetime execution fail the test.
type noDispatchTransport struct{ t *testing.T }

func (n *noDispatchTransport) ExchangeCommand(context.Context, string, *pb.DurableCommand, string) (*pb.CommandEvidence, error) {
	n.t.Fatal("submission contacted Relay instead of committing outbox")
	return nil, nil
}

// appliedCommandTransport models protocol acceptance independently of observations.
type appliedCommandTransport struct{ progress func(string) error }

func (*appliedCommandTransport) EnsureOperationContext(context.Context, string, *pb.SetOperationContextCommand) error {
	return nil
}
func (*appliedCommandTransport) ClearOperationContextForReconciliation(context.Context, string, *pb.ClearOperationContextCommand, *pb.OperationContext) error {
	return nil
}
func (*appliedCommandTransport) DeployMission(_ context.Context, _ string, c *pb.DeployMissionCommand) (*pb.MissionDeploymentResult, error) {
	return &pb.MissionDeploymentResult{CommandId: c.CommandId, Binding: c.Binding, Status: pb.MissionDeploymentResult_STATUS_APPLIED, UploadedItemCount: uint32(len(c.Plan.Items)), OnboardMissionDigest: c.Binding.MissionDigest, CompletedAtUnixMs: time.Now().UnixMilli()}, nil
}
func (*appliedCommandTransport) ExchangeCommand(_ context.Context, _ string, c *pb.DurableCommand, _ string) (*pb.CommandEvidence, error) {
	return &pb.CommandEvidence{CommandId: c.CommandId, CommandDigest: c.CommandDigest, Events: []*pb.CommandEvent{{EventId: c.CommandId + "/applied", Stage: "applied", OccurredAtUnixMs: c.IssuedAtUnixMs, EvidenceSource: "mavlink_command_ack"}}}, nil
}

func (a *appliedCommandTransport) ExecuteCommand(ctx context.Context, agent string, c *pb.DurableCommand, attempt string, receive func(*pb.CommandEvidence) error) error {
	acknowledged := &pb.CommandEvidence{CommandId: c.CommandId, CommandDigest: c.CommandDigest, Events: []*pb.CommandEvent{{EventId: c.CommandId + "/acknowledged", Stage: "acknowledged", OccurredAtUnixMs: c.IssuedAtUnixMs, EvidenceSource: "agent_journal"}}}
	if err := receive(acknowledged); err != nil {
		return err
	}
	if a.progress != nil {
		if err := a.progress(c.CommandId); err != nil {
			return err
		}
	}
	applied, err := a.ExchangeCommand(ctx, agent, c, attempt)
	if err != nil {
		return err
	}
	applied.Events = append(acknowledged.Events, applied.Events...)
	applied.DeliveryComplete = true
	return receive(applied)
}
