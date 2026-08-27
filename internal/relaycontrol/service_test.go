package relaycontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	agentv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	relayv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/relay/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type fakeRegistry struct {
	relayIDs []string
	calls    int
}

func (f *fakeRegistry) GetAgentPlacement(_ context.Context, _ *registryv1.GetAgentPlacementRequest, _ ...grpc.CallOption) (*registryv1.GetAgentPlacementResponse, error) {
	i := f.calls
	if i >= len(f.relayIDs) {
		i = len(f.relayIDs) - 1
	}
	f.calls++
	return &registryv1.GetAgentPlacementResponse{Placement: &registryv1.AgentPlacement{AgentId: "agent-1", RelayId: f.relayIDs[i]}}, nil
}
func (f *fakeRegistry) ListRelays(context.Context, *registryv1.ListRelaysRequest, ...grpc.CallOption) (*registryv1.ListRelaysResponse, error) {
	return &registryv1.ListRelaysResponse{Relays: []*registryv1.Relay{{RelayId: "relay-1", Address: "relay-one", GrpcPort: 50051}, {RelayId: "relay-2", Address: "relay-two:50052"}}}, nil
}

type fakePool struct {
	clients     map[string]*fakeRelayClient
	invalidated []string
}

func (f *fakePool) Client(_ context.Context, id, _ string) (relayv1.RelayControlClient, error) {
	return f.clients[id], nil
}
func (f *fakePool) Invalidate(id string) { f.invalidated = append(f.invalidated, id) }
func (f *fakePool) Close() error         { return nil }

type fakeRelayClient struct {
	setRequests       []*relayv1.SetOperationContextRequest
	clearRequests     []*relayv1.ClearOperationContextRequest
	deployRequests    []*relayv1.DeployMissionRequest
	setErrors         []error
	activeContext     *agentv1.OperationContext
	omitActiveContext bool
	clearResult       *agentv1.OperationContextCommandAck
	clearErr          error
	block             bool
}

