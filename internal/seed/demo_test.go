package seed_test

import (
	"context"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	"github.com/Aero-Arc/aero-arc-api/internal/seed"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	replaymemory "github.com/Aero-Arc/aero-arc-api/internal/store/replay/memory"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
)

func TestDemoPopulatesDashboardStores(t *testing.T) {
	ctx := context.Background()
	durable := durablememory.NewStore()
	seedStore := persistedDuplicateStore{Store: durable}
	telemetry := telemetrymemory.NewStore()
	replay := replaymemory.NewStore()
	registryClient := registry.NewMemoryClient()

	if err := seed.Demo(ctx, seedStore, telemetry, replay, registryClient); err != nil {
		t.Fatalf("Demo returned error: %v", err)
	}
	if err := seed.Demo(ctx, seedStore, telemetry, replay, registryClient); err != nil {
		t.Fatalf("Demo restart returned error: %v", err)
	}

	aircraft, err := durable.ListAircraft(ctx)
	if err != nil {
		t.Fatalf("ListAircraft returned error: %v", err)
	}
	if len(aircraft) != 4 {
		t.Fatalf("aircraft count = %d, want 4", len(aircraft))
	}

	intents, err := durable.ListOperationalIntents(ctx, "")
	if err != nil {
		t.Fatalf("ListOperationalIntents returned error: %v", err)
	}
	if len(intents) != 3 {
		t.Fatalf("intent count = %d, want 3", len(intents))
	}
	hawkIntents, err := durable.ListOperationalIntents(ctx, "aircraft-hawk-2")
	if err != nil {
		t.Fatalf("ListOperationalIntents hawk returned error: %v", err)
	}
	if len(hawkIntents) != 0 {
		t.Fatalf("hawk intent count = %d, want 0", len(hawkIntents))
	}

	hawk, err := durable.GetAircraft(ctx, "aircraft-hawk-2")
	if err != nil {
		t.Fatalf("GetAircraft hawk returned error: %v", err)
	}
	if hawk.AcceptanceStatus != domain.AcceptanceStatusAccepted {
		t.Fatalf("hawk acceptance = %q, want %q", hawk.AcceptanceStatus, domain.AcceptanceStatusAccepted)
	}
	if hawk.Status != domain.AircraftStatusActive {
		t.Fatalf("hawk status = %q, want %q", hawk.Status, domain.AircraftStatusActive)
	}

	hawkProfile, err := durable.GetAircraftOperatingProfile(ctx, "aircraft-hawk-2")
	if err != nil {
		t.Fatalf("GetAircraftOperatingProfile hawk returned error: %v", err)
	}
	if hawkProfile == nil || hawkProfile.MaxAltitudeFtAGL == nil || *hawkProfile.MaxAltitudeFtAGL != 400 {
		t.Fatalf("hawk profile = %#v, want max altitude 400 ft AGL", hawkProfile)
	}
	hawkLimits, err := durable.ListOperatingLimits(ctx, "aircraft-hawk-2")
	if err != nil {
		t.Fatalf("ListOperatingLimits hawk returned error: %v", err)
	}
	if len(hawkLimits) != 4 {
		t.Fatalf("hawk operating limit count = %d, want 4", len(hawkLimits))
	}

	volumes, err := durable.ListOperationalVolumes(ctx, "20410000-0000-4000-8000-000000000000")
	if err != nil {
		t.Fatalf("ListOperationalVolumes returned error: %v", err)
	}
	if len(volumes) != 1 {
		t.Fatalf("volume count = %d, want 1", len(volumes))
	}
	if volumes[0].ID != "volume-2041-route" || volumes[0].GeoJSON == "" {
		t.Fatalf("volume = %#v, want seeded inline GeoJSON route volume", volumes[0])
	}
	allVolumes, err := durable.ListOperationalVolumes(ctx, "")
	if err != nil {
		t.Fatalf("ListOperationalVolumes all returned error: %v", err)
	}
	if len(allVolumes) != 3 {
		t.Fatalf("all volume count = %d, want 3", len(allVolumes))
	}

	sample, err := telemetry.GetLatestSample(ctx, "aircraft-eagle-7")
	if err != nil {
		t.Fatalf("GetLatestSample returned error: %v", err)
	}
	if sample == nil {
		t.Fatal("expected latest telemetry sample")
	}

	ravenSample, err := telemetry.GetLatestSample(ctx, "aircraft-raven-5")
	if err != nil {
		t.Fatalf("GetLatestSample raven returned error: %v", err)
	}
	if ravenSample == nil {
		t.Fatal("expected raven last-known telemetry sample")
	}
	hawkSample, err := telemetry.GetLatestSample(ctx, "aircraft-hawk-2")
	if err != nil {
		t.Fatalf("GetLatestSample hawk returned error: %v", err)
	}
	if hawkSample == nil {
		t.Fatal("expected hawk ready telemetry sample")
	}

	fleet := service.NewFleetService(durable, telemetry, replay, registryClient)
	hawkDashboard, err := fleet.GetAircraftDashboard(ctx, "aircraft-hawk-2")
	if err != nil {
		t.Fatalf("GetAircraftDashboard hawk returned error: %v", err)
	}
	if hawkDashboard.Readiness.Status != domain.ReadinessStatusReady {
		t.Fatalf("hawk readiness = %q, want %q (%v)", hawkDashboard.Readiness.Status, domain.ReadinessStatusReady, hawkDashboard.Readiness.Reasons)
	}
	if hawkDashboard.CurrentIntent != nil {
		t.Fatalf("hawk current intent = %#v, want nil", hawkDashboard.CurrentIntent)
	}

	eagleDashboard, err := fleet.GetAircraftDashboard(ctx, "aircraft-eagle-7")
	if err != nil {
		t.Fatalf("GetAircraftDashboard eagle returned error: %v", err)
	}
	if eagleDashboard.CurrentIntent == nil || eagleDashboard.CurrentIntent.ID != "20410000-0000-4000-8000-000000000000" {
		t.Fatalf("eagle current intent = %#v, want 20410000-0000-4000-8000-000000000000", eagleDashboard.CurrentIntent)
	}
	falconDashboard, err := fleet.GetAircraftDashboard(ctx, "aircraft-falcon-3")
	if err != nil {
		t.Fatalf("GetAircraftDashboard falcon returned error: %v", err)
	}
	if falconDashboard.CurrentIntent == nil || falconDashboard.CurrentIntent.ID != "20420000-0000-4000-8000-000000000000" {
		t.Fatalf("falcon current intent = %#v, want 20420000-0000-4000-8000-000000000000", falconDashboard.CurrentIntent)
	}

	manifest, err := replay.GetReplayManifest(ctx, "flight-2041-a")
	if err != nil {
		t.Fatalf("GetReplayManifest returned error: %v", err)
	}
	if manifest == nil {
		t.Fatal("expected replay manifest")
	}
}

type persistedDuplicateStore struct {
	durable.Store
}

func (s persistedDuplicateStore) CreateAircraft(ctx context.Context, aircraft domain.Aircraft) error {
	if _, err := s.Store.GetAircraft(ctx, aircraft.ID); err == nil {
		return durable.ErrAlreadyExists
	}
	return s.Store.CreateAircraft(ctx, aircraft)
}
