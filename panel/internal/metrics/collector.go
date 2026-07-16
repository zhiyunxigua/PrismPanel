package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"PrismPanel/internal/daemon"
)

const sampleInterval = 5 * time.Second

type Collector struct {
	connections *daemon.Manager
	store       *Store
	logger      *slog.Logger
}

func NewCollector(connections *daemon.Manager, store *Store, logger *slog.Logger) *Collector {
	return &Collector{connections: connections, store: store, logger: logger}
}

func (c *Collector) Start(ctx context.Context) {
	go func() {
		c.sample(ctx)
		ticker := time.NewTicker(sampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.sample(ctx)
			}
		}
	}()
}

func (c *Collector) sample(ctx context.Context) {
	var wait sync.WaitGroup
	for _, nodeID := range c.connections.NodeIDs() {
		if c.connections.Status(nodeID).State != "ONLINE" {
			continue
		}
		wait.Add(1)
		go func(targetNodeID string) {
			defer wait.Done()
			callContext, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			var snapshot Snapshot
			if err := c.connections.Call(callContext, targetNodeID, "metrics.snapshot", map[string]any{}, &snapshot); err != nil {
				if callContext.Err() == nil {
					c.logger.Debug("collect node metrics", "node_id", targetNodeID, "error", err)
				}
				return
			}
			c.store.Record(targetNodeID, snapshot)
		}(nodeID)
	}
	wait.Wait()
}
