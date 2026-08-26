package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
)

const (
	DurableStoreMemory   = "memory"
	DurableStorePostgres = "postgres"

	AirspaceProviderLocal    = airspaceprovider.ProviderLocal
	AirspaceProviderInterUSS = airspaceprovider.ProviderInterUSS
)

const (
	defaultAddr                = ":8080"
	defaultDurableStore        = DurableStoreMemory
	defaultTelemetryStore      = "memory"
	defaultReplayStore         = "memory"
	defaultRegistryMode        = "memory"
	defaultRegistryAddress     = "localhost:50051"
	defaultRegistryDialTimeout = 5 * time.Second
	defaultRegistryFreshness   = 30 * time.Second
	defaultRelayControlTimeout = 45 * time.Second
	defaultRelayPlacementTTL   = 10 * time.Second
	defaultTelemetryFreshness  = 15 * time.Second
	defaultTelemetryLookback   = 5 * time.Minute
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
	DatabaseURL              string
	AirspaceProviders        []string
	TelemetryStore           string
	InfluxDBHost             string
	InfluxDBToken            string
	InfluxDBDatabase         string
	DSSBaseURL               string
	DSSStaticToken           string
	DSSOAuthTokenURL         string
	DSSOAuthAudience         string
	DSSOAuthIssuer           string
	DSSOAuthSubject          string
	DSSAllowInsecurePeerURLs bool
	USSBaseURL               string
	USSJWTPublicKeyFile      string
	USSJWTIssuer             string
	USSJWTAudience           string
	ReplayStore              string
	RegistryMode             string
	RegistryAddress          string
	RegistryDialTimeout      time.Duration
	RegistryFreshness        time.Duration
	RelayControlCAFile       string
	RelayControlCertFile     string
	RelayControlKeyFile      string
	RelayControlServerName   string
	MissionDeploymentToken   string
	RelayControlTimeout      time.Duration
	RelayPlacementTTL        time.Duration
	TelemetryFreshness       time.Duration
	TelemetryLatestLookback  time.Duration
	RequestTimeout           time.Duration
	Seed                     string
	Debug                    bool
}

// Defaults returns the API's baseline development configuration.
//
// Returns:
//   - result: is the *Config value produced by Defaults.
func Defaults() *Config {
	return &Config{
		Addr:                    defaultAddr,
		DurableStore:            defaultDurableStore,
		AirspaceProviders:       []string{AirspaceProviderLocal},
		TelemetryStore:          defaultTelemetryStore,
		ReplayStore:             defaultReplayStore,
		RegistryMode:            defaultRegistryMode,
		RegistryAddress:         defaultRegistryAddress,
		RegistryDialTimeout:     defaultRegistryDialTimeout,
		RegistryFreshness:       defaultRegistryFreshness,
		RelayControlTimeout:     defaultRelayControlTimeout,
		RelayPlacementTTL:       defaultRelayPlacementTTL,
		TelemetryFreshness:      defaultTelemetryFreshness,
		TelemetryLatestLookback: defaultTelemetryLookback,
		RequestTimeout:          defaultRequestTimeout,
		DSSOAuthAudience:        defaultDSSOAuthAudience,
		DSSOAuthIssuer:          defaultDSSOAuthIssuer,
		DSSOAuthSubject:         defaultDSSOAuthSubject,
		Seed:                    defaultSeed,
		Debug:                   defaultDebug,
	}
}

