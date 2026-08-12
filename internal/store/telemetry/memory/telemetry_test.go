package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/telemetry"
)

func TestZeroLimitUsesDefaultSampleLimit(t *testing.T) {
	store := NewStore()
	start := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	count := telemetry.DefaultSampleLimit + 1
	for i := 0; i < count; i++ {
		if err := store.AddSample(context.Background(), domain.TelemetrySample{
			ID:         fmt.Sprintf("sample-%04d", i),
			AircraftID: "aircraft-1",
			FlightID:   "flight-1",
			RecordedAt: start.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	aircraftSamples, err := store.QueryAircraftSamples(context.Background(), "aircraft-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(aircraftSamples) != telemetry.DefaultSampleLimit || aircraftSamples[0].ID != "sample-0001" {
		t.Fatalf("unexpected latest window: len=%d first=%q", len(aircraftSamples), aircraftSamples[0].ID)
	}

	flightSamples, err := store.QueryFlightSamples(context.Background(), "flight-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(flightSamples) != telemetry.DefaultSampleLimit || flightSamples[0].ID != "sample-0000" {
		t.Fatalf("unexpected earliest window: len=%d first=%q", len(flightSamples), flightSamples[0].ID)
	}
}

func TestLatestAircraftStatesKeepsIndependentOutOfOrderObservations(t *testing.T) {
	store := NewStore()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	remaining := 64.0
	for _, sample := range []domain.TelemetrySample{
		{ID: "position-new", AircraftID: "aircraft-1", RecordedAt: now, Latitude: 2},
		{ID: "battery-old", AircraftID: "aircraft-1", RecordedAt: now.Add(-time.Second), Latitude: 1, BatteryPct: &remaining},
	} {
		if err := store.AddSample(context.Background(), sample); err != nil {
			t.Fatal(err)
		}
	}
	states, err := store.GetLatestAircraftStates(context.Background(), []string{"aircraft-1", "aircraft-missing"})
	if err != nil {
		t.Fatal(err)
	}
	state := states["aircraft-1"]
	if state.Position == nil || state.Position.FrameID != "position-new" {
		t.Fatalf("position = %#v", state.Position)
	}
	if state.Battery == nil || state.Battery.FrameID != "battery-old" || state.Battery.BatteryRemainingPct == nil || *state.Battery.BatteryRemainingPct != 64 {
		t.Fatalf("battery = %#v", state.Battery)
	}
	if _, ok := states["aircraft-missing"]; !ok {
		t.Fatal("missing requested aircraft key")
	}
}
