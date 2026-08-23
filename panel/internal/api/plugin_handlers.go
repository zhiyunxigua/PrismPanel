package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"

	panelplugins "PrismPanel/internal/plugins"
	"PrismPanel/internal/store"
)

func (s *Server) deployPluginBundle(request *http.Request, nodeID, serverID, path string, output any) error {
	return s.deployPluginArchive(request, nodeID, serverID, path, "plugin.deploy", false, false, output)
}

func (s *Server) deployPluginConfigBundle(request *http.Request, nodeID, serverID, path string, output any) error {
	return s.deployPluginArchive(request, nodeID, serverID, path, "plugin.config.deploy", true, false, output)
}

// deployPluginContentBundle 部署通用内容包（kind = config|full）。
// backupSnapshot 为完全配置的高风险标记：true 时 daemon 在部署前做整目录快照备份。
func (s *Server) deployPluginContentBundle(request *http.Request, nodeID, serverID, path string, backupSnapshot bool, output any) error {
	return s.deployPluginArchive(request, nodeID, serverID, path, "plugin.content.deploy", false, backupSnapshot, output)
}

func (s *Server) deployPluginArchive(request *http.Request, nodeID, serverID, path, scope string, configOnly, backupSnapshot bool, output any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		file.Close()
		return err
	}
	info, err := file.Stat()
	file.Close()
	if err != nil {
		return err
	}
	var ticket struct {
		Ticket string `json:"ticket"`
	}
	err = s.connections.Call(request.Context(), nodeID, "ticket.create", map[string]any{
		"scope": scope, "instance_id": serverID, "ttl_seconds": 300,
		"sha256": hex.EncodeToString(hash.Sum(nil)), "size": info.Size(),
	}, &ticket)
	if err != nil {
		return err
	}
	switch {
	case configOnly:
		return s.connections.UploadPluginConfig(request.Context(), nodeID, ticket.Ticket, serverID, path, output)
	case scope == "plugin.content.deploy":
		return s.connections.UploadPluginContent(request.Context(), nodeID, ticket.Ticket, serverID, path, backupSnapshot, output)
	default:
		return s.connections.UploadPlugin(request.Context(), nodeID, ticket.Ticket, serverID, path, output)
	}
}

const (
	maxPluginUploadBody = int64(1024 * 1024 * 1024)
	maxPluginJARUpload  = int64(256 * 1024 * 1024)
	maxPluginConfigZIP  = int64(512 * 1024 * 1024)
	maxPluginContentZIP = int64(700 * 1024 * 1024)
)

func (s *Server) handlePlugins(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if err := s.authorize(request, "plugin.view"); err != nil {
			writeRequestError(writer, err)
			return
		}
		catalog, err := s.plugins.List()
		if err != nil {
			s.logger.Error("list plugin repository", "error", err)
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"items": catalog})
	case http.MethodPost:
		if err := s.authorize(request, "plugin.upload"); err != nil {
			writeRequestError(writer, err)
			return
		}
		s.handlePluginUpload(writer, request)
	default:
		methodNotAllowed(writer, "GET, POST")
	}
}

