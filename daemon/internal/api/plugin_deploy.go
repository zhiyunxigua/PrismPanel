package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"PrismPanel-daemon/internal/apperr"
)

func (s *Server) handlePluginDeploy(writer http.ResponseWriter, request *http.Request) {
	s.handlePluginBundleDeploy(writer, request, "plugin.deploy", "plugin", false)
}

func (s *Server) handlePluginConfigDeploy(writer http.ResponseWriter, request *http.Request) {
	s.handlePluginBundleDeploy(writer, request, "plugin.config.deploy", "config", false)
}

func (s *Server) handlePluginContentDeploy(writer http.ResponseWriter, request *http.Request) {
	backupSnapshot := strings.EqualFold(strings.TrimSpace(request.URL.Query().Get("backup_snapshot")), "true")
	s.handlePluginBundleDeploy(writer, request, "plugin.content.deploy", "content", backupSnapshot)
}

func (s *Server) handlePluginBundleDeploy(writer http.ResponseWriter, request *http.Request, scope, kind string, backupSnapshot bool) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", "POST")
		writeJSON(writer, http.StatusMethodNotAllowed, map[string]any{"success": false,
			"error": apperr.New("METHOD_NOT_ALLOWED", "POST is required")})
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer "))
	serverID := strings.TrimSpace(request.URL.Query().Get("server_id"))
	created, err := s.tickets.Consume(token, scope, serverID)
	if err != nil {
		writeJSON(writer, http.StatusUnauthorized, map[string]any{"success": false, "error": apperr.From(err)})
		return
	}
	// ContentLength == -1 表示 chunked 传输，无法预知大小，交由 io.LimitReader 上限与
	// 写后校验兜底；ContentLength == 0 的空 body 直接拒绝（与既有行为一致）。
	if request.ContentLength == 0 || request.ContentLength > created.MaxBytes {
		writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]any{"success": false,
			"error": apperr.New("INVALID_PLUGIN_BUNDLE", "plugin bundle size does not match ticket")})
		return
	}
	temp, err := os.CreateTemp("", "prism-plugin-upload-*.zip")
	if err != nil {
		writePluginDeployError(writer, err)
		return
	}
	path := temp.Name()
	defer os.Remove(path)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(request.Body, created.MaxBytes+1))
	closeErr := temp.Close()
	if copyErr != nil || closeErr != nil {
		writePluginDeployError(writer, errors.Join(copyErr, closeErr,
			fmt.Errorf("received %d bytes", written)))
		return
	}
	// 长度校验：chunked（ContentLength<0）时跳过相等校验（written != -1 恒成立），
	// 仅保留 written > MaxBytes 上限；显式声明长度时仍强制相等。
	if written > created.MaxBytes {
		writeJSON(writer, http.StatusRequestEntityTooLarge, map[string]any{"success": false,
			"error": apperr.New("INVALID_PLUGIN_BUNDLE", "plugin bundle size does not match ticket")})
		return
	}
	if request.ContentLength >= 0 && written != request.ContentLength {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"success": false,
			"error": apperr.New("INVALID_PLUGIN_BUNDLE",
				fmt.Sprintf("plugin bundle size does not match ticket (received %d bytes)", written))})
		return
	}
	if hex.EncodeToString(hash.Sum(nil)) != created.SHA256 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"success": false,
			"error": apperr.New("PLUGIN_HASH_MISMATCH", "plugin bundle sha256 does not match ticket")})
		return
	}
	var result any
	switch kind {
	case "config":
		result, err = s.plugins.DeployConfig(serverID, path)
	case "content":
		result, err = s.plugins.DeployContent(serverID, path, backupSnapshot)
	default:
		result, err = s.plugins.Deploy(serverID, path)
	}
	if err != nil {
		writePluginDeployError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"success": true, "data": result})
}

func writePluginDeployError(writer http.ResponseWriter, err error) {
	apiError := apperr.From(err)
	status := http.StatusBadRequest
	if apiError.Code == "INTERNAL" {
		status = http.StatusInternalServerError
	}
	writeJSON(writer, status, map[string]any{"success": false, "error": apiError})
}
