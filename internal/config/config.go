package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultAddr                = ":8080"
	defaultStoreMode           = "memory"
	defaultRegistryMode        = "memory"
	defaultRegistryAddress     = "localhost:50051"
	defaultRegistryDialTimeout = 5 * time.Second
	defaultRequestTimeout      = 3 * time.Second
)

type Config struct {
	Addr                string
	StoreMode           string
	RegistryMode        string
	RegistryAddress     string
	RegistryDialTimeout time.Duration
	RequestTimeout      time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		Addr:                getenvOr("AERO_API_ADDR", defaultAddr),
		StoreMode:           getenvOr("AERO_API_STORE_MODE", defaultStoreMode),
		RegistryMode:        getenvOr("AERO_API_REGISTRY_MODE", defaultRegistryMode),
		RegistryAddress:     getenvOr("AERO_API_REGISTRY_ADDR", defaultRegistryAddress),
		RegistryDialTimeout: defaultRegistryDialTimeout,
		RequestTimeout:      defaultRequestTimeout,
	}

	if err := applyDurationEnv("AERO_API_REGISTRY_DIAL_TIMEOUT", &cfg.RegistryDialTimeout); err != nil {
		return nil, err
	}
	if err := applyDurationEnv("AERO_API_REQUEST_TIMEOUT", &cfg.RequestTimeout); err != nil {
		return nil, err
	}

	if cfg.Addr == "" {
		return nil, fmt.Errorf("AERO_API_ADDR cannot be empty")
	}
	if cfg.StoreMode != "memory" {
		// TODO: support tidb and postgres durable store modes plus telemetry/replay backends.
		return nil, fmt.Errorf("unsupported store mode %q", cfg.StoreMode)
	}
	switch cfg.RegistryMode {
	case "memory", "grpc":
	default:
		return nil, fmt.Errorf("unsupported registry mode %q", cfg.RegistryMode)
	}
	if cfg.RegistryAddress == "" {
		return nil, fmt.Errorf("AERO_API_REGISTRY_ADDR cannot be empty")
	}
	if cfg.RegistryDialTimeout <= 0 {
		return nil, fmt.Errorf("AERO_API_REGISTRY_DIAL_TIMEOUT must be > 0")
	}
	if cfg.RequestTimeout <= 0 {
		return nil, fmt.Errorf("AERO_API_REQUEST_TIMEOUT must be > 0")
	}

	return cfg, nil
}

func getenvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func applyDurationEnv(key string, dst *time.Duration) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}

	if ms, err := strconv.Atoi(v); err == nil {
		*dst = time.Duration(ms) * time.Millisecond
		return nil
	}

	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("invalid duration for %s: %w", key, err)
	}

	*dst = d
	return nil
}