// Load resolves API configuration from environment variables and validates the result.
//
// Returns:
//   - result: is the *Config value produced by Load.
//   - error: reports validation, dependency, cancellation, or persistence failures.
func Load() (*Config, error) {
	cfg := Defaults()

	applyStringEnv("AERO_API_ADDR", &cfg.Addr)
	applyStringEnv("AERO_API_DURABLE_STORE", &cfg.DurableStore)
	applyStringEnv("AERO_API_DATABASE_URL", &cfg.DatabaseURL)
	applyStringSliceEnv("AERO_API_AIRSPACE_PROVIDERS", &cfg.AirspaceProviders)
	applyStringEnv("AERO_API_TELEMETRY_STORE", &cfg.TelemetryStore)
	applyStringEnv("AERO_API_INFLUXDB_HOST", &cfg.InfluxDBHost)
	applyStringEnv("AERO_API_INFLUXDB_TOKEN", &cfg.InfluxDBToken)
	applyStringEnv("AERO_API_INFLUXDB_DATABASE", &cfg.InfluxDBDatabase)
	applyStringEnv("AERO_API_DSS_BASE_URL", &cfg.DSSBaseURL)
	applyStringEnv("AERO_API_DSS_STATIC_TOKEN", &cfg.DSSStaticToken)
	applyStringEnv("AERO_API_DSS_OAUTH_TOKEN_URL", &cfg.DSSOAuthTokenURL)
	applyStringEnv("AERO_API_DSS_OAUTH_AUDIENCE", &cfg.DSSOAuthAudience)
	applyStringEnv("AERO_API_DSS_OAUTH_ISSUER", &cfg.DSSOAuthIssuer)
	applyStringEnv("AERO_API_DSS_OAUTH_SUBJECT", &cfg.DSSOAuthSubject)
	applyStringEnv("AERO_API_USS_BASE_URL", &cfg.USSBaseURL)
	applyStringEnv("AERO_API_USS_JWT_PUBLIC_KEY_FILE", &cfg.USSJWTPublicKeyFile)
	applyStringEnv("AERO_API_USS_JWT_ISSUER", &cfg.USSJWTIssuer)
	applyStringEnv("AERO_API_USS_JWT_AUDIENCE", &cfg.USSJWTAudience)
	applyStringEnv("AERO_API_REPLAY_STORE", &cfg.ReplayStore)
	applyStringEnv("AERO_API_REGISTRY_MODE", &cfg.RegistryMode)
	applyStringEnv("AERO_API_REGISTRY_ADDR", &cfg.RegistryAddress)
	applyStringEnv("AERO_API_RELAY_CONTROL_CA_FILE", &cfg.RelayControlCAFile)
	applyStringEnv("AERO_API_RELAY_CONTROL_CERT_FILE", &cfg.RelayControlCertFile)
	applyStringEnv("AERO_API_RELAY_CONTROL_KEY_FILE", &cfg.RelayControlKeyFile)
	applyStringEnv("AERO_API_RELAY_CONTROL_SERVER_NAME", &cfg.RelayControlServerName)
	applyStringEnv("AERO_API_MISSION_DEPLOY_TOKEN", &cfg.MissionDeploymentToken)
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
	if err := applyDurationEnv("AERO_API_REGISTRY_FRESHNESS", &cfg.RegistryFreshness); err != nil {
		return nil, err
	}
	if err := applyDurationEnv("AERO_API_RELAY_CONTROL_TIMEOUT", &cfg.RelayControlTimeout); err != nil {
		return nil, err
	}
	if err := applyDurationEnv("AERO_API_RELAY_PLACEMENT_TTL", &cfg.RelayPlacementTTL); err != nil {
		return nil, err
	}
	if err := applyDurationEnv("AERO_API_TELEMETRY_FRESHNESS", &cfg.TelemetryFreshness); err != nil {
		return nil, err
	}
	if err := applyDurationEnv("AERO_API_TELEMETRY_LATEST_LOOKBACK", &cfg.TelemetryLatestLookback); err != nil {
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

// Validate validates Config for required fields, supported values, and safety constraints.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (cfg *Config) Validate() error {
	if cfg.Addr == "" {
		return fmt.Errorf("AERO_API_ADDR cannot be empty")
	}
	if cfg.DurableStore != DurableStoreMemory && cfg.DurableStore != DurableStorePostgres {
		return fmt.Errorf("unsupported durable store %q", cfg.DurableStore)
	}
	providers := make(map[string]struct{}, len(cfg.AirspaceProviders))
	for index, provider := range cfg.AirspaceProviders {
		provider = strings.TrimSpace(provider)
		cfg.AirspaceProviders[index] = provider
		switch provider {
		case AirspaceProviderLocal, AirspaceProviderInterUSS:
		case "":
			return fmt.Errorf("AERO_API_AIRSPACE_PROVIDERS cannot contain an empty provider")
		default:
			return fmt.Errorf("unsupported airspace provider %q", provider)
		}
		if _, exists := providers[provider]; exists {
			return fmt.Errorf("duplicate airspace provider %q", provider)
		}
		providers[provider] = struct{}{}
	}
	if len(providers) == 0 {
		return fmt.Errorf("AERO_API_AIRSPACE_PROVIDERS must configure at least one provider")
	}
	if cfg.DurableStore == DurableStorePostgres && cfg.DatabaseURL == "" {
		return fmt.Errorf("AERO_API_DATABASE_URL is required when durable store is postgres")
	}
	if cfg.DurableStore != DurableStorePostgres && cfg.DatabaseURL != "" {
		return fmt.Errorf("AERO_API_DATABASE_URL requires durable store postgres")
	}
	_, interussEnabled := providers[AirspaceProviderInterUSS]
	if interussEnabled && cfg.DSSBaseURL == "" {
		return fmt.Errorf("AERO_API_DSS_BASE_URL is required when airspace provider interuss is enabled")
	}
	if !interussEnabled && (cfg.DSSBaseURL != "" || cfg.DSSStaticToken != "" || cfg.DSSOAuthTokenURL != "" || cfg.DSSAllowInsecurePeerURLs) {
		return fmt.Errorf("InterUSS DSS configuration requires airspace provider interuss")
	}
	if cfg.USSBaseURL != "" && !interussEnabled {
		return fmt.Errorf("AERO_API_USS_BASE_URL requires airspace provider interuss")
	}
	if cfg.USSBaseURL != "" && cfg.DurableStore != DurableStorePostgres {
		return fmt.Errorf("AERO_API_USS_BASE_URL requires durable store postgres")
	}
	if cfg.USSBaseURL != "" && (cfg.USSJWTPublicKeyFile == "" || cfg.USSJWTIssuer == "" || cfg.USSJWTAudience == "") {
		return fmt.Errorf("AERO_API_USS_JWT_PUBLIC_KEY_FILE, AERO_API_USS_JWT_ISSUER, and AERO_API_USS_JWT_AUDIENCE are required when USS publication is enabled")
	}
	if cfg.USSBaseURL == "" && (cfg.USSJWTPublicKeyFile != "" || cfg.USSJWTIssuer != "" || cfg.USSJWTAudience != "") {
		return fmt.Errorf("USS JWT configuration requires AERO_API_USS_BASE_URL")
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
	if cfg.RegistryFreshness <= 0 {
		return fmt.Errorf("AERO_API_REGISTRY_FRESHNESS must be > 0")
	}
	if cfg.RelayControlTimeout <= 0 {
		return fmt.Errorf("AERO_API_RELAY_CONTROL_TIMEOUT must be > 0")
	}
	if cfg.RelayControlTimeout > defaultRelayControlTimeout {
		return fmt.Errorf("AERO_API_RELAY_CONTROL_TIMEOUT must be <= %s so a context-and-deploy sequence cannot outlive the command expiry", defaultRelayControlTimeout)
	}
	if cfg.RelayPlacementTTL <= 0 {
		return fmt.Errorf("AERO_API_RELAY_PLACEMENT_TTL must be > 0")
	}
	relayTLSValues := []string{cfg.RelayControlCAFile, cfg.RelayControlCertFile, cfg.RelayControlKeyFile, cfg.RelayControlServerName}
	configuredRelayTLS := 0
	for _, value := range relayTLSValues {
		if strings.TrimSpace(value) != "" {
			configuredRelayTLS++
		}
	}
	if configuredRelayTLS != 0 && configuredRelayTLS != len(relayTLSValues) {
		return fmt.Errorf("AERO_API_RELAY_CONTROL_CA_FILE, AERO_API_RELAY_CONTROL_CERT_FILE, AERO_API_RELAY_CONTROL_KEY_FILE, and AERO_API_RELAY_CONTROL_SERVER_NAME must be configured together")
	}
	if configuredRelayTLS > 0 && len(cfg.MissionDeploymentToken) < 24 {
		return fmt.Errorf("AERO_API_MISSION_DEPLOY_TOKEN must be at least 24 bytes when Relay mission control is configured")
	}
	if cfg.MissionDeploymentToken != "" {
		for _, character := range cfg.MissionDeploymentToken {
			if character < 0x21 || character > 0x7e {
				return fmt.Errorf("AERO_API_MISSION_DEPLOY_TOKEN must contain visible ASCII characters without whitespace")
			}
		}
	}
	if configuredRelayTLS == 0 && cfg.MissionDeploymentToken != "" {
		return fmt.Errorf("AERO_API_MISSION_DEPLOY_TOKEN requires complete Relay control mTLS configuration")
	}
	if configuredRelayTLS > 0 && cfg.RegistryMode != "grpc" {
		return fmt.Errorf("Relay mission control requires registry mode grpc")
	}
	if cfg.TelemetryFreshness <= 0 {
		return fmt.Errorf("AERO_API_TELEMETRY_FRESHNESS must be > 0")
	}
	if cfg.TelemetryLatestLookback <= 0 {
		return fmt.Errorf("AERO_API_TELEMETRY_LATEST_LOOKBACK must be > 0")
	}
	if cfg.TelemetryLatestLookback < cfg.TelemetryFreshness {
		return fmt.Errorf("AERO_API_TELEMETRY_LATEST_LOOKBACK must be >= AERO_API_TELEMETRY_FRESHNESS")
	}
	if cfg.RequestTimeout <= 0 {
		return fmt.Errorf("AERO_API_REQUEST_TIMEOUT must be > 0")
	}
	for name, value := range map[string]string{
		"AERO_API_DATABASE_URL":        cfg.DatabaseURL,
		"AERO_API_DSS_BASE_URL":        cfg.DSSBaseURL,
		"AERO_API_DSS_OAUTH_TOKEN_URL": cfg.DSSOAuthTokenURL,
		"AERO_API_USS_BASE_URL":        cfg.USSBaseURL,
	} {
		if value == "" {
			continue
		}
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("%s must be an absolute URL", name)
		}
	}
	if cfg.USSBaseURL != "" {
		parsed, _ := url.Parse(cfg.USSBaseURL)
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("AERO_API_USS_BASE_URL must contain only a scheme, host, port, and optional path")
		}
		if parsed.Scheme != "https" && !cfg.DSSAllowInsecurePeerURLs {
			return fmt.Errorf("AERO_API_USS_BASE_URL must use HTTPS unless insecure peer URLs are enabled for local development")
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

// RelayControlEnabled reports whether the complete outbound mTLS identity is configured.
func (cfg *Config) RelayControlEnabled() bool {
	return strings.TrimSpace(cfg.RelayControlCAFile) != ""
}

func applyStringEnv(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func applyStringSliceEnv(key string, dst *[]string) {
	if value := os.Getenv(key); value != "" {
		*dst = strings.Split(value, ",")
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
