package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	panelplugins "PrismPanel/internal/plugins"
	"PrismPanel/internal/store"
)

func (s *Server) handlePluginArtifact(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/plugins/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 3 && parts[2] == "deploy" {
		s.handlePluginDeployment(writer, request, "spigot", parts[0], parts[1], bundleKindPlugin)
		return
	}
	if len(parts) == 2 {
		// 仓库条目级操作（删除整个插件/模组）。
		s.handlePluginEntry(writer, request, parts[0], parts[1])
		return
	}
	if len(parts) == 3 && parts[2] != "deploy" {
		// 制品版本级操作（删除一个制品版本）。
		s.handlePluginArtifactDelete(writer, request, parts[0], parts[1], parts[2])
		return
	}
	if len(parts) < 4 {
		http.NotFound(writer, request)
		return
	}
	pluginType, pluginID, artifactPart := parts[0], parts[1], parts[2]
	if parts[3] == "deploy" && len(parts) == 4 {
		s.handlePluginDeployment(writer, request, pluginType, pluginID, artifactPart, bundleKindPlugin)
		return
	}
	if parts[3] == "config" && len(parts) == 4 {
		s.handlePluginConfig(writer, request, pluginType, pluginID, artifactPart)
		return
	}
	if parts[3] == "config" && len(parts) == 5 && parts[4] == "deploy" {
		s.handlePluginDeployment(writer, request, pluginType, pluginID, artifactPart, bundleKindConfig)
		return
	}
	if parts[3] == "content" && len(parts) == 4 {
		s.handlePluginContent(writer, request, pluginType, pluginID, artifactPart)
		return
	}
	if parts[3] == "current" && len(parts) == 4 {
		s.handlePluginCurrent(writer, request, pluginType, pluginID, artifactPart)
		return
	}
	if parts[3] == "content" && len(parts) == 5 && parts[4] == "deploy" {
		// kind 可来自 query `kind`（panel 早前约定）或 body `content_type`（前端已实现）；
		// 两者都不给时由 handlePluginDeployment 按 body 解析并报错。
		switch strings.ToLower(strings.TrimSpace(request.URL.Query().Get("kind"))) {
		case panelplugins.ContentTypeConfig:
			s.handlePluginDeployment(writer, request, pluginType, pluginID, artifactPart, bundleKindContentConfig)
		case panelplugins.ContentTypeFull:
			s.handlePluginDeployment(writer, request, pluginType, pluginID, artifactPart, bundleKindContentFull)
		case "":
			s.handlePluginDeployment(writer, request, pluginType, pluginID, artifactPart, bundleKindContentAuto)
		default:
			writeRequestError(writer, apiError("INVALID_REQUEST", "content deploy requires kind=config or kind=full"))
			return
		}
		return
	}
	if parts[3] == "content" && len(parts) == 5 && parts[4] != "deploy" {
		// 内容包版本级操作（删除一个内容包版本）。
		s.handlePluginContentDelete(writer, request, pluginType, pluginID, artifactPart, parts[4])
		return
	}
	if parts[3] == "icon" && len(parts) == 4 {
		s.handlePluginIcon(writer, request, pluginType, pluginID, artifactPart)
		return
	}
	http.NotFound(writer, request)
}

// bundleKind 标识部署的 bundle 类型：插件 jar / 旧配置 bundle / 内容包（单独配置或完全配置）。
// bundleKindContentAuto 表示内容包类型由请求 body 的 content_type 字段决定。
const (
	bundleKindPlugin        = iota
	bundleKindConfig
	bundleKindContentConfig
	bundleKindContentFull
	bundleKindContentAuto
)

// confirmDeployedDelete 检查被部署记录引用的删除：存在部署偏好（规则）且未显式确认时拒绝。
// 已部署目标记录不受删除影响（发布不可变），前端二次确认后带 confirm_deployed=true 重试。
func (s *Server) confirmDeployedDelete(request *http.Request, pluginType, pluginID string) error {
	if strings.EqualFold(strings.TrimSpace(request.URL.Query().Get("confirm_deployed")), "true") {
		return nil
	}
	rules, err := s.store.PluginDeployPreferences(request.Context(), pluginType, pluginID)
	if err != nil {
		return err
	}
	if len(rules) > 0 {
		return apiError("PLUGIN_DEPLOYED_CONFIRM_REQUIRED",
			"该插件存在部署记录，删除需二次确认（confirm_deployed=true）；已部署目标不受影响")
	}
	return nil
}

