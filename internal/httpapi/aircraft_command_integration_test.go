//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	"github.com/Aero-Arc/aero-arc-api/internal/relaycontrol"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	agentv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	relayv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/relay/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type commandRelayServer struct {
	relayv1.UnimplementedRelayControlServer
	requests chan *relayv1.SendAircraftCommandRequest
}

func (s *commandRelayServer) SendAircraftCommand(_ context.Context, request *relayv1.SendAircraftCommandRequest) (*relayv1.SendAircraftCommandResponse, error) {
	s.requests <- request
	command := request.GetCommand()
	return &relayv1.SendAircraftCommandResponse{Result: &agentv1.AircraftCommandResult{
		CommandId: command.GetCommandId(), AircraftId: command.GetAircraftId(),
		Status:  agentv1.AircraftCommandResult_STATUS_ACCEPTED,
		Message: "MAV_CMD_COMPONENT_ARM_DISARM accepted",
	}}, nil
}

func TestAircraftCommandAcrossHTTPRegistryAndRelayGRPC(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	relay := &commandRelayServer{requests: make(chan *relayv1.SendAircraftCommandRequest, 1)}
	relayv1.RegisterRelayControlServer(server, relay)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	registryClient := registry.NewMemoryClient()
	if _, err := registryClient.RegisterRelay(context.Background(), &registryv1.RegisterRelayRequest{Relay: &registryv1.Relay{
		RelayId: "relay-command", Address: host, GrpcPort: int32(port), LastHeartbeatUnixMs: now.UnixMilli(),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := registryClient.RegisterAgent(context.Background(), &registryv1.RegisterAgentRequest{
		Agent: &registryv1.Agent{AgentId: "agent-command", LastHeartbeatUnixMs: now.UnixMilli()}, RelayId: "relay-command",
	}); err != nil {
		t.Fatal(err)
	}

	transport, err := relaycontrol.New(registryClient, insecure.NewCredentials(), 2*time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	durable := durablememory.NewStore()
	if err := durable.CreateAircraft(context.Background(), domain.Aircraft{ID: "aircraft-command", AgentID: "agent-command"}); err != nil {
		t.Fatal(err)
	}
	commands := service.NewAircraftCommandService(durable, transport)
	handler := New(nil, time.Second).WithAircraftCommands(commands, 3*time.Second).Handler()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/aircraft/aircraft-command/commands/arm", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result domain.AircraftCommandResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.AircraftCommandStatusAccepted || result.AircraftID != "aircraft-command" || result.CommandID == "" {
		t.Fatalf("result = %+v", result)
	}
	select {
	case routed := <-relay.requests:
		if routed.GetAgentId() != "agent-command" || routed.GetCommand().GetType() != agentv1.AircraftCommandType_AIRCRAFT_COMMAND_TYPE_ARM {
			t.Fatalf("routed request = %+v", routed)
		}
	case <-time.After(time.Second):
		t.Fatal("owning Relay did not receive the command")
	}
}
