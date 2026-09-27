package service

import (
	"context"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	"google.golang.org/protobuf/proto"
)

type dispatchFenceStore struct {
	durable.Store
	durable.CommandStore
	completed bool
	permits   int
}

func (s *dispatchFenceStore) BeginCommandDispatch(context.Context, domain.Command) error {
	s.permits++
	if s.completed {
		return durable.ErrVersionConflict
	}
	return nil
}

type forbiddenCommandTransport struct{ t *testing.T }

func (s forbiddenCommandTransport) ExchangeCommand(context.Context, string, *pb.DurableCommand, string) (*pb.CommandEvidence, error) {
	s.t.Fatal("command crossed admitted-completion fence")
	return nil, nil
}
func TestCommandRechecksCompletionAfterContextSetup(t *testing.T) {
	for _, alreadyComplete := range []bool{false, true} {
		t.Run(map[bool]string{false: "during-context", true: "before-attempt"}[alreadyComplete], func(t *testing.T) {
			store := &dispatchFenceStore{completed: alreadyComplete}
			deployer := &fakeMissionDeployer{contextHook: func() { store.completed = true }}
			svc := &FleetService{durable: store, missionDeployer: deployer, commandTransport: forbiddenCommandTransport{t: t}}
			raw, err := proto.Marshal(&pb.DurableCommand{CommandId: "arm", AgentId: "agent", Context: &pb.OperationContext{FlightId: "flight"}})
			if err != nil {
				t.Fatal(err)
			}
			events, result := svc.executeCommandAttempt(context.Background(), domain.Command{ID: "arm", FlightID: "flight", Type: "ARM", Payload: raw, ExpiresAt: time.Now().Add(time.Minute)})
			if len(events) != 0 || result != durable.ErrVersionConflict.Error() {
				t.Fatalf("fence invented outcome: %+v %s", events, result)
			}
			want := 2
			if alreadyComplete {
				want = 1
			}
			if store.permits != want {
				t.Fatalf("dispatch checks=%d want %d", store.permits, want)
			}
			if alreadyComplete && len(deployer.contexts) != 0 {
				t.Fatal("context installed after completion")
			}
		})
	}
}
