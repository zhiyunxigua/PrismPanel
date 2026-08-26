package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"PrismPanel/internal/daemon"
)

const (
	maxBatchTargets  = 200
	batchCallTimeout = 30 * time.Second
)

// batchTarget 批量操作目标：按 node_id+server_id 选中服务器组内全部实例，
// 或按 node_id+instance_id 指定单个实例。删除仅支持 node_id+server_id（组级）。
type batchTarget struct {
	NodeID     string `json:"node_id"`
	ServerID   string `json:"server_id"`
	InstanceID string `json:"instance_id"`
}

type batchRequest struct {
	Action  string        `json:"action"`
	Targets []batchTarget `json:"targets"`
	Confirm bool          `json:"confirm"`
}

type batchResult struct {
	NodeID     string    `json:"node_id"`
	ServerID   string    `json:"server_id,omitempty"`
	InstanceID string    `json:"instance_id,omitempty"`
	Success    bool      `json:"success"`
	Error      *APIError `json:"error,omitempty"`
}

type batchSummary struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
}

var batchInstanceActions = map[string]bool{
	"start":   true,
	"stop":    true,
	"restart": true,
	"kill":    true,
}

// handleInstancesBatch 批量服务器操作：POST /api/v1/instances/batch
//
//	body: {"action":"start|stop|restart|kill|delete",
//	       "targets":[{"node_id","server_id"|"instance_id"}...],
//	       "confirm":true}   // delete 必须 confirm=true
//
// 逐目标鉴权（instance.<action> / server.delete），无权限目标标 failed；
// 聚合 per-target 结果返回，部分失败不视为 HTTP 错误。
func (s *Server) handleInstancesBatch(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	body, err := readBody(request)
	if err != nil {
		writeError(writer, err)
		return
	}
	req, err := parseBatchRequest(body)
	if err != nil {
		writeError(writer, err)
		return
	}
	results := s.executeBatch(request, req)
	summary := summarizeBatch(results)
	detail := map[string]any{"action": req.Action, "targets": req.Targets, "results": results}
	auditAction := "instance.batch." + req.Action
	if req.Action == "delete" {
		auditAction = "server.batch.delete"
	}
	var auditErr error
	if summary.Failed > 0 {
		auditErr = apiError("PARTIAL_FAILURE", fmt.Sprintf("%d/%d 目标执行失败", summary.Failed, summary.Total))
	}
	s.record(request, auditAction, batchResourceTarget(req.Targets), detail, auditErr)
	writeSuccess(writer, map[string]any{
		"action":  req.Action,
		"summary": summary,
		"results": results,
	})
}

func batchResourceTarget(targets []batchTarget) string {
	if len(targets) == 1 {
		target := targets[0]
		if target.InstanceID != "" {
			return target.NodeID + ":" + target.InstanceID
		}
		return target.NodeID + ":" + target.ServerID
	}
	return fmt.Sprintf("batch(%d)", len(targets))
}

// parseBatchRequest 校验并规范化批量请求（纯函数，便于测试）。
func parseBatchRequest(body []byte) (*batchRequest, error) {
	var req batchRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, &daemon.APIError{Code: "INVALID_REQUEST", Message: "批量操作请求格式无效"}
	}
	action := strings.TrimSpace(req.Action)
	if action == "delete" {
		if !req.Confirm {
			return nil, apiError("CONFIRM_REQUIRED", "批量删除为高风险操作，需二次确认后执行")
		}
	} else if !batchInstanceActions[action] {
		return nil, apiError("INVALID_REQUEST", "不支持的批量操作："+req.Action)
	}
	if len(req.Targets) == 0 {
		return nil, apiError("INVALID_REQUEST", "未指定批量操作目标")
	}
	if len(req.Targets) > maxBatchTargets {
		return nil, apiError("INVALID_REQUEST", fmt.Sprintf("批量操作目标数量超过上限 %d", maxBatchTargets))
	}
	seen := make(map[string]bool, len(req.Targets))
	for index := range req.Targets {
		target := &req.Targets[index]
		target.NodeID = strings.TrimSpace(target.NodeID)
		target.ServerID = strings.TrimSpace(target.ServerID)
		target.InstanceID = strings.TrimSpace(target.InstanceID)
		if target.NodeID == "" {
			return nil, apiError("INVALID_REQUEST", "批量操作目标缺少 node_id")
		}
		if action == "delete" {
			if target.ServerID == "" {
				return nil, apiError("INVALID_REQUEST", "批量删除目标必须指定 server_id")
			}
			target.InstanceID = ""
		} else if target.ServerID == "" && target.InstanceID == "" {
			return nil, apiError("INVALID_REQUEST", "批量操作目标必须指定 server_id 或 instance_id")
		}
		key := target.NodeID + "|" + target.ServerID + "|" + target.InstanceID
		if seen[key] {
			return nil, apiError("INVALID_REQUEST", "批量操作目标重复")
		}
		seen[key] = true
	}
	req.Action = action
	return &req, nil
}