func TestNewRequiresRelayTransportCredentials(t *testing.T) {
	if _, err := New(&fakeRegistry{relayIDs: []string{"relay-1"}}, nil, time.Second, time.Minute); err == nil {
		t.Fatal("New returned nil error without relay transport credentials")
	}
	service, err := New(&fakeRegistry{relayIDs: []string{"relay-1"}}, insecure.NewCredentials(), time.Second, time.Minute)
	if err != nil {
		t.Fatalf("New returned error with relay transport credentials: %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func (f *fakeRelayClient) ListActiveDrones(context.Context, *relayv1.ListActiveDronesRequest, ...grpc.CallOption) (*relayv1.ListActiveDronesResponse, error) {
	return nil, errors.New("unused")
}
func (f *fakeRelayClient) GetDroneStatus(context.Context, *relayv1.GetDroneStatusRequest, ...grpc.CallOption) (*relayv1.GetDroneStatusResponse, error) {
	return nil, errors.New("unused")
}
func (f *fakeRelayClient) SendAircraftCommand(context.Context, *relayv1.SendAircraftCommandRequest, ...grpc.CallOption) (*relayv1.SendAircraftCommandResponse, error) {
	return nil, errors.New("unused")
}
func (f *fakeRelayClient) DeployMission(_ context.Context, request *relayv1.DeployMissionRequest, _ ...grpc.CallOption) (*relayv1.DeployMissionResponse, error) {
	f.deployRequests = append(f.deployRequests, request)
	return &relayv1.DeployMissionResponse{Result: &agentv1.MissionDeploymentResult{
		CommandId: request.GetCommand().GetCommandId(), Binding: request.GetCommand().GetBinding(),
		Status: agentv1.MissionDeploymentResult_STATUS_APPLIED,
	}}, nil
}
func (f *fakeRelayClient) SetOperationContext(ctx context.Context, r *relayv1.SetOperationContextRequest, _ ...grpc.CallOption) (*relayv1.SetOperationContextResponse, error) {
	f.setRequests = append(f.setRequests, r)
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if len(f.setErrors) > 0 {
		err := f.setErrors[0]
		f.setErrors = f.setErrors[1:]
		if err != nil {
			return nil, err
		}
	}
	active := f.activeContext
	if active == nil && !f.omitActiveContext {
		active = r.Command.GetContext()
	}
	return &relayv1.SetOperationContextResponse{Result: &agentv1.OperationContextCommandAck{CommandId: r.Command.GetCommandId(), Status: agentv1.OperationContextCommandAck_STATUS_APPLIED, ActiveContext: active}}, nil
}

func TestEnsureOperationContextRejectsMissingOrMismatchedActiveContext(t *testing.T) {
	command := &agentv1.SetOperationContextCommand{CommandId: "context-command", Context: &agentv1.OperationContext{
		AircraftId: "aircraft-1", FlightId: "flight-1", IntentId: "intent-1", IntentVersion: 3,
	}}
	for _, test := range []struct {
		name   string
		client *fakeRelayClient
	}{
		{name: "missing", client: &fakeRelayClient{omitActiveContext: true}},
		{name: "mismatch", client: &fakeRelayClient{activeContext: &agentv1.OperationContext{AircraftId: "aircraft-2", FlightId: "flight-1", IntentId: "intent-1", IntentVersion: 3}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newWithPool(&fakeRegistry{relayIDs: []string{"relay-1"}}, &fakePool{clients: map[string]*fakeRelayClient{"relay-1": test.client}}, time.Second, time.Minute)
			if err := service.EnsureOperationContext(context.Background(), "agent-1", command); err == nil {
				t.Fatal("EnsureOperationContext returned nil error")
			}
		})
	}
}
func (f *fakeRelayClient) ClearOperationContext(_ context.Context, r *relayv1.ClearOperationContextRequest, _ ...grpc.CallOption) (*relayv1.ClearOperationContextResponse, error) {
	f.clearRequests = append(f.clearRequests, r)
	if f.clearErr != nil {
		return nil, f.clearErr
	}
	if f.clearResult != nil {
		return &relayv1.ClearOperationContextResponse{Result: f.clearResult}, nil
	}
	return &relayv1.ClearOperationContextResponse{Result: &agentv1.OperationContextCommandAck{CommandId: r.Command.GetCommandId(), Status: agentv1.OperationContextCommandAck_STATUS_ALREADY_APPLIED}}, nil
}

func TestClearOperationContextForReconciliationRequiresCorrelatedSafeState(t *testing.T) {
	old := &agentv1.OperationContext{AircraftId: "aircraft-1", FlightId: "flight-1", IntentId: "intent-1", IntentVersion: 3}
	command := &agentv1.ClearOperationContextCommand{CommandId: "clear-command", FlightId: old.GetFlightId()}
	for _, test := range []struct {
		name    string
		result  *agentv1.OperationContextCommandAck
		wantErr bool
	}{
		{name: "cleared", result: &agentv1.OperationContextCommandAck{CommandId: command.GetCommandId(), Status: agentv1.OperationContextCommandAck_STATUS_APPLIED}},
		{name: "newer context preserved", result: &agentv1.OperationContextCommandAck{CommandId: command.GetCommandId(), Status: agentv1.OperationContextCommandAck_STATUS_ALREADY_APPLIED, ActiveContext: &agentv1.OperationContext{AircraftId: old.GetAircraftId(), FlightId: "new-flight", IntentId: "new-intent", IntentVersion: 4}}},
		{name: "old context remains", result: &agentv1.OperationContextCommandAck{CommandId: command.GetCommandId(), Status: agentv1.OperationContextCommandAck_STATUS_APPLIED, ActiveContext: old}, wantErr: true},
		{name: "mismatched ack", result: &agentv1.OperationContextCommandAck{CommandId: "another-command", Status: agentv1.OperationContextCommandAck_STATUS_APPLIED}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeRelayClient{clearResult: test.result}
			service := newWithPool(&fakeRegistry{relayIDs: []string{"relay-1"}}, &fakePool{clients: map[string]*fakeRelayClient{"relay-1": client}}, time.Second, time.Minute)
			err := service.ClearOperationContextForReconciliation(context.Background(), "agent-1", command, old)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if len(client.clearRequests) != 1 {
				t.Fatalf("clear requests = %d", len(client.clearRequests))
			}
		})
	}
	service := newWithPool(&fakeRegistry{}, &fakePool{}, time.Second, time.Minute)
	if err := service.ClearOperationContextForReconciliation(context.Background(), "", command, old); err == nil {
		t.Fatal("invalid clear request returned nil error")
	}
	transportFailure := &fakeRelayClient{clearErr: errors.New("relay unavailable")}
	service = newWithPool(&fakeRegistry{relayIDs: []string{"relay-1"}}, &fakePool{clients: map[string]*fakeRelayClient{"relay-1": transportFailure}}, time.Second, time.Minute)
	if err := service.ClearOperationContextForReconciliation(context.Background(), "agent-1", command, old); err == nil {
		t.Fatal("clear transport failure returned nil error")
	}
}

func TestSetOperationContextCachesPlacementAndPreservesCommandID(t *testing.T) {
	registry := &fakeRegistry{relayIDs: []string{"relay-1"}}
	client := &fakeRelayClient{}
	service := newWithPool(registry, &fakePool{clients: map[string]*fakeRelayClient{"relay-1": client}}, time.Second, time.Minute)
	for i := 0; i < 2; i++ {
		id, err := service.SetOperationContext(context.Background(), SetRequest{AgentID: "agent-1", AircraftID: "aircraft-1", FlightID: "flight-1", IntentID: "intent-1", IntentVersion: 3, CommandID: "command-1"})
		if err != nil {
			t.Fatal(err)
		}
		if id != "command-1" {
			t.Fatalf("id=%q", id)
		}
	}
	if registry.calls != 1 {
		t.Fatalf("registry calls=%d", registry.calls)
	}
	if len(client.setRequests) != 2 {
		t.Fatalf("requests=%d", len(client.setRequests))
	}
	context := client.setRequests[0].Command.GetContext()
	if context.GetAircraftId() != "aircraft-1" || context.GetFlightId() != "flight-1" || context.GetIntentId() != "intent-1" || context.GetIntentVersion() != 3 {
		t.Fatalf("context=%v", context)
	}
}

func TestDeployMissionUsesAuthoritativePlacementAndPreservesCommand(t *testing.T) {
	client := &fakeRelayClient{}
	service := newWithPool(&fakeRegistry{relayIDs: []string{"relay-1"}}, &fakePool{clients: map[string]*fakeRelayClient{"relay-1": client}}, time.Second, time.Minute)
	command := &agentv1.DeployMissionCommand{CommandId: "deploy-command", Binding: &agentv1.MissionBinding{MissionId: "mission-1"}}
	result, err := service.DeployMission(context.Background(), "agent-1", command)
	if err != nil {
		t.Fatal(err)
	}
	if result.GetCommandId() != command.GetCommandId() || len(client.deployRequests) != 1 ||
		client.deployRequests[0].GetAgentId() != "agent-1" || client.deployRequests[0].GetCommand() != command {
		t.Fatalf("result=%#v requests=%#v", result, client.deployRequests)
	}
}

func TestUnavailableInvalidatesPlacementAndRetriesSameCommand(t *testing.T) {
	registry := &fakeRegistry{relayIDs: []string{"relay-1", "relay-2"}}
	first := &fakeRelayClient{setErrors: []error{status.Error(codes.Unavailable, "moved")}}
	second := &fakeRelayClient{}
	pool := &fakePool{clients: map[string]*fakeRelayClient{"relay-1": first, "relay-2": second}}
	service := newWithPool(registry, pool, time.Second, time.Minute)
	_, err := service.SetOperationContext(context.Background(), SetRequest{AgentID: "agent-1", FlightID: "flight-1", CommandID: "stable"})
	if err != nil {
		t.Fatal(err)
	}
	if registry.calls != 2 || len(pool.invalidated) != 1 || pool.invalidated[0] != "relay-1" {
		t.Fatalf("calls=%d invalidated=%v", registry.calls, pool.invalidated)
	}
	if second.setRequests[0].Command.GetCommandId() != "stable" {
		t.Fatalf("command changed: %v", second.setRequests[0].Command)
	}
}

func TestClearOperationContext(t *testing.T) {
	client := &fakeRelayClient{}
	service := newWithPool(&fakeRegistry{relayIDs: []string{"relay-1"}}, &fakePool{clients: map[string]*fakeRelayClient{"relay-1": client}}, time.Second, time.Minute)
	id, err := service.ClearOperationContext(context.Background(), ClearRequest{AgentID: "agent-1", FlightID: "flight-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 {
		t.Fatalf("generated id=%q", id)
	}
	if client.clearRequests[0].Command.GetFlightId() != "flight-1" {
		t.Fatalf("request=%v", client.clearRequests[0])
	}
}

func TestTimeout(t *testing.T) {
	client := &fakeRelayClient{block: true}
	service := newWithPool(&fakeRegistry{relayIDs: []string{"relay-1"}}, &fakePool{clients: map[string]*fakeRelayClient{"relay-1": client}}, 10*time.Millisecond, time.Minute)
	_, err := service.SetOperationContext(context.Background(), SetRequest{AgentID: "agent-1", FlightID: "flight-1", CommandID: "command"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
}
