package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	interussprovider "github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider/interuss"
	localprovider "github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider/local"
	"github.com/Aero-Arc/aero-arc-api/internal/config"
	"github.com/Aero-Arc/aero-arc-api/internal/httpapi"
	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	"github.com/Aero-Arc/aero-arc-api/internal/relaycontrol"
	"github.com/Aero-Arc/aero-arc-api/internal/seed"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	durablepostgres "github.com/Aero-Arc/aero-arc-api/internal/store/durable/postgres"
	"github.com/Aero-Arc/aero-arc-api/internal/store/replay"
	replaymemory "github.com/Aero-Arc/aero-arc-api/internal/store/replay/memory"
	"github.com/Aero-Arc/aero-arc-api/internal/store/telemetry"
	telemetryinfluxdb "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/influxdb"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
	"github.com/urfave/cli/v3"
	"google.golang.org/grpc/credentials"
)

func main() {
	cmd := newCommand()
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		slog.Error("aero-arc-api failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func newCommand() *cli.Command {
	defaults := config.Defaults()

	return &cli.Command{
		Name:  "aero-arc-api",
		Usage: "run the Aero Arc API service",
		Commands: []*cli.Command{
			{
				Name:  "start",
				Usage: "start the HTTP API server",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "addr",
						Value:   defaults.Addr,
						Usage:   "HTTP listen address",
						Sources: cli.EnvVars("AERO_API_ADDR"),
					},
					&cli.StringSliceFlag{
						Name:    "airspace-provider",
						Value:   append([]string(nil), defaults.AirspaceProviders...),
						Usage:   "airspace source; repeat for local and interuss",
						Sources: cli.EnvVars("AERO_API_AIRSPACE_PROVIDERS"),
					},
					&cli.StringFlag{
						Name:    "durable-store",
						Value:   defaults.DurableStore,
						Usage:   "durable store mode: memory or postgres",
						Sources: cli.EnvVars("AERO_API_DURABLE_STORE"),
					},
					&cli.StringFlag{Name: "database-url", Usage: "PostgreSQL/PostGIS URL", Sources: cli.EnvVars("AERO_API_DATABASE_URL")},
					&cli.StringFlag{
						Name:    "telemetry-store",
						Value:   defaults.TelemetryStore,
						Usage:   "telemetry store mode",
						Sources: cli.EnvVars("AERO_API_TELEMETRY_STORE"),
					},
					&cli.StringFlag{Name: "influxdb-host", Usage: "InfluxDB 3 host URL", Sources: cli.EnvVars("AERO_API_INFLUXDB_HOST")},
					&cli.StringFlag{Name: "influxdb-token", Usage: "InfluxDB 3 access token", Sources: cli.EnvVars("AERO_API_INFLUXDB_TOKEN")},
					&cli.StringFlag{Name: "influxdb-database", Usage: "InfluxDB 3 database", Sources: cli.EnvVars("AERO_API_INFLUXDB_DATABASE")},
					&cli.StringFlag{Name: "dss-base-url", Usage: "InterUSS DSS base URL", Sources: cli.EnvVars("AERO_API_DSS_BASE_URL")},
					&cli.StringFlag{Name: "dss-static-token", Usage: "static DSS bearer token", Sources: cli.EnvVars("AERO_API_DSS_STATIC_TOKEN")},
					&cli.StringFlag{Name: "dss-oauth-token-url", Usage: "local dummy OAuth token URL", Sources: cli.EnvVars("AERO_API_DSS_OAUTH_TOKEN_URL")},
					&cli.StringFlag{Name: "dss-oauth-audience", Value: defaults.DSSOAuthAudience, Usage: "DSS OAuth audience", Sources: cli.EnvVars("AERO_API_DSS_OAUTH_AUDIENCE")},
					&cli.StringFlag{Name: "dss-oauth-issuer", Value: defaults.DSSOAuthIssuer, Usage: "DSS OAuth issuer", Sources: cli.EnvVars("AERO_API_DSS_OAUTH_ISSUER")},
					&cli.StringFlag{Name: "dss-oauth-subject", Value: defaults.DSSOAuthSubject, Usage: "stable Aero Arc USS identity", Sources: cli.EnvVars("AERO_API_DSS_OAUTH_SUBJECT")},
					&cli.BoolFlag{Name: "dss-allow-insecure-peer-urls", Usage: "allow HTTP and private peer USS URLs for local development", Sources: cli.EnvVars("AERO_API_DSS_ALLOW_INSECURE_PEER_URLS")},
					&cli.StringFlag{Name: "uss-base-url", Usage: "public USS base URL advertised through the DSS", Sources: cli.EnvVars("AERO_API_USS_BASE_URL")},
					&cli.StringFlag{Name: "uss-jwt-public-key-file", Usage: "PEM RSA public key used to verify peer USS tokens", Sources: cli.EnvVars("AERO_API_USS_JWT_PUBLIC_KEY_FILE")},
					&cli.StringFlag{Name: "uss-jwt-issuer", Usage: "required issuer for peer USS JWTs", Sources: cli.EnvVars("AERO_API_USS_JWT_ISSUER")},
					&cli.StringFlag{Name: "uss-jwt-audience", Usage: "required audience for peer USS JWTs", Sources: cli.EnvVars("AERO_API_USS_JWT_AUDIENCE")},
					&cli.StringFlag{
						Name:    "replay-store",
						Value:   defaults.ReplayStore,
						Usage:   "replay store mode",
						Sources: cli.EnvVars("AERO_API_REPLAY_STORE"),
					},
					&cli.StringFlag{
						Name:    "registry-mode",
						Value:   defaults.RegistryMode,
						Usage:   "registry client mode",
						Sources: cli.EnvVars("AERO_API_REGISTRY_MODE"),
					},
					&cli.StringFlag{
						Name:    "registry-addr",
						Value:   defaults.RegistryAddress,
						Usage:   "registry gRPC address",
						Sources: cli.EnvVars("AERO_API_REGISTRY_ADDR"),
					},
					&cli.DurationFlag{
						Name:    "registry-dial-timeout",
						Value:   defaults.RegistryDialTimeout,
						Usage:   "registry gRPC dial timeout",
						Sources: cli.EnvVars("AERO_API_REGISTRY_DIAL_TIMEOUT"),
					},
					&cli.DurationFlag{Name: "registry-freshness", Value: defaults.RegistryFreshness, Usage: "maximum registry heartbeat age considered connected", Sources: cli.EnvVars("AERO_API_REGISTRY_FRESHNESS")},
					&cli.DurationFlag{Name: "telemetry-freshness", Value: defaults.TelemetryFreshness, Usage: "maximum telemetry observation age considered fresh", Sources: cli.EnvVars("AERO_API_TELEMETRY_FRESHNESS")},
					&cli.DurationFlag{Name: "telemetry-latest-lookback", Value: defaults.TelemetryLatestLookback, Usage: "maximum telemetry history scanned for live state", Sources: cli.EnvVars("AERO_API_TELEMETRY_LATEST_LOOKBACK")},
					&cli.DurationFlag{
						Name:    "request-timeout",
						Value:   defaults.RequestTimeout,
						Usage:   "per-request timeout",
						Sources: cli.EnvVars("AERO_API_REQUEST_TIMEOUT"),
					},
					&cli.DurationFlag{Name: "command-timeout", Value: defaults.CommandTimeout, Usage: "end-to-end aircraft command timeout", Sources: cli.EnvVars("AERO_API_COMMAND_TIMEOUT")},
					&cli.StringFlag{Name: "relay-ca-file", Usage: "CA certificate used to verify Relay control endpoints", Sources: cli.EnvVars("AERO_API_RELAY_CA_FILE")},
					&cli.StringFlag{Name: "relay-server-name", Usage: "TLS server name expected from Relay control endpoints", Sources: cli.EnvVars("AERO_API_RELAY_SERVER_NAME")},
					&cli.BoolFlag{Name: "relay-insecure-skip-verify", Usage: "disable Relay TLS verification for local SITL only", Sources: cli.EnvVars("AERO_API_RELAY_INSECURE_SKIP_VERIFY")},
					&cli.StringFlag{
						Name:    "seed",
						Value:   defaults.Seed,
						Usage:   "optional startup fixture seed mode: demo",
						Sources: cli.EnvVars("AERO_API_SEED"),
					},
					&cli.BoolFlag{
						Name:    "debug",
						Value:   defaults.Debug,
						Usage:   "enable debug operation logging",
						Sources: cli.EnvVars("AERO_API_DEBUG"),
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg := &config.Config{
						Addr:                     cmd.String("addr"),
						DurableStore:             cmd.String("durable-store"),
						DatabaseURL:              cmd.String("database-url"),
						AirspaceProviders:        cmd.StringSlice("airspace-provider"),
						TelemetryStore:           cmd.String("telemetry-store"),
						InfluxDBHost:             cmd.String("influxdb-host"),
						InfluxDBToken:            cmd.String("influxdb-token"),
						InfluxDBDatabase:         cmd.String("influxdb-database"),
						DSSBaseURL:               cmd.String("dss-base-url"),
						DSSStaticToken:           cmd.String("dss-static-token"),
						DSSOAuthTokenURL:         cmd.String("dss-oauth-token-url"),
						DSSOAuthAudience:         cmd.String("dss-oauth-audience"),
						DSSOAuthIssuer:           cmd.String("dss-oauth-issuer"),
						DSSOAuthSubject:          cmd.String("dss-oauth-subject"),
						DSSAllowInsecurePeerURLs: cmd.Bool("dss-allow-insecure-peer-urls"),
						USSBaseURL:               cmd.String("uss-base-url"),
						USSJWTPublicKeyFile:      cmd.String("uss-jwt-public-key-file"),
						USSJWTIssuer:             cmd.String("uss-jwt-issuer"),
						USSJWTAudience:           cmd.String("uss-jwt-audience"),
						ReplayStore:              cmd.String("replay-store"),
						RegistryMode:             cmd.String("registry-mode"),
						RegistryAddress:          cmd.String("registry-addr"),
						RegistryDialTimeout:      cmd.Duration("registry-dial-timeout"),
						RegistryFreshness:        cmd.Duration("registry-freshness"),
						TelemetryFreshness:       cmd.Duration("telemetry-freshness"),
						TelemetryLatestLookback:  cmd.Duration("telemetry-latest-lookback"),
						RequestTimeout:           cmd.Duration("request-timeout"),
						CommandTimeout:           cmd.Duration("command-timeout"),
						RelayCAFile:              cmd.String("relay-ca-file"),
						RelayServerName:          cmd.String("relay-server-name"),
						RelayInsecureSkipVerify:  cmd.Bool("relay-insecure-skip-verify"),
						Seed:                     cmd.String("seed"),
						Debug:                    cmd.Bool("debug"),
					}
					if err := cfg.Validate(); err != nil {
						return err
					}

					return run(ctx, cfg)
				},
			},
		},
	}
}

