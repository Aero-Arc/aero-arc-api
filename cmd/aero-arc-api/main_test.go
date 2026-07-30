package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/config"
)

func TestNewDurableStoreUsesLocalMemoryProviderWithoutPostGIS(t *testing.T) {
	cfg := config.Defaults()
	store, providers, closeStore, err := newDurableStore(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeStore)
	if store == nil || len(providers) != 1 || providers[0].ID() != "local_durable_store" {
		t.Fatalf("store = %#v, providers = %#v", store, providers)
	}
}

func TestNewDurableStoreRejectsUnsupportedBaseStore(t *testing.T) {
	cfg := config.Defaults()
	cfg.DurableStore = "unknown"
	_, _, _, err := newDurableStore(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "unsupported durable store") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewDurableStoreReportsInvalidPostGISConfiguration(t *testing.T) {
	cfg := config.Defaults()
	cfg.PostGISDatabaseURL = "postgres://invalid host"
	_, _, _, err := newDurableStore(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected invalid PostGIS configuration to fail")
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
