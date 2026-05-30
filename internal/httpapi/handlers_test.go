package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/Aero-Arc/aero-arc-api/internal/store/memory"
)

func TestHandleListAircraft(t *testing.T) {
	ctx := context.Background()
	durable := memory.NewDurableStore()
	telemetry := memory.NewTelemetryStore()
	replay := memory.NewReplayStore()
	reg := registry.NewMemoryClient()

	if err := durable.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", TailNumber: "N100AA"}); err != nil {
		t.Fatal(err)
	}
	if err := durable.CreateBattery(ctx, domain.Battery{ID: "battery-1", StateOfHealth: 91}); err != nil {
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
		Aircraft []domain.AircraftDashboard `json:"aircraft"`
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