func run(ctx context.Context, cfg *config.Config) error {
	if cfg.Debug {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	registryClient, closeRegistry, err := registry.New(ctx, cfg.RegistryMode, cfg.RegistryAddress, cfg.RegistryDialTimeout)
	if err != nil {
		return err
	}
	defer func() {
		if err := closeRegistry(); err != nil {
			slog.Warn("failed to close registry connection", slog.String("error", err.Error()))
		}
	}()

	durableStore, err := newDurableStore(ctx, cfg)
	if err != nil {
		return err
	}
	if closer, ok := durableStore.(interface{ Close() }); ok {
		defer closer.Close()
	}
	providers, err := newAirspaceProviders(cfg, durableStore)
	if err != nil {
		return err
	}
	telemetryStore, err := newTelemetryStore(cfg)
	if err != nil {
		return err
	}
	if closer, ok := telemetryStore.(interface{ Close() error }); ok {
		defer func() {
			if err := closer.Close(); err != nil {
				slog.Warn("failed to close telemetry store", slog.String("error", err.Error()))
			}
		}()
	}
	replayStore, err := newReplayStore(cfg.ReplayStore)
	if err != nil {
		return err
	}
	if cfg.Seed == "demo" {
		if err := seed.Demo(ctx, durableStore, telemetryStore, replayStore, registryClient); err != nil {
			return err
		}
		slog.Info("seeded demo data")
	}
	fleetService := service.NewFleetService(durableStore, telemetryStore, replayStore, registryClient).
		WithLiveStatePolicy(cfg.RegistryFreshness, cfg.TelemetryFreshness, nil)
	deconflictionService, err := deconfliction.NewDeconflictionServiceWithPublicationLease(
		durableStore,
		cfg.RequestTimeout+30*time.Second,
		providers...,
	)
	if err != nil {
		return err
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	intentService := service.NewIntentService(durableStore, deconflictionService)
	preflightService := service.NewPreflightService(durableStore)
	conformanceService := service.NewConformanceService(durableStore, telemetryStore)
	relayCredentials, err := relayTransportCredentials(cfg)
	if err != nil {
		return err
	}
	relayControl, err := relaycontrol.New(registryClient, relayCredentials, cfg.CommandTimeout, cfg.RegistryFreshness)
	if err != nil {
		return err
	}
	defer func() {
		if err := relayControl.Close(); err != nil {
			slog.Warn("failed to close Relay control connections", slog.String("error", err.Error()))
		}
	}()
	commandService := service.NewAircraftCommandService(durableStore, relayControl)

	apiServer := httpapi.NewWithWorkflows(fleetService, intentService, preflightService, conformanceService, cfg.RequestTimeout, deconflictionService).
		WithAircraftCommands(commandService, cfg.CommandTimeout).
		WithDebug(cfg.Debug)
	if deconflictionService.PublishingEnabled() {
		authorizer, err := httpapi.NewUSSJWTAuthorizer(cfg.USSJWTPublicKeyFile, cfg.USSJWTIssuer, cfg.USSJWTAudience)
		if err != nil {
			return err
		}
		apiServer.WithUSSAuthorizer(authorizer)
	}
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}
	defer func() { _ = listener.Close() }()

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("starting aero-arc-api",
			slog.String("http_addr", cfg.Addr),
			slog.String("durable_store", cfg.DurableStore),
			slog.Any("airspace_providers", cfg.AirspaceProviders),
			slog.String("telemetry_store", cfg.TelemetryStore),
			slog.String("replay_store", cfg.ReplayStore),
			slog.String("registry_mode", cfg.RegistryMode),
			slog.String("registry_addr", cfg.RegistryAddress),
			slog.Duration("registry_freshness", cfg.RegistryFreshness),
			slog.Duration("telemetry_freshness", cfg.TelemetryFreshness),
			slog.Duration("telemetry_latest_lookback", cfg.TelemetryLatestLookback),
			slog.String("seed", cfg.Seed),
			slog.Bool("debug", cfg.Debug),
		)

		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()
	if deconflictionService.PublishingEnabled() {
		go deconflictionService.RunPublicationWorker(workerCtx)
	}

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Warn("failed to shut down http server gracefully", slog.String("error", err.Error()))
	}

	slog.Info("aero-arc-api shutdown complete")
	return nil
}