// executeBatch 逐目标执行批量操作，返回与目标顺序一致的 per-target 结果。
func (s *Server) executeBatch(request *http.Request, req *batchRequest) []batchResult {
	results := make([]batchResult, 0, len(req.Targets)*2)
	listCache := make(map[string]serverListSnapshot)
	for _, target := range req.Targets {
		if req.Action == "delete" {
			results = append(results, s.executeBatchDelete(request, target))
			continue
		}
		instanceIDs, resolveErr := s.resolveBatchInstanceIDs(request, target, listCache)
		if resolveErr != nil {
			results = append(results, batchResult{
				NodeID: target.NodeID, ServerID: target.ServerID,
				Success: false, Error: apiErrorFrom(resolveErr),
			})
			continue
		}
		permission := "instance." + req.Action
		allowed := s.allow(request, permission)
		for _, instanceID := range instanceIDs {
			result := batchResult{NodeID: target.NodeID, ServerID: target.ServerID, InstanceID: instanceID}
			if !allowed {
				result.Success = false
				result.Error = apiError("FORBIDDEN", "无权执行此操作")
				results = append(results, result)
				continue
			}
			var raw json.RawMessage
			callErr := s.callNodeTarget(request, target.NodeID, "instance."+req.Action,
				map[string]any{"instance_id": instanceID}, &raw)
			if callErr != nil {
				result.Success = false
				result.Error = apiErrorFrom(callErr)
			} else {
				result.Success = true
			}
			results = append(results, result)
		}
	}
	return results
}

// executeBatchDelete 组级删除：依赖 daemon server.delete 的「全部实例已停止」约束，
// 成功后清理 panel 侧 proxy sync 记录（与单实例 DELETE 行为一致）。
func (s *Server) executeBatchDelete(request *http.Request, target batchTarget) batchResult {
	result := batchResult{NodeID: target.NodeID, ServerID: target.ServerID}
	if !s.allow(request, "server.delete") {
		result.Error = apiError("FORBIDDEN", "无权删除服务器")
		return result
	}
	var raw json.RawMessage
	err := s.callNodeTarget(request, target.NodeID, "server.delete",
		map[string]any{"server_id": target.ServerID}, &raw)
	if err != nil {
		result.Error = apiErrorFrom(err)
		return result
	}
	if cleanupErr := s.store.DeleteProxySyncOwner(request.Context(), target.NodeID, target.ServerID); cleanupErr != nil {
		s.logger.Error("batch delete proxy sync owner", "node_id", target.NodeID, "server_id", target.ServerID, "error", cleanupErr)
	}
	if cleanupErr := s.store.DeleteProxySyncTarget(request.Context(), target.NodeID, target.ServerID); cleanupErr != nil {
		s.logger.Error("batch delete proxy sync target", "node_id", target.NodeID, "server_id", target.ServerID, "error", cleanupErr)
	}
	result.Success = true
	return result
}

// resolveBatchInstanceIDs 把目标解析为实例 ID 列表：优先 instance_id 直指；
// 否则取 node 的 server.list 缓存并过滤该 server_id 的全部实例。
func (s *Server) resolveBatchInstanceIDs(request *http.Request, target batchTarget, listCache map[string]serverListSnapshot) ([]string, error) {
	if target.InstanceID != "" {
		return []string{target.InstanceID}, nil
	}
	snapshot, cached := listCache[target.NodeID]
	if !cached {
		var content struct {
			Servers   []json.RawMessage `json:"servers"`
			Instances []json.RawMessage `json:"instances"`
		}
		err := s.callNodeTarget(request, target.NodeID, "server.list", map[string]any{}, &content)
		if err != nil {
			return nil, err
		}
		snapshot = serverListSnapshot{Instances: content.Instances}
		listCache[target.NodeID] = snapshot
	}
	instanceIDs := make([]string, 0, len(snapshot.Instances))
	for _, raw := range snapshot.Instances {
		var item struct {
			InstanceID string `json:"instance_id"`
			ServerID   string `json:"server_id"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if item.ServerID == target.ServerID && item.InstanceID != "" {
			instanceIDs = append(instanceIDs, item.InstanceID)
		}
	}
	if len(instanceIDs) == 0 {
		return nil, apiError("NOT_FOUND", "该服务器组下没有可用实例")
	}
	return instanceIDs, nil
}

type serverListSnapshot struct {
	Instances []json.RawMessage
}

// callNodeTarget 向指定节点发起 daemon 调用，带单次超时避免拖垮整个批量请求。
func (s *Server) callNodeTarget(request *http.Request, nodeID, messageType string, input, output any) error {
	ctx, cancel := context.WithTimeout(request.Context(), batchCallTimeout)
	defer cancel()
	return s.connections.Call(ctx, nodeID, messageType, input, output)
}

func summarizeBatch(results []batchResult) batchSummary {
	summary := batchSummary{Total: len(results)}
	for _, result := range results {
		if result.Success {
			summary.Succeeded++
		} else {
			summary.Failed++
		}
	}
	return summary
}

// apiErrorFrom 把任意错误映射为面板 APIError（批量结果内嵌用）。
func apiErrorFrom(err error) *APIError {
	if err == nil {
		return nil
	}
	var panelError *APIError
	if errors.As(err, &panelError) {
		return panelError
	}
	var daemonError *daemon.APIError
	if errors.As(err, &daemonError) {
		return &APIError{Code: daemonError.Code, Message: daemonError.Message}
	}
	if errors.Is(err, daemon.ErrDisconnected) {
		return apiError("DAEMON_UNAVAILABLE", "守护进程当前不可用")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return apiError("DAEMON_TIMEOUT", "节点响应超时")
	}
	return apiError("INTERNAL", "面板内部错误")
}
