package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
)

type fakeAircraftCommander struct {
	aircraftID  string
	commandType domain.AircraftCommandType
	result      domain.AircraftCommandResult
	err         error
}

func (f *fakeAircraftCommander) SendAircraftCommand(_ context.Context, aircraftID string, commandType domain.AircraftCommandType) (domain.AircraftCommandResult, error) {
	f.aircraftID = aircraftID
	f.commandType = commandType
	return f.result, f.err
}

func TestArmAircraftEndpointReturnsAutopilotResult(t *testing.T) {
	commands := &fakeAircraftCommander{result: domain.AircraftCommandResult{
		CommandID: "command-1", AircraftID: "aircraft-1", Status: domain.AircraftCommandStatusAccepted,
	}}
	handler := New(nil, time.Second).WithAircraftCommands(commands).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/aircraft/aircraft-1/commands/arm", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result domain.AircraftCommandResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if commands.aircraftID != "aircraft-1" || commands.commandType != domain.AircraftCommandTypeArm || result.Status != domain.AircraftCommandStatusAccepted {
		t.Fatalf("request = %s %s, result = %+v", commands.aircraftID, commands.commandType, result)
	}
}

func TestAircraftCommandEndpointReportsOfflineAircraft(t *testing.T) {
	commands := &fakeAircraftCommander{err: fmt.Errorf("%w: no active session", service.ErrAircraftNotConnected)}
	handler := New(nil, time.Second).WithAircraftCommands(commands).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/aircraft/aircraft-1/commands/disarm", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "AIRCRAFT_NOT_CONNECTED" || commands.commandType != domain.AircraftCommandTypeDisarm {
		t.Fatalf("body = %+v, command = %s", body, commands.commandType)
	}
}
