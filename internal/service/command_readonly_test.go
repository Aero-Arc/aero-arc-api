package service

import (
	"context"
	"errors"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"testing"
)

type readOnlyCommandStore struct {
	durable.Store
	durable.CommandStore
}

func (*readOnlyCommandStore) GetFlightRecord(context.Context, string) (domain.FlightRecord, error) {
	return domain.FlightRecord{ID: "flight"}, nil
}
func (*readOnlyCommandStore) ListCommands(context.Context, string) ([]domain.Command, error) {
	return []domain.Command{{ID: "historical"}}, nil
}
func TestReadOnlyCommandConfigurationRejectsSubmission(t *testing.T) {
	svc := (&FleetService{durable: &readOnlyCommandStore{}}).WithCommandControl(nil, func(context.Context, string, domain.FlightRecord, string) error { return nil })
	if _, err := svc.SubmitCommand(context.Background(), "flight", "reader", "key", CommandRequest{Type: "ARM"}); !errors.Is(err, ErrMissionDeploymentUnavailable) {
		t.Fatalf("read-only submission: %v", err)
	}
	records, err := svc.ListFlightCommands(context.Background(), "flight", "reader")
	if err != nil || len(records) != 1 {
		t.Fatalf("read-only history unavailable: %v %v", records, err)
	}
}
