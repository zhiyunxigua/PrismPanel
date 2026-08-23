package api

import (
	"errors"
	"testing"

	"PrismPanel/internal/store"
)

func TestResolveSelectedServersUsesNodeDefaultsAndOverrides(t *testing.T) {
	catalog := []catalogNode{{
		ID: "node-a",
		Servers: []catalogServer{
			{NodeID: "node-a", ServerID: "lobby", Platform: "paper"},
			{NodeID: "node-a", ServerID: "test", Platform: "paper"},
			{NodeID: "node-a", ServerID: "proxy", Platform: "velocity"},
		},
	}}
	rules := []store.TargetRule{
		{NodeID: "node-a", Enabled: true},
		{NodeID: "node-a", ServerID: "test", Enabled: false},
	}
	targets := resolveSelectedServers(catalog, rules, "spigot")
	if len(targets) != 1 || targets[0].ServerID != "lobby" {
		t.Fatalf("unexpected selected targets: %#v", targets)
	}
}

func TestResolveProxyBackendsRejectsCrossNodeIDConflict(t *testing.T) {
	catalog := []catalogNode{
		{
			ID: "node-a", BaseURL: "http://10.0.0.1:24444",
			Servers:   []catalogServer{{NodeID: "node-a", ServerID: "lobby", Platform: "paper"}},
			Instances: []catalogInstance{{NodeID: "node-a", InstanceID: "lobby_1", ServerID: "lobby", ConfiguredPort: 25565}},
		},
		{
			ID: "node-b", BaseURL: "http://10.0.0.2:24444",
			Servers:   []catalogServer{{NodeID: "node-b", ServerID: "lobby", Platform: "paper"}},
			Instances: []catalogInstance{{NodeID: "node-b", InstanceID: "lobby_1", ServerID: "lobby", ConfiguredPort: 25566}},
		},
	}
	rules := []store.TargetRule{{NodeID: "node-a", Enabled: true}, {NodeID: "node-b", Enabled: true}}
	if _, err := resolveProxyBackends(catalog, rules); err == nil {
		t.Fatal("expected duplicate instance id to be rejected")
	}
}

func TestResolveProxyBackendsUsesRuntimePort(t *testing.T) {
	runtimePort := 25570
	catalog := []catalogNode{{
		ID: "node-a", BaseURL: "http://10.0.0.1:24444",
		Servers: []catalogServer{{NodeID: "node-a", ServerID: "lobby", Platform: "paper"}},
		Instances: []catalogInstance{{
			NodeID: "node-a", InstanceID: "lobby", ServerID: "lobby",
			ConfiguredPort: 25565, RuntimePort: &runtimePort,
		}},
	}}
	backends, err := resolveProxyBackends(catalog, []store.TargetRule{{NodeID: "node-a", Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(backends) != 1 || backends[0]["address"] != "10.0.0.1:25570" {
		t.Fatalf("unexpected backends: %#v", backends)
	}
}

func TestResolveProxyBackendsRejectsPartialSnapshotForSelectedOfflineNode(t *testing.T) {
	catalog := []catalogNode{{
		ID: "node-a", BaseURL: "http://10.0.0.1:24444", Err: errors.New("offline"),
	}}
	_, err := resolveProxyBackends(catalog, []store.TargetRule{{NodeID: "node-a", Enabled: true}})
	if err == nil {
		t.Fatal("expected selected offline node to block synchronization")
	}
}
