package config

import (
	"strings"
	"testing"
)

func TestValidateAirspaceConfiguration(t *testing.T) {
	t.Run("valid URLs", func(t *testing.T) {
		cfg := Defaults()
		cfg.PostGISDatabaseURL = "postgres://aero_arc:secret@localhost:5432/aero_arc"
		cfg.DSSBaseURL = "http://localhost:8082"
		cfg.DSSOAuthTokenURL = "http://localhost:8085/token"
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("invalid DSS URL", func(t *testing.T) {
		cfg := Defaults()
		cfg.DSSBaseURL = "localhost:8082"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "DSS_BASE_URL") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("conflicting credentials", func(t *testing.T) {
		cfg := Defaults()
		cfg.DSSStaticToken = "token"
		cfg.DSSOAuthTokenURL = "http://localhost:8085/token"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("invalid insecure peer flag", func(t *testing.T) {
		t.Setenv("AERO_API_DSS_ALLOW_INSECURE_PEER_URLS", "sometimes")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ALLOW_INSECURE") {
			t.Fatalf("error = %v", err)
		}
	})
}
