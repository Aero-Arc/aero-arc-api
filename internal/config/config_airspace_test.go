package config

import (
	"strings"
	"testing"
)

func TestValidateAirspaceConfiguration(t *testing.T) {
	t.Run("valid URLs", func(t *testing.T) {
		cfg := Defaults()
		cfg.SpatialIndex = SpatialIndexPostGIS
		cfg.AirspaceProviders = []string{AirspaceProviderLocal, AirspaceProviderInterUSS}
		cfg.PostGISDatabaseURL = "postgres://aero_arc:secret@localhost:5432/aero_arc"
		cfg.DSSBaseURL = "http://localhost:8082"
		cfg.DSSOAuthTokenURL = "http://localhost:8085/token"
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("invalid DSS URL", func(t *testing.T) {
		cfg := Defaults()
		cfg.AirspaceProviders = []string{AirspaceProviderInterUSS}
		cfg.SpatialIndex = SpatialIndexNone
		cfg.DSSBaseURL = "localhost:8082"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "DSS_BASE_URL") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("conflicting credentials", func(t *testing.T) {
		cfg := Defaults()
		cfg.AirspaceProviders = []string{AirspaceProviderInterUSS}
		cfg.SpatialIndex = SpatialIndexNone
		cfg.DSSBaseURL = "http://localhost:8082"
		cfg.DSSStaticToken = "token"
		cfg.DSSOAuthTokenURL = "http://localhost:8085/token"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("postgis requires URL", func(t *testing.T) {
		cfg := Defaults()
		cfg.SpatialIndex = SpatialIndexPostGIS
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "POSTGIS_DATABASE_URL") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("local requires spatial index", func(t *testing.T) {
		cfg := Defaults()
		cfg.SpatialIndex = SpatialIndexNone
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires a spatial index") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("InterUSS must be explicit", func(t *testing.T) {
		cfg := Defaults()
		cfg.DSSBaseURL = "http://localhost:8082"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "requires airspace provider interuss") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("providers are unique", func(t *testing.T) {
		cfg := Defaults()
		cfg.AirspaceProviders = []string{AirspaceProviderLocal, AirspaceProviderLocal}
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("providers load from environment", func(t *testing.T) {
		t.Setenv("AERO_API_AIRSPACE_PROVIDERS", "local,interuss")
		t.Setenv("AERO_API_DSS_BASE_URL", "http://localhost:8082")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.AirspaceProviders) != 2 || cfg.AirspaceProviders[1] != AirspaceProviderInterUSS {
			t.Fatalf("providers = %#v", cfg.AirspaceProviders)
		}
	})

	t.Run("invalid insecure peer flag", func(t *testing.T) {
		t.Setenv("AERO_API_DSS_ALLOW_INSECURE_PEER_URLS", "sometimes")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ALLOW_INSECURE") {
			t.Fatalf("error = %v", err)
		}
	})
}
