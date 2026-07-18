package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"sync"
	"time"

	"PrismPanel/internal/daemon"
	panelplugins "PrismPanel/internal/plugins"
	"PrismPanel/internal/store"
)

type catalogServer struct {
	NodeID   string `json:"node_id"`
	ServerID string `json:"server_id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Platform string `json:"platform"`
}

type catalogInstance struct {
	NodeID         string `json:"node_id"`
	InstanceID     string `json:"instance_id"`
	ServerID       string `json:"server_id"`
	ConfiguredPort int    `json:"configured_port"`
	RuntimePort    *int   `json:"runtime_port"`
}

type catalogNode struct {
	ID        string
	Name      string
	BaseURL   string
	Servers   []catalogServer
	Instances []catalogInstance
	Err       error
}

type deploymentTarget struct {
	NodeID   string `json:"node_id"`
	ServerID string `json:"server_id"`
}

func (s *Server) loadCatalog(ctx context.Context) ([]catalogNode, error) {
	nodes, err := s.nodes.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]catalogNode, len(nodes))
	var wait sync.WaitGroup
	for index, node := range nodes {
		result[index] = catalogNode{ID: node.ID, Name: node.Name, BaseURL: node.BaseURL}
		wait.Add(1)
		go func(target int) {
			defer wait.Done()
			var payload struct {
				Servers []struct {
					ServerID string `json:"server_id"`
					Name     string `json:"name"`
					Type     string `json:"type"`
					Platform string `json:"platform"`
				} `json:"servers"`
				Instances []struct {
					InstanceID     string `json:"instance_id"`
					ServerID       string `json:"server_id"`
					ConfiguredPort int    `json:"configured_port"`
					RuntimePort    *int   `json:"runtime_port"`
				} `json:"instances"`
			}
			callCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			if err := s.connections.Call(callCtx, result[target].ID, "server.list", map[string]any{}, &payload); err != nil {
				result[target].Err = err
				return
			}
			for _, server := range payload.Servers {
				result[target].Servers = append(result[target].Servers, catalogServer{
					NodeID: result[target].ID, ServerID: server.ServerID, Name: server.Name,
					Type: server.Type, Platform: server.Platform,
				})
			}
			for _, instance := range payload.Instances {
				result[target].Instances = append(result[target].Instances, catalogInstance{
					NodeID: result[target].ID, InstanceID: instance.InstanceID,
					ServerID: instance.ServerID, ConfiguredPort: instance.ConfiguredPort,
					RuntimePort: instance.RuntimePort,
				})
			}
		}(index)
	}
	wait.Wait()
	return result, nil
}

func resolveSelectedServers(
	catalog []catalogNode,
	rules []store.TargetRule,
	pluginType string,
) []deploymentTarget {
	nodeDefaults, overrides := splitTargetRules(rules)
	result := make([]deploymentTarget, 0)
	for _, node := range catalog {
		if node.Err != nil {
			continue
		}
		for _, server := range node.Servers {
			if pluginTypeForPlatform(server.Platform) != pluginType {
				continue
			}
			selected := nodeDefaults[node.ID]
			if value, exists := overrides[node.ID+"\x00"+server.ServerID]; exists {
				selected = value
			}
			if selected {
				result = append(result, deploymentTarget{NodeID: node.ID, ServerID: server.ServerID})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].NodeID == result[j].NodeID {
			return result[i].ServerID < result[j].ServerID
		}
		return result[i].NodeID < result[j].NodeID
	})
	return result
}

func resolveProxyBackends(catalog []catalogNode, rules []store.TargetRule) ([]map[string]string, error) {
	nodeDefaults, overrides := splitTargetRules(rules)
	selectedServers := make(map[string]struct{})
	for _, node := range catalog {
		if node.Err != nil {
			if targetRulesMaySelectNode(rules, node.ID) {
				return nil, fmt.Errorf("selected backend node %s is unavailable: %w", node.ID, node.Err)
			}
			continue
		}
		for _, server := range node.Servers {
			if isProxyPlatform(server.Platform) {
				continue
			}
			selected := nodeDefaults[node.ID]
			if value, exists := overrides[node.ID+"\x00"+server.ServerID]; exists {
				selected = value
			}
			if selected {
				selectedServers[node.ID+"\x00"+server.ServerID] = struct{}{}
			}
		}
	}
	addresses := make([]map[string]string, 0)
	seen := make(map[string]string)
	for _, node := range catalog {
		if node.Err != nil {
			continue
		}
		host, err := nodeHost(node.BaseURL)
		if err != nil {
			return nil, err
		}
		for _, instance := range node.Instances {
			if _, selected := selectedServers[node.ID+"\x00"+instance.ServerID]; !selected {
				continue
			}
			port := instance.ConfiguredPort
			if instance.RuntimePort != nil {
				port = *instance.RuntimePort
			}
			address := net.JoinHostPort(host, fmt.Sprintf("%d", port))
			if _, exists := seen[instance.InstanceID]; exists {
				return nil, fmt.Errorf("duplicate proxy backend id %s across nodes", instance.InstanceID)
			}
			seen[instance.InstanceID] = address
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		addresses = append(addresses, map[string]string{"id": id, "address": seen[id]})
	}
	return addresses, nil
}

func targetRulesMaySelectNode(rules []store.TargetRule, nodeID string) bool {
	for _, rule := range rules {
		if rule.NodeID == nodeID && rule.Enabled {
			return true
		}
	}
	return false
}

func splitTargetRules(rules []store.TargetRule) (map[string]bool, map[string]bool) {
	nodeDefaults := make(map[string]bool)
	overrides := make(map[string]bool)
	for _, rule := range rules {
		if rule.ServerID == "" {
			nodeDefaults[rule.NodeID] = rule.Enabled
		} else {
			overrides[rule.NodeID+"\x00"+rule.ServerID] = rule.Enabled
		}
	}
	return nodeDefaults, overrides
}

func selectedByRules(rules []store.TargetRule, nodeID, serverID string) bool {
	nodeDefaults, overrides := splitTargetRules(rules)
	if value, exists := overrides[nodeID+"\x00"+serverID]; exists {
		return value
	}
	return nodeDefaults[nodeID]
}

func pluginTypeForPlatform(platform string) string {
	if isProxyPlatform(platform) {
		return platform
	}
	return panelplugins.PluginTypeSpigot
}

func isProxyPlatform(platform string) bool {
	return platform == panelplugins.PluginTypeVelocity || platform == panelplugins.PluginTypeBungee
}

func nodeHost(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid daemon URL")
	}
	return parsed.Hostname(), nil
}

func daemonAPIError(err error) *daemon.APIError {
	var target *daemon.APIError
	if errors.As(err, &target) {
		return target
	}
	return &daemon.APIError{Code: "INTERNAL", Message: err.Error()}
}

func encodeRaw(value any) json.RawMessage {
	contents, _ := json.Marshal(value)
	return contents
}
