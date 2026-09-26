package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	conformance "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/conformance/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// CompletionTransport consumes Relay delivery obligations after API persistence.
type CompletionTransport interface {
	DrainFlightCompletions(context.Context, func(context.Context, *pb.FlightCompletionEvidence) error) error
}

// WithFlightFinalization configures authoritative monitoring and publication
// cleanup. Parameters: publication supplies the existing DSS coordinator.
// Returns: this service for startup composition.
func (s *FleetService) WithFlightFinalization(publication DeconflictionCoordinator) *FleetService {
	s.completionPublication = publication
	return s
}

// RunFlightFinalization consumes completion events and retries external cleanup
// independently of HTTP. Only a current database lease can commit finalization.
func (s *FleetService) RunFlightFinalization(ctx context.Context) {
	store, ok := s.durable.(durable.CompletionStore)
	if !ok {
		return
	}
	transport, ok := s.commandTransport.(CompletionTransport)
	if !ok {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		delivery, cancel := context.WithTimeout(ctx, 20*time.Second)
		if err := transport.DrainFlightCompletions(delivery, store.AdmitFlightCompletion); err != nil && ctx.Err() == nil {
			slog.Warn("flight completion delivery pending", "error", err)
		}
		cancel()
		for i := 0; i < 4 && ctx.Err() == nil; i++ {
			c, err := store.ClaimFlightCompletion(ctx)
			if errors.Is(err, durable.ErrNotFound) {
				break
			}
			if err != nil {
				slog.Error("flight completion claim failed", "error", err)
				break
			}
			attempt, stop := context.WithTimeout(ctx, 60*time.Second)
			err = s.finalizeFlight(attempt, c, store)
			stop()
			if err != nil {
				if retryErr := store.RetryFlightCompletion(ctx, c, err); retryErr != nil {
					slog.Error("completion retry persistence failed", "error", retryErr)
				}
				slog.Warn("flight finalization pending", "flight_id", c.Evidence.Context.FlightId, "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *FleetService) finalizeFlight(ctx context.Context, c domain.FlightCompletion, store durable.CompletionStore) error {
	e := c.Evidence
	// Resolve command uncertainty before changing monitoring or Agent context.
	commands, ok := s.durable.(durable.CommandStore)
	if !ok {
		return ErrMissionDeploymentUnavailable
	}
	records, err := commands.ListCommands(ctx, e.Context.FlightId)
	if err != nil {
		return err
	}
	for _, command := range records {
		switch command.State {
		case "applied", "rejected", "failed", "timed_out":
		default:
			return fmt.Errorf("command %s requires reconciliation", command.ID)
		}
	}
	intent, err := s.durable.GetOperationalIntentVersion(ctx, e.Context.IntentId, int(e.Context.IntentVersion))
	if err != nil {
		return err
	}
	if intent.ConformanceRequired {
		client, ok := s.conformanceHistory.(conformance.ConformanceServiceClient)
		if !ok {
			return ErrConformanceHistoryUnavailable
		}
		response, err := client.EndAssignment(ctx, &conformance.EndAssignmentRequest{Source: "aero-arc-api", MessageId: e.EventId, AssignmentId: intent.ID, FlightId: e.Context.FlightId, AircraftId: e.Context.AircraftId, IntentVersion: e.Context.IntentVersion, FlightCompletedAt: timestamppb.New(time.Unix(0, max(e.LandedAtUnixNs, e.DisarmedAtUnixNs)))})
		if err != nil {
			return err
		}
		a := response.GetRecord().GetAssignment()
		if a.GetFlightId() != e.Context.FlightId || a.GetAircraftId() != e.Context.AircraftId || a.GetIntentVersion() != e.Context.IntentVersion {
			return fmt.Errorf("monitoring closure binding mismatch")
		}
	}
	if s.missionDeployer == nil {
		return ErrMissionDeploymentUnavailable
	}
	if err = s.missionDeployer.ClearOperationContextForReconciliation(ctx, e.AgentId, &pb.ClearOperationContextCommand{CommandId: e.EventId + "/clear", FlightId: e.Context.FlightId}, e.Context); err != nil {
		return err
	}
	var publication *domain.OperationalIntentPublication
	if s.completionPublication != nil && s.completionPublication.PublishingEnabled() {
		p := s.completionPublication.PublicationRequest(intent, domain.OperationalIntentExternalStateWithdrawn)
		publication = &p
	}
	return store.CompleteFlight(ctx, c, publication)
}

// GetFlightCompletion returns durable completion progress without inferring it
// from live connection state. Missing evidence is a not-found result.
func (s *FleetService) GetFlightCompletion(ctx context.Context, flightID string) (domain.FlightCompletion, error) {
	store, ok := s.durable.(durable.CompletionStore)
	if !ok {
		return domain.FlightCompletion{}, ErrMissionDeploymentUnavailable
	}
	return store.GetFlightCompletion(ctx, flightID)
}

// RequestIntentCompletion reports the automatically retried completion obligation.
// Parameters: ctx bounds reads; intentID scopes the current active flight.
// Returns: persisted progress; absent aircraft evidence is a validation error.
// No physical command or external cleanup executes in the request lifetime.
func (s *FleetService) RequestIntentCompletion(ctx context.Context, intentID string) (domain.FlightCompletion, error) {
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return domain.FlightCompletion{}, err
	}
	flights, err := s.durable.ListFlightRecords(ctx, intent.AircraftID)
	if err != nil {
		return domain.FlightCompletion{}, err
	}
	for _, f := range flights {
		if f.IntentID == intent.ID && f.IntentVersion == intent.Version && (f.Status == domain.FlightStatusActive || f.Status == domain.FlightStatusComplete) {
			c, err := s.GetFlightCompletion(ctx, f.ID)
			if errors.Is(err, durable.ErrNotFound) {
				return c, fmt.Errorf("%w: completion requires Agent mission/recovery, landed, and disarmed evidence", ErrValidation)
			}
			return c, err
		}
	}
	return domain.FlightCompletion{}, fmt.Errorf("%w: no flight exists for intent completion", ErrInvalidTransition)
}