// handlePluginEntry 仓库条目级操作：DELETE 删除整个插件/模组条目。
func (s *Server) handlePluginEntry(writer http.ResponseWriter, request *http.Request, pluginType, pluginID string) {
	if request.Method != http.MethodDelete {
		methodNotAllowed(writer, "DELETE")
		return
	}
	if err := s.authorize(request, "plugin.remove"); err != nil {
		writeRequestError(writer, err)
		return
	}
	if !panelplugins.ValidPluginType(pluginType) {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid plugin type"))
		return
	}
	err := s.confirmDeployedDelete(request, pluginType, pluginID)
	if err == nil {
		err = s.plugins.DeletePlugin(pluginID, pluginType)
	}
	if err == nil {
		var catalog []panelplugins.Plugin
		catalog, err = s.plugins.List()
		if err == nil {
			err = syncPluginCatalogAll(request.Context(), s.store, catalog)
		}
	}
	s.record(request, "plugin.remove", pluginType+"/"+pluginID, map[string]any{
		"entry": true, "confirm_deployed": request.URL.Query().Get("confirm_deployed"),
	}, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"removed": pluginType + "/" + pluginID})
}

// handlePluginArtifactDelete 制品版本级操作：DELETE 删除一个制品版本（jar + 其内容包与配置）。
func (s *Server) handlePluginArtifactDelete(writer http.ResponseWriter, request *http.Request, pluginType, pluginID, artifactPart string) {
	if request.Method != http.MethodDelete {
		methodNotAllowed(writer, "DELETE")
		return
	}
	if err := s.authorize(request, "plugin.remove"); err != nil {
		writeRequestError(writer, err)
		return
	}
	artifactID, err := strconv.ParseInt(artifactPart, 10, 64)
	if err != nil || artifactID < 1 || !panelplugins.ValidPluginType(pluginType) {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid plugin artifact"))
		return
	}
	err = s.confirmDeployedDelete(request, pluginType, pluginID)
	var plugin panelplugins.Plugin
	if err == nil {
		plugin, err = s.plugins.DeleteArtifact(pluginID, artifactID, pluginType)
	}
	if err == nil {
		var catalog []panelplugins.Plugin
		catalog, err = s.plugins.List()
		if err == nil {
			err = syncPluginCatalogAll(request.Context(), s.store, catalog)
		}
	}
	s.record(request, "plugin.remove", pluginType+"/"+pluginID, map[string]any{
		"artifact_id": artifactID, "confirm_deployed": request.URL.Query().Get("confirm_deployed"),
	}, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	if plugin.PluginID == "" {
		writeSuccess(writer, map[string]any{"removed_artifact": artifactID, "removed_plugin": true})
		return
	}
	writeSuccess(writer, map[string]any{"removed_artifact": artifactID, "plugin": plugin})
}

// handlePluginContent 内容包列表/新增/编辑/整包删除：
// GET 列出制品下全部内容包版本；POST/PUT 新增一个内容包版本（编辑 = 重传 zip = 新增版本并标记 current）；
// DELETE 删除制品下全部内容包版本（「仅删除内容包」）；单个版本删除走 .../content/{contentID}。
func (s *Server) handlePluginContent(writer http.ResponseWriter, request *http.Request, pluginType, pluginID, artifactPart string) {
	artifactID, err := strconv.ParseInt(artifactPart, 10, 64)
	if err != nil || artifactID < 1 || !panelplugins.ValidPluginType(pluginType) {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid plugin artifact"))
		return
	}
	switch request.Method {
	case http.MethodGet:
		if err := s.authorize(request, "plugin.view"); err != nil {
			writeRequestError(writer, err)
			return
		}
		versions, err := s.plugins.ListContent(pluginID, artifactID, pluginType)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"items": versions})
	case http.MethodPost, http.MethodPut:
		if err := s.authorize(request, "plugin.upload"); err != nil {
			writeRequestError(writer, err)
			return
		}
		s.handleContentAdd(writer, request, pluginType, pluginID, artifactID)
	case http.MethodDelete:
		if err := s.authorize(request, "plugin.remove"); err != nil {
			writeRequestError(writer, err)
			return
		}
		s.handleContentDeleteAll(writer, request, pluginType, pluginID, artifactID)
	default:
		methodNotAllowed(writer, "GET, POST, PUT, DELETE")
	}
}

