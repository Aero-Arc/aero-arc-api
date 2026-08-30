package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	agentv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	"github.com/google/uuid"
)

const missionDeploymentCommandTTL = 2 * time.Minute
const missionDeploymentReconciliationTTL = 15 * time.Minute

// ErrMissionDeploymentUnavailable means secure API-to-Relay control is not configured.
var ErrMissionDeploymentUnavailable = errors.New("mission deployment unavailable")

// MissionDeployer sends one stable command through authoritative Registry placement.
type MissionDeployer interface {
	EnsureOperationContext(context.Context, string, *agentv1.SetOperationContextCommand) error
	ClearOperationContextForReconciliation(context.Context, string, *agentv1.ClearOperationContextCommand, *agentv1.OperationContext) error
	DeployMission(context.Context, string, *agentv1.DeployMissionCommand) (*agentv1.MissionDeploymentResult, error)
}

// DeployMissionResult identifies an idempotent replay separately from command status.
type DeployMissionResult struct {
	Deployment domain.MissionDeployment `json:"deployment"`
	Replayed   bool                     `json:"replayed"`
}

// DeployCurrentMission durably identifies and dispatches the exact current
// mission. The caller supplies neither mission bytes nor control-plane routing.
func (s *FleetService) DeployCurrentMission(ctx context.Context, flightID, expectedMissionID, expectedDigest, idempotencyKey string) (DeployMissionResult, error) {
	flightID, expectedMissionID = strings.TrimSpace(flightID), strings.TrimSpace(expectedMissionID)
	expectedDigest, idempotencyKey = strings.TrimSpace(expectedDigest), strings.TrimSpace(idempotencyKey)
	if flightID == "" || expectedMissionID == "" || len(expectedDigest) != 64 {
		return DeployMissionResult{}, fmt.Errorf("%w: flight_id, expected mission_id, and mission digest are required", ErrValidation)
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return DeployMissionResult{}, err
	}
	existing, lookupErr := s.durable.GetMissionDeploymentByIdempotencyKey(ctx, idempotencyKey)
	if lookupErr == nil {
		if existing.FlightID != flightID || existing.MissionID != expectedMissionID || existing.MissionDigest != expectedDigest {
			return DeployMissionResult{}, durable.ErrIdempotencyConflict
		}
		existingRequestHash := sha256Hex(strings.Join([]string{
			"deploy-mission-v1", existing.FlightID, existing.MissionID, fmt.Sprint(existing.MissionVersion), existing.MissionDigest,
		}, "\x00"))
		if existing.IdempotencyRequest != existingRequestHash {
			return DeployMissionResult{}, durable.ErrIdempotencyConflict
		}
		if !missionDeploymentRetryable(existing.Status) {
			if err := s.validateCurrentMissionDeploymentReplay(ctx, existing); err != nil {
				return DeployMissionResult{}, err
			}
			return DeployMissionResult{Deployment: existing, Replayed: true}, nil
		}
	} else if !errors.Is(lookupErr, durable.ErrNotFound) {
		return DeployMissionResult{}, fmt.Errorf("get mission deployment replay: %w", lookupErr)
	}
	if s.missionDeployer == nil {
		return DeployMissionResult{}, ErrMissionDeploymentUnavailable
	}

	flight, mission, aircraft, err := s.validateMissionDeploymentBinding(ctx, flightID)
	if err != nil {
		return DeployMissionResult{}, err
	}
	if mission.ID != expectedMissionID || mission.MissionDigest != expectedDigest {
		return DeployMissionResult{}, fmt.Errorf("%w: expected mission identity or digest is not the current immutable mission", durable.ErrVersionConflict)
	}
	requestHash := sha256Hex(strings.Join([]string{
		"deploy-mission-v1", flight.ID, mission.ID, fmt.Sprint(mission.Version), mission.MissionDigest,
	}, "\x00"))
	now := s.now().UTC()
	created := domain.MissionDeployment{
		ID: uuid.NewString(), OperatorID: mission.OperatorID, FlightID: flight.ID,
		AircraftID: flight.AircraftID, AgentID: aircraft.AgentID, IntentID: flight.IntentID,
		IntentVersion: flight.IntentVersion, MissionID: mission.ID, MissionVersion: mission.Version,
		MissionDigest: mission.MissionDigest, CommandID: uuid.NewString(), IdempotencyKey: idempotencyKey,
		OperationContextCommandID:    uuid.NewString(),
		ReconciliationClearCommandID: uuid.NewString(),
		IdempotencyRequest:           requestHash, Status: domain.MissionDeploymentPending,
		IssuedAt: now, ExpiresAt: now.Add(missionDeploymentCommandTTL),
		ReconcileUntil: now.Add(missionDeploymentCommandTTL + missionDeploymentReconciliationTTL), CreatedAt: now, UpdatedAt: now,
	}
	deployment, err := s.durable.CreateMissionDeploymentForPlannedFlight(ctx, created)
	if err != nil {
		return DeployMissionResult{}, fmt.Errorf("create mission deployment: %w", err)
	}
	replayed := deployment.ID != created.ID
	if replayed && deployment.IdempotencyRequest != requestHash {
		return DeployMissionResult{}, durable.ErrIdempotencyConflict
	}
	if replayed && !missionDeploymentRetryable(deployment.Status) {
		if err := s.validateCurrentMissionDeploymentReplay(ctx, deployment); err != nil {
			return DeployMissionResult{}, err
		}
		return DeployMissionResult{Deployment: deployment, Replayed: true}, nil
	}
	commandExpired := !deployment.ExpiresAt.After(now)
	reconciliationOpen := deployment.ReconcileUntil.After(now)
	if commandExpired && (!replayed || deployment.Status != domain.MissionDeploymentOutcomeUnknown || !deployment.DispatchStarted || !reconciliationOpen) {
		if missionDeploymentRetryable(deployment.Status) {
			message := "deployment command expired before its first dispatch; no effect was authorized"
			if reconciliationOpen {
				return s.persistUndispatchedMissionExpiry(ctx, deployment, replayed, message, now)
			}
			message = "deployment reconciliation window elapsed with no correlated terminal outcome"
			return s.persistUndispatchedMissionExpiry(ctx, deployment, replayed, message, now)
		}
		return DeployMissionResult{Deployment: deployment, Replayed: replayed}, nil
	}
	postExpiryReadback := commandExpired && deployment.DispatchStarted

	// Re-read all authoritative bindings immediately before every first dispatch
	// or reconciliation retry. A stale UI request can never choose an old mission.
	// The Agent independently fences a first effect at ExpiresAt while allowing
	// the same durably uncertain command to recover through ReconcileUntil.
	flight, mission, aircraft, err = s.validateMissionDeploymentBinding(ctx, flightID)
	if err != nil {
		return DeployMissionResult{}, err
	}
	if mission.ID != deployment.MissionID || mission.Version != deployment.MissionVersion ||
		mission.MissionDigest != deployment.MissionDigest || aircraft.AgentID != deployment.AgentID {
		return DeployMissionResult{}, fmt.Errorf("%w: mission or aircraft placement binding changed before dispatch", durable.ErrVersionConflict)
	}

	if !postExpiryReadback {
		previous, previousErr := s.durable.GetPreviousMissionDeploymentForAircraft(ctx, deployment.AircraftID, deployment.ID)
		if previousErr != nil && !errors.Is(previousErr, durable.ErrNotFound) {
			return DeployMissionResult{}, fmt.Errorf("get previous aircraft mission deployment: %w", previousErr)
		}
		if previousErr == nil && previous.FlightID != deployment.FlightID {
			oldContext := &agentv1.OperationContext{
				AircraftId:    previous.AircraftID,
				FlightId:      previous.FlightID,
				IntentId:      previous.IntentID,
				IntentVersion: uint32(previous.IntentVersion),
			}
			clearCommand := &agentv1.ClearOperationContextCommand{
				CommandId: deployment.ReconciliationClearCommandID,
				FlightId:  previous.FlightID,
			}
			if clearErr := s.missionDeployer.ClearOperationContextForReconciliation(ctx, deployment.AgentID, clearCommand, oldContext); clearErr != nil {
				updated := deployment
				updated.AttemptCount++
				updated.UpdatedAt = s.now().UTC()
				updated.Status = domain.MissionDeploymentTemporaryError
				updated.Message = "previous operation context was not conditionally cleared; mission was not dispatched: " + clearErr.Error()
				if err := s.durable.UpdateMissionDeployment(ctx, updated, deployment.Revision); err != nil {
					if errors.Is(err, durable.ErrVersionConflict) {
						current, getErr := s.durable.GetMissionDeployment(ctx, deployment.ID)
						if getErr == nil {
							return DeployMissionResult{Deployment: current, Replayed: true}, nil
						}
					}
					return DeployMissionResult{}, fmt.Errorf("persist operation context clear failure: %w", err)
				}
				updated.Revision++
				return DeployMissionResult{Deployment: updated, Replayed: replayed}, nil
			}
		}
		if phaseNow := s.now().UTC(); !deployment.ExpiresAt.After(phaseNow) {
			if deployment.Status == domain.MissionDeploymentOutcomeUnknown {
				postExpiryReadback = true
			} else {
				return s.persistUndispatchedMissionExpiry(ctx, deployment, replayed, "deployment authorization expired after context clear; mission was not dispatched", phaseNow)
			}
		}
	}

	if !postExpiryReadback {
		contextCommand := &agentv1.SetOperationContextCommand{
			CommandId: deployment.OperationContextCommandID,
			Context:   &agentv1.OperationContext{FlightId: flight.ID, IntentId: flight.IntentID, IntentVersion: uint32(flight.IntentVersion), AircraftId: flight.AircraftID},
		}
		if contextErr := s.missionDeployer.EnsureOperationContext(ctx, deployment.AgentID, contextCommand); contextErr != nil {
			updated := deployment
			updated.AttemptCount++
			updated.UpdatedAt = s.now().UTC()
			updated.Status = domain.MissionDeploymentTemporaryError
			updated.Message = "operation context was not acknowledged; mission was not dispatched: " + contextErr.Error()
			if err := s.durable.UpdateMissionDeployment(ctx, updated, deployment.Revision); err != nil {
				if errors.Is(err, durable.ErrVersionConflict) {
					current, getErr := s.durable.GetMissionDeployment(ctx, deployment.ID)
					if getErr == nil {
						return DeployMissionResult{Deployment: current, Replayed: true}, nil
					}
				}
				return DeployMissionResult{}, fmt.Errorf("persist operation context failure: %w", err)
			}
			updated.Revision++
			return DeployMissionResult{Deployment: updated, Replayed: replayed}, nil
		}
		if phaseNow := s.now().UTC(); !deployment.ExpiresAt.After(phaseNow) && deployment.Status != domain.MissionDeploymentOutcomeUnknown {
			return s.persistUndispatchedMissionExpiry(ctx, deployment, replayed, "deployment authorization expired after context acknowledgement; mission was not dispatched", phaseNow)
		}
	}

	command := missionDeploymentCommand(deployment, mission)
	dispatching := deployment
	dispatching.AttemptCount++
	dispatching.DispatchStarted = true
	dispatching.UpdatedAt = s.now().UTC()
	dispatching.Status = domain.MissionDeploymentOutcomeUnknown
	dispatching.Message = "mission dispatch began; no correlated Agent outcome has been persisted yet"
	if err := s.durable.UpdateMissionDeployment(ctx, dispatching, deployment.Revision); err != nil {
		if errors.Is(err, durable.ErrVersionConflict) {
			current, getErr := s.durable.GetMissionDeployment(ctx, deployment.ID)
			if getErr == nil {
				return DeployMissionResult{Deployment: current, Replayed: true}, nil
			}
		}
		return DeployMissionResult{}, fmt.Errorf("persist mission dispatch attempt: %w", err)
	}
	dispatching.Revision++
	deployment = dispatching

	result, deployErr := s.missionDeployer.DeployMission(ctx, deployment.AgentID, command)
	updated := deployment
	updated.UpdatedAt = s.now().UTC()
	if deployErr != nil {
		updated.Status = domain.MissionDeploymentOutcomeUnknown
		updated.Message = deployErr.Error()
	} else if resultErr := applyMissionDeploymentResult(&updated, result, command.GetBinding(), len(mission.Items)); resultErr != nil {
		updated.Status = domain.MissionDeploymentOutcomeUnknown
		updated.Message = resultErr.Error()
	}
	if err := s.durable.UpdateMissionDeployment(ctx, updated, deployment.Revision); err != nil {
		if errors.Is(err, durable.ErrVersionConflict) {
			current, getErr := s.durable.GetMissionDeployment(ctx, deployment.ID)
			if getErr == nil {
				return DeployMissionResult{Deployment: current, Replayed: true}, nil
			}
		}
		return DeployMissionResult{}, fmt.Errorf("persist mission deployment result: %w", err)
	}
	updated.Revision++
	return DeployMissionResult{Deployment: updated, Replayed: replayed}, nil
}

