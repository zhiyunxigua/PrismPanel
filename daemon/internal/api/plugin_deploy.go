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
	s.handlePluginBundleDeploy(writer, request, "plugin.deploy", false)
}

func (s *Server) handlePluginConfigDeploy(writer http.ResponseWriter, request *http.Request) {
	s.handlePluginBundleDeploy(writer, request, "plugin.config.deploy", true)
}

func (s *Server) handlePluginBundleDeploy(writer http.ResponseWriter, request *http.Request, scope string, configOnly bool) {
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
	if request.ContentLength < 1 || request.ContentLength > created.MaxBytes {
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
	if copyErr != nil || closeErr != nil || written != request.ContentLength || written > created.MaxBytes {
		writePluginDeployError(writer, errors.Join(copyErr, closeErr,
			fmt.Errorf("received %d bytes", written)))
		return
	}
	if hex.EncodeToString(hash.Sum(nil)) != created.SHA256 {
		writeJSON(writer, http.StatusBadRequest, map[string]any{"success": false,
			"error": apperr.New("PLUGIN_HASH_MISMATCH", "plugin bundle sha256 does not match ticket")})
		return
	}
	var result any
	if configOnly {
		result, err = s.plugins.DeployConfig(serverID, path)
	} else {
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
