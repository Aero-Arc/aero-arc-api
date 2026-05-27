package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	defaultHTTPListenAddr      = ":8081"
	defaultRegistryAddress     = "localhost:50052"
	defaultRegistryDialTimeout = 5 * time.Second
	defaultRequestTimeout      = 3 * time.Second
)

type Config struct {
	HTTPListenAddr      string
	RegistryAddress     string
	RegistryDialTimeout time.Duration
	RequestTimeout      time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		HTTPListenAddr:      getenvOr("AERO_API_HTTP_ADDR", defaultHTTPListenAddr),
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

	if cfg.HTTPListenAddr == "" {
		return nil, fmt.Errorf("AERO_API_HTTP_ADDR cannot be empty")
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
