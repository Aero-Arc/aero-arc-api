package main

import (
	"context"
	"fmt"
	"log/slog"
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
	"github.com/Aero-Arc/aero-arc-api/internal/seed"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
	spatialmemory "github.com/Aero-Arc/aero-arc-api/internal/spatialindex/memory"
	spatialpostgis "github.com/Aero-Arc/aero-arc-api/internal/spatialindex/postgis"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	"github.com/Aero-Arc/aero-arc-api/internal/store/replay"
	replaymemory "github.com/Aero-Arc/aero-arc-api/internal/store/replay/memory"
	"github.com/Aero-Arc/aero-arc-api/internal/store/telemetry"
	telemetryinfluxdb "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/influxdb"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
	"github.com/urfave/cli/v3"
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
					&cli.StringFlag{
						Name:    "spatial-index",
						Value:   defaults.SpatialIndex,
						Usage:   "spatial candidate index: none, memory, or postgis",
						Sources: cli.EnvVars("AERO_API_SPATIAL_INDEX"),
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
						Usage:   "durable store mode",
						Sources: cli.EnvVars("AERO_API_DURABLE_STORE"),
					},
					&cli.StringFlag{
						Name:    "telemetry-store",
						Value:   defaults.TelemetryStore,
						Usage:   "telemetry store mode",
						Sources: cli.EnvVars("AERO_API_TELEMETRY_STORE"),
					},
					&cli.StringFlag{Name: "influxdb-host", Usage: "InfluxDB 3 host URL", Sources: cli.EnvVars("AERO_API_INFLUXDB_HOST")},
					&cli.StringFlag{Name: "influxdb-token", Usage: "InfluxDB 3 access token", Sources: cli.EnvVars("AERO_API_INFLUXDB_TOKEN")},
					&cli.StringFlag{Name: "influxdb-database", Usage: "InfluxDB 3 database", Sources: cli.EnvVars("AERO_API_INFLUXDB_DATABASE")},
					&cli.StringFlag{Name: "postgis-database-url", Usage: "PostGIS URL for the spatial candidate index", Sources: cli.EnvVars("AERO_API_POSTGIS_DATABASE_URL")},
					&cli.StringFlag{Name: "dss-base-url", Usage: "InterUSS DSS base URL", Sources: cli.EnvVars("AERO_API_DSS_BASE_URL")},
					&cli.StringFlag{Name: "dss-static-token", Usage: "static DSS bearer token", Sources: cli.EnvVars("AERO_API_DSS_STATIC_TOKEN")},
					&cli.StringFlag{Name: "dss-oauth-token-url", Usage: "local dummy OAuth token URL", Sources: cli.EnvVars("AERO_API_DSS_OAUTH_TOKEN_URL")},
					&cli.StringFlag{Name: "dss-oauth-audience", Value: defaults.DSSOAuthAudience, Usage: "DSS OAuth audience", Sources: cli.EnvVars("AERO_API_DSS_OAUTH_AUDIENCE")},
					&cli.StringFlag{Name: "dss-oauth-issuer", Value: defaults.DSSOAuthIssuer, Usage: "DSS OAuth issuer", Sources: cli.EnvVars("AERO_API_DSS_OAUTH_ISSUER")},
					&cli.StringFlag{Name: "dss-oauth-subject", Value: defaults.DSSOAuthSubject, Usage: "stable Aero Arc USS identity", Sources: cli.EnvVars("AERO_API_DSS_OAUTH_SUBJECT")},
					&cli.BoolFlag{Name: "dss-allow-insecure-peer-urls", Usage: "allow HTTP and private peer USS URLs for local development", Sources: cli.EnvVars("AERO_API_DSS_ALLOW_INSECURE_PEER_URLS")},
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
					&cli.DurationFlag{
						Name:    "request-timeout",
						Value:   defaults.RequestTimeout,
						Usage:   "per-request timeout",
						Sources: cli.EnvVars("AERO_API_REQUEST_TIMEOUT"),
					},
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
						SpatialIndex:             cmd.String("spatial-index"),
						AirspaceProviders:        cmd.StringSlice("airspace-provider"),
						TelemetryStore:           cmd.String("telemetry-store"),
						InfluxDBHost:             cmd.String("influxdb-host"),
						InfluxDBToken:            cmd.String("influxdb-token"),
						InfluxDBDatabase:         cmd.String("influxdb-database"),
						PostGISDatabaseURL:       cmd.String("postgis-database-url"),
						DSSBaseURL:               cmd.String("dss-base-url"),
						DSSStaticToken:           cmd.String("dss-static-token"),
						DSSOAuthTokenURL:         cmd.String("dss-oauth-token-url"),
						DSSOAuthAudience:         cmd.String("dss-oauth-audience"),
						DSSOAuthIssuer:           cmd.String("dss-oauth-issuer"),
						DSSOAuthSubject:          cmd.String("dss-oauth-subject"),
						DSSAllowInsecurePeerURLs: cmd.Bool("dss-allow-insecure-peer-urls"),
						ReplayStore:              cmd.String("replay-store"),
						RegistryMode:             cmd.String("registry-mode"),
						RegistryAddress:          cmd.String("registry-addr"),
						RegistryDialTimeout:      cmd.Duration("registry-dial-timeout"),
						RequestTimeout:           cmd.Duration("request-timeout"),
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

	registryClient, closeRegistry, err := registry.New(ctx, cfg.RegistryMode, cfg.RegistryAddress, cfg.RegistryDialTimeout)
	if err != nil {
		return err
	}
	defer func() {
		if err := closeRegistry(); err != nil {
			slog.Warn("failed to close registry connection", slog.String("error", err.Error()))
		}
	}()

	durableStore, err := newDurableStore(cfg.DurableStore)
	if err != nil {
		return err
	}
	spatial, err := newSpatialIndex(ctx, cfg)
	if err != nil {
		return err
	}
	var projection *spatialindex.Projection
	if spatial != nil {
		projection = spatialindex.NewProjection(spatial)
		volumes, err := durableStore.ListOperationalVolumes(ctx, "")
		if err != nil {
			spatial.Close()
			return fmt.Errorf("load durable volumes for spatial rebuild: %w", err)
		}
		if err := projection.Rebuild(ctx, volumes); err != nil {
			spatial.Close()
			return fmt.Errorf("rebuild spatial index: %w", err)
		}
		defer projection.Close()
		durableStore = durable.UseSpatialIndex(durableStore, projection)
	}
	providers, err := newAirspaceProviders(cfg, durableStore, projection)
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
	fleetService := service.NewFleetService(durableStore, telemetryStore, replayStore, registryClient)
	deconflictionService := deconfliction.NewDeconflictionService(durableStore, providers...)
	intentService := service.NewIntentService(durableStore, deconflictionService)
	preflightService := service.NewPreflightService(durableStore)
	conformanceService := service.NewConformanceService(durableStore, telemetryStore)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.NewWithWorkflows(fleetService, intentService, preflightService, conformanceService, cfg.RequestTimeout, deconflictionService).WithDebug(cfg.Debug).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("starting aero-arc-api",
			slog.String("http_addr", cfg.Addr),
			slog.String("durable_store", cfg.DurableStore),
			slog.String("spatial_index", cfg.SpatialIndex),
			slog.Any("airspace_providers", cfg.AirspaceProviders),
			slog.String("telemetry_store", cfg.TelemetryStore),
			slog.String("replay_store", cfg.ReplayStore),
			slog.String("registry_mode", cfg.RegistryMode),
			slog.String("registry_addr", cfg.RegistryAddress),
			slog.String("seed", cfg.Seed),
			slog.Bool("debug", cfg.Debug),
		)

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

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

func newDurableStore(mode string) (durable.Store, error) {
	switch mode {
	case "memory":
		return durablememory.NewStore(), nil
	default:
		return nil, fmt.Errorf("unsupported durable store %q", mode)
	}
}

func newSpatialIndex(ctx context.Context, cfg *config.Config) (spatialindex.Index, error) {
	switch cfg.SpatialIndex {
	case config.SpatialIndexNone:
		return nil, nil
	case config.SpatialIndexMemory:
		return spatialmemory.New(), nil
	case config.SpatialIndexPostGIS:
		return spatialpostgis.Open(ctx, cfg.PostGISDatabaseURL)
	default:
		return nil, fmt.Errorf("unsupported spatial index %q", cfg.SpatialIndex)
	}
}

func newAirspaceProviders(
	cfg *config.Config,
	durableStore durable.Store,
	localIndex spatialindex.CandidateFinder,
) ([]airspaceprovider.Provider, error) {
	providers := make([]airspaceprovider.Provider, 0, len(cfg.AirspaceProviders))
	for _, name := range cfg.AirspaceProviders {
		switch name {
		case airspaceprovider.ProviderLocal:
			if localIndex == nil {
				return nil, fmt.Errorf("local airspace provider requires a spatial index")
			}
			providers = append(providers, localprovider.New(durableStore, localIndex))
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
	})
}

func newTelemetryStore(cfg *config.Config) (telemetry.Store, error) {
	switch cfg.TelemetryStore {
	case "memory":
		return telemetrymemory.NewStore(), nil
	case "influxdb":
		return telemetryinfluxdb.New(cfg.InfluxDBHost, cfg.InfluxDBToken, cfg.InfluxDBDatabase)
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