func relayTransportCredentials(cfg *config.Config) (credentials.TransportCredentials, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         cfg.RelayServerName,
		InsecureSkipVerify: cfg.RelayInsecureSkipVerify, // #nosec G402 -- explicit local-SITL escape hatch.
	}
	if cfg.RelayCAFile != "" {
		pem, err := os.ReadFile(cfg.RelayCAFile)
		if err != nil {
			return nil, fmt.Errorf("read Relay CA file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load system certificate pool: %w", err)
		}
		if roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("relay CA file contains no certificates")
		}
		tlsConfig.RootCAs = roots
	}
	return credentials.NewTLS(tlsConfig), nil
}

func newDurableStore(ctx context.Context, cfg *config.Config) (durable.Store, error) {
	switch cfg.DurableStore {
	case config.DurableStoreMemory:
		return durablememory.NewStore(), nil
	case config.DurableStorePostgres:
		return durablepostgres.Open(ctx, cfg.DatabaseURL)
	default:
		return nil, fmt.Errorf("unsupported durable store %q", cfg.DurableStore)
	}
}

func newAirspaceProviders(
	cfg *config.Config,
	durableStore durable.OperationalStore,
) ([]airspaceprovider.Provider, error) {
	providers := make([]airspaceprovider.Provider, 0, len(cfg.AirspaceProviders))
	for _, name := range cfg.AirspaceProviders {
		switch name {
		case airspaceprovider.ProviderLocal:
			providers = append(providers, localprovider.New(durableStore))
		case airspaceprovider.ProviderInterUSS:
			provider, err := newInterUSSProvider(cfg)
			if err != nil {
				return nil, err
			}
			providers = append(providers, provider)
		default:
			return nil, fmt.Errorf("unsupported airspace provider %q", name)
		}
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf("at least one airspace provider is required")
	}
	return providers, nil
}

