//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/readmodel"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	replaymemory "github.com/Aero-Arc/aero-arc-api/internal/store/replay/memory"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type liveStateRegistryServer struct {
	registryv1.UnimplementedAeroRegistryServer
	agent     *registryv1.Agent
	placement *registryv1.AgentPlacement
}

func (s liveStateRegistryServer) ListAgents(context.Context, *registryv1.ListAgentsRequest) (*registryv1.ListAgentsResponse, error) {
	return &registryv1.ListAgentsResponse{Agents: []*registryv1.Agent{s.agent}}, nil
}

func (s liveStateRegistryServer) GetAgentPlacement(context.Context, *registryv1.GetAgentPlacementRequest) (*registryv1.GetAgentPlacementResponse, error) {
	return &registryv1.GetAgentPlacementResponse{Placement: s.placement}, nil
}

func TestAircraftLiveStateAcrossHTTPAndRegistryGRPC(t *testing.T) {
	now := time.Now().UTC()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	registryv1.RegisterAeroRegistryServer(grpcServer, liveStateRegistryServer{
		agent:     &registryv1.Agent{AgentId: "agent-1", LastHeartbeatUnixMs: now.UnixMilli()},
		placement: &registryv1.AgentPlacement{AgentId: "agent-1", RelayId: "relay-1", LastUpdatedUnixMs: now.UnixMilli()},
	})
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	if err := durable.CreateAircraft(context.Background(), domain.Aircraft{ID: "aircraft-1", AgentID: "agent-1"}); err != nil {
		t.Fatal(err)
	}
	remaining := 88.0
	if err := telemetry.AddSample(context.Background(), domain.TelemetrySample{ID: "frame-1", AircraftID: "aircraft-1", RecordedAt: now, Latitude: 41.88, Longitude: -87.63, BatteryPct: &remaining}); err != nil {
		t.Fatal(err)
	}
	fleet := service.NewFleetService(durable, telemetry, replaymemory.NewStore(), registryv1.NewAeroRegistryClient(connection))
	handler := New(fleet, 3*time.Second).Handler()

	request := httptest.NewRequest(http.MethodGet, "/api/v1/aircraft/aircraft-1/state", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var state readmodel.AircraftLiveState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Connection.Connected || state.Connection.RelayID != "relay-1" || state.Connection.ConnectionStatus != domain.ConnectionStatusConnected {
		t.Fatalf("connection = %#v", state.Connection)
	}
	if state.Telemetry.Position == nil || state.Telemetry.Battery == nil || state.Telemetry.Status != domain.DataFreshnessFresh {
		t.Fatalf("telemetry = %#v", state.Telemetry)
	}
}
