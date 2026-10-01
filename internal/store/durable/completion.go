package durable

import (
	"context"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
)

// CompletionStore owns idempotent completion evidence and fenced finalization.
type CompletionStore interface {
	AdmitFlightCompletion(context.Context, *pb.FlightCompletionEvidence) error
	ClaimFlightCompletion(context.Context) (domain.FlightCompletion, error)
	RetryFlightCompletion(context.Context, domain.FlightCompletion, error) error
	CompleteFlight(context.Context, domain.FlightCompletion, *domain.OperationalIntentPublication) error
	GetFlightCompletion(context.Context, string) (domain.FlightCompletion, error)
}