func newInterUSSProvider(cfg *config.Config) (airspaceprovider.Provider, error) {
	return interussprovider.New(interussprovider.Config{
		BaseURL:               cfg.DSSBaseURL,
		StaticToken:           cfg.DSSStaticToken,
		OAuthTokenURL:         cfg.DSSOAuthTokenURL,
		OAuthAudience:         cfg.DSSOAuthAudience,
		OAuthIssuer:           cfg.DSSOAuthIssuer,
		OAuthSubject:          cfg.DSSOAuthSubject,
		AllowInsecurePeerURLs: cfg.DSSAllowInsecurePeerURLs,
		RequestTimeout:        cfg.RequestTimeout,
		USSBaseURL:            cfg.USSBaseURL,
	})
}

func newTelemetryStore(cfg *config.Config) (telemetry.Store, error) {
	switch cfg.TelemetryStore {
	case "memory":
		return telemetrymemory.NewStore(), nil
	case "influxdb":
		return telemetryinfluxdb.New(cfg.InfluxDBHost, cfg.InfluxDBToken, cfg.InfluxDBDatabase, cfg.TelemetryLatestLookback)
	default:
		return nil, fmt.Errorf("unsupported telemetry store %q", cfg.TelemetryStore)
	}
}

func newReplayStore(mode string) (replay.Store, error) {
	switch mode {
	case "memory":
		return replaymemory.NewStore(), nil
	default:
		return nil, fmt.Errorf("unsupported replay store %q", mode)
	}
}
