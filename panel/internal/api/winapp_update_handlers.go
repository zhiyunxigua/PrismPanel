package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"PrismPanel/internal/winappupdates"
)

const maxWinAppReleaseUpload = int64(300 * 1024 * 1024)

type winAppReleaseView struct {
	Version     string                 `json:"version"`
	Platform    string                 `json:"platform"`
	Arch        string                 `json:"arch"`
	Size        int64                  `json:"size"`
	SHA256      string                 `json:"sha256"`
	BuiltAt     time.Time              `json:"built_at"`
	Notes       string                 `json:"notes"`
	PublishedBy winappupdates.Uploader `json:"published_by"`
	PublishedAt time.Time              `json:"published_at"`
	DownloadURL string                 `json:"download_url"`
}

func (s *Server) handleWinAppUpdate(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	currentVersion := strings.TrimSpace(request.URL.Query().Get("version"))
	platform := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("platform")))
	arch := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("arch")))
	if currentVersion == "" || len(currentVersion) > 64 {
		writeRequestError(writer, apiError("INVALID_REQUEST", "缺少当前 WinApp 版本号"))
		return
	}
	if platform == "" {
		platform = "windows"
	}
	if arch == "" {
		arch = "amd64"
	}
	latest, err := s.winApp.Latest()
	if errors.Is(err, winappupdates.ErrNoRelease) {
		writeSuccess(writer, map[string]any{
			"current_version": currentVersion, "update_available": false,
		})
		return
	}
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	available := latest.Platform == platform && latest.Arch == arch &&
		winappupdates.CompareVersions(latest.Version, currentVersion) > 0
	writeSuccess(writer, map[string]any{
		"current_version":  currentVersion,
		"update_available": available,
		"latest":           releaseView(latest),
	})
}

func (s *Server) handleWinAppReleases(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		releases, err := s.winApp.List()
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		items := make([]winAppReleaseView, 0, len(releases))
		for _, release := range releases {
			items = append(items, releaseView(release))
		}
		writeSuccess(writer, map[string]any{"items": items})
	case http.MethodPost:
		s.publishWinAppRelease(writer, request)
	default:
		methodNotAllowed(writer, "GET, POST")
	}
}

func (s *Server) publishWinAppRelease(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxWinAppReleaseUpload+1024*1024)
	if err := request.ParseMultipartForm(32 * 1024 * 1024); err != nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", "WinApp 发布包上传表单无效或文件超过限制"))
		return
	}
	if request.MultipartForm != nil {
		defer request.MultipartForm.RemoveAll()
	}
	bundle, header, err := request.FormFile("bundle")
	if err != nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", "请选择 WinApp 发布 ZIP"))
		return
	}
	defer bundle.Close()
	if !strings.EqualFold(strings.TrimSpace(fileExtension(header.Filename)), ".zip") {
		writeRequestError(writer, apiError("INVALID_REQUEST", "WinApp 发布包必须是 ZIP 文件"))
		return
	}
	session := currentSession(request)
	release, publishErr := s.winApp.Publish(bundle, header.Size, request.FormValue("notes"), winappupdates.Uploader{
		UserID: session.User.ID, Username: session.User.Username, DisplayName: session.User.DisplayName,
	})
	s.record(request, "winapp.release.publish", release.Version, map[string]any{
		"filename": header.Filename, "version": release.Version,
		"sha256": release.SHA256, "size": release.Size,
	}, publishErr)
	if publishErr != nil {
		writeRequestError(writer, apiError("INVALID_WINAPP_RELEASE", "WinApp 版本发布失败: "+publishErr.Error()))
		return
	}
	writeSuccess(writer, releaseView(release))
}

func (s *Server) handleWinAppReleaseDownload(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, "GET, HEAD")
		return
	}
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/winapp/releases/"), "/")
	version, suffix, found := strings.Cut(path, "/")
	if !found || suffix != "download" {
		http.NotFound(writer, request)
		return
	}
	release, artifactPath, err := s.winApp.Artifact(version)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.NotFound(writer, request)
			return
		}
		writeRequestError(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/vnd.microsoft.portable-executable")
	writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="PrismPanel-%s.exe"`, release.Version))
	writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	writer.Header().Set("ETag", `"`+release.SHA256+`"`)
	http.ServeFile(writer, request, artifactPath)
}

func releaseView(release winappupdates.Release) winAppReleaseView {
	return winAppReleaseView{
		Version: release.Version, Platform: release.Platform, Arch: release.Arch,
		Size: release.Size, SHA256: release.SHA256, BuiltAt: release.BuiltAt,
		Notes: release.Notes, PublishedBy: release.PublishedBy, PublishedAt: release.PublishedAt,
		DownloadURL: "/api/v1/winapp/releases/" + release.Version + "/download",
	}
}
