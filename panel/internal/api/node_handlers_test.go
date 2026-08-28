package api

import (
	"testing"

	panelmetrics "PrismPanel/internal/metrics"
)

func TestDashboardOnlinePlayersUsesLargerProxyOrServerTotal(t *testing.T) {
	proxyPlayers := 100
	serverPlayers := 90
	instances := []panelmetrics.InstanceCurrent{
		{Platform: "velocity", OnlinePlayers: &proxyPlayers},
		{Platform: "paper", OnlinePlayers: &serverPlayers},
	}
	proxy, servers := dashboardOnlinePlayerTotals(instances)
	if got := maxInt(proxy, servers); got != 100 {
		t.Fatalf("online players = %d, want 100", got)
	}

	serverPlayers = 120
	proxy, servers = dashboardOnlinePlayerTotals(instances)
	if got := maxInt(proxy, servers); got != 120 {
		t.Fatalf("online players = %d, want 120", got)
	}
}

func TestDashboardOnlinePlayersSumsAcrossProxyAndServerInstances(t *testing.T) {
	proxyA, proxyB := 40, 60
	serverA, serverB := 45, 55
	instances := []panelmetrics.InstanceCurrent{
		{Platform: "bungee", OnlinePlayers: &proxyA},
		{Platform: "velocity", OnlinePlayers: &proxyB},
		{Platform: "spigot", OnlinePlayers: &serverA},
		{Platform: "paper", OnlinePlayers: &serverB},
	}
	proxy, servers := dashboardOnlinePlayerTotals(instances)
	if got := maxInt(proxy, servers); got != 100 {
		t.Fatalf("online players = %d, want 100", got)
	}
}

func TestDashboardOnlinePlayersAggregatesBeforeTakingMaximum(t *testing.T) {
	proxyPlayers, serverPlayers := 100, 120
	nodeA := []panelmetrics.InstanceCurrent{
		{Platform: "velocity", OnlinePlayers: &proxyPlayers},
	}
	nodeB := []panelmetrics.InstanceCurrent{
		{Platform: "paper", OnlinePlayers: &serverPlayers},
	}
	proxy, servers := dashboardOnlinePlayerTotals(nodeA)
	proxyB, serversB := dashboardOnlinePlayerTotals(nodeB)
	proxy += proxyB
	servers += serversB
	if got := maxInt(proxy, servers); got != 120 {
		t.Fatalf("online players = %d, want 120", got)
	}
}
