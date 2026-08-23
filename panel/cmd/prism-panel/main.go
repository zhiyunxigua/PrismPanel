package main

import (
	"context"
	"encoding/json"
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
	"PrismPanel/internal/netgames"
	"PrismPanel/internal/nodes"
	panelplugins "PrismPanel/internal/plugins"
	"PrismPanel/internal/schedule"
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
	configPath := flag.String("config", "panel.yaml", "path to panel YAML configuration")
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
	netGameService, err := netgames.NewService(
		repository, filepath.Join(filepath.Dir(cfg.Security.MasterKeyFile), "net-games"), slog.Default(),
	)
	if err != nil {
		return err
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
	connectionManager.SetEventCallback(func(nodeID, eventType string, data json.RawMessage) {
		if eventType == "operator.drift" {
			var event struct {
				InstanceID    string `json:"instance_id"`
				Revision      uint64 `json:"revision"`
				RestoredCount int    `json:"restored_count"`
				RemovedCount  int    `json:"removed_count"`
			}
			if err := json.Unmarshal(data, &event); err != nil {
				slog.Error("decode operator drift event", "node_id", nodeID, "error", err)
				return
			}
			slog.Warn("corrected Minecraft operator drift",
				"node_id", nodeID, "instance_id", event.InstanceID, "revision", event.Revision,
				"restored_count", event.RestoredCount, "removed_count", event.RemovedCount)
			return
		}
		if eventType != "file.operation_result" {
			return
		}
		var event struct {
			OperationID string           `json:"operation_id"`
			Success     bool             `json:"success"`
			Error       *daemon.APIError `json:"error"`
		}
		if err := json.Unmarshal(data, &event); err != nil || event.OperationID == "" {
			slog.Error("decode file operation result", "node_id", nodeID, "error", err)
			return
		}
		errorCode := ""
		if event.Error != nil {
			errorCode = event.Error.Code
		}
		completeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		operation, changed, err := repository.CompleteFileOperation(
			completeContext, event.OperationID, nodeID, event.Success, errorCode,
		)
		if err != nil {
			slog.Error("complete file operation", "operation_id", event.OperationID, "error", err)
			return
		}
		if changed {
			writeFileOperationAudit(completeContext, repository, operation, event.Success, errorCode)
		}
	})
	go expireFileOperations(ctx, repository)
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
	netGameService.Start(ctx)
	scheduler := schedule.NewService(repository, connectionManager, cfg.Minecraft.ManageOperators, slog.Default())
	scheduler.Start(ctx)
	httpServer := api.NewServer(
		cfg, authService, repository, nodeService, connectionManager, metricStore,
		pluginRepository, netGameService, scheduler, slog.Default(),
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

func writeFileOperationAudit(ctx context.Context, repository *store.Store, operation store.FileOperation, success bool, errorCode string) {
	risk := "high"
	if operation.Action == "file.delete" {
		risk = "critical"
	}
	detail := operation.Detail
	if detail == nil {
		detail = make(map[string]any)
	}
	detail["node_id"] = operation.NodeID
	if err := repository.CreateAudit(ctx, store.AuditLog{
		RequestID: operation.RequestID, ActorUserID: operation.ActorUserID,
		SessionID: operation.SessionID, ActorUsername: operation.ActorUsername,
		ActorDisplayName: operation.ActorDisplayName, SourceIP: operation.SourceIP,
		UserAgent: operation.UserAgent, Action: operation.Action, ResourceType: "file",
		ResourceID: operation.ID, RiskLevel: risk, Success: success,
		ErrorCode: errorCode, Detail: detail,
	}); err != nil {
		slog.Error("write file operation audit", "operation_id", operation.ID, "error", err)
	}
}

func expireFileOperations(ctx context.Context, repository *store.Store) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expireContext, cancel := context.WithTimeout(ctx, 10*time.Second)
			operations, err := repository.ExpireFileOperations(expireContext, 100)
			if err != nil {
				slog.Error("expire file operations", "error", err)
			}
			for _, operation := range operations {
				writeFileOperationAudit(expireContext, repository, operation, false, "TICKET_EXPIRED")
			}
			cancel()
		}
	}
}