func (s *FleetService) persistUndispatchedMissionExpiry(ctx context.Context, deployment domain.MissionDeployment, replayed bool, message string, now time.Time) (DeployMissionResult, error) {
	updated := deployment
	updated.Status = domain.MissionDeploymentOutcomeUnknown
	updated.Message = message
	updated.UpdatedAt = now
	if err := s.durable.UpdateMissionDeployment(ctx, updated, deployment.Revision); err != nil {
		if errors.Is(err, durable.ErrVersionConflict) {
			current, getErr := s.durable.GetMissionDeployment(ctx, deployment.ID)
			if getErr == nil {
				return DeployMissionResult{Deployment: current, Replayed: true}, nil
			}
		}
		return DeployMissionResult{}, fmt.Errorf("persist expired mission deployment: %w", err)
	}
	updated.Revision++
	return DeployMissionResult{Deployment: updated, Replayed: replayed}, nil
}

// GetMissionDeployment returns a durable result scoped to its flight.
func (s *FleetService) GetMissionDeployment(ctx context.Context, flightID, deploymentID string) (domain.MissionDeployment, error) {
	if strings.TrimSpace(flightID) == "" || strings.TrimSpace(deploymentID) == "" {
		return domain.MissionDeployment{}, fmt.Errorf("%w: flight_id and deployment_id are required", ErrValidation)
	}
	deployment, err := s.durable.GetMissionDeployment(ctx, deploymentID)
	if err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("get mission deployment: %w", err)
	}
	if deployment.FlightID != flightID {
		return domain.MissionDeployment{}, durable.ErrNotFound
	}
	return deployment, nil
}