// handlePluginCurrent 切换 current 制品（回滚到指定制品版本）。
func (s *Server) handlePluginCurrent(writer http.ResponseWriter, request *http.Request, pluginType, pluginID, artifactPart string) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	if err := s.authorize(request, "plugin.upload"); err != nil {
		writeRequestError(writer, err)
		return
	}
	artifactID, err := strconv.ParseInt(artifactPart, 10, 64)
	if err != nil || artifactID < 1 || !panelplugins.ValidPluginType(pluginType) {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid plugin artifact"))
		return
	}
	plugin, err := s.plugins.SetCurrentArtifact(pluginID, artifactID, pluginType)
	if err == nil {
		var catalog []panelplugins.Plugin
		catalog, err = s.plugins.List()
		if err == nil {
			err = syncPluginCatalogAll(request.Context(), s.store, catalog)
		}
	}
	s.record(request, "plugin.current", pluginType+"/"+pluginID, map[string]any{
		"artifact_id": artifactID,
	}, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"plugin": plugin})
}

// handleContentDeleteAll 删除制品下的全部内容包版本（「仅删除内容包」）。
func (s *Server) handleContentDeleteAll(writer http.ResponseWriter, request *http.Request, pluginType, pluginID string, artifactID int64) {
	err := s.confirmDeployedDelete(request, pluginType, pluginID)
	var manifest panelplugins.Manifest
	if err == nil {
		manifest, err = s.plugins.DeleteAllContent(pluginID, artifactID, pluginType)
	}
	if err == nil {
		var catalog []panelplugins.Plugin
		catalog, err = s.plugins.List()
		if err == nil {
			err = syncPluginCatalogAll(request.Context(), s.store, catalog)
		}
	}
	s.record(request, "plugin.remove", pluginType+"/"+pluginID, map[string]any{
		"artifact_id": artifactID, "content": "all",
		"confirm_deployed": request.URL.Query().Get("confirm_deployed"),
	}, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"content": manifest.Content})
}

func (s *Server) handleContentAdd(writer http.ResponseWriter, request *http.Request, pluginType, pluginID string, artifactID int64) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxPluginUploadBody)
	if err := request.ParseMultipartForm(32 * 1024 * 1024); err != nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", "内容包表单无效或文件超过限制"))
		return
	}
	if request.MultipartForm != nil {
		defer request.MultipartForm.RemoveAll()
	}
	zipBytes, zipHeader, err := readMultipartFile(request, "content", maxPluginContentZIP, true)
	if err != nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", err.Error()))
		return
	}
	if !strings.EqualFold(strings.TrimSpace(fileExtension(zipHeader.Filename)), ".zip") {
		writeRequestError(writer, apiError("INVALID_REQUEST", "内容包必须是 ZIP 文件"))
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(request.FormValue("content_type")))
	if contentType != panelplugins.ContentTypeConfig && contentType != panelplugins.ContentTypeFull {
		writeRequestError(writer, apiError("INVALID_REQUEST", "content_type must be config or full"))
		return
	}
	manifest, err := s.plugins.AddContent(pluginID, artifactID, contentType, zipBytes, pluginType)
	if err == nil {
		var catalog []panelplugins.Plugin
		catalog, err = s.plugins.List()
		if err == nil {
			err = syncPluginCatalogAll(request.Context(), s.store, catalog)
		}
	}
	s.record(request, "plugin.content.add", pluginType+"/"+pluginID, map[string]any{
		"artifact_id": artifactID, "content_type": contentType,
		"content_id": manifest.ContentID(),
	}, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"artifact": manifest})
}

