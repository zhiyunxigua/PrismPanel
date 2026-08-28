package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"PrismPanel/internal/daemon"
	panelmetrics "PrismPanel/internal/metrics"
	panelnodes "PrismPanel/internal/nodes"
	"PrismPanel/internal/store"
)

type nodeRequest struct {
	Name      string `json:"name"`
	BaseURL   string `json:"base_url"`
	PublicURL string `json:"public_url"`
	Token     string `json:"token"`
	Enabled   *bool  `json:"enabled"`
}

type nodeMetricView struct {
	panelnodes.View
	Metrics *panelmetrics.HostSnapshot `json:"metrics,omitempty"`
}

func (input nodeRequest) serviceInput(defaultEnabled bool) panelnodes.Input {
	enabled := defaultEnabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	return panelnodes.Input{
		Name: input.Name, BaseURL: input.BaseURL, PublicURL: input.PublicURL,
		Token: input.Token, Enabled: enabled,
	}
}

func readNodeInput(request *http.Request) (nodeRequest, error) {
	var input nodeRequest
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	if err != nil {
		return input, apiError("INVALID_REQUEST", "节点配置格式无效")
	}
	return input, nil
}

func (s *Server) handleNodes(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if err := s.authorize(request, "node.view"); err != nil {
			writeRequestError(writer, err)
			return
		}
		items, err := s.visibleNodes(request)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		decorated := s.decorateNodes(items)
		writeSuccess(writer, map[string]any{"items": decorated, "total": len(decorated)})
	case http.MethodPost:
		if !s.allow(request, "node.create") {
			writeRequestError(writer, apiError("FORBIDDEN", "无权新增节点"))
			return
		}
		input, err := readNodeInput(request)
		var created panelnodes.View
		if err == nil {
			created, err = s.nodes.Create(request.Context(), input.serviceInput(true))
		}
		err = nodePublicError(err)
		s.record(request, "node.create", created.ID, map[string]any{
			"name": input.Name, "base_url": input.BaseURL, "public_url": input.PublicURL,
		}, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, response{Success: true, Data: created})
	default:
		methodNotAllowed(writer, "GET, POST")
	}
}

func (s *Server) handleNode(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/nodes/"), "/")
	if path == "test" {
		s.handleNodeTest(writer, request)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] == "metrics" {
		s.handleNodeMetrics(writer, request, parts[0])
		return
	}
	if path == "" || strings.Contains(path, "/") {
		http.NotFound(writer, request)
		return
	}
	switch request.Method {
	case http.MethodGet:
		if !s.allow(request, "node.view") {
			writeRequestError(writer, apiError("FORBIDDEN", "无权查看此节点"))
			return
		}
		item, err := s.nodes.Get(request.Context(), path)
		if err != nil {
			writeRequestError(writer, publicError(err))
			return
		}
		writeSuccess(writer, item)
	case http.MethodPut:
		if !s.allow(request, "node.update") {
			writeRequestError(writer, apiError("FORBIDDEN", "无权修改此节点"))
			return
		}
		input, err := readNodeInput(request)
		var updated panelnodes.View
		if err == nil {
			updated, err = s.nodes.Update(request.Context(), path, input.serviceInput(true))
		}
		err = nodePublicError(err)
		s.record(request, "node.update", path, map[string]any{
			"name": input.Name, "base_url": input.BaseURL, "public_url": input.PublicURL,
			"enabled": input.Enabled,
		}, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, updated)
	case http.MethodDelete:
		if !s.allow(request, "node.delete") {
			writeRequestError(writer, apiError("FORBIDDEN", "无权删除此节点"))
			return
		}
		err := s.nodes.Delete(request.Context(), path)
		s.record(request, "node.delete", path, nil, publicError(err))
		if err != nil {
			writeRequestError(writer, publicError(err))
			return
		}
		s.metrics.RemoveNode(path)
		if cleanupErr := s.store.DeleteSelectionNode(request.Context(), path); cleanupErr != nil {
			s.logger.Error("delete node selection rules", "node_id", path, "error", cleanupErr)
		}
		go s.reconcileAllProxies(context.Background())
		writeSuccess(writer, map[string]any{})
	default:
		methodNotAllowed(writer, "GET, PUT, DELETE")
	}
}