// GetCurrentMissionDeployment restores the authoritative deployment state for
// a flight's current immutable mission. Outstanding retryable work is returned
// ahead of terminal history so a reloaded client cannot accidentally create a
// second command while the original outcome is unresolved.
//
// Parameters:
//   - ctx: controls cancellation and deadlines for the durable read.
//   - flightID: identifies the flight whose current deployment is being restored.
//
// Returns:
//   - deployment: is the outstanding or latest deployment for the current mission.
//   - error: reports invalid scope, missing state, cancellation, or persistence failures.
func (s *FleetService) GetCurrentMissionDeployment(ctx context.Context, flightID string) (domain.MissionDeployment, error) {
	flightID = strings.TrimSpace(flightID)
	if flightID == "" {
		return domain.MissionDeployment{}, fmt.Errorf("%w: flight_id is required", ErrValidation)
	}
	deployment, err := s.durable.GetCurrentMissionDeploymentForFlight(ctx, flightID)
	if err != nil {
		return domain.MissionDeployment{}, fmt.Errorf("get current mission deployment: %w", err)
	}
	if deployment.FlightID != flightID {
		return domain.MissionDeployment{}, durable.ErrNotFound
	}
	return deployment, nil
}

// ReconcileMissionDeployment safely retries one server-owned deployment after
// restoring it by identity. Mission bytes, routing, command IDs, and the
// original idempotency key remain durable server state and are never accepted
// from the caller.
//
// Parameters:
//   - ctx: controls the Relay reconciliation attempt and durable writes.
//   - flightID: scopes the deployment to its owning flight.
//   - deploymentID: identifies the durable deployment restored by the client.
//
// Returns:
//   - result: is the original terminal result or the updated retry result.
//   - error: reports invalid scope, stale mission binding, unavailable control, or dependency failures.
func (s *FleetService) ReconcileMissionDeployment(ctx context.Context, flightID, deploymentID string) (DeployMissionResult, error) {
	deployment, err := s.GetMissionDeployment(ctx, strings.TrimSpace(flightID), strings.TrimSpace(deploymentID))
	if err != nil {
		return DeployMissionResult{}, err
	}
	if !missionDeploymentRetryable(deployment.Status) {
		if err := s.validateCurrentMissionDeploymentReplay(ctx, deployment); err != nil {
			return DeployMissionResult{}, err
		}
		return DeployMissionResult{Deployment: deployment, Replayed: true}, nil
	}
	return s.DeployCurrentMission(ctx, deployment.FlightID, deployment.MissionID, deployment.MissionDigest, deployment.IdempotencyKey)
}

