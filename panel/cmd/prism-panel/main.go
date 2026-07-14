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
	"syscall"
	"time"

	"PrismPanel/internal/api"
	"PrismPanel/internal/audit"
	"PrismPanel/internal/config"
	"PrismPanel/internal/daemon"
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
	daemonSecret, err := cfg.DaemonSecret()
	if err != nil {
		return err
	}
	auditLogger, err := audit.NewLogger(cfg.Audit.File)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	daemonClient := daemon.NewClient(cfg.Daemon.URL, daemonSecret, slog.Default())
	go daemonClient.Run(ctx)
	httpServer := api.NewServer(cfg, daemonClient, auditLogger, slog.Default())
	serverError := make(chan error, 1)
	go func() {
		slog.Info("test panel listening", "address", fmt.Sprintf("http://%s:%d", cfg.Server.Listen, cfg.Server.Port))
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
