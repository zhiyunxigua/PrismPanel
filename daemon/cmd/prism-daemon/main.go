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

	"PrismPanel-daemon/internal/api"
	"PrismPanel-daemon/internal/config"
	"PrismPanel-daemon/internal/deployment"
	"PrismPanel-daemon/internal/eventbus"
	fileservice "PrismPanel-daemon/internal/files"
	firewallservice "PrismPanel-daemon/internal/firewall"
	pluginservice "PrismPanel-daemon/internal/plugins"
	"PrismPanel-daemon/internal/secret"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/store"
	"PrismPanel-daemon/internal/supervisor"
	"PrismPanel-daemon/internal/ticket"
)

func main() {
	if err := run(); err != nil {
		slog.Error("daemon stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "daemon.yaml", "path to daemon YAML configuration")
	showSecret := flag.Bool("show-secret", false, "print the local daemon main secret and exit")
	resetSecret := flag.Bool("reset-secret", false, "replace the local daemon main secret and exit")
	flag.Parse()

	cfg, configCreated, err := config.LoadOrCreate(*configPath)
	if err != nil {
		return err
	}
	dataDir := cfg.DataDir()
	secretPath := filepath.Join(dataDir, "secret.json")
	secretFile, secretCreated, err := secret.LoadOrCreate(secretPath)
	if err != nil {
		return err
	}
	if *resetSecret {
		secretFile, err = secret.Reset(secretPath)
		if err != nil {
			return err
		}
		fmt.Println(secretFile.Secret)
		return nil
	}
	if *showSecret {
		fmt.Println(secretFile.Secret)
		return nil
	}
	if configCreated {
		slog.Info("created default daemon configuration", "path", *configPath)
	}
	if secretCreated {
		slog.Info("generated daemon main secret", "path", secretPath)
	}

	serverStore := store.NewServerStore(dataDir)
	serverConfigs, loadErrors, err := serverStore.LoadAll()
	if err != nil {
		return err
	}
	for _, loadErr := range loadErrors {
		slog.Error("server config was isolated", "path", loadErr.Path, "error", loadErr.Err)
	}
	events := &eventbus.Bus{}
	manager, err := supervisor.NewManager(cfg, events, serverConfigs)
	if err != nil {
		return err
	}
	operatorStore := store.NewOperatorStore(dataDir)
	operatorState, err := operatorStore.Load()
	if err != nil {
		return err
	}
	if err := manager.ConfigureOperators(operatorState, operatorStore.Save); err != nil {
		return fmt.Errorf("configure operator registry: %w", err)
	}
	serverService := serverservice.NewService(serverStore, manager, serverConfigs)
	ticketManager := ticket.NewManager()
	deploymentManager := deployment.NewManager(serverService, manager, cfg.Files.CopyConcurrency)
	pluginManager, err := pluginservice.NewService(manager, serverService, dataDir)
	if err != nil {
		return err
	}
	fileManager := fileservice.NewService(
		serverService, manager, deploymentManager, cfg.Files.MaxEditFileSize,
		cfg.Files.MaxUploadFileSize, cfg.Files.MaxExtractedSize, cfg.Files.MaxConcurrentTransfers,
	)
	firewallManager, err := firewallservice.New(dataDir, cfg.Server.Port, slog.Default())
	if err != nil {
		return err
	}
	firewallContext, firewallCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := firewallManager.Initialize(firewallContext); err != nil {
		slog.Error("initialize firewall service", "error", err)
	}
	firewallCancel()
	httpServer := api.NewServer(
		cfg, secretFile.Secret, secretFile.NodeID, serverService, manager, ticketManager,
		deploymentManager, pluginManager, fileManager, firewallManager, events, slog.Default(),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	manager.StartMetrics(ctx)
	pluginManager.Start(ctx)
	serverError := make(chan error, 1)
	go func() {
		scheme := "http"
		if cfg.SSL.Enabled {
			scheme = "https"
		}
		slog.Info("daemon listening", "address", fmt.Sprintf("%s://%s:%d", scheme, cfg.Server.Listen, cfg.Server.Port))
		serverError <- httpServer.ListenAndServe()
	}()
	manager.StartAuto()

	select {
	case <-ctx.Done():
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownTimeout := time.Duration(cfg.Process.ShutdownTimeoutSec) * time.Second
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = httpServer.Shutdown(shutdownContext)
	if err := manager.Shutdown(shutdownContext); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}