// validateCurrentMissionDeploymentReplay permits a terminal result to replay
// across later lifecycle transitions, but never after another immutable mission
// has become current for the flight. This keeps an old successful command from
// masquerading as the deployment result for a newly imported route.
func (s *FleetService) validateCurrentMissionDeploymentReplay(ctx context.Context, deployment domain.MissionDeployment) error {
	mission, err := s.GetCurrentMission(ctx, deployment.FlightID)
	if err != nil {
		return err
	}
	if mission.ID != deployment.MissionID || mission.Version != deployment.MissionVersion ||
		mission.MissionDigest != deployment.MissionDigest || mission.OperatorID != deployment.OperatorID ||
		mission.AircraftID != deployment.AircraftID || mission.IntentID != deployment.IntentID ||
		mission.IntentVersion != deployment.IntentVersion {
		return fmt.Errorf("%w: terminal deployment belongs to a superseded mission binding", durable.ErrVersionConflict)
	}
	return nil
}

func (s *FleetService) validateMissionDeploymentBinding(ctx context.Context, flightID string) (domain.FlightRecord, domain.Mission, domain.Aircraft, error) {
	flight, err := s.durable.GetFlightRecord(ctx, flightID)
	if err != nil {
		return domain.FlightRecord{}, domain.Mission{}, domain.Aircraft{}, fmt.Errorf("get flight record: %w", err)
	}
	if flight.Status != domain.FlightStatusPlanned {
		return domain.FlightRecord{}, domain.Mission{}, domain.Aircraft{}, fmt.Errorf("%w: missions may only be deployed while flight %s is planned", ErrInvalidTransition, flight.ID)
	}
	mission, err := s.GetCurrentMission(ctx, flightID)
	if err != nil {
		return domain.FlightRecord{}, domain.Mission{}, domain.Aircraft{}, err
	}
	intent, err := s.durable.GetOperationalIntent(ctx, flight.IntentID)
	if err != nil {
		return domain.FlightRecord{}, domain.Mission{}, domain.Aircraft{}, fmt.Errorf("get current operational intent: %w", err)
	}
	if intent.Version != flight.IntentVersion || intent.AircraftID != flight.AircraftID ||
		(intent.Status != domain.IntentStatusAccepted && intent.Status != domain.IntentStatusActive) {
		return domain.FlightRecord{}, domain.Mission{}, domain.Aircraft{}, fmt.Errorf("%w: flight is not bound to the current accepted or active intent version", ErrInvalidTransition)
	}
	aircraft, err := s.durable.GetAircraft(ctx, flight.AircraftID)
	if err != nil {
		return domain.FlightRecord{}, domain.Mission{}, domain.Aircraft{}, fmt.Errorf("get bound aircraft: %w", err)
	}
	if strings.TrimSpace(aircraft.AgentID) == "" {
		return domain.FlightRecord{}, domain.Mission{}, domain.Aircraft{}, fmt.Errorf("%w: bound aircraft has no authoritative agent_id", ErrValidation)
	}
	if aircraft.OperatorID != mission.OperatorID || intent.OperatorID != mission.OperatorID {
		return domain.FlightRecord{}, domain.Mission{}, domain.Aircraft{}, fmt.Errorf("%w: deployment operator bindings disagree", durable.ErrVersionConflict)
	}
	return flight, mission, aircraft, nil
}

