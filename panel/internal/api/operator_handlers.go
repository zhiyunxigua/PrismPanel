package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"PrismPanel/internal/daemon"
	"PrismPanel/internal/store"
)

type operatorNodeStatus struct {
	NodeID string          `json:"node_id"`
	State  string          `json:"state"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type operatorResponse struct {
	State         store.OperatorState  `json:"state"`
	ManageEnabled bool                 `json:"manage_enabled"`
	Nodes         []operatorNodeStatus `json:"nodes"`
}

func (s *Server) handleOperators(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	state, err := s.store.OperatorState(request.Context())
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, operatorResponse{
		State: state, ManageEnabled: s.config.Minecraft.ManageOperators,
		Nodes: s.operatorStatuses(request.Context(), state),
	})
}

func (s *Server) handleOperator(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/operators/"), "/")
	if path == "activate" {
		s.activateOperators(writer, request)
		return
	}
	if path == "" || strings.Contains(path, "/") {
		http.NotFound(writer, request)
		return
	}
	uuid, uuidErr := store.NormalizeMinecraftUUID(path)
	if uuidErr != nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", "玩家 UUID 格式无效"))
		return
	}
	switch request.Method {
	case http.MethodPut:
		var input struct {
			Name string `json:"name"`
		}
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &input)
		}
		if err == nil && len([]rune(strings.TrimSpace(input.Name))) > 64 {
			err = apiError("INVALID_REQUEST", "玩家名不能超过 64 个字符")
		}
		var state store.OperatorState
		if err == nil {
			session := currentSession(request)
			state, err = s.store.PutOperator(
				request.Context(), uuid, input.Name, session.User.ID, session.User.Username,
			)
		}
		s.record(request, "operator.add", uuid, map[string]any{"name": strings.TrimSpace(input.Name)}, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, s.operatorMutationResponse(state))
	case http.MethodDelete:
		state, err := s.store.DeleteOperator(request.Context(), uuid)
		err = publicError(err)
		s.record(request, "operator.delete", uuid, nil, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, s.operatorMutationResponse(state))
	default:
		methodNotAllowed(writer, "PUT, DELETE")
	}
}

func (s *Server) activateOperators(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	var err error
	if !s.config.Minecraft.ManageOperators {
		err = apiError("OPERATOR_MANAGEMENT_DISABLED", "面板配置未启用统一 OP 管理")
	}
	var state store.OperatorState
	if err == nil {
		state, err = s.store.ActivateOperatorManagement(request.Context())
	}
	s.record(request, "operator.activate", state.PanelID, map[string]any{
		"operator_count": len(state.Operators),
	}, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, s.operatorMutationResponse(state))
}

func (s *Server) operatorMutationResponse(state store.OperatorState) operatorResponse {
	response := operatorResponse{State: state, ManageEnabled: s.config.Minecraft.ManageOperators}
	if !state.Initialized || !s.config.Minecraft.ManageOperators {
		response.Nodes = s.operatorStatuses(context.Background(), state)
		return response
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	response.Nodes = s.reconcileOperatorState(ctx, state)
	return response
}

func (s *Server) reconcileOperators(ctx context.Context) {
	state, err := s.store.OperatorState(ctx)
	if err != nil {
		s.logger.Error("load operator state", "error", err)
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, status := range s.reconcileOperatorState(callCtx, state) {
		if status.State == "failed" {
			s.logger.Error("reconcile operators", "node_id", status.NodeID, "error", status.Error)
		}
	}
}

func (s *Server) reconcileOperatorState(ctx context.Context, state store.OperatorState) []operatorNodeStatus {
	s.operatorMu.Lock()
	defer s.operatorMu.Unlock()
	if !s.config.Minecraft.ManageOperators {
		return s.callOperatorNodes(ctx, "operators.source.remove", map[string]any{"panel_id": state.PanelID})
	}
	if !state.Initialized {
		return s.unavailableOperatorStatuses("uninitialized")
	}
	operators := make([]map[string]string, 0, len(state.Operators))
	for _, item := range state.Operators {
		operators = append(operators, map[string]string{"uuid": item.UUID, "name": item.Name})
	}
	return s.callOperatorNodes(ctx, "operators.replace", map[string]any{
		"panel_id": state.PanelID, "revision": state.Revision, "operators": operators,
	})
}

func (s *Server) operatorStatuses(ctx context.Context, state store.OperatorState) []operatorNodeStatus {
	if !s.config.Minecraft.ManageOperators {
		return s.unavailableOperatorStatuses("disabled")
	}
	if !state.Initialized {
		return s.unavailableOperatorStatuses("uninitialized")
	}
	return s.callOperatorNodes(ctx, "operators.status", map[string]any{"panel_id": state.PanelID})
}

func (s *Server) unavailableOperatorStatuses(state string) []operatorNodeStatus {
	result := make([]operatorNodeStatus, 0, len(s.connections.NodeIDs()))
	for _, nodeID := range s.connections.NodeIDs() {
		result = append(result, operatorNodeStatus{NodeID: nodeID, State: state})
	}
	return result
}

func (s *Server) callOperatorNodes(ctx context.Context, command string, input any) []operatorNodeStatus {
	nodeIDs := s.connections.NodeIDs()
	result := make([]operatorNodeStatus, len(nodeIDs))
	var wait sync.WaitGroup
	for index, nodeID := range nodeIDs {
		index, nodeID := index, nodeID
		result[index] = operatorNodeStatus{NodeID: nodeID, State: "pending"}
		if s.connections.Status(nodeID).State != "ONLINE" {
			continue
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			var response json.RawMessage
			err := s.connections.Call(callCtx, nodeID, command, input, &response)
			if err != nil {
				result[index].State = "failed"
				result[index].Error = operatorSyncError(err)
				return
			}
			result[index].State = "synced"
			result[index].Result = response
		}()
	}
	wait.Wait()
	sort.Slice(result, func(i, j int) bool { return result[i].NodeID < result[j].NodeID })
	return result
}

func operatorSyncError(err error) string {
	var daemonError *daemon.APIError
	if errors.As(err, &daemonError) {
		return daemonError.Message
	}
	return err.Error()
}
