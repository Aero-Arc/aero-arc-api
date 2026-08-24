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
	setRequests   []*relayv1.SetOperationContextRequest
	clearRequests []*relayv1.ClearOperationContextRequest
	setErrors     []error
	block         bool
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
	return &relayv1.SetOperationContextResponse{Result: &agentv1.OperationContextCommandAck{CommandId: r.Command.GetCommandId(), Status: agentv1.OperationContextCommandAck_STATUS_APPLIED}}, nil
}
func (f *fakeRelayClient) ClearOperationContext(_ context.Context, r *relayv1.ClearOperationContextRequest, _ ...grpc.CallOption) (*relayv1.ClearOperationContextResponse, error) {
	f.clearRequests = append(f.clearRequests, r)
	return &relayv1.ClearOperationContextResponse{Result: &agentv1.OperationContextCommandAck{CommandId: r.Command.GetCommandId(), Status: agentv1.OperationContextCommandAck_STATUS_ALREADY_APPLIED}}, nil
}

func TestSetOperationContextCachesPlacementAndPreservesCommandID(t *testing.T) {
	registry := &fakeRegistry{relayIDs: []string{"relay-1"}}
	client := &fakeRelayClient{}
	service := newWithPool(registry, &fakePool{clients: map[string]*fakeRelayClient{"relay-1": client}}, time.Second, time.Minute)
	for i := 0; i < 2; i++ {
		id, err := service.SetOperationContext(context.Background(), SetRequest{AgentID: "agent-1", FlightID: "flight-1", IntentID: "intent-1", IntentVersion: 3, CommandID: "command-1"})
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
	if context.GetFlightId() != "flight-1" || context.GetIntentId() != "intent-1" || context.GetIntentVersion() != 3 {
		t.Fatalf("context=%v", context)
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
