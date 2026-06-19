package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultAddr                = ":8080"
	defaultDurableStore        = "memory"
	defaultTelemetryStore      = "memory"
	defaultReplayStore         = "memory"
	defaultRegistryMode        = "memory"
	defaultRegistryAddress     = "localhost:50051"
	defaultRegistryDialTimeout = 5 * time.Second
	defaultRequestTimeout      = 3 * time.Second
	defaultSeed                = ""
	defaultDebug               = false
)

type Config struct {
	Addr                string
	DurableStore        string
	TelemetryStore      string
	ReplayStore         string
	RegistryMode        string
	RegistryAddress     string
	RegistryDialTimeout time.Duration
	RequestTimeout      time.Duration
	Seed                string
	Debug               bool
}

func Defaults() *Config {
	return &Config{
		Addr:                defaultAddr,
		DurableStore:        defaultDurableStore,
		TelemetryStore:      defaultTelemetryStore,
		ReplayStore:         defaultReplayStore,
		RegistryMode:        defaultRegistryMode,
		RegistryAddress:     defaultRegistryAddress,
		RegistryDialTimeout: defaultRegistryDialTimeout,
		RequestTimeout:      defaultRequestTimeout,
		Seed:                defaultSeed,
		Debug:               defaultDebug,
	}
}

func Load() (*Config, error) {
	cfg := Defaults()

	applyStringEnv("AERO_API_ADDR", &cfg.Addr)
	applyStringEnv("AERO_API_DURABLE_STORE", &cfg.DurableStore)
	applyStringEnv("AERO_API_TELEMETRY_STORE", &cfg.TelemetryStore)
	applyStringEnv("AERO_API_REPLAY_STORE", &cfg.ReplayStore)
	applyStringEnv("AERO_API_REGISTRY_MODE", &cfg.RegistryMode)
	applyStringEnv("AERO_API_REGISTRY_ADDR", &cfg.RegistryAddress)
	applyStringEnv("AERO_API_SEED", &cfg.Seed)
	if err := applyBoolEnv("AERO_API_DEBUG", &cfg.Debug); err != nil {
		return nil, err
	}
	if err := applyDurationEnv("AERO_API_REGISTRY_DIAL_TIMEOUT", &cfg.RegistryDialTimeout); err != nil {
		return nil, err
	}
	if err := applyDurationEnv("AERO_API_REQUEST_TIMEOUT", &cfg.RequestTimeout); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (cfg *Config) Validate() error {
	if cfg.Addr == "" {
		return fmt.Errorf("AERO_API_ADDR cannot be empty")
	}
	if cfg.DurableStore != "memory" {
		// TODO: support tidb and postgres durable stores.
		return fmt.Errorf("unsupported durable store %q", cfg.DurableStore)
	}
	if cfg.TelemetryStore != "memory" {
		// TODO: support influxdb telemetry stores.
		return fmt.Errorf("unsupported telemetry store %q", cfg.TelemetryStore)
	}
	if cfg.ReplayStore != "memory" {
		// TODO: support s3 replay stores.
		return fmt.Errorf("unsupported replay store %q", cfg.ReplayStore)
	}
	switch cfg.RegistryMode {
	case "memory", "grpc":
	default:
		return fmt.Errorf("unsupported registry mode %q", cfg.RegistryMode)
	}
	if cfg.RegistryAddress == "" {
		return fmt.Errorf("AERO_API_REGISTRY_ADDR cannot be empty")
	}
	if cfg.RegistryDialTimeout <= 0 {
		return fmt.Errorf("AERO_API_REGISTRY_DIAL_TIMEOUT must be > 0")
	}
	if cfg.RequestTimeout <= 0 {
		return fmt.Errorf("AERO_API_REQUEST_TIMEOUT must be > 0")
	}
	switch cfg.Seed {
	case "", "none", "demo":
	default:
		return fmt.Errorf("unsupported seed mode %q", cfg.Seed)
	}

	return nil
}

func applyStringEnv(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func applyDurationEnv(key string, dst *time.Duration) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}

	d, err := parseDuration(v)
	if err != nil {
		return fmt.Errorf("invalid duration for %s: %w", key, err)
	}

	*dst = d
	return nil
}

func applyBoolEnv(key string, dst *bool) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}

	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fmt.Errorf("invalid boolean for %s: %w", key, err)
	}

	*dst = parsed
	return nil
}

func parseDuration(v string) (time.Duration, error) {
	if ms, err := strconv.Atoi(v); err == nil {
		return time.Duration(ms) * time.Millisecond, nil
	}

	return time.ParseDuration(v)
}