// handlePluginContentDelete 内容包版本级操作：DELETE 删除一个内容包版本。
func (s *Server) handlePluginContentDelete(writer http.ResponseWriter, request *http.Request, pluginType, pluginID, artifactPart, contentPart string) {
	if request.Method != http.MethodDelete {
		methodNotAllowed(writer, "DELETE")
		return
	}
	if err := s.authorize(request, "plugin.remove"); err != nil {
		writeRequestError(writer, err)
		return
	}
	artifactID, err := strconv.ParseInt(artifactPart, 10, 64)
	if err != nil || artifactID < 1 || !panelplugins.ValidPluginType(pluginType) {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid plugin artifact"))
		return
	}
	contentID, err := strconv.ParseInt(contentPart, 10, 64)
	if err != nil || contentID < 1 {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid content id"))
		return
	}
	err = s.confirmDeployedDelete(request, pluginType, pluginID)
	var manifest panelplugins.Manifest
	if err == nil {
		manifest, err = s.plugins.DeleteContent(pluginID, artifactID, contentID, pluginType)
	}
	if err == nil {
		var catalog []panelplugins.Plugin
		catalog, err = s.plugins.List()
		if err == nil {
			err = syncPluginCatalogAll(request.Context(), s.store, catalog)
		}
	}
	s.record(request, "plugin.remove", pluginType+"/"+pluginID, map[string]any{
		"artifact_id": artifactID, "content_id": contentID,
		"confirm_deployed": request.URL.Query().Get("confirm_deployed"),
	}, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"content": manifest.Content})
}

// handlePluginIcon 返回制品 jar 内的 mod 图标（fabric.mod.json 的 icon 字段）。
func (s *Server) handlePluginIcon(writer http.ResponseWriter, request *http.Request, pluginType, pluginID, artifactPart string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	if err := s.authorize(request, "plugin.view"); err != nil {
		writeRequestError(writer, err)
		return
	}
	artifactID, err := strconv.ParseInt(artifactPart, 10, 64)
	if err != nil || artifactID < 1 || !panelplugins.ValidPluginType(pluginType) {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid plugin artifact"))
		return
	}
	contents, contentType, err := s.plugins.Icon(pluginID, artifactID, pluginType)
	if err != nil {
		writeError(writer, err)
		return
	}
	if len(contents) == 0 {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "private, max-age=3600")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = writer.Write(contents)
}

func (s *Server) handlePluginDeployment(writer http.ResponseWriter, request *http.Request, pluginType, pluginID, artifactPart string, bundleKind int) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	if err := s.authorize(request, "plugin.deploy"); err != nil {
		writeRequestError(writer, err)
		return
	}
	artifactID, err := strconv.ParseInt(artifactPart, 10, 64)
	if err != nil || artifactID < 1 {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid plugin artifact id"))
		return
	}
	var input struct {
		ServerID       string             `json:"server_id"`
		Targets        []deploymentTarget `json:"targets"`
		Rules          []store.TargetRule `json:"rules"`
		BackupSnapshot bool               `json:"backup_snapshot"`
		ContentType    string             `json:"content_type,omitempty"`
	}
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	// 内容包类型双通道：query `kind` 优先，缺省时用 body content_type。
	if bundleKind == bundleKindContentAuto {
		switch strings.ToLower(strings.TrimSpace(input.ContentType)) {
		case panelplugins.ContentTypeConfig:
			bundleKind = bundleKindContentConfig
		case panelplugins.ContentTypeFull:
			bundleKind = bundleKindContentFull
		default:
			err = apiError("INVALID_REQUEST", "content deploy requires content_type=config or content_type=full")
		}
	}
	nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	if err == nil && input.Rules != nil {
		err = s.store.ReplacePluginDeployPreferences(
			request.Context(), pluginType, pluginID, input.Rules,
		)
		if err == nil {
			var catalog []catalogNode
			catalog, err = s.loadCatalog(request.Context())
			if err == nil {
				input.Targets = resolveSelectedServers(catalog, input.Rules, pluginType)
			}
		}
	}
	if len(input.Targets) == 0 && nodeID != "" && strings.TrimSpace(input.ServerID) != "" {
		input.Targets = append(input.Targets, deploymentTarget{NodeID: nodeID, ServerID: input.ServerID})
	}
	if err == nil && len(input.Targets) == 0 {
		err = apiError("INVALID_REQUEST", "node_id and server_id are required")
	}
	type deploymentResult struct {
		NodeID   string          `json:"node_id"`
		ServerID string          `json:"server_id"`
		Data     json.RawMessage `json:"data,omitempty"`
		Error    string          `json:"error,omitempty"`
	}
	results := make([]deploymentResult, 0, len(input.Targets))
	if err == nil {
		var bundlePath string
		switch bundleKind {
		case bundleKindConfig:
			bundlePath, _, err = s.plugins.BuildConfigBundle(pluginID, artifactID, pluginType)
		case bundleKindContentConfig, bundleKindContentFull:
			kind := panelplugins.ContentTypeConfig
			if bundleKind == bundleKindContentFull {
				kind = panelplugins.ContentTypeFull
			}
			bundlePath, _, err = s.plugins.BuildContentBundle(pluginID, artifactID, kind, pluginType)
		default:
			bundlePath, _, err = s.plugins.BuildBundle(pluginID, artifactID, pluginType)
		}
		if bundlePath != "" {
			defer os.Remove(bundlePath)
		}
		if err == nil {
			for _, target := range input.Targets {
				item := deploymentResult{
					NodeID:   strings.TrimSpace(target.NodeID),
					ServerID: strings.TrimSpace(target.ServerID),
				}
				if item.NodeID == "" || item.ServerID == "" {
					item.Error = "node_id and server_id are required"
				} else {
					item.Error = ""
					var deployErr error
					switch bundleKind {
					case bundleKindConfig:
						deployErr = s.deployPluginConfigBundle(request, item.NodeID, item.ServerID, bundlePath, &item.Data)
					case bundleKindContentConfig, bundleKindContentFull:
						deployErr = s.deployPluginContentBundle(request, item.NodeID, item.ServerID, bundlePath, input.BackupSnapshot, &item.Data)
					default:
						deployErr = s.deployPluginBundle(request, item.NodeID, item.ServerID, bundlePath, &item.Data)
					}
					if deployErr != nil {
						item.Error = deployErr.Error()
					}
				}
				results = append(results, item)
			}
		}
	}
	action := "plugin.deploy"
	switch bundleKind {
	case bundleKindConfig:
		action = "plugin.config.deploy"
	case bundleKindContentConfig, bundleKindContentFull:
		action = "plugin.content.deploy"
	}
	s.record(request, action, pluginType+"/"+pluginID, map[string]any{
		"artifact_id": artifactID, "targets": input.Targets, "results": results,
		"backup_snapshot": input.BackupSnapshot,
	}, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"targets": results})
}

