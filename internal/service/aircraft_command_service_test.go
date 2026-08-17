package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/relaycontrol"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

type recordingAircraftCommandTransport struct {
	request relaycontrol.AircraftCommandRequest
	result  domain.AircraftCommandResult
	err     error
}

func (t *recordingAircraftCommandTransport) SendAircraftCommand(_ context.Context, request relaycontrol.AircraftCommandRequest) (domain.AircraftCommandResult, error) {
	t.request = request
	return t.result, t.err
}

func TestAircraftCommandServiceRoutesUsingDurableAgentMapping(t *testing.T) {
	store := durablememory.NewStore()
	if err := store.CreateAircraft(context.Background(), domain.Aircraft{ID: "aircraft-1", AgentID: "agent-7"}); err != nil {
		t.Fatal(err)
	}
	transport := &recordingAircraftCommandTransport{result: domain.AircraftCommandResult{
		CommandID: "command-1", AircraftID: "aircraft-1", Status: domain.AircraftCommandStatusAccepted,
	}}
	service := NewAircraftCommandService(store, transport)
	result, err := service.SendAircraftCommand(context.Background(), "aircraft-1", domain.AircraftCommandTypeArm)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.AircraftCommandStatusAccepted {
		t.Fatalf("result = %+v", result)
	}
	if transport.request.AgentID != "agent-7" || transport.request.AircraftID != "aircraft-1" || transport.request.Type != domain.AircraftCommandTypeArm {
		t.Fatalf("transport request = %+v", transport.request)
	}
}

func TestAircraftCommandServiceRejectsUnmappedAircraft(t *testing.T) {
	store := durablememory.NewStore()
	if err := store.CreateAircraft(context.Background(), domain.Aircraft{ID: "aircraft-1"}); err != nil {
		t.Fatal(err)
	}
	_, err := NewAircraftCommandService(store, &recordingAircraftCommandTransport{}).
		SendAircraftCommand(context.Background(), "aircraft-1", domain.AircraftCommandTypeArm)
	if !errors.Is(err, ErrAircraftNotConnected) {
		t.Fatalf("error = %v", err)
	}
}

func TestAircraftCommandServiceMapsOfflineAgent(t *testing.T) {
	store := durablememory.NewStore()
	if err := store.CreateAircraft(context.Background(), domain.Aircraft{ID: "aircraft-1", AgentID: "agent-1"}); err != nil {
		t.Fatal(err)
	}
	transport := &recordingAircraftCommandTransport{err: relaycontrol.ErrAgentNotConnected}
	_, err := NewAircraftCommandService(store, transport).
		SendAircraftCommand(context.Background(), "aircraft-1", domain.AircraftCommandTypeDisarm)
	if !errors.Is(err, ErrAircraftNotConnected) {
		t.Fatalf("error = %v", err)
	}
}