func (s *Server) handlePluginUpload(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxPluginUploadBody)
	if err := request.ParseMultipartForm(32 * 1024 * 1024); err != nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", "插件上传表单无效或文件超过限制"))
		return
	}
	if request.MultipartForm != nil {
		defer request.MultipartForm.RemoveAll()
	}
	// 三种上传方式：仅 jar / jar + 内容包 / 仅内容包。jar 可省略（仅内容包上传）。
	jar, jarHeader, err := readMultipartFile(request, "jar", maxPluginJARUpload, false)
	if err != nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", err.Error()))
		return
	}
	config, configHeader, err := readMultipartFile(request, "config", maxPluginConfigZIP, false)
	if err != nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", err.Error()))
		return
	}
	if configHeader != nil && !strings.EqualFold(strings.TrimSpace(fileExtension(configHeader.Filename)), ".zip") {
		writeRequestError(writer, apiError("INVALID_REQUEST", "插件配置必须是 ZIP 文件"))
		return
	}
	content, contentHeader, err := readMultipartFile(request, "content", maxPluginContentZIP, false)
	if err != nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", err.Error()))
		return
	}
	if contentHeader != nil && !strings.EqualFold(strings.TrimSpace(fileExtension(contentHeader.Filename)), ".zip") {
		writeRequestError(writer, apiError("INVALID_REQUEST", "内容包必须是 ZIP 文件"))
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(request.FormValue("content_type")))
	// 兼容前端约定：内容包 zip 可用 `content` 字段；`config` 字段 + content_type 也按内容包处理。
	if contentHeader == nil && contentType != "" && configHeader != nil {
		content, contentHeader = config, configHeader
		config, configHeader = nil, nil
	}
	if contentHeader != nil && configHeader != nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", "config 与 content 内容包不能同时上传"))
		return
	}
	if contentType != "" && contentHeader == nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", "content_type 需要上传内容包文件（content 字段）"))
		return
	}
	pluginType := strings.ToLower(strings.TrimSpace(request.FormValue("plugin_type")))
	if !panelplugins.ValidPluginType(pluginType) {
		writeRequestError(writer, apiError("INVALID_REQUEST", "plugin_type must be spigot, paper, velocity, bungee, fabric, forge or neoforge"))
		return
	}
	autoInstall, _ := strconv.ParseBool(request.FormValue("auto_install"))
	session := currentSession(request)
	jarFilename := ""
	if jarHeader != nil {
		jarFilename = jarHeader.Filename
	}
	name := strings.TrimSpace(request.FormValue("name"))
	if name == "" {
		name = strings.TrimSpace(request.FormValue("content_name"))
	}
	version := strings.TrimSpace(request.FormValue("version"))
	if version == "" {
		version = strings.TrimSpace(request.FormValue("content_version"))
	}
	result, err := s.plugins.Upload(panelplugins.UploadInput{
		PluginType: pluginType, AutoInstall: autoInstall,
		JARFilename: jarFilename, JAR: jar, ConfigZIP: config,
		ConfigDirectory: request.FormValue("config_directory"),
		ContentZIP: content, ContentType: contentType,
		ContentName: name, ContentVersion: version,
		Uploader: panelplugins.Uploader{
			UserID: session.User.ID, Username: session.User.Username,
			DisplayName: session.User.DisplayName,
		},
	})
	if err == nil {
		var catalog []panelplugins.Plugin
		catalog, err = s.plugins.List()
		if err == nil {
			err = syncPluginCatalogAll(request.Context(), s.store, catalog)
		}
	}
	s.record(request, "plugin.upload", result.Plugin.PluginID, map[string]any{
		"plugin_type": pluginType, "auto_install": autoInstall,
		"filename": jarFilename, "version": result.Artifact.Version,
		"artifact_id": result.Artifact.ArtifactID, "duplicate": result.Duplicate,
		"content_type": result.Artifact.ContentType(),
	}, err)
	if err != nil {
		writeRequestError(writer, apiError("INVALID_PLUGIN", "插件上传失败: "+err.Error()))
		return
	}
	writeSuccess(writer, result)
}

func (s *Server) handlePluginRescan(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	if err := s.authorize(request, "plugin.upload"); err != nil {
		writeRequestError(writer, err)
		return
	}
	report, err := s.plugins.Rescan()
	if err == nil {
		err = syncPluginCatalogAll(request.Context(), s.store, report.Plugins)
	}
	s.record(request, "plugin.rescan", "repository", map[string]any{
		"imported": report.Imported, "duplicates": report.Duplicates,
		"rebuilt_manifests": report.RebuiltManifests,
		"recovered_changes": report.RecoveredChanges, "warnings": report.Warnings,
	}, err)
	if err != nil {
		writeRequestError(writer, apiError("PLUGIN_SCAN_FAILED", "插件仓库扫描失败: "+err.Error()))
		return
	}
	writeSuccess(writer, report)
}

func readMultipartFile(request *http.Request, field string, maximum int64, required bool) ([]byte, *multipart.FileHeader, error) {
	file, header, err := request.FormFile(field)
	if err == http.ErrMissingFile && !required {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("缺少上传字段 %s", field)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, nil, fmt.Errorf("读取上传字段 %s 失败", field)
	}
	if len(contents) == 0 || int64(len(contents)) > maximum {
		return nil, nil, fmt.Errorf("上传字段 %s 的大小必须在 1 到 %d 字节之间", field, maximum)
	}
	return contents, header, nil
}

func fileExtension(name string) string {
	index := strings.LastIndex(name, ".")
	if index < 0 {
		return ""
	}
	return name[index:]
}

func syncPluginCatalogAll(ctx context.Context, repository *store.Store, catalog []panelplugins.Plugin) error {
	artifacts := make([]store.PluginArtifactIndex, 0)
	for _, plugin := range catalog {
		for _, artifact := range plugin.Artifacts {
			manifest, err := json.Marshal(artifact)
			if err != nil {
				return err
			}
			artifacts = append(artifacts, store.PluginArtifactIndex{
				PluginType: plugin.PluginType, PluginID: plugin.PluginID, ArtifactID: artifact.ArtifactID,
				PluginName: artifact.Name, Version: artifact.Version, MainClass: artifact.Main,
				JARSHA256: artifact.Artifact.SHA256, ConfigSHA256: artifact.Config.SHA256,
				Current:      artifact.ArtifactID == plugin.CurrentArtifactID,
				ManifestJSON: manifest, UploadedAt: artifact.UploadedAt,
			})
		}
	}
	return repository.ReplacePluginCatalog(ctx, artifacts)
}
