package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/config"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
	spatialmemory "github.com/Aero-Arc/aero-arc-api/internal/spatialindex/memory"
)

func TestNewDurableStoreIsIndependentFromAirspaceConfiguration(t *testing.T) {
	store, err := newDurableStore("memory")
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("store is nil")
	}
}

func TestNewDurableStoreRejectsUnsupportedBaseStore(t *testing.T) {
	_, err := newDurableStore("unknown")
	if err == nil || !strings.Contains(err.Error(), "unsupported durable store") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewSpatialIndexModes(t *testing.T) {
	cfg := config.Defaults()
	cfg.SpatialIndex = config.SpatialIndexNone
	index, err := newSpatialIndex(context.Background(), cfg)
	if err != nil || index != nil {
		t.Fatalf("none index = %#v, error = %v", index, err)
	}
	cfg.SpatialIndex = config.SpatialIndexMemory
	index, err = newSpatialIndex(context.Background(), cfg)
	if err != nil || index == nil || index.ID() != "memory" {
		t.Fatalf("memory index = %#v, error = %v", index, err)
	}
	index.Close()

	cfg.SpatialIndex = config.SpatialIndexPostGIS
	cfg.PostGISDatabaseURL = "postgres://invalid host"
	_, err = newSpatialIndex(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected invalid PostGIS configuration to fail")
	}
}

func TestNewAirspaceProvidersUsesExplicitOrder(t *testing.T) {
	cfg := config.Defaults()
	cfg.AirspaceProviders = []string{config.AirspaceProviderLocal, config.AirspaceProviderInterUSS}
	cfg.DSSBaseURL = "http://dss.example"
	store, err := newDurableStore(cfg.DurableStore)
	if err != nil {
		t.Fatal(err)
	}
	projection := spatialindex.NewProjection(spatialmemory.New())
	if err := projection.Rebuild(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	providers, err := newAirspaceProviders(cfg, store, projection)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0].ID() != "local" || providers[1].ID() != "interuss_scd" {
		t.Fatalf("providers = %#v", providers)
	}
}

func TestNewInterUSSProviderBuildsConfiguredAdapter(t *testing.T) {
	cfg := config.Defaults()
	cfg.DSSBaseURL = "http://dss.example"
	cfg.DSSStaticToken = "test-token"
	provider, err := newInterUSSProvider(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if provider.ID() != "interuss_scd" {
		t.Fatalf("provider ID = %q", provider.ID())
	}
}
