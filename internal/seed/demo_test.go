package seed_test

import (
	"context"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	"github.com/Aero-Arc/aero-arc-api/internal/seed"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	replaymemory "github.com/Aero-Arc/aero-arc-api/internal/store/replay/memory"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
)

func TestDemoPopulatesDashboardStores(t *testing.T) {
	ctx := context.Background()
	durable := durablememory.NewStore()
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	registryClient := registry.NewMemoryClient()

	if err := seed.Demo(ctx, durable, telemetry, replay, registryClient); err != nil {
		t.Fatalf("Demo returned error: %v", err)
	}

	aircraft, err := durable.ListAircraft(ctx)
	if err != nil {
		t.Fatalf("ListAircraft returned error: %v", err)
	}
	if len(aircraft) != 3 {
		t.Fatalf("aircraft count = %d, want 3", len(aircraft))
	}

	intents, err := durable.ListOperationalIntents(ctx, "")
	if err != nil {
		t.Fatalf("ListOperationalIntents returned error: %v", err)
	}
	if len(intents) != 3 {
		t.Fatalf("intent count = %d, want 3", len(intents))
	}

	sample, err := telemetry.GetLatestSample(ctx, "aircraft-eagle-7")
	if err != nil {
		t.Fatalf("GetLatestSample returned error: %v", err)
	}
	if sample == nil {
		t.Fatal("expected latest telemetry sample")
	}

	manifest, err := replay.GetReplayManifest(ctx, "flight-2041-a")
	if err != nil {
		t.Fatalf("GetReplayManifest returned error: %v", err)
	}
	if manifest == nil {
		t.Fatal("expected replay manifest")
	}
}
