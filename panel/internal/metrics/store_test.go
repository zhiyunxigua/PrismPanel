package metrics

import (
	"fmt"
	"testing"
	"time"
)

func TestStoreRetainsLatestTenMinutes(t *testing.T) {
	store := NewStore()
	startedAt := time.Now().UTC().Add(-129 * 5 * time.Second)
	for index := 0; index < 130; index++ {
		value := float64(index)
		players := index
		store.Record("node-1", Snapshot{
			Host: HostSnapshot{SampledAt: startedAt.Add(time.Duration(index) * 5 * time.Second), CPUPercent: value},
			Instances: []InstanceSnapshot{{
				InstanceID: "survival_1", ServerID: "survival", Name: "Survival #1",
				State: "running", CPUPercent: &value, OnlinePlayers: &players,
			}},
		})
	}
	host := store.NodeHistory("node-1")
	if len(host) != maxHistoryPoints {
		t.Fatalf("host history length = %d, want %d", len(host), maxHistoryPoints)
	}
	series := store.ServerHistory("node-1", "survival")
	if len(series) != 1 || len(series[0].Points) != maxHistoryPoints {
		t.Fatalf("unexpected instance history: %#v", series)
	}
	if got := series[0].Points[0].OnlinePlayers; got == nil || *got != 10 {
		t.Fatalf("first retained player count = %v, want 10", got)
	}
}

func TestStoreSeparatesNodesAndServers(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	for index, nodeID := range []string{"node-a", "node-b"} {
		store.Record(nodeID, Snapshot{
			Host: HostSnapshot{SampledAt: now},
			Instances: []InstanceSnapshot{{
				InstanceID: fmt.Sprintf("server_%d", index), ServerID: fmt.Sprintf("group-%d", index),
			}},
		})
	}
	if got := store.ServerHistory("node-a", "group-1"); len(got) != 0 {
		t.Fatalf("cross-node history leaked: %#v", got)
	}
}

func TestStoreReturnsCurrentNodeSnapshot(t *testing.T) {
	store := NewStore()
	now := time.Now().UTC()
	players := 3
	store.Record("node-a", Snapshot{
		Host: HostSnapshot{SampledAt: now, CPUPercent: 25, MemoryUsedBytes: 1024},
		Instances: []InstanceSnapshot{{
			InstanceID: "survival", ServerID: "survival", Name: "Survival",
			State: "running", OnlinePlayers: &players,
		}},
	})
	current := store.CurrentNode("node-a")
	if current.Host == nil || current.Host.CPUPercent != 25 {
		t.Fatalf("unexpected current host: %#v", current.Host)
	}
	if len(current.Instances) != 1 || current.Instances[0].OnlinePlayers == nil ||
		*current.Instances[0].OnlinePlayers != 3 {
		t.Fatalf("unexpected current instances: %#v", current.Instances)
	}
}
