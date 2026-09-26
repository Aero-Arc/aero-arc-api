package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/aero-arc/aero-arc-protos/commanddigest"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// CommandAuthorizer evaluates an authenticated principal against an exact flight
// and action. Implementations must fail closed and return their denial as an error.
type CommandAuthorizer func(context.Context, string, domain.FlightRecord, string) error

// CommandTransport exchanges immutable commands for durable Agent evidence.
type CommandTransport interface {
	ExchangeCommand(context.Context, string, *pb.DurableCommand, string) (*pb.CommandEvidence, error)
}

// CommandRequest selects an approved definition; callers cannot supply MAVLink parameters.
type CommandRequest struct {
	Type          string `json:"type"`
	MissionID     string `json:"mission_id,omitempty"`
	MissionDigest string `json:"mission_digest,omitempty"`
}

// WithCommandControl configures command execution and authorization independently
// from HTTP lifetime. A nil authorizer leaves submission and reads disabled.
//
// Parameters: transport carries background attempts; authorize checks caller, flight, and action.
//
// Returns: The configured service; nil authorization disables control.
func (s *FleetService) WithCommandControl(transport CommandTransport, authorize CommandAuthorizer) *FleetService {
	s.commandTransport = transport
	s.commandAuthorizer = authorize
	return s
}

func (s *FleetService) commandStore() (durable.CommandStore, error) {
	store, ok := s.durable.(durable.CommandStore)
	if !ok || s.commandAuthorizer == nil {
		return nil, ErrMissionDeploymentUnavailable
	}
	return store, nil
}

