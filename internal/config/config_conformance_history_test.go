package config

import "testing"

func TestConformanceHistoryConfiguration(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.ConformanceAddress = "localhost:50052"
	if err := cfg.Validate(); err == nil {
		t.Fatal("accepted incomplete history TLS")
	}
	cfg.ConformanceCAFile = "ca.pem"
	cfg.ConformanceCertFile = "client.pem"
	cfg.ConformanceKeyFile = "client-key.pem"
	cfg.ConformanceServerName = "conformance"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AERO_API_CONFORMANCE_ADDR", cfg.ConformanceAddress)
	t.Setenv("AERO_API_CONFORMANCE_CA_FILE", cfg.ConformanceCAFile)
	t.Setenv("AERO_API_CONFORMANCE_CERT_FILE", cfg.ConformanceCertFile)
	t.Setenv("AERO_API_CONFORMANCE_KEY_FILE", cfg.ConformanceKeyFile)
	t.Setenv("AERO_API_CONFORMANCE_SERVER_NAME", cfg.ConformanceServerName)
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ConformanceAddress != cfg.ConformanceAddress || loaded.ConformanceServerName != cfg.ConformanceServerName {
		t.Fatalf("env not loaded: %+v", loaded)
	}
}
