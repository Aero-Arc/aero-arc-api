package config

import (
	"fmt"
	"net/url"
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
	defaultDSSOAuthAudience    = "localhost"
	defaultDSSOAuthIssuer      = "localhost"
	defaultDSSOAuthSubject     = "aero-arc-api"
	defaultSeed                = ""
	defaultDebug               = false
)

type Config struct {
	Addr                     string
	DurableStore             string
	TelemetryStore           string
	InfluxDBHost             string
	InfluxDBToken            string
	InfluxDBDatabase         string
	PostGISDatabaseURL       string
	DSSBaseURL               string
	DSSStaticToken           string
	DSSOAuthTokenURL         string
	DSSOAuthAudience         string
	DSSOAuthIssuer           string
	DSSOAuthSubject          string
	DSSAllowInsecurePeerURLs bool
	ReplayStore              string
	RegistryMode             string
	RegistryAddress          string
	RegistryDialTimeout      time.Duration
	RequestTimeout           time.Duration
	Seed                     string
	Debug                    bool
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
		DSSOAuthAudience:    defaultDSSOAuthAudience,
		DSSOAuthIssuer:      defaultDSSOAuthIssuer,
		DSSOAuthSubject:     defaultDSSOAuthSubject,
		Seed:                defaultSeed,
		Debug:               defaultDebug,
	}
}

func Load() (*Config, error) {
	cfg := Defaults()

	applyStringEnv("AERO_API_ADDR", &cfg.Addr)
	applyStringEnv("AERO_API_DURABLE_STORE", &cfg.DurableStore)
	applyStringEnv("AERO_API_TELEMETRY_STORE", &cfg.TelemetryStore)
	applyStringEnv("AERO_API_INFLUXDB_HOST", &cfg.InfluxDBHost)
	applyStringEnv("AERO_API_INFLUXDB_TOKEN", &cfg.InfluxDBToken)
	applyStringEnv("AERO_API_INFLUXDB_DATABASE", &cfg.InfluxDBDatabase)
	applyStringEnv("AERO_API_POSTGIS_DATABASE_URL", &cfg.PostGISDatabaseURL)
	applyStringEnv("AERO_API_DSS_BASE_URL", &cfg.DSSBaseURL)
	applyStringEnv("AERO_API_DSS_STATIC_TOKEN", &cfg.DSSStaticToken)
	applyStringEnv("AERO_API_DSS_OAUTH_TOKEN_URL", &cfg.DSSOAuthTokenURL)
	applyStringEnv("AERO_API_DSS_OAUTH_AUDIENCE", &cfg.DSSOAuthAudience)
	applyStringEnv("AERO_API_DSS_OAUTH_ISSUER", &cfg.DSSOAuthIssuer)
	applyStringEnv("AERO_API_DSS_OAUTH_SUBJECT", &cfg.DSSOAuthSubject)
	applyStringEnv("AERO_API_REPLAY_STORE", &cfg.ReplayStore)
	applyStringEnv("AERO_API_REGISTRY_MODE", &cfg.RegistryMode)
	applyStringEnv("AERO_API_REGISTRY_ADDR", &cfg.RegistryAddress)
	applyStringEnv("AERO_API_SEED", &cfg.Seed)
	if err := applyBoolEnv("AERO_API_DEBUG", &cfg.Debug); err != nil {
		return nil, err
	}
	if err := applyBoolEnv("AERO_API_DSS_ALLOW_INSECURE_PEER_URLS", &cfg.DSSAllowInsecurePeerURLs); err != nil {
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
	if cfg.TelemetryStore != "memory" && cfg.TelemetryStore != "influxdb" {
		return fmt.Errorf("unsupported telemetry store %q", cfg.TelemetryStore)
	}
	if cfg.TelemetryStore == "influxdb" {
		if cfg.InfluxDBHost == "" {
			return fmt.Errorf("AERO_API_INFLUXDB_HOST cannot be empty when telemetry store is influxdb")
		}
		if cfg.InfluxDBToken == "" {
			return fmt.Errorf("AERO_API_INFLUXDB_TOKEN cannot be empty when telemetry store is influxdb")
		}
		if cfg.InfluxDBDatabase == "" {
			return fmt.Errorf("AERO_API_INFLUXDB_DATABASE cannot be empty when telemetry store is influxdb")
		}
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
	for name, value := range map[string]string{
		"AERO_API_POSTGIS_DATABASE_URL": cfg.PostGISDatabaseURL,
		"AERO_API_DSS_BASE_URL":         cfg.DSSBaseURL,
		"AERO_API_DSS_OAUTH_TOKEN_URL":  cfg.DSSOAuthTokenURL,
	} {
		if value == "" {
			continue
		}
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("%s must be an absolute URL", name)
		}
	}
	if cfg.DSSStaticToken != "" && cfg.DSSOAuthTokenURL != "" {
		return fmt.Errorf("AERO_API_DSS_STATIC_TOKEN and AERO_API_DSS_OAUTH_TOKEN_URL are mutually exclusive")
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
