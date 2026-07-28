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
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
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
		MinAltitudeM: float64Ptr(10),
		MaxAltitudeM: float64Ptr(120),
		AltitudeRef:  domain.AltitudeReferenceAGL,
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

func TestHandleModifyOperationalIntentBlocksActiveIntent(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()

	if err := durable.CreateAircraft(ctx, domain.Aircraft{
		ID:               "aircraft-1",
		OperatorID:       "operator-1",
		Status:           domain.AircraftStatusActive,
		AcceptanceStatus: domain.AcceptanceStatusAccepted,
		RemoteIDStatus:   domain.RemoteIDStatusBroadcasting,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := durable.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID:                  "intent-1",
		OperatorID:          "operator-1",
		AircraftID:          "aircraft-1",
		Version:             1,
		Name:                "Mission aircraft-1",
		Summary:             "Active mission",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		Status:              domain.IntentStatusActive,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
		ActivatedAt:         timePtr(now),
		UpdatedAt:           now,
	}); err != nil {
		t.Fatal(err)
	}

	fleet := service.NewFleetService(durable, telemetry, replay, reg)
	server := NewWithWorkflows(
		fleet,
		service.NewIntentService(durable),
		service.NewPreflightService(durable),
		service.NewConformanceService(durable, telemetry),
		time.Second,
	)
	body := []byte(`{"reason":"operator_adjustment","expected_version":1,"intent":{"summary":"Adjusted inspection area"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operational-intents/intent-1/modify", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	var response map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["code"] != "active_intent_modification_blocked" {
		t.Fatalf("code = %#v, want active_intent_modification_blocked; body=%#v", response["code"], response)
	}
	if response["intent_id"] != "intent-1" || response["status"] != string(domain.IntentStatusActive) {
		t.Fatalf("response = %#v, want active intent identity", response)
	}
}

func TestHandleAddOperationalVolumeRejectsMissingAltitudeFields(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()
	if err := durable.CreateAircraft(ctx, domain.Aircraft{
		ID:               "aircraft-1",
		OperatorID:       "operator-1",
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
		Summary:             "Missing altitude fields should fail",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateIntent returned error: %v", err)
	}

	fleet := service.NewFleetService(durable, telemetry, replay, reg)
	server := NewWithWorkflows(
		fleet,
		intents,
		service.NewPreflightService(durable),
		service.NewConformanceService(durable, telemetry),
		time.Second,
		deconfliction.NewDeconflictionService(durable),
	)
	body := []byte(`{"id":"volume-1","sequence":1,"geojson":"{\"type\":\"Polygon\",\"coordinates\":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}","max_altitude_m":120,"altitude_ref":"agl","starts_at":"2026-06-15T15:00:00Z","ends_at":"2026-06-15T16:00:00Z"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operational-intents/"+intent.ID+"/volumes", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleCheckOperationalIntentDeconfliction(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()
	seedHTTPDeconflictionIntents(t, ctx, durable, now)

	fleet := service.NewFleetService(durable, telemetry, replay, reg)
	deconflictionService := deconfliction.NewDeconflictionService(durable)
	server := NewWithWorkflows(
		fleet,
		service.NewIntentService(durable, deconflictionService),
		service.NewPreflightService(durable),
		service.NewConformanceService(durable, telemetry),
		time.Second,
		deconflictionService,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operational-intents/intent-target/deconfliction/check", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body domain.DeconflictionResult
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Posture != domain.DeconflictionPosturePotentialConflict {
		t.Fatalf("posture = %q, want potential_conflict; body=%#v", body.Posture, body)
	}
	if len(body.Findings) != 1 || body.Findings[0].ConflictingIntentID != "intent-peer" {
		t.Fatalf("findings = %#v, want peer potential conflict", body.Findings)
	}
}

func TestHandleActivateOperationalIntentBlocksOnDeconflictionPotentialConflict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()
	seedHTTPDeconflictionIntents(t, ctx, durable, now)
	if err := durable.UpdateOperationalIntent(ctx, domain.OperationalIntent{
		ID:                  "intent-target",
		OperatorID:          "operator-1",
		AircraftID:          "aircraft-1",
		Version:             1,
		Name:                "target",
		Summary:             "target intent",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		Status:              domain.IntentStatusAccepted,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
		AcceptedAt:          timePtr(now),
		UpdatedAt:           now,
	}); err != nil {
		t.Fatalf("UpdateOperationalIntent returned error: %v", err)
	}

	fleet := service.NewFleetService(durable, telemetry, replay, reg)
	deconflictionService := deconfliction.NewDeconflictionService(durable)
	server := NewWithWorkflows(
		fleet,
		service.NewIntentService(durable, deconflictionService),
		service.NewPreflightService(durable),
		service.NewConformanceService(durable, telemetry),
		time.Second,
		deconflictionService,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operational-intents/intent-target/activate", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	findings, err := durable.ListConflictFindings(ctx, "intent-target", 1)
	if err != nil {
		t.Fatalf("ListConflictFindings returned error: %v", err)
	}
	if len(findings) != 1 || findings[0].Status != domain.ConflictFindingStatusPotentialConflict {
		t.Fatalf("findings = %#v, want stored potential conflict", findings)
	}
}

func TestHandleActivateOperationalIntentInvalidTransitionDoesNotRunDeconfliction(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()
	seedHTTPDeconflictionIntents(t, ctx, durable, now)

	fleet := service.NewFleetService(durable, telemetry, replay, reg)
	server := NewWithWorkflows(
		fleet,
		service.NewIntentService(durable),
		service.NewPreflightService(durable),
		service.NewConformanceService(durable, telemetry),
		time.Second,
		deconfliction.NewDeconflictionService(durable),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operational-intents/intent-target/activate", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	findings, err := durable.ListConflictFindings(ctx, "intent-target", 1)
	if err != nil {
		t.Fatalf("ListConflictFindings returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %#v, want none before lifecycle-valid activation", findings)
	}
}

func TestHandleActivateOperationalIntentDoesNotTrustOldVersionClearFinding(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	reg := registry.NewMemoryClient()
	if err := durable.CreateAircraft(ctx, domain.Aircraft{
		ID:               "aircraft-1",
		OperatorID:       "operator-1",
		Status:           domain.AircraftStatusActive,
		AcceptanceStatus: domain.AcceptanceStatusAccepted,
		RemoteIDStatus:   domain.RemoteIDStatusBroadcasting,
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatal(err)
	}
	soh := 95.0
	if err := durable.CreateBattery(ctx, domain.Battery{
		ID:            "battery-1",
		OperatorID:    "operator-1",
		SerialNumber:  "B-1",
		StateOfHealth: &soh,
		Status:        domain.MaintenanceStatusCurrent,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := durable.RecordBatteryInstallation(ctx, domain.BatteryInstallation{
		ID:          "install-1",
		OperatorID:  "operator-1",
		AircraftID:  "aircraft-1",
		BatteryID:   "battery-1",
		InstalledAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := durable.CreateOperationalIntent(ctx, domain.OperationalIntent{
		ID:                  "intent-target",
		OperatorID:          "operator-1",
		AircraftID:          "aircraft-1",
		Version:             2,
		Name:                "target",
		Summary:             "target intent",
		AuthorizationPath:   domain.AuthorizationPathDemo,
		PopulationCategory:  domain.PopulationCategoryOne,
		Status:              domain.IntentStatusAccepted,
		ConformanceRequired: true,
		PlannedStartAt:      now,
		PlannedEndAt:        now.Add(time.Hour),
		AcceptedAt:          timePtr(now),
		UpdatedAt:           now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := durable.RecordConflictFinding(ctx, domain.ConflictFinding{
		ID:            "old-clear",
		IntentID:      "intent-target",
		IntentVersion: 1,
		AircraftID:    "aircraft-1",
		Status:        domain.ConflictFindingStatusClear,
		Severity:      domain.SeverityInfo,
		SourceType:    domain.ConflictFindingSourceLocal,
		RuleVersion:   "local-dss-shaped-v1",
		EvaluatedAt:   now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := durable.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-target-v1",
		OperatorID:    "operator-1",
		IntentID:      "intent-target",
		IntentVersion: 1,
		Sequence:      1,
		GeoJSON:       `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := durable.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID:            "volume-target-v2",
		OperatorID:    "operator-1",
		IntentID:      "intent-target",
		IntentVersion: 2,
		Sequence:      1,
		GeoJSON:       `{bad-json`,
		MinAltitudeM:  10,
		MaxAltitudeM:  120,
		AltitudeRef:   domain.AltitudeReferenceAGL,
		StartsAt:      now,
		EndsAt:        now.Add(time.Hour),
		VolumeType:    domain.OperationalVolumeLoiter,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}

	fleet := service.NewFleetService(durable, telemetry, replay, reg)
	deconflictionService := deconfliction.NewDeconflictionService(durable)
	server := NewWithWorkflows(
		fleet,
		service.NewIntentService(durable, deconflictionService),
		service.NewPreflightService(durable),
		service.NewConformanceService(durable, telemetry),
		time.Second,
		deconflictionService,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/operational-intents/intent-target/activate", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	v1Findings, err := durable.ListConflictFindings(ctx, "intent-target", 1)
	if err != nil {
		t.Fatalf("ListConflictFindings v1 returned error: %v", err)
	}
	v2Findings, err := durable.ListConflictFindings(ctx, "intent-target", 2)
	if err != nil {
		t.Fatalf("ListConflictFindings v2 returned error: %v", err)
	}
	if len(v1Findings) != 1 || v1Findings[0].Status != domain.ConflictFindingStatusClear {
		t.Fatalf("v1 findings = %#v, want old clear preserved", v1Findings)
	}
	if len(v2Findings) != 1 || v2Findings[0].Status != domain.ConflictFindingStatusIndeterminate {
		t.Fatalf("v2 findings = %#v, want current version indeterminate", v2Findings)
	}
}

func seedHTTPDeconflictionIntents(t *testing.T, ctx context.Context, store *durablememory.Store, now time.Time) {
	t.Helper()
	for _, aircraftID := range []string{"aircraft-1", "aircraft-2"} {
		if err := store.CreateAircraft(ctx, domain.Aircraft{
			ID:               aircraftID,
			OperatorID:       "operator-1",
			Status:           domain.AircraftStatusActive,
			AcceptanceStatus: domain.AcceptanceStatusAccepted,
			RemoteIDStatus:   domain.RemoteIDStatusBroadcasting,
			CreatedAt:        now,
			UpdatedAt:        now,
		}); err != nil {
			t.Fatalf("CreateAircraft %s returned error: %v", aircraftID, err)
		}
	}
	soh := 95.0
	if err := store.CreateBattery(ctx, domain.Battery{
		ID:            "battery-1",
		OperatorID:    "operator-1",
		SerialNumber:  "B-1",
		StateOfHealth: &soh,
		Status:        domain.MaintenanceStatusCurrent,
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("CreateBattery returned error: %v", err)
	}
	if err := store.RecordBatteryInstallation(ctx, domain.BatteryInstallation{
		ID:          "install-1",
		OperatorID:  "operator-1",
		AircraftID:  "aircraft-1",
		BatteryID:   "battery-1",
		InstalledAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("RecordBatteryInstallation returned error: %v", err)
	}
	for _, intent := range []domain.OperationalIntent{
		{
			ID:                  "intent-target",
			OperatorID:          "operator-1",
			AircraftID:          "aircraft-1",
			Version:             1,
			Name:                "target",
			Summary:             "target intent",
			AuthorizationPath:   domain.AuthorizationPathDemo,
			PopulationCategory:  domain.PopulationCategoryOne,
			Status:              domain.IntentStatusDraft,
			ConformanceRequired: true,
			PlannedStartAt:      now,
			PlannedEndAt:        now.Add(time.Hour),
			UpdatedAt:           now,
		},
		{
			ID:                  "intent-peer",
			OperatorID:          "operator-1",
			AircraftID:          "aircraft-2",
			Version:             1,
			Name:                "peer",
			Summary:             "peer intent",
			AuthorizationPath:   domain.AuthorizationPathDemo,
			PopulationCategory:  domain.PopulationCategoryOne,
			Status:              domain.IntentStatusAccepted,
			ConformanceRequired: true,
			PlannedStartAt:      now,
			PlannedEndAt:        now.Add(time.Hour),
			AcceptedAt:          timePtr(now),
			UpdatedAt:           now,
		},
	} {
		if err := store.CreateOperationalIntent(ctx, intent); err != nil {
			t.Fatalf("CreateOperationalIntent %s returned error: %v", intent.ID, err)
		}
	}
	for _, volume := range []domain.OperationalVolume{
		{
			ID:            "volume-target",
			OperatorID:    "operator-1",
			IntentID:      "intent-target",
			IntentVersion: 1,
			Sequence:      1,
			GeoJSON:       `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
			MinAltitudeM:  10,
			MaxAltitudeM:  120,
			AltitudeRef:   domain.AltitudeReferenceAGL,
			StartsAt:      now,
			EndsAt:        now.Add(time.Hour),
			VolumeType:    domain.OperationalVolumeLoiter,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
		{
			ID:            "volume-peer",
			OperatorID:    "operator-1",
			IntentID:      "intent-peer",
			IntentVersion: 1,
			Sequence:      1,
			GeoJSON:       `{"type":"Polygon","coordinates":[[[-98,35],[-97,35],[-97,36],[-98,36],[-98,35]]]}`,
			MinAltitudeM:  20,
			MaxAltitudeM:  100,
			AltitudeRef:   domain.AltitudeReferenceAGL,
			StartsAt:      now,
			EndsAt:        now.Add(time.Hour),
			VolumeType:    domain.OperationalVolumeLoiter,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	} {
		if err := store.RecordOperationalVolume(ctx, volume); err != nil {
			t.Fatalf("RecordOperationalVolume %s returned error: %v", volume.ID, err)
		}
	}
}

func float64Ptr(value float64) *float64 {
	return &value
}

func timePtr(value time.Time) *time.Time {
	return &value
}
