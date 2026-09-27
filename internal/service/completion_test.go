package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	pb "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	conformance "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/conformance/v1"
	"google.golang.org/grpc"
)

type closureBindingStore struct {
	durable.Store
	durable.CommandStore
}

func (*closureBindingStore) ListCommands(context.Context, string) ([]domain.Command, error) {
	return nil, nil
}
func (*closureBindingStore) GetOperationalIntentVersion(context.Context, string, int) (domain.OperationalIntent, error) {
	return domain.OperationalIntent{ID: "intent-a", Version: 1, ConformanceRequired: true}, nil
}

type closureBindingClient struct {
	conformance.ConformanceServiceClient
	request  *conformance.EndAssignmentRequest
	intentID string
	agentID  string
}

func (c *closureBindingClient) EndAssignment(_ context.Context, req *conformance.EndAssignmentRequest, _ ...grpc.CallOption) (*conformance.EndAssignmentResponse, error) {
	c.request = req
	return &conformance.EndAssignmentResponse{Record: &conformance.AssignmentRecord{Assignment: &conformance.Assignment{FlightId: "flight", AircraftId: "aircraft", IntentId: c.intentID, AgentId: c.agentID, IntentVersion: 1}}}, nil
}
func TestFinalizationRequiresExactClosureIntent(t *testing.T) {
	for _, intentID := range []string{"intent-a", "intent-b", "wrong-agent"} {
		t.Run(intentID, func(t *testing.T) {
			client := &closureBindingClient{intentID: intentID, agentID: "agent-a"}
			if intentID == "wrong-agent" {
				client.intentID = "intent-a"
				client.agentID = "agent-b"
			}
			stop := errors.New("stop after binding validation")
			deployer := &fakeMissionDeployer{clearErr: stop}
			svc := &FleetService{durable: &closureBindingStore{}, conformanceHistory: client, missionDeployer: deployer}
			completion := domain.FlightCompletion{Evidence: &pb.FlightCompletionEvidence{EventId: "event", AgentId: "agent-a", Context: &pb.OperationContext{FlightId: "flight", AircraftId: "aircraft", IntentId: "intent-a", IntentVersion: 1}}}
			err := svc.finalizeFlight(context.Background(), completion, nil)
			if client.request.GetIntentId() != "intent-a" || client.request.GetAgentId() != "agent-a" {
				t.Fatalf("closure omitted evidence intent: %v", client.request)
			}
			if intentID == "intent-a" {
				if !errors.Is(err, stop) || len(deployer.clears) != 1 {
					t.Fatalf("valid closure did not reach cleanup: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "closure binding mismatch") || len(deployer.clears) != 0 {
				t.Fatalf("wrong intent closure reached cleanup: %v", err)
			}
		})
	}
}