func (s *Server) handleNodeTest(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	if !s.allow(request, "node.create") && !s.allow(request, "node.update") {
		writeRequestError(writer, apiError("FORBIDDEN", "无权测试节点连接"))
		return
	}
	input, err := readNodeInput(request)
	var result panelnodes.TestResult
	if err == nil {
		result, err = s.nodes.Test(request.Context(), input.BaseURL, input.Token)
	}
	err = nodePublicError(err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, result)
}

func (s *Server) handleDashboard(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	if err := s.authorize(request, "dashboard.view"); err != nil {
		writeRequestError(writer, err)
		return
	}
	items, err := s.visibleNodes(request)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	online := 0
	proxyPlayers := 0
	serverPlayers := 0
	runningInstances := 0
	for _, item := range items {
		if item.Status == "ONLINE" || item.Status == "DEGRADED" {
			online++
		}
		current := s.metrics.CurrentNode(item.ID)
		proxy, servers := dashboardOnlinePlayerTotals(current.Instances)
		proxyPlayers += proxy
		serverPlayers += servers
		for _, instance := range current.Instances {
			if instance.State == "running" {
				runningInstances++
			}
		}
	}
	writeSuccess(writer, map[string]any{
		"summary": map[string]int{
			"online_players": maxInt(proxyPlayers, serverPlayers), "online_nodes": online,
			"running_instances": runningInstances, "active_alerts": 0,
		},
		"nodes": s.decorateNodes(items),
	})
}

func dashboardOnlinePlayerTotals(instances []panelmetrics.InstanceCurrent) (int, int) {
	proxyPlayers := 0
	serverPlayers := 0
	for _, instance := range instances {
		if instance.OnlinePlayers == nil {
			continue
		}
		if isProxyPlatform(instance.Platform) {
			proxyPlayers += *instance.OnlinePlayers
		} else {
			serverPlayers += *instance.OnlinePlayers
		}
	}
	return proxyPlayers, serverPlayers
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func (s *Server) decorateNodes(items []panelnodes.View) []nodeMetricView {
	result := make([]nodeMetricView, len(items))
	for index, item := range items {
		current := s.metrics.CurrentNode(item.ID)
		result[index] = nodeMetricView{View: item, Metrics: current.Host}
	}
	return result
}

func (s *Server) visibleNodes(request *http.Request) ([]panelnodes.View, error) {
	allowed, err := s.store.Can(request.Context(), currentSession(request).User, "node.view")
	if err != nil {
		return nil, err
	}
	if !allowed {
		return []panelnodes.View{}, nil
	}
	items, err := s.nodes.List(request.Context())
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Server) allow(request *http.Request, permission string) bool {
	allowed, err := s.store.Can(request.Context(), currentSession(request).User, permission)
	if err != nil {
		s.logger.Error("check permission", "permission", permission, "error", err)
		return false
	}
	return allowed
}

func nodePublicError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return publicError(err)
	}
	if errors.Is(err, store.ErrConflict) {
		return apiError("CONFLICT", "该连接 URL 已添加")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return apiError("NODE_UNREACHABLE", "节点连接超时")
	}
	var daemonError *daemon.APIError
	if errors.As(err, &daemonError) {
		if daemonError.Code == "UNAUTHENTICATED" {
			return apiError("INVALID_NODE_TOKEN", "节点令牌错误")
		}
		return apiError("NODE_CONNECTION_FAILED", daemonError.Message)
	}
	return apiError("NODE_CONNECTION_FAILED", err.Error())
}
