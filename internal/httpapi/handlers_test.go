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
	"github.com/Aero-Arc/aero-arc-api/internal/readmodel"
	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	replaymemory "github.com/Aero-Arc/aero-arc-api/internal/store/replay/memory"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
)

func TestHandleListAircraft(t *testing.T) {
	ctx := context.Background()
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()

	if err := durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", TailNumber: "N100AA"}); err != nil {
		t.Fatal(err)
	}
	if err := durable.CreateBattery(ctx, domain.Battery{ID: "battery-1", StateOfHealth: float64Ptr(91)}); err != nil {
		t.Fatal(err)
	}
	if err := durable.RecordBatteryInstallation(ctx, domain.BatteryInstallation{
		ID: "install-1", AircraftID: "aircraft-1", BatteryID: "battery-1", InstalledAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetLiveAircraftState(ctx, domain.LiveAircraftState{AircraftID: "aircraft-1", Connected: true}); err != nil {
		t.Fatal(err)
	}

	svc := service.NewFleetService(durable, telemetry, replay, reg)
	server := New(svc, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/aircraft", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body struct {
		Aircraft []readmodel.AircraftDashboard `json:"aircraft"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Aircraft) != 1 {
		t.Fatalf("aircraft count = %d, want 1", len(body.Aircraft))
	}
	if body.Aircraft[0].Aircraft.ID != "aircraft-1" {
		t.Fatalf("aircraft ID = %q, want aircraft-1", body.Aircraft[0].Aircraft.ID)
	}
	if body.Aircraft[0].Readiness.Status != "ready" {
		t.Fatalf("readiness = %q, want ready", body.Aircraft[0].Readiness.Status)
	}
}

func TestHandleGetAircraftUsesMachPathParam(t *testing.T) {
	ctx := context.Background()
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()

	if err := durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", TailNumber: "N100AA"}); err != nil {
		t.Fatal(err)
	}

	svc := service.NewFleetService(durable, telemetry, replay, reg)
	server := New(svc, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/aircraft/aircraft-1", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body readmodel.AircraftDashboard
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Aircraft.ID != "aircraft-1" {
		t.Fatalf("aircraft ID = %q, want aircraft-1", body.Aircraft.ID)
	}
}

func TestHandleGetAircraftMap(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 17, 15, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()

	if err := durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", AgentID: "agent-1", TailNumber: "N100AA", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetLiveAircraftState(ctx, domain.LiveAircraftState{AircraftID: "aircraft-1", AgentID: "agent-1", RelayID: "relay-1", Connected: true}); err != nil {
		t.Fatal(err)
	}
	for _, sample := range []domain.TelemetrySample{
		{ID: "sample-1", AircraftID: "aircraft-1", RecordedAt: now.Add(time.Minute), Latitude: 35.1, Longitude: -97.1, AltitudeM: 51},
		{ID: "sample-2", AircraftID: "aircraft-1", RecordedAt: now.Add(2 * time.Minute), Latitude: 35.2, Longitude: -97.2, AltitudeM: 52},
		{ID: "sample-3", AircraftID: "aircraft-1", RecordedAt: now.Add(3 * time.Minute), Latitude: 35.3, Longitude: -97.3, AltitudeM: 53},
	} {
		if err := telemetry.AddSample(ctx, sample); err != nil {
			t.Fatal(err)
		}
	}
	if err := durable.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID:             "intent-1",
		AircraftID:     "aircraft-1",
		Version:        1,
		Status:         domain.IntentStatusActive,
		PlannedStartAt: now,
		PlannedEndAt:   now.Add(time.Hour),
		UpdatedAt:      now,
	}); err != nil {
		t.Fatal(err)
	}

	svc := service.NewFleetService(durable, telemetry, replay, reg)
	server := New(svc, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/aircraft/aircraft-1/map?limit=2", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body readmodel.AircraftMapView
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Aircraft.ID != "aircraft-1" {
		t.Fatalf("aircraft ID = %q, want aircraft-1", body.Aircraft.ID)
	}
	if !body.LiveStateAvailable {
		t.Fatal("live state should be available")
	}
	if body.LatestTelemetry == nil || body.LatestTelemetry.ID != "sample-3" {
		t.Fatalf("latest telemetry = %#v, want sample-3", body.LatestTelemetry)
	}
	if len(body.ReplaySamples) != 2 || body.ReplaySamples[0].ID != "sample-2" || body.ReplaySamples[1].ID != "sample-3" {
		t.Fatalf("replay samples = %#v, want sample-2/sample-3", body.ReplaySamples)
	}
	if body.ActiveIntent == nil || body.ActiveIntent.ID != "intent-1" {
		t.Fatalf("active intent = %#v, want intent-1", body.ActiveIntent)
	}
}

func TestHandleGetOverviewDashboard(t *testing.T) {
	ctx := context.Background()
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()

	if err := durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", TailNumber: "N100AA"}); err != nil {
		t.Fatal(err)
	}

	svc := service.NewFleetService(durable, telemetry, replay, reg)
	server := New(svc, time.Second)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body readmodel.OverviewDashboard
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Metrics) == 0 {
		t.Fatal("metrics are empty")
	}
	if len(body.Aircraft) != 1 {
		t.Fatalf("aircraft count = %d, want 1", len(body.Aircraft))
	}
}

func TestHandleActivateOperationalIntentRunsPreflight(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()

	if err := durable.CreateAircraft(ctx, domain.Aircraft{
		ID:               "aircraft-1",
		OperatorID:       "operator-1",
		TailNumber:       "N100AA",
		Status:           domain.AircraftStatusActive,
		AcceptanceStatus: domain.AcceptanceStatusAccepted,
		RemoteIDStatus:   domain.RemoteIDStatusBroadcasting,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatal(err)
	}

	intents := service.NewIntentService(durable)
	intent, err := intents.CreateIntent(ctx, service.CreateIntentRequest{
		ID:                  "intent-1",
		OperatorID:          "operator-1",
		AircraftID:          "aircraft-1",
		Name:                "Demo intent",
		Summary:             "Activation should evaluate preflight",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if _, err = intents.AddOperationalVolume(ctx, intent.ID, service.AddOperationalVolumeRequest{
		ID:           "volume-1",
		Sequence:     1,
		GeoJSON:      `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
		MinAltitudeM: 10,
		MaxAltitudeM: 120,
		StartsAt:     now,
		EndsAt:       now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("AddOperationalVolume returned error: %v", err)
	}
	if _, err = intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatalf("SubmitIntent returned error: %v", err)
	}
	if _, err = intents.AcceptIntent(ctx, intent.ID); err != nil {
		t.Fatalf("AcceptIntent returned error: %v", err)
	}

	fleet := service.NewFleetService(durable, telemetry, replay, reg)
	server := NewWithWorkflows(
		fleet,
		intents,
		service.NewPreflightService(durable),
		service.NewConformanceService(durable, telemetry),
		time.Second,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operational-intents/intent-1/activate", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	checks, err := durable.ListPreflightChecks(ctx, intent.ID)
	if err != nil {
		t.Fatalf("ListPreflightChecks returned error: %v", err)
	}
	if len(checks) == 0 {
		t.Fatal("activate handler should have recorded preflight checks")
	}
}

func TestHandleAddOperationalVolumeRejectsSubmittedIntent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()

	if err := durable.CreateAircraft(ctx, domain.Aircraft{
		ID:               "aircraft-1",
		OperatorID:       "operator-1",
		TailNumber:       "N100AA",
		Status:           domain.AircraftStatusActive,
		AcceptanceStatus: domain.AcceptanceStatusAccepted,
		RemoteIDStatus:   domain.RemoteIDStatusBroadcasting,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatal(err)
	}

	intents := service.NewIntentService(durable)
	intent, err := intents.CreateIntent(ctx, service.CreateIntentRequest{
		ID:                  "intent-1",
		OperatorID:          "operator-1",
		AircraftID:          "aircraft-1",
		Name:                "Demo intent",
		Summary:             "Volume edits should lock after submit",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}
	if _, err = intents.SubmitIntent(ctx, intent.ID); err != nil {
		t.Fatalf("SubmitIntent returned error: %v", err)
	}

	fleet := service.NewFleetService(durable, telemetry, replay, reg)
	server := NewWithWorkflows(
		fleet,
		intents,
		service.NewPreflightService(durable),
		service.NewConformanceService(durable, telemetry),
		time.Second,
	)
	body := []byte(`{"id":"volume-1","sequence":1,"geojson":"{\"type\":\"Polygon\",\"coordinates\":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}","min_altitude_m":10,"max_altitude_m":120,"starts_at":"2026-06-15T15:00:00Z","ends_at":"2026-06-15T16:00:00Z"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operational-intents/intent-1/volumes", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func float64Ptr(value float64) *float64 {
	return &value
}
