package config

import (
	"strings"
	"testing"
	"time"
)

func TestRelayMissionControlConfigurationIsAllOrNone(t *testing.T) {
	cfg := Defaults()
	cfg.RegistryMode = "grpc"
	cfg.RelayControlCAFile = "ca.pem"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "must be configured together") {
		t.Fatalf("partial TLS error = %v", err)
	}
	cfg.RelayControlCertFile = "client.pem"
	cfg.RelayControlKeyFile = "client-key.pem"
	cfg.RelayControlServerName = "relay.internal"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "MISSION_DEPLOY_TOKEN") {
		t.Fatalf("missing token error = %v", err)
	}
	cfg.MissionDeploymentToken = "short"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "at least 24 bytes") {
		t.Fatalf("short token error = %v", err)
	}
	cfg.MissionDeploymentToken = "0123456789abcdefghijklmn"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("complete Relay mission config error = %v", err)
	}
	if !cfg.RelayControlEnabled() {
		t.Fatal("RelayControlEnabled = false")
	}
}

func TestRelayMissionControlTimeoutFitsThreePhaseDeployment(t *testing.T) {
	cfg := Defaults()
	if cfg.RelayControlTimeout != 35*time.Second {
		t.Fatalf("default Relay control timeout = %s", cfg.RelayControlTimeout)
	}
	cfg.RelayControlTimeout += time.Nanosecond
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "clear-context-and-deploy") {
		t.Fatalf("oversized Relay control timeout error = %v", err)
	}
}
