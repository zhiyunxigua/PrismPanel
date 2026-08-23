package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"PrismPanel/internal/store"
)

func (s *Server) handleProxySyncRules(writer http.ResponseWriter, request *http.Request) {
	if !s.authorizeServerRequest(writer, request, "server.configure") {
		return
	}
	proxyNodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	proxyServerID := strings.TrimSpace(request.URL.Query().Get("server_id"))
	targetNodeID := strings.TrimSpace(request.URL.Query().Get("target_node_id"))
	targetServerID := strings.TrimSpace(request.URL.Query().Get("target_server_id"))
	if targetNodeID != "" {
		if request.Method != http.MethodGet && targetServerID == "" {
			writeRequestError(writer, apiError("INVALID_REQUEST", "target_server_id is required"))
			return
		}
		s.handleTargetProxyRules(writer, request, targetNodeID, targetServerID)
		return
	}
	if proxyNodeID == "" || proxyServerID == "" {
		writeRequestError(writer, apiError("INVALID_REQUEST", "node_id and server_id are required"))
		return
	}
	switch request.Method {
	case http.MethodGet:
		rules, err := s.store.ProxySyncRules(request.Context(), proxyNodeID, proxyServerID)
		var status json.RawMessage
		if err == nil {
			statusErr := s.connections.Call(request.Context(), proxyNodeID, "proxy.backends.status", map[string]any{
				"instance_id": proxyServerID,
			}, &status)
			if statusErr != nil {
				status, _ = json.Marshal(map[string]any{
					"instance_id": proxyServerID,
					"state":       "unavailable",
					"error":       statusErr.Error(),
				})
			}
		}
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"rules": rules, "status": status})
	case http.MethodPut:
		var input struct {
			Rules []store.TargetRule `json:"rules"`
		}
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &input)
		}
		if err == nil {
			err = s.store.ReplaceProxySyncRules(request.Context(), proxyNodeID, proxyServerID, input.Rules)
		}
		var status json.RawMessage
		if err == nil {
			status, err = s.syncProxy(request.Context(), proxyNodeID, proxyServerID)
		}
		s.record(request, "proxy.sync.configure", proxyNodeID+"/"+proxyServerID, input, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"rules": input.Rules, "status": status})
	default:
		methodNotAllowed(writer, "GET, PUT")
	}
}

func (s *Server) handleTargetProxyRules(
	writer http.ResponseWriter,
	request *http.Request,
	targetNodeID, targetServerID string,
) {
	owners, err := s.store.ProxySyncOwners(request.Context())
	if err != nil {
		writeError(writer, err)
		return
	}
	if request.Method == http.MethodGet {
		result := make([]map[string]any, 0, len(owners))
		for _, owner := range owners {
			rules, ruleErr := s.store.ProxySyncRules(request.Context(), owner.NodeID, owner.ServerID)
			if ruleErr != nil {
				writeError(writer, ruleErr)
				return
			}
			result = append(result, map[string]any{
				"node_id":   owner.NodeID,
				"server_id": owner.ServerID,
				"selected":  selectedByRules(rules, targetNodeID, targetServerID),
			})
		}
		writeSuccess(writer, map[string]any{"proxies": result})
		return
	}
	if request.Method != http.MethodPut {
		methodNotAllowed(writer, "GET, PUT")
		return
	}
	var input struct {
		Proxies []struct {
			NodeID   string `json:"node_id"`
			ServerID string `json:"server_id"`
			Enabled  bool   `json:"enabled"`
		} `json:"proxies"`
	}
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	if err == nil {
		for _, proxy := range input.Proxies {
			rules, ruleErr := s.store.ProxySyncRules(request.Context(), proxy.NodeID, proxy.ServerID)
			if ruleErr != nil {
				if err == nil {
					err = ruleErr
				}
				continue
			}
			rules = upsertTargetRule(rules, store.TargetRule{
				NodeID: targetNodeID, ServerID: targetServerID, Enabled: proxy.Enabled,
			})
			if ruleErr = s.store.ReplaceProxySyncRules(
				request.Context(), proxy.NodeID, proxy.ServerID, rules,
			); ruleErr != nil {
				if err == nil {
					err = ruleErr
				}
				continue
			}
			if _, ruleErr = s.syncProxy(request.Context(), proxy.NodeID, proxy.ServerID); ruleErr != nil {
				if err == nil {
					err = ruleErr
				}
			}
		}
	}
	s.record(request, "proxy.sync.target.configure", targetNodeID+"/"+targetServerID, input, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, input)
}

func (s *Server) syncProxy(ctx context.Context, nodeID, serverID string) (json.RawMessage, error) {
	catalog, err := s.loadCatalog(ctx)
	if err != nil {
		return nil, err
	}
	proxyFound := false
	for _, node := range catalog {
		if node.ID != nodeID {
			continue
		}
		for _, server := range node.Servers {
			if server.ServerID == serverID && server.Type == "standalone" && isProxyPlatform(server.Platform) {
				proxyFound = true
				break
			}
		}
	}
	if !proxyFound {
		return nil, apiError("INVALID_REQUEST", "target server is not a Velocity or Bungee proxy")
	}
	rules, err := s.store.ProxySyncRules(ctx, nodeID, serverID)
	if err != nil {
		return nil, err
	}
	servers, err := resolveProxyBackends(catalog, rules)
	if err != nil {
		return nil, err
	}
	var status json.RawMessage
	err = s.connections.Call(ctx, nodeID, "proxy.backends.sync", map[string]any{
		"instance_id": serverID,
		"revision":    time.Now().UnixNano(),
		"servers":     servers,
	}, &status)
	return status, err
}

func (s *Server) reconcileAllProxies(ctx context.Context) {
	s.proxySyncMu.Lock()
	defer s.proxySyncMu.Unlock()
	owners, err := s.store.ProxySyncOwners(ctx)
	if err != nil {
		s.logger.Error("list proxy sync owners", "error", err)
		return
	}
	for _, owner := range owners {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, syncErr := s.syncProxy(callCtx, owner.NodeID, owner.ServerID)
		cancel()
		if syncErr != nil {
			s.logger.Error("reconcile proxy backends",
				"node_id", owner.NodeID, "server_id", owner.ServerID, "error", syncErr)
		}
	}
}

func upsertTargetRule(rules []store.TargetRule, update store.TargetRule) []store.TargetRule {
	for index := range rules {
		if rules[index].NodeID == update.NodeID && rules[index].ServerID == update.ServerID {
			rules[index] = update
			return rules
		}
	}
	return append(rules, update)
}
