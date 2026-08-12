package config

import (
	"strings"
	"testing"
	"time"
)

func TestLiveStateFreshnessMustBePositive(t *testing.T) {
	cfg := Defaults()
	cfg.RegistryFreshness = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "REGISTRY_FRESHNESS") {
		t.Fatalf("unexpected registry freshness error: %v", err)
	}
	cfg = Defaults()
	cfg.TelemetryFreshness = -time.Second
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "TELEMETRY_FRESHNESS") {
		t.Fatalf("unexpected telemetry freshness error: %v", err)
	}
}

func TestTelemetryLatestLookbackCoversFreshnessWindow(t *testing.T) {
	cfg := Defaults()
	if cfg.TelemetryLatestLookback != 5*time.Minute {
		t.Fatalf("default telemetry latest lookback = %v, want 5m", cfg.TelemetryLatestLookback)
	}
	cfg.TelemetryLatestLookback = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "TELEMETRY_LATEST_LOOKBACK must be > 0") {
		t.Fatalf("unexpected zero telemetry lookback error: %v", err)
	}

	cfg = Defaults()
	cfg.TelemetryLatestLookback = cfg.TelemetryFreshness - time.Second
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "TELEMETRY_LATEST_LOOKBACK") {
		t.Fatalf("unexpected telemetry lookback error: %v", err)
	}

	t.Setenv("AERO_API_TELEMETRY_LATEST_LOOKBACK", "7m")
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TelemetryLatestLookback != 7*time.Minute {
		t.Fatalf("telemetry latest lookback = %v, want 7m", loaded.TelemetryLatestLookback)
	}
}

func TestValidateInfluxDBTelemetry(t *testing.T) {
	cfg := Defaults()
	cfg.TelemetryStore = "influxdb"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "INFLUXDB_HOST") {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg.InfluxDBHost = "http://localhost:8181"
	cfg.InfluxDBToken = "token"
	cfg.InfluxDBDatabase = "aero_arc"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