// SubmitCommand atomically accepts an approved operation for later dispatch.
// Parameters identify the flight, authenticated principal, stable submission key,
// and immutable reviewed mission where required. It never contacts Relay.
// Exact retries restore the original command even after its flight changes state.
//
// Parameters: ctx bounds acceptance; flightID selects the flight; principal identifies the caller; key is mandatory scoped idempotency; req selects an approved definition.
//
// Returns: The committed command or exact replay; authorization, binding, validation, and storage errors prevent new acceptance.
func (s *FleetService) SubmitCommand(ctx context.Context, flightID, principal, key string, req CommandRequest) (domain.Command, error) {
	store, err := s.commandStore()
	if err != nil {
		return domain.Command{}, err
	}
	if err = validateIdempotencyKey(key); err != nil {
		return domain.Command{}, err
	}
	flight, err := s.durable.GetFlightRecord(ctx, flightID)
	if err != nil {
		return domain.Command{}, err
	}
	if err = s.commandAuthorizer(ctx, principal, flight, req.Type); err != nil {
		return domain.Command{}, err
	}
	requestBytes, err := json.Marshal(struct {
		Flight  string
		Request CommandRequest
	}{flightID, req})
	if err != nil {
		return domain.Command{}, err
	}
	hash := sha256Hex(string(requestBytes))
	existing, err := store.FindCommand(ctx, flight.OperatorID, key)
	if err == nil {
		if existing.RequestHash != hash {
			return domain.Command{}, durable.ErrIdempotencyConflict
		}
		return existing, nil
	}
	if !errors.Is(err, durable.ErrNotFound) {
		return domain.Command{}, err
	}
	aircraft, err := s.durable.GetAircraft(ctx, flight.AircraftID)
	if err != nil {
		return domain.Command{}, err
	}
	if aircraft.AgentID == "" {
		return domain.Command{}, fmt.Errorf("%w: aircraft has no Agent binding", ErrValidation)
	}
	now := s.now().UTC()
	id := uuid.NewString()
	envelope := &pb.DurableCommand{CommandId: id, OperatorId: flight.OperatorID, AircraftId: flight.AircraftID, AgentId: aircraft.AgentID, Context: &pb.OperationContext{AircraftId: flight.AircraftID, FlightId: flight.ID, IntentId: flight.IntentID, IntentVersion: uint32(flight.IntentVersion)}, Definition: req.Type, DefinitionVersion: 1, Capability: "mavlink_command_v1", IssuedAtUnixMs: now.UnixMilli(), ExpiresAtUnixMs: now.Add(30 * time.Second).UnixMilli(), RecoveryPolicy: "no_repeat_effect_v1"}
	var deployment *domain.MissionDeployment
	m := &pb.MavlinkExecution{Parameters: make([]float32, 7), VehicleProfile: "arducopter_v1"}
	switch req.Type {
	case "ARM":
		m.Command = 400
		m.Parameters[0] = 1
		m.Observation = "armed"
	case "DISARM":
		m.Command = 400
		m.Observation = "disarmed"
	case "MISSION_START":
		m.Command = 300
		m.Observation = "mission_running"
	case "PAUSE":
		m.Command = 193
		m.Parameters[0] = 0
		m.Observation = "unavailable"
	case "RESUME":
		m.Command = 193
		m.Parameters[0] = 1
		m.Observation = "unavailable"
	case "RTL":
		m.Command = 20
		m.Observation = "custom_mode"
		m.ExpectedCustomMode = 6
	case "LAND":
		m.Command = 21
		m.Observation = "landed"
	case "MISSION_UPLOAD":
		f, mission, a, e := s.validateMissionDeploymentBinding(ctx, flightID)
		if e != nil {
			return domain.Command{}, e
		}
		if mission.ID != req.MissionID || mission.MissionDigest != req.MissionDigest {
			return domain.Command{}, durable.ErrVersionConflict
		}
		envelope.ExpiresAtUnixMs = now.Add(missionDeploymentCommandTTL).UnixMilli()
		envelope.Capability = "mission_upload_v1"
		envelope.RecoveryPolicy = "mission_readback_v1"
		d := domain.MissionDeployment{ID: uuid.NewString(), OperatorID: mission.OperatorID, FlightID: f.ID, AircraftID: f.AircraftID, AgentID: a.AgentID, IntentID: f.IntentID, IntentVersion: f.IntentVersion, MissionID: mission.ID, MissionVersion: mission.Version, MissionDigest: mission.MissionDigest, CommandID: id, OperationContextCommandID: uuid.NewString(), ReconciliationClearCommandID: uuid.NewString(), IdempotencyKey: "c2/" + id, IdempotencyRequest: sha256Hex(fmt.Sprintf("deploy-mission-v1\x00%s\x00%s\x00%d\x00%s", f.ID, mission.ID, mission.Version, mission.MissionDigest)), Status: domain.MissionDeploymentPending, IssuedAt: now, ExpiresAt: time.UnixMilli(envelope.ExpiresAtUnixMs), ReconcileUntil: now.Add(missionDeploymentCommandTTL + missionDeploymentReconciliationTTL), CreatedAt: now, UpdatedAt: now}
		deployment = &d
		envelope.Execution = &pb.DurableCommand_Mission{Mission: missionDeploymentCommand(d, mission)}
	default:
		return domain.Command{}, fmt.Errorf("%w: unsupported command definition", ErrValidation)
	}
	if req.Type == "MISSION_START" || req.Type == "RESUME" {
		mission, e := s.durable.GetCurrentMissionForFlight(ctx, flightID)
		if e != nil {
			return domain.Command{}, e
		}
		m.MissionPrecondition = canonicalMissionPlan(mission.Items)
		m.MissionPreconditionId = mission.ID
		m.MissionPreconditionVersion = uint32(mission.Version)
	}
	if req.Type != "MISSION_UPLOAD" {
		if req.MissionID != "" || req.MissionDigest != "" {
			return domain.Command{}, fmt.Errorf("%w: mission review fields only apply to upload", ErrValidation)
		}
		envelope.Execution = &pb.DurableCommand_Mavlink{Mavlink: m}
	}
	envelope.CommandDigest, err = commanddigest.Digest(envelope)
	if err != nil {
		return domain.Command{}, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	payload, err := proto.Marshal(envelope)
	if err != nil {
		return domain.Command{}, err
	}
	return store.AcceptCommand(ctx, domain.Command{ID: id, OperatorID: flight.OperatorID, AircraftID: flight.AircraftID, FlightID: flight.ID, Type: req.Type, Digest: envelope.CommandDigest, RequestedBy: principal, IdempotencyKey: key, RequestHash: hash, Payload: payload, ExpiresAt: time.UnixMilli(envelope.ExpiresAtUnixMs)}, deployment)
}

// ListFlightCommands authorizes a flight-scoped read and returns durable evidence.
//
// Parameters: ctx bounds reads; flightID selects scope; principal identifies the caller.
//
// Returns: Complete command history or an authorization, lookup, or database error.
func (s *FleetService) ListFlightCommands(ctx context.Context, flightID, principal string) ([]domain.Command, error) {
	store, err := s.commandStore()
	if err != nil {
		return nil, err
	}
	f, err := s.durable.GetFlightRecord(ctx, flightID)
	if err != nil {
		return nil, err
	}
	if err = s.commandAuthorizer(ctx, principal, f, "READ"); err != nil {
		return nil, err
	}
	return store.ListCommands(ctx, flightID)
}

// RunCommandWorker drains the durable outbox until process cancellation. A worker
// lease covers a bounded attempt; errors never create a new command identity.
//
// Parameters: ctx controls worker lifetime and cancels bounded delivery attempts.
//
// Returns: Returns after all workers stop; failed attempts preserve identity for recovery.
func (s *FleetService) RunCommandWorker(ctx context.Context) {
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() { defer workers.Done(); s.runCommandWorker(ctx) }()
	}
	workers.Wait()
}

