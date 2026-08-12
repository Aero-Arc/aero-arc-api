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
