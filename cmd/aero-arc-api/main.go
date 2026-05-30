package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/config"
	"github.com/Aero-Arc/aero-arc-api/internal/httpapi"
	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/Aero-Arc/aero-arc-api/internal/store/memory"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	ctx := context.Background()
	registryClient, closeRegistry, err := registry.New(ctx, cfg.RegistryMode, cfg.RegistryAddress, cfg.RegistryDialTimeout)
	if err != nil {
		slog.Error("failed to initialize registry client", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer func() {
		if err := closeRegistry(); err != nil {
			slog.Warn("failed to close registry connection", slog.String("error", err.Error()))
		}
	}()

	// TODO: wire tidb/postgres durable stores, influxdb telemetry, s3 replay storage, and real registry gRPC clients.
	durableStore := memory.NewDurableStore()
	telemetryStore := memory.NewTelemetryStore()
	replayStore := memory.NewReplayStore()
	fleetService := service.NewFleetService(durableStore, telemetryStore, replayStore, registryClient)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(fleetService, cfg.RequestTimeout).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("starting aero-arc-api",
			slog.String("http_addr", cfg.Addr),
			slog.String("store_mode", cfg.StoreMode),
			slog.String("registry_mode", cfg.RegistryMode),
			slog.String("registry_addr", cfg.RegistryAddress),
		)

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server stopped unexpectedly", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Warn("failed to shut down http server gracefully", slog.String("error", err.Error()))
	}

	slog.Info("aero-arc-api shutdown complete")
}