func (s *FleetService) runCommandWorker(ctx context.Context) {
	store, err := s.commandStore()
	if err != nil || s.commandTransport == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c, e := store.ClaimCommand(ctx)
			if e != nil {
				continue
			}
			attempt, cancel := context.WithTimeout(ctx, 120*time.Second)
			events, result := s.executeCommandAttempt(attempt, c)
			cancel()
			persist, cancelPersist := context.WithTimeout(ctx, 5*time.Second)
			if e = store.FinishCommandAttempt(persist, c, events, result); e != nil {
				slog.Error("command evidence persistence failed", "command_id", c.ID, "error", e)
			}
			cancelPersist()
		}
	}
}

func (s *FleetService) executeCommandAttempt(ctx context.Context, c domain.Command) ([]domain.CommandEvent, string) {
	var envelope pb.DurableCommand
	if err := proto.Unmarshal(c.Payload, &envelope); err != nil {
		return nil, err.Error()
	}
	event := func(stage, msg, source string, at time.Time) domain.CommandEvent {
		return domain.CommandEvent{ID: c.ID + "/" + stage, Stage: stage, Message: msg, Source: source, OccurredAt: at}
	}
	if c.Type == "MISSION_UPLOAD" {
		result, err := s.ReconcileMissionDeployment(ctx, c.FlightID, c.DeploymentID)
		if err != nil {
			return nil, err.Error()
		}
		d := result.Deployment
		// Legacy mission protocol combines receipt and readback. Preserve that fact
		// instead of inventing earlier individual timestamps.
		at := d.UpdatedAt
		if d.CompletedAt != nil {
			at = *d.CompletedAt
		}
		switch d.Status {
		case domain.MissionDeploymentApplied, domain.MissionDeploymentAlreadyApplied:
			return []domain.CommandEvent{event("acknowledged", "durable mission result received", "mission_deployment", at), event("applied", "mission protocol accepted upload", "mission_deployment", at), event("observed", "verified onboard mission digest "+d.OnboardMissionDigest, "mavlink_mission_readback", at)}, string(d.Status)
		case domain.MissionDeploymentRejected, domain.MissionDeploymentBindingMismatch, domain.MissionDeploymentOnboardMissionMismatch:
			return []domain.CommandEvent{event("rejected", d.Message, "mission_deployment", at)}, string(d.Status)
		default:
			for _, e := range c.Events {
				if e.Stage == "outcome_unknown" {
					return nil, string(d.Status)
				}
			}
			if d.DispatchStarted {
				return []domain.CommandEvent{event("outcome_unknown", d.Message, "mission_deployment", at)}, string(d.Status)
			}
			if time.Now().After(c.ExpiresAt) {
				return []domain.CommandEvent{event("timed_out", "authorization expired before mission dispatch", "api_worker", c.ExpiresAt)}, string(d.Status)
			}
			return nil, string(d.Status)
		}
	}
	// Context installation is its own existing durable, idempotent operation.
	// Never change operation context for post-expiry result recovery.
	if time.Now().Before(c.ExpiresAt) {
		if err := s.missionDeployer.EnsureOperationContext(ctx, envelope.AgentId, &pb.SetOperationContextCommand{CommandId: c.ID + "/context", Context: envelope.Context}); err != nil {
			return nil, err.Error()
		}
	}

	var evidence *pb.CommandEvidence
	var err error
	if transport, ok := s.commandTransport.(interface {
		ExecuteCommand(context.Context, string, *pb.DurableCommand, string, func(*pb.CommandEvidence) error) error
	}); ok {
		store, storeErr := s.commandStore()
		if storeErr != nil {
			return nil, storeErr.Error()
		}
		progress, ok := store.(interface {
			RecordCommandProgress(context.Context, domain.Command, []domain.CommandEvent) error
		})
		if !ok {
			return nil, "command store cannot persist streaming progress"
		}
		err = transport.ExecuteCommand(ctx, envelope.AgentId, &envelope, fmt.Sprintf("%s/attempt-%d", c.ID, c.Attempts), func(snapshot *pb.CommandEvidence) error {
			events, validationErr := commandEvidenceEvents(c, snapshot)
			if validationErr != nil {
				return validationErr
			}
			if persistErr := progress.RecordCommandProgress(ctx, c, events); persistErr != nil {
				return persistErr
			}
			evidence = snapshot
			return nil
		})
	} else {
		evidence, err = s.commandTransport.ExchangeCommand(ctx, envelope.AgentId, &envelope, fmt.Sprintf("%s/attempt-%d", c.ID, c.Attempts))
	}
	if err != nil {
		unknown := event("delivery_unknown", "delivery attempt has no authoritative outcome", "api_worker", time.Now().UTC())
		unknown.ID = fmt.Sprintf("%s/attempt-%d/delivery_unknown", c.ID, c.Attempts)
		return []domain.CommandEvent{unknown}, err.Error()
	}
	events, err := commandEvidenceEvents(c, evidence)
	if err != nil {
		return nil, err.Error()
	}
	return events, "Agent evidence received"
}

