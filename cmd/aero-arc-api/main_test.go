package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	"github.com/Aero-Arc/aero-arc-api/internal/config"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestStartCommandConformanceHistoryConfiguration(t *testing.T) {
	keys := []string{"ADDR", "CA_FILE", "CERT_FILE", "KEY_FILE", "SERVER_NAME"}
	values := []string{"localhost:50052", "/demo/ca.crt", "/demo/client.crt", "/demo/client.key", "localhost"}
	flags := []string{"conformance-addr", "conformance-ca-file", "conformance-cert-file", "conformance-key-file", "conformance-server-name"}
	for _, mode := range []string{"environment", "flags", "disabled", "partial"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{"aero-arc-api", "start"}
			for i, key := range keys {
				t.Setenv("AERO_API_CONFORMANCE_"+key, "")
				if mode == "environment" || (mode == "partial" && i == 0) {
					t.Setenv("AERO_API_CONFORMANCE_"+key, values[i])
				}
				if mode == "flags" {
					t.Setenv("AERO_API_CONFORMANCE_"+key, "overridden")
					args = append(args, "--"+flags[i], values[i])
				}
			}
			var got *config.Config
			cmd := newCommandWithRunner(func(_ context.Context, cfg *config.Config) error { got = cfg; return nil })
			err := cmd.Run(context.Background(), args)
			if mode == "partial" {
				if err == nil || got != nil {
					t.Fatalf("partial configuration started: cfg=%v err=%v", got, err)
				}
				return
			}
			if err != nil || got == nil {
				t.Fatalf("start: cfg=%v err=%v", got, err)
			}
			actual := []string{got.ConformanceAddress, got.ConformanceCAFile, got.ConformanceCertFile, got.ConformanceKeyFile, got.ConformanceServerName}
			for i, value := range actual {
				want := values[i]
				if mode == "disabled" {
					want = ""
				}
				if value != want {
					t.Errorf("%s = %q, want %q", keys[i], value, want)
				}
			}
		})
	}
}

func TestMissionDeploymentRequestTimeoutBudgetsAllControlPhases(t *testing.T) {
	if got := missionDeploymentRequestTimeout(35 * time.Second); got != 110*time.Second {
		t.Fatalf("mission deployment request timeout = %s, want 1m50s", got)
	}
}

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