func (s *Server) handlePluginConfig(writer http.ResponseWriter, request *http.Request, pluginType, pluginID, artifactPart string) {
	artifactID, err := strconv.ParseInt(artifactPart, 10, 64)
	if err != nil || artifactID < 1 || !panelplugins.ValidPluginType(pluginType) {
		writeRequestError(writer, apiError("INVALID_REQUEST", "invalid plugin artifact"))
		return
	}
	path := request.URL.Query().Get("path")
	switch request.Method {
	case http.MethodGet:
		if err := s.authorize(request, "plugin.view"); err != nil {
			writeRequestError(writer, err)
			return
		}
		if strings.TrimSpace(path) == "" {
			files, err := s.plugins.ListConfig(pluginID, artifactID, pluginType)
			if err != nil {
				writeError(writer, err)
				return
			}
			writeSuccess(writer, map[string]any{"files": files})
			return
		}
		contents, err := s.plugins.ReadConfig(pluginID, artifactID, path, pluginType)
		if err != nil {
			writeError(writer, err)
			return
		}
		if !utf8.Valid(contents) {
			writeRequestError(writer, apiError("UNSUPPORTED_ENCODING", "配置文件不是 UTF-8 文本"))
			return
		}
		writeSuccess(writer, map[string]any{"path": path, "content": string(contents)})
	case http.MethodPut:
		if err := s.authorize(request, "plugin.upload"); err != nil {
			writeRequestError(writer, err)
			return
		}
		var input struct {
			Content string `json:"content"`
		}
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &input)
		}
		if err == nil {
			if strings.TrimSpace(path) == "" {
				err = apiError("INVALID_REQUEST", "config file path is required")
			} else {
				_, err = s.plugins.UpdateConfig(pluginID, artifactID, path, []byte(input.Content), pluginType)
			}
		}
		if err == nil {
			var catalog []panelplugins.Plugin
			catalog, err = s.plugins.List()
			if err == nil {
				err = syncPluginCatalogAll(request.Context(), s.store, catalog)
			}
		}
		s.record(request, "plugin.config.update", pluginType+"/"+pluginID, map[string]any{
			"artifact_id": artifactID, "path": path,
		}, err)
		if err != nil {
			writeError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"path": path})
	default:
		methodNotAllowed(writer, "GET, PUT")
	}
}
