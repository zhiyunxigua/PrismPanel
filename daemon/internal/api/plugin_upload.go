package api

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"PrismPanel-daemon/internal/apperr"
	pluginservice "PrismPanel-daemon/internal/plugins"
)

const maxInstancePluginUploadSize = int64(256 * 1024 * 1024)

func (s *Server) handlePluginUpload(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", "POST")
		writeJSON(writer, http.StatusMethodNotAllowed, map[string]any{
			"success": false, "error": apperr.New("METHOD_NOT_ALLOWED", "POST is required"),
		})
		return
	}
	instanceID := strings.TrimSpace(request.URL.Query().Get("instance_id"))
	filename, err := cleanPluginUploadFilename(request.URL.Query().Get("filename"))
	token := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	created, ticketErr := s.tickets.ConsumeRestricted(
		token, "plugin.upload", "instance", instanceID, ".", http.MethodPost,
	)
	if err == nil {
		err = ticketErr
	}
	overwrite := strings.EqualFold(request.Header.Get("X-Prism-Overwrite"), "true")
	if err == nil && overwrite && !created.AllowOverwrite {
		err = apperr.New("PERMISSION_DENIED", "临时凭证不允许替换现有插件")
	}
	if err == nil && (request.ContentLength <= 0 || request.ContentLength != created.MaxBytes) {
		err = apperr.New("FILE_SIZE_MISMATCH", "插件上传大小与授权不一致")
	}
	if err != nil {
		writeInstancePluginUploadError(writer, pluginservice.InstanceUploadResult{}, err)
		return
	}
	result, uploadErr := s.receiveInstancePlugin(request, instanceID, filename, overwrite, created.MaxBytes)
	if uploadErr != nil {
		writeInstancePluginUploadError(writer, result, uploadErr)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"success": true, "data": result})
}

func cleanPluginUploadFilename(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value != filepath.Base(value) || strings.Contains(value, "/") ||
		strings.ContainsRune(value, rune(92)) || !strings.HasSuffix(strings.ToLower(value), ".jar") {
		return "", apperr.New("INVALID_REQUEST", "必须提供有效的 JAR 文件名")
	}
	return value, nil
}

func (s *Server) receiveInstancePlugin(
	request *http.Request,
	instanceID string,
	filename string,
	overwrite bool,
	expectedSize int64,
) (pluginservice.InstanceUploadResult, error) {
	temp, err := os.CreateTemp("", ".prism-plugin-upload-*.jar")
	if err != nil {
		return pluginservice.InstanceUploadResult{}, apperr.Wrap("FILE_WRITE_FAILED", "无法创建插件上传临时文件", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	written, copyErr := io.Copy(temp, io.LimitReader(request.Body, expectedSize+1))
	syncErr := temp.Sync()
	closeErr := temp.Close()
	if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
		return pluginservice.InstanceUploadResult{}, apperr.Wrap("FILE_WRITE_FAILED", "插件上传写入失败", err)
	}
	if written != expectedSize {
		return pluginservice.InstanceUploadResult{}, apperr.New("FILE_SIZE_MISMATCH", "插件上传大小与授权不一致")
	}
	return s.plugins.UploadInstance(instanceID, tempPath, filename, overwrite)
}

func writeInstancePluginUploadError(
	writer http.ResponseWriter,
	result pluginservice.InstanceUploadResult,
	err error,
) {
	apiError := apperr.From(err)
	status := http.StatusBadRequest
	switch apiError.Code {
	case "UNAUTHENTICATED", "TICKET_EXPIRED":
		status = http.StatusUnauthorized
	case "PERMISSION_DENIED":
		status = http.StatusForbidden
	case "INSTANCE_NOT_FOUND":
		status = http.StatusNotFound
	case "PLUGIN_EXISTS", "PLUGIN_NAME_CONFLICT", "PLUGIN_FILE_CONFLICT", "INSTANCE_BUSY":
		status = http.StatusConflict
	case "FILE_TOO_LARGE":
		status = http.StatusRequestEntityTooLarge
	case "FILE_WRITE_FAILED", "PLUGIN_UPLOAD_FAILED", "INTERNAL":
		status = http.StatusInternalServerError
	}
	payload := map[string]any{"success": false, "error": apiError}
	if result.PluginName != "" {
		payload["data"] = result
	}
	writeJSON(writer, status, payload)
}
