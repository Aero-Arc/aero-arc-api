package main

import (
	"context"
	"strings"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/config"
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
