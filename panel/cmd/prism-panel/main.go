package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"PrismPanel/internal/api"
	"PrismPanel/internal/auth"
	"PrismPanel/internal/config"
	"PrismPanel/internal/daemon"
	panelmetrics "PrismPanel/internal/metrics"
	"PrismPanel/internal/nodes"
	panelplugins "PrismPanel/internal/plugins"
	"PrismPanel/internal/secret"
	"PrismPanel/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("panel stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "data/panel.yaml", "path to panel YAML configuration")
	flag.Parse()
	cfg, created, err := config.LoadOrCreate(*configPath)
	if err != nil {
		return err
	}
	if created {
		slog.Info("created default panel configuration", "path", *configPath)
	}
	openContext, cancelOpen := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelOpen()
	repository, err := store.Open(openContext, cfg.Database)
	if err != nil {
		return err
	}
	defer repository.Close()
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve panel executable: %w", err)
	}
	pluginRepository, err := panelplugins.NewRepository(filepath.Join(filepath.Dir(executable), "plugins"))
	if err != nil {
		return err
	}
	scanReport, err := pluginRepository.Rescan()
	if err != nil {
		return fmt.Errorf("scan plugin repository: %w", err)
	}
	catalogContext, cancelCatalog := context.WithTimeout(context.Background(), 30*time.Second)
	if err := syncPluginCatalog(catalogContext, repository, scanReport.Plugins); err != nil {
		cancelCatalog()
		return err
	}
	cancelCatalog()
	for _, warning := range scanReport.Warnings {
		slog.Warn("plugin repository scan warning", "warning", warning)
	}
	sessionLifetime, _ := cfg.SessionLifetime()
	idleTimeout, _ := cfg.IdleTimeout()
	authService, err := auth.NewService(repository, auth.Options{
		SessionLifetime: sessionLifetime, IdleTimeout: idleTimeout,
	})
	if err != nil {
		return err
	}
	masterKey, keyCreated, err := secret.LoadOrCreateMasterKey(cfg.Security.MasterKeyFile)
	if err != nil {
		return err
	}
	if keyCreated {
		slog.Info("generated panel master key", "path", cfg.Security.MasterKeyFile)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	connectionManager := daemon.NewManager(slog.Default(), func(nodeID string, status daemon.RuntimeStatus) {
		updateContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var connectedAt *time.Time
		if !status.ConnectedAt.IsZero() {
			value := status.ConnectedAt
			connectedAt = &value
		}
		if err := repository.UpdateNodeRuntime(updateContext, nodeID, status.NodeID, status.Version,
			status.ProtocolVersion, status.Capabilities, connectedAt, status.LastError); err != nil {
			slog.Error("update node runtime", "node_id", nodeID, "error", err)
		}
	})
	nodeService, err := nodes.NewService(repository, connectionManager, masterKey)
	if err != nil {
		return err
	}
	definitions, err := nodeService.LoadConnections(context.Background())
	if err != nil {
		return err
	}
	connectionManager.Start(ctx, definitions)
	defer connectionManager.Close()
	metricStore := panelmetrics.NewStore()
	panelmetrics.NewCollector(connectionManager, metricStore, slog.Default()).Start(ctx)
	httpServer := api.NewServer(
		cfg, authService, repository, nodeService, connectionManager, metricStore,
		pluginRepository, slog.Default(),
	)
	serverError := make(chan error, 1)
	go func() {
		slog.Info("panel listening", "address", fmt.Sprintf("http://%s:%d", cfg.Server.Listen, cfg.Server.Port))
		serverError <- httpServer.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownContext)
}