func commandEvidenceEvents(c domain.Command, evidence *pb.CommandEvidence) ([]domain.CommandEvent, error) {
	if evidence == nil || evidence.CommandId != c.ID || evidence.CommandDigest != c.Digest {
		return nil, fmt.Errorf("uncorrelated Agent evidence")
	}
	out := []domain.CommandEvent{}
	for _, e := range evidence.Events {
		if e == nil {
			return nil, fmt.Errorf("empty Agent evidence event")
		}
		switch e.Stage {
		case "verifying_mission", "awaiting_ack", "acknowledged", "applied", "observed", "observation_unavailable", "observation_superseded", "rejected", "outcome_unknown", "relay_received", "dispatched":
		default:
			return nil, fmt.Errorf("invalid Agent evidence stage")
		}
		expectedID := c.ID + "/" + e.Stage
		if e.Stage == "relay_received" || e.Stage == "dispatched" {
			expectedID = fmt.Sprintf("%s/attempt-%d/%s", c.ID, c.Attempts, e.Stage)
		}
		if e.EventId != expectedID || e.OccurredAtUnixMs <= 0 {
			return nil, fmt.Errorf("invalid Agent event identity")
		}
		// API uncertainty is transport evidence, not the immutable Agent event.
		if e.Stage == "outcome_unknown" {
			found := false
			for _, old := range c.Events {
				if old.Stage == e.Stage {
					found = true
				}
			}
			if found {
				continue
			}
		}
		out = append(out, domain.CommandEvent{ID: e.EventId, Stage: e.Stage, OccurredAt: time.UnixMilli(e.OccurredAtUnixMs), Source: e.EvidenceSource, Message: e.Message})
	}
	return out, nil
}

// CommandControlEnabled reports whether durable command routes are configured.
func (s *FleetService) CommandControlEnabled() bool {
	_, err := s.commandStore()
	return err == nil && s.commandTransport != nil
}

// GetFlightCommand authorizes one historical command through its exact flight.
//
// Parameters: ctx bounds reads; flightID and id identify the command; principal identifies the caller.
//
// Returns: The authorized record or a denial, missing-record, or database error.
func (s *FleetService) GetFlightCommand(ctx context.Context, flightID, id, principal string) (domain.Command, error) {
	store, err := s.commandStore()
	if err != nil {
		return domain.Command{}, err
	}
	f, err := s.durable.GetFlightRecord(ctx, flightID)
	if err != nil {
		return domain.Command{}, err
	}
	if err = s.commandAuthorizer(ctx, principal, f, "READ"); err != nil {
		return domain.Command{}, err
	}
	c, err := store.GetCommand(ctx, id)
	if err != nil {
		return domain.Command{}, err
	}
	if c.FlightID != flightID || c.OperatorID != f.OperatorID {
		return domain.Command{}, durable.ErrNotFound
	}
	return c, nil
}

// ReconcileFlightCommand reschedules the same immutable authority after an
// authorized recovery request. Expired commands remain readback/replay-only.
//
// Parameters: ctx bounds scheduling; flightID and id identify original authority; principal identifies the caller.
//
// Returns: The unchanged command or an authorization, lookup, or scheduling error.
func (s *FleetService) ReconcileFlightCommand(ctx context.Context, flightID, id, principal string) (domain.Command, error) {
	c, err := s.GetFlightCommand(ctx, flightID, id, principal)
	if err != nil {
		return c, err
	}
	f, err := s.durable.GetFlightRecord(ctx, flightID)
	if err != nil {
		return c, err
	}
	if err = s.commandAuthorizer(ctx, principal, f, "RECONCILE"); err != nil {
		return c, err
	}
	store, err := s.commandStore()
	if err != nil {
		return c, err
	}
	if err = store.RequeueCommand(ctx, id); err != nil {
		return c, err
	}
	return c, nil
}
