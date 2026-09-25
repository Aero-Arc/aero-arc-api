package durable

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

// CommandStore is the durable command authority; memory backends must not enable execution.
type CommandStore interface {
	RequeueCommand(context.Context, string) error
	AcceptCommand(context.Context, domain.Command, *domain.MissionDeployment) (domain.Command, error)
	ListCommands(context.Context, string) ([]domain.Command, error)
	GetCommand(context.Context, string) (domain.Command, error)
	FindCommand(context.Context, string, string) (domain.Command, error)
	ClaimCommand(context.Context) (domain.Command, error)
	FinishCommandAttempt(context.Context, domain.Command, []domain.CommandEvent, string) error
}
