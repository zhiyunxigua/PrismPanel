package api

import (
	"net/http"
	"strings"

	"PrismPanel/internal/metrics"
)

func (s *Server) handleNodeMetrics(writer http.ResponseWriter, request *http.Request, nodeID string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	if !s.allow(request, "node.view") {
		writeRequestError(writer, apiError("FORBIDDEN", "无权查看节点性能"))
		return
	}
	if _, err := s.nodes.Get(request.Context(), nodeID); err != nil {
		writeRequestError(writer, publicError(err))
		return
	}
	writeSuccess(writer, map[string]any{
		"retention_seconds":       600,
		"sample_interval_seconds": 5,
		"points":                  s.metrics.NodeHistory(nodeID),
	})
}

func (s *Server) handleServerMetrics(writer http.ResponseWriter, request *http.Request, serverID string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	canViewAll := s.allow(request, "server.view")
	nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	if nodeID == "" {
		writeRequestError(writer, apiError("INVALID_REQUEST", "必须通过 node_id 指定目标节点"))
		return
	}
	if _, err := s.nodes.Get(request.Context(), nodeID); err != nil {
		writeRequestError(writer, publicError(err))
		return
	}
	series := s.metrics.ServerHistory(nodeID, serverID)
	if !canViewAll {
		adminSet, err := s.instanceAdminSet(request)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		visible := make([]metrics.InstanceSeries, 0, len(series))
		for _, item := range series {
			if _, assigned := adminSet[nodeID+"\x00"+item.InstanceID]; assigned {
				visible = append(visible, item)
			}
		}
		series = visible
	}
	writeSuccess(writer, map[string]any{
		"retention_seconds":       600,
		"sample_interval_seconds": 5,
		"instances":               series,
	})
}