func missionDeploymentCommand(deployment domain.MissionDeployment, mission domain.Mission) *agentv1.DeployMissionCommand {
	return &agentv1.DeployMissionCommand{
		CommandId: deployment.CommandID,
		Binding: &agentv1.MissionBinding{
			MissionId: mission.ID, MissionVersion: uint32(mission.Version), MissionDigest: mission.MissionDigest,
			DeploymentId: deployment.ID, OperatorId: mission.OperatorID, AircraftId: mission.AircraftID,
			FlightId: mission.FlightID, IntentId: mission.IntentID, IntentVersion: uint32(mission.IntentVersion),
		},
		Plan: canonicalMissionPlan(mission.Items), IssuedAtUnixMs: deployment.IssuedAt.UnixMilli(),
		ExpiresAtUnixMs: deployment.ExpiresAt.UnixMilli(),
	}
}

func missionDeploymentRetryable(status domain.MissionDeploymentStatus) bool {
	return status == domain.MissionDeploymentPending || status == domain.MissionDeploymentTemporaryError || status == domain.MissionDeploymentOutcomeUnknown
}

func applyMissionDeploymentResult(deployment *domain.MissionDeployment, result *agentv1.MissionDeploymentResult, binding *agentv1.MissionBinding, itemCount int) error {
	if result == nil {
		return errors.New("relay returned no mission deployment result")
	}
	if result.GetCommandId() != deployment.CommandID || !missionBindingsEqual(result.GetBinding(), binding) {
		return errors.New("relay returned a mission deployment result for a different command or binding")
	}
	statuses := map[agentv1.MissionDeploymentResult_Status]domain.MissionDeploymentStatus{
		agentv1.MissionDeploymentResult_STATUS_APPLIED:                  domain.MissionDeploymentApplied,
		agentv1.MissionDeploymentResult_STATUS_ALREADY_APPLIED:          domain.MissionDeploymentAlreadyApplied,
		agentv1.MissionDeploymentResult_STATUS_REJECTED:                 domain.MissionDeploymentRejected,
		agentv1.MissionDeploymentResult_STATUS_TEMPORARY_ERROR:          domain.MissionDeploymentTemporaryError,
		agentv1.MissionDeploymentResult_STATUS_OUTCOME_UNKNOWN:          domain.MissionDeploymentOutcomeUnknown,
		agentv1.MissionDeploymentResult_STATUS_BINDING_MISMATCH:         domain.MissionDeploymentBindingMismatch,
		agentv1.MissionDeploymentResult_STATUS_ONBOARD_MISSION_MISMATCH: domain.MissionDeploymentOnboardMissionMismatch,
	}
	statusValue, ok := statuses[result.GetStatus()]
	if !ok {
		return fmt.Errorf("relay returned unsupported mission deployment status %s", result.GetStatus())
	}
	if statusValue == domain.MissionDeploymentApplied &&
		(result.GetOnboardMissionDigest() != deployment.MissionDigest || int(result.GetUploadedItemCount()) != itemCount) {
		statusValue = domain.MissionDeploymentOnboardMissionMismatch
	}
	if statusValue == domain.MissionDeploymentAlreadyApplied && result.GetOnboardMissionDigest() != deployment.MissionDigest {
		statusValue = domain.MissionDeploymentOnboardMissionMismatch
	}
	deployment.Status, deployment.Message = statusValue, result.GetMessage()
	deployment.UploadedItemCount, deployment.OnboardMissionDigest = result.GetUploadedItemCount(), result.GetOnboardMissionDigest()
	if result.MavlinkMissionAckType != nil {
		value := result.GetMavlinkMissionAckType()
		deployment.MAVLinkMissionAckType = &value
	}
	if completed := result.GetCompletedAtUnixMs(); completed > 0 {
		value := time.UnixMilli(completed).UTC()
		deployment.CompletedAt = &value
	}
	return nil
}

func missionBindingsEqual(left, right *agentv1.MissionBinding) bool {
	return left != nil && right != nil && left.GetMissionId() == right.GetMissionId() &&
		left.GetMissionVersion() == right.GetMissionVersion() && left.GetMissionDigest() == right.GetMissionDigest() &&
		left.GetDeploymentId() == right.GetDeploymentId() && left.GetOperatorId() == right.GetOperatorId() &&
		left.GetAircraftId() == right.GetAircraftId() && left.GetFlightId() == right.GetFlightId() &&
		left.GetIntentId() == right.GetIntentId() && left.GetIntentVersion() == right.GetIntentVersion()
}
