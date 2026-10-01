package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

type readOnlyCommandStore struct {
	existing *domain.Command
	durable.Store
	durable.CommandStore
}

func (*readOnlyCommandStore) GetFlightRecord(context.Context, string) (domain.FlightRecord, error) {
	return domain.FlightRecord{ID: "flight"}, nil
}
func (*readOnlyCommandStore) ListCommands(context.Context, string) ([]domain.Command, error) {
	return []domain.Command{{ID: "historical"}}, nil
}
func (s *readOnlyCommandStore) FindCommand(context.Context, string, string) (domain.Command, error) {
	if s.existing != nil {
		return *s.existing, nil
	}
	return domain.Command{}, durable.ErrNotFound
}
func TestReadOnlyExactCommandReplay(t *testing.T) {
	req := CommandRequest{Type: "ARM"}
	raw, _ := json.Marshal(struct {
		Flight  string
		Request CommandRequest
	}{"flight", req})
	store := &readOnlyCommandStore{existing: &domain.Command{ID: "original", RequestHash: sha256Hex(string(raw))}}
	svc := (&FleetService{durable: store}).WithCommandControl(nil, func(context.Context, string, domain.FlightRecord, string) error { return nil })
	got, err := svc.SubmitCommand(context.Background(), "flight", "reader", "key", req)
	if err != nil || got.ID != "original" || !got.Replayed {
		t.Fatalf("exact read-only replay: %+v %v", got, err)
	}
	if _, err = svc.SubmitCommand(context.Background(), "flight", "reader", "key", CommandRequest{Type: "DISARM"}); !errors.Is(err, durable.ErrIdempotencyConflict) {
		t.Fatalf("conflicting read-only replay: %v", err)
	}
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

type clockCommandTransport struct{}

func (clockCommandTransport) ExchangeCommand(_ context.Context, _ string, c *pb.DurableCommand, _ string) (*pb.CommandEvidence, error) {
	return &pb.CommandEvidence{CommandId: c.CommandId, CommandDigest: c.CommandDigest}, nil
}
func TestCommandWorkerUsesConfiguredClockForExpiry(t *testing.T) {
	for _, offset := range []time.Duration{-24 * time.Hour, 24 * time.Hour} {
		for _, expired := range []bool{false, true} {
			now := time.Now().Add(offset)
			expiry := now.Add(time.Minute)
			if expired {
				expiry = now
			}
			deployer := &fakeMissionDeployer{}
			svc := &FleetService{now: func() time.Time { return now }, missionDeployer: deployer, commandTransport: clockCommandTransport{}}
			raw, _ := proto.Marshal(&pb.DurableCommand{CommandId: "c", AgentId: "agent"})
			svc.executeCommandAttempt(context.Background(), domain.Command{ID: "c", Type: "ARM", ExpiresAt: expiry, Payload: raw})
			want := 1
			if expired {
				want = 0
			}
			if len(deployer.contexts) != want {
				t.Fatalf("offset=%v expired=%v contexts=%d", offset, expired, len(deployer.contexts))
			}
		}
	}
}
