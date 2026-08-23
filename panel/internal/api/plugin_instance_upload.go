package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"PrismPanel/internal/daemon"
)

func (s *Server) handleInstancePluginUpload(writer http.ResponseWriter, request *http.Request, instanceID string) {
	nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	filename := strings.TrimSpace(request.URL.Query().Get("filename"))
	overwrite, _ := strconv.ParseBool(request.URL.Query().Get("overwrite"))
	var err error
	if nodeID != "" {
		err = s.authorizeInstance(request, "plugin.upload", instanceID)
	}
	if nodeID == "" {
		err = apiError("INVALID_REQUEST", "必须指定目标节点")
	} else if filename == "" || !strings.EqualFold(fileExtension(filename), ".jar") {
		err = apiError("INVALID_REQUEST", "仅允许上传 JAR 插件文件")
	} else if request.ContentLength <= 0 || request.ContentLength > maxPluginJARUpload {
		err = apiError("INVALID_REQUEST", "插件 JAR 大小无效或超过限制")
	}
	var ticket struct {
		Ticket string
	}
	if err == nil {
		err = s.connections.Call(request.Context(), nodeID, "ticket.create", map[string]any{
			"scope": "plugin.upload", "instance_id": instanceID, "size": request.ContentLength,
			"overwrite": overwrite, "ttl_seconds": 300,
		}, &ticket)
	}
	result := map[string]any{}
	if err == nil {
		request.Body = http.MaxBytesReader(writer, request.Body, maxPluginJARUpload)
		err = s.connections.UploadInstancePlugin(
			request.Context(), nodeID, ticket.Ticket, instanceID, filename, overwrite,
			request.Body, request.ContentLength, &result,
		)
	}
	s.record(request, "plugin.upload.instance", instanceID, map[string]any{
		"filename": filename, "overwrite": overwrite, "result": result,
	}, err)
	if err != nil {
		var daemonError *daemon.APIError
		if errors.As(err, &daemonError) && daemonError.Code == "PLUGIN_EXISTS" {
			writeJSON(writer, http.StatusConflict, response{Success: false, Data: result, Error: daemonError})
			return
		}
		writeError(writer, err)
		return
	}
	writeSuccess(writer, result)
}
