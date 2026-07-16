package config

import (
	"strings"
	"testing"
)

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
