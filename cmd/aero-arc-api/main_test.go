package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/config"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestNewDurableStoreIsIndependentFromAirspaceConfiguration(t *testing.T) {
	cfg := config.Defaults()
	store, err := newDurableStore(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if store == nil {
		t.Fatal("store is nil")
	}
}

func TestNewDurableStoreRejectsUnsupportedBaseStore(t *testing.T) {
	cfg := config.Defaults()
	cfg.DurableStore = "unknown"
	_, err := newDurableStore(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "unsupported durable store") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewPostgresStoreRejectsInvalidConfiguration(t *testing.T) {
	cfg := config.Defaults()
	cfg.DurableStore = config.DurableStorePostgres
	cfg.DatabaseURL = "postgres://invalid host"
	_, err := newDurableStore(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected invalid PostGIS configuration to fail")
	}
}

func TestNewAirspaceProvidersUsesExplicitOrder(t *testing.T) {
	cfg := config.Defaults()
	cfg.AirspaceProviders = []string{airspaceprovider.ProviderLocal, airspaceprovider.ProviderInterUSS}
	cfg.DSSBaseURL = "http://dss.example"
	providers, err := newAirspaceProviders(cfg, durablememory.NewStore())
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0].ID() != "local" || providers[1].ID() != "interuss_scd" {
		t.Fatalf("providers = %#v", providers)
	}
}

func TestNewAirspaceProvidersRejectsUnsupportedProvider(t *testing.T) {
	cfg := config.Defaults()
	cfg.AirspaceProviders = []string{"unknown"}
	_, err := newAirspaceProviders(cfg, durablememory.NewStore())
	if err == nil || !strings.Contains(err.Error(), "unsupported airspace provider") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewAirspaceProvidersRequiresAtLeastOneProvider(t *testing.T) {
	cfg := config.Defaults()
	cfg.AirspaceProviders = nil
	_, err := newAirspaceProviders(cfg, durablememory.NewStore())
	if err == nil || !strings.Contains(err.Error(), "at least one airspace provider") {
		t.Fatalf("error = %v", err)
	}
}
