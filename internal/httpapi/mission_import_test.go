package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	replaymemory "github.com/Aero-Arc/aero-arc-api/internal/store/replay/memory"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
	agentv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
)

func TestMissionImportAndCurrentHTTPContract(t *testing.T) {
	handler := newMissionHTTPHandler(t)
	payload := map[string]any{
		"source_format": "qgc_wpl_110", "aircraft_id": "aircraft-1", "intent_id": "intent-1", "intent_version": 1,
		"source": "QGC WPL 110\n0\t1\t0\t16\t0\t0\t0\t0\t-35.363262\t149.165237\t0\t1\n1\t0\t0\t22\t0\t0\t0\t0\t-35.363262\t149.165237\t20\t1\n2\t0\t0\t21\t0\t0\t0\t0\t-35.363262\t149.165237\t0\t1\n",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/flights/flight-1/missions/import", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "http-import-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("import status=%d body=%s", response.Code, response.Body.String())
	}
	var imported service.ImportMissionResult
	if err := json.Unmarshal(response.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Replayed || imported.Mission.FlightID != "flight-1" || len(imported.Mission.Items) != 2 {
		t.Fatalf("imported = %#v", imported)
	}
	if land := imported.Mission.Items[1]; land.Command != 21 || land.Param4 != 1 {
		t.Fatalf("canonical HTTP LAND item = %#v", land)
	}

	replayRequest := httptest.NewRequest(http.MethodPost, "/api/v1/flights/flight-1/missions/import", bytes.NewReader(raw))
	replayRequest.Header.Set("Idempotency-Key", "http-import-1")
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK || replayResponse.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay status=%d header=%q body=%s", replayResponse.Code, replayResponse.Header().Get("Idempotent-Replayed"), replayResponse.Body.String())
	}

	currentResponse := httptest.NewRecorder()
	handler.ServeHTTP(currentResponse, httptest.NewRequest(http.MethodGet, "/api/v1/flights/flight-1/missions/current", nil))
	if currentResponse.Code != http.StatusOK {
		t.Fatalf("current status=%d body=%s", currentResponse.Code, currentResponse.Body.String())
	}
	var current domain.Mission
	if err := json.Unmarshal(currentResponse.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if current.ID != imported.Mission.ID || current.MissionDigest == "" {
		t.Fatalf("current = %#v", current)
	}
}

type httpMissionDeployer struct{}

func (*httpMissionDeployer) EnsureOperationContext(context.Context, string, *agentv1.SetOperationContextCommand) error {
	return nil
}

func (*httpMissionDeployer) DeployMission(_ context.Context, _ string, command *agentv1.DeployMissionCommand) (*agentv1.MissionDeploymentResult, error) {
	return &agentv1.MissionDeploymentResult{
		CommandId: command.GetCommandId(), Binding: command.GetBinding(), Status: agentv1.MissionDeploymentResult_STATUS_APPLIED,
		UploadedItemCount: uint32(len(command.GetPlan().GetItems())), OnboardMissionDigest: command.GetBinding().GetMissionDigest(), CompletedAtUnixMs: 1,
	}, nil
}

func TestMissionDeploymentHTTPRequiresAuthorizationAndNoRoutingPayload(t *testing.T) {
	handler := newMissionHTTPHandler(t)
	importBody := `{"source_format":"qgc_wpl_110","aircraft_id":"aircraft-1","intent_id":"intent-1","intent_version":1,"source":"QGC WPL 110\n0\t1\t0\t16\t0\t0\t0\t0\t-35.363262\t149.165237\t0\t1\n1\t0\t0\t22\t0\t0\t0\t0\t-35.363262\t149.165237\t20\t1\n2\t0\t0\t21\t0\t0\t0\t0\t-35.363262\t149.165237\t0\t1\n"}`
	importRequest := httptest.NewRequest(http.MethodPost, "/api/v1/flights/flight-1/missions/import", bytes.NewBufferString(importBody))
	importRequest.Header.Set("Idempotency-Key", "http-deploy-import")
	importResponse := httptest.NewRecorder()
	handler.ServeHTTP(importResponse, importRequest)
	if importResponse.Code != http.StatusCreated {
		t.Fatalf("import status=%d body=%s", importResponse.Code, importResponse.Body.String())
	}

	unauthorized := httptest.NewRequest(http.MethodPost, "/api/v1/flights/flight-1/missions/current/deploy", nil)
	unauthorized.Header.Set("Idempotency-Key", "http-deploy")
	unauthorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", unauthorizedResponse.Code, unauthorizedResponse.Body.String())
	}

	withPayload := httptest.NewRequest(http.MethodPost, "/api/v1/flights/flight-1/missions/current/deploy", bytes.NewBufferString(`{"agent_id":"attacker"}`))
	withPayload.Header.Set("Authorization", "Bearer test-mission-deployment-token")
	withPayload.Header.Set("Idempotency-Key", "http-deploy")
	withPayloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(withPayloadResponse, withPayload)
	if withPayloadResponse.Code != http.StatusBadRequest {
		t.Fatalf("payload status=%d body=%s", withPayloadResponse.Code, withPayloadResponse.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/flights/flight-1/missions/current/deploy", nil)
	request.Header.Set("Authorization", "Bearer test-mission-deployment-token")
	request.Header.Set("Idempotency-Key", "http-deploy")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", response.Code, response.Body.String())
	}
	var result service.DeployMissionResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Deployment.Status != domain.MissionDeploymentApplied || bytes.Contains(response.Body.Bytes(), []byte("agent_id")) {
		t.Fatalf("deployment response=%s", response.Body.String())
	}
	statusRequest := httptest.NewRequest(http.MethodGet, "/api/v1/flights/flight-1/mission-deployments/"+result.Deployment.ID, nil)
	statusRequest.Header.Set("Authorization", "Bearer test-mission-deployment-token")
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
}

func TestMissionDeploymentHTTPFailsClosedWhenControlIsUnconfigured(t *testing.T) {
	handler := New(nil, time.Second).Handler()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/flights/flight-1/missions/current/deploy", nil)
	request.Header.Set("Authorization", "Bearer any-token")
	request.Header.Set("Idempotency-Key", "deploy-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMissionImportReturnsStructuredValidationFindings(t *testing.T) {
	handler := newMissionHTTPHandler(t)
	body := `{"source_format":"qgc_wpl_110","aircraft_id":"aircraft-1","intent_id":"intent-1","intent_version":1,"source":"QGC WPL 120\\n"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/flights/flight-1/missions/import", bytes.NewBufferString(body))
	request.Header.Set("Idempotency-Key", "invalid-http-import")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Findings []domain.MissionValidationFinding `json:"validation_findings"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) == 0 || result.Findings[0].Code != "invalid_header" {
		t.Fatalf("findings = %#v", result.Findings)
	}
}

func newMissionHTTPHandler(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	store := durablememory.NewStore()
	now := time.Now().UTC()
	if err := store.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", OperatorID: "operator-1", AgentID: "agent-1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID: "intent-1", Version: 1, OperatorID: "operator-1", AircraftID: "aircraft-1",
		Status: domain.IntentStatusAccepted, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID: "volume-1", IntentID: "intent-1", IntentVersion: 1, Sequence: 0,
		GeoJSON:      `{"type":"Polygon","coordinates":[[[149.15,-35.37],[149.18,-35.37],[149.18,-35.35],[149.15,-35.35],[149.15,-35.37]]]}`,
		MinAltitudeM: 0, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceMSL,
		StartsAt: now, EndsAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateFlightRecord(ctx, domain.FlightRecord{
		ID: "flight-1", OperatorID: "operator-1", AircraftID: "aircraft-1", IntentID: "intent-1", IntentVersion: 1, Status: domain.FlightStatusPlanned,
	}); err != nil {
		t.Fatal(err)
	}
	fleet := service.NewFleetService(store, telemetrymemory.NewStore(), replaymemory.NewStore(), registry.NewMemoryClient()).WithMissionDeployer(&httpMissionDeployer{})
	return New(fleet, time.Second).WithMissionDeploymentControl(time.Second, "test-mission-deployment-token").Handler()
}
