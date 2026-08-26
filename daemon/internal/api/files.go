package api

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"PrismPanel-daemon/internal/apperr"
	fileservice "PrismPanel-daemon/internal/files"
	"PrismPanel-daemon/internal/protocol"
	"PrismPanel-daemon/internal/ticket"
)

const (
	resourceTypeHeader = "X-Prism-Resource-Type"
	resourceIDHeader   = "X-Prism-Resource-ID"
	filePathHeader     = "X-Prism-Path"
	uploadOffsetHeader = "X-Prism-Upload-Offset"
	uploadFinalHeader  = "X-Prism-Upload-Final"
)

func (s *Server) handleFiles(writer http.ResponseWriter, request *http.Request) {
	setFileCORS(writer, request)
	if request.Method == http.MethodOptions {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	operation := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/files/"), "/")
	switch operation {
	case "list":
		s.handleFileList(writer, request)
	case "content":
		s.handleFileContent(writer, request)
	case "upload":
		s.handleFileUpload(writer, request)
	case "import":
		s.handleFileImport(writer, request)
	case "download":
		s.handleFileDownload(writer, request)
	case "create":
		s.handleFileCreate(writer, request)
	case "move":
		s.handleFileMove(writer, request)
	case "copy":
		s.handleFileCopy(writer, request)
	case "archive":
		s.handleFileArchive(writer, request)
	case "delete":
		s.handleFileDelete(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (s *Server) handleFileImport(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeFileMethodError(writer, "POST")
		return
	}
	target, relative := fileTarget(request)
	created, err := s.consumeFileTicket(request, "file.import", target, relative)
	if err != nil {
		writeFileError(writer, err)
		return
	}
	if request.ContentLength > created.MaxBytes {
		err = apperr.New("FILE_TOO_LARGE", "压缩包请求超过票据限制")
	} else {
		var result fileservice.ImportResult
		result, err = s.files.ImportArchive(target, relative, request.Body, created.MaxBytes, created.SHA256)
		if err == nil {
			s.publishFileResult(created, target, []string{"."}, nil)
			writeFileSuccess(writer, result)
			return
		}
	}
	s.publishFileResult(created, target, []string{"."}, err)
	writeFileError(writer, err)
}

func (s *Server) handleFileList(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeFileMethodError(writer, "POST")
		return
	}
	var input struct {
		Directories   []fileservice.DirectoryRequest `json:"directories"`
		IncludeHidden *bool                          `json:"include_hidden"`
	}
	if err := decodeFileJSON(request, &input, 1024*1024); err != nil {
		writeFileError(writer, err)
		return
	}
	target, relative := fileTarget(request)
	created, err := s.consumeFileTicket(request, "file.list", target, relative)
	if err != nil {
		writeFileError(writer, err)
		return
	}
	for _, directory := range input.Directories {
		if !created.AllowsPath(directory.Path) {
			writeFileError(writer, apperr.New("PERMISSION_DENIED", "临时凭证不允许请求的目录"))
			return
		}
	}
	includeHidden := true
	if input.IncludeHidden != nil {
		includeHidden = *input.IncludeHidden
	}
	results, err := s.files.List(target, input.Directories, includeHidden)
	if err != nil {
		writeFileError(writer, err)
		return
	}
	writeFileSuccess(writer, map[string]any{"results": results})
}

func (s *Server) handleFileContent(writer http.ResponseWriter, request *http.Request) {
	target, relative := fileTarget(request)
	switch request.Method {
	case http.MethodGet:
		if _, err := s.consumeFileTicket(request, "file.read", target, relative); err != nil {
			writeFileError(writer, err)
			return
		}
		content, err := s.files.Read(target, relative)
		if err != nil {
			writeFileError(writer, err)
			return
		}
		writeFileSuccess(writer, content)
	case http.MethodPut:
		created, err := s.consumeFileTicket(request, "file.edit", target, relative)
		if err != nil {
			writeFileError(writer, err)
			return
		}
		var input struct {
			Content         string `json:"content"`
			Encoding        string `json:"encoding"`
			ExpectedVersion string `json:"expected_version"`
		}
		if err = decodeFileJSON(request, &input, s.config.Files.MaxEditFileSize+1024*1024); err == nil {
			var content fileservice.Content
			content, err = s.files.Save(target, relative, input.Content, input.Encoding, input.ExpectedVersion)
			if err == nil {
				s.publishFileResult(created, target, []string{relative}, nil)
				writeFileSuccess(writer, content)
				return
			}
		}
		s.publishFileResult(created, target, []string{relative}, err)
		writeFileError(writer, err)
	default:
		writeFileMethodError(writer, "GET, PUT")
	}
}

func (s *Server) handleFileUpload(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeFileMethodError(writer, "POST")
		return
	}
	target, relative := fileTarget(request)
	created, err := s.consumeFileTicket(request, "file.upload", target, relative)
	if err != nil {
		writeFileError(writer, err)
		return
	}
	overwrite := strings.EqualFold(request.Header.Get("X-Prism-Overwrite"), "true")
	if overwrite && !created.AllowOverwrite {
		err = apperr.New("PERMISSION_DENIED", "upload ticket does not allow overwrite")
		s.publishFileResult(created, target, []string{relative}, err)
		writeFileError(writer, err)
		return
	}
	if rawOffset := strings.TrimSpace(request.Header.Get(uploadOffsetHeader)); rawOffset != "" {
		offset, offsetErr := strconv.ParseInt(rawOffset, 10, 64)
		final, finalErr := strconv.ParseBool(strings.TrimSpace(request.Header.Get(uploadFinalHeader)))
		if offsetErr != nil || finalErr != nil || request.ContentLength < 0 || request.ContentLength > fileservice.UploadChunkSize || request.ContentLength > created.MaxBytes {
			err = apperr.New("INVALID_REQUEST", "invalid chunked upload request")
		} else {
			var entry fileservice.Entry
			var nextOffset int64
			entry, nextOffset, err = s.files.UploadChunk(target, relative, request.Body, created.MaxBytes, created.SHA256, overwrite, created.ExpectedVersion, created.ID, offset, final)
			if err == nil {
				if final {
					s.publishFileResult(created, target, []string{relative}, nil)
					writeFileSuccess(writer, entry)
				} else {
					writeFileSuccess(writer, map[string]any{"offset": nextOffset, "complete": false})
				}
				return
			}
		}
		if final {
			s.publishFileResult(created, target, []string{relative}, err)
		}
		writeFileError(writer, err)
		return
	}
	if overwrite && !created.AllowOverwrite {
		err = apperr.New("PERMISSION_DENIED", "临时凭证不允许覆盖现有文件")
	} else if request.ContentLength > created.MaxBytes {
		err = apperr.New("FILE_TOO_LARGE", "上传请求超过票据限制")
	} else {
		entry := fileservice.Entry{}
		entry, err = s.files.Upload(target, relative, request.Body, created.MaxBytes, created.SHA256, overwrite, created.ExpectedVersion)
		if err == nil {
			s.publishFileResult(created, target, []string{relative}, nil)
			writeFileSuccess(writer, entry)
			return
		}
	}
	s.publishFileResult(created, target, []string{relative}, err)
	writeFileError(writer, err)
}

func (s *Server) handleFileDownload(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeFileMethodError(writer, "GET")
		return
	}
	target, relative := fileTarget(request)
	if _, err := s.consumeFileTicket(request, "file.download", target, relative); err != nil {
		writeFileError(writer, err)
		return
	}
	download, err := s.files.OpenDownload(target, relative)
	if err != nil {
		writeFileError(writer, err)
		return
	}
	defer download.Close()
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": download.Name})
	writer.Header().Set("Content-Disposition", disposition)
	writer.Header().Set("Content-Length", strconv.FormatInt(download.Size, 10))
	if download.ETag != "" {
		writer.Header().Set("ETag", download.ETag)
	}
	http.ServeContent(writer, request, download.Name, download.Mode, download.File)
}

func (s *Server) handleFileCreate(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeFileMethodError(writer, "POST")
		return
	}
	target, relative := fileTarget(request)
	created, err := s.consumeFileTicket(request, "file.create", target, relative)
	if err != nil {
		writeFileError(writer, err)
		return
	}
	var input struct {
		Type string `json:"type"`
	}
	if err = decodeFileJSON(request, &input, 64*1024); err == nil {
		err = s.files.Create(target, relative, input.Type)
	}
	s.publishFileResult(created, target, []string{relative}, err)
	if err != nil {
		writeFileError(writer, err)
		return
	}
	writeFileSuccess(writer, map[string]any{})
}

func (s *Server) handleFileMove(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeFileMethodError(writer, "POST")
		return
	}
	target, source := fileTarget(request)
	var input struct {
		Destination string `json:"destination"`
		Overwrite   bool   `json:"overwrite"`
	}
	if err := decodeFileJSON(request, &input, 64*1024); err != nil {
		writeFileError(writer, err)
		return
	}
	created, err := s.consumeFileTicket(request, "file.move", target, source)
	if err == nil && !created.AllowsPath(input.Destination) {
		err = apperr.New("PERMISSION_DENIED", "临时凭证不允许移动目标路径")
	}
	if err == nil {
		err = s.files.Move(target, source, input.Destination, input.Overwrite)
	}
	s.publishFileResult(created, target, []string{source, input.Destination}, err)
	if err != nil {
		writeFileError(writer, err)
		return
	}
	writeFileSuccess(writer, map[string]any{})
}

func (s *Server) handleFileCopy(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeFileMethodError(writer, "POST")
		return
	}
	target, source := fileTarget(request)
	var input struct {
		Destination string `json:"destination"`
	}
	if err := decodeFileJSON(request, &input, 64*1024); err != nil {
		writeFileError(writer, err)
		return
	}
	created, err := s.consumeFileTicket(request, "file.copy", target, source)
	if err == nil && !created.AllowsPath(input.Destination) {
		err = apperr.New("PERMISSION_DENIED", "临时凭证不允许复制目标路径")
	}
	var result fileservice.Entry
	if err == nil {
		result, err = s.files.Copy(target, source, input.Destination)
	}
	s.publishFileResult(created, target, []string{source, input.Destination}, err)
	if err != nil {
		writeFileError(writer, err)
		return
	}
	writeFileSuccess(writer, result)
}

func (s *Server) handleFileArchive(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeFileMethodError(writer, "POST")
		return
	}
	target, source := fileTarget(request)
	var input struct {
		Destination string `json:"destination"`
	}
	if err := decodeFileJSON(request, &input, 64*1024); err != nil {
		writeFileError(writer, err)
		return
	}
	created, err := s.consumeFileTicket(request, "file.archive", target, source)
	if err == nil && !created.AllowsPath(input.Destination) {
		err = apperr.New("PERMISSION_DENIED", "临时凭证不允许写入压缩目标路径")
	}
	var result fileservice.Entry
	if err == nil {
		result, err = s.files.Archive(target, source, input.Destination)
	}
	s.publishFileResult(created, target, []string{source, input.Destination}, err)
	if err != nil {
		writeFileError(writer, err)
		return
	}
	writeFileSuccess(writer, result)
}

func (s *Server) handleFileDelete(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeFileMethodError(writer, "POST")
		return
	}
	var input struct {
		Paths     []string `json:"paths"`
		Recursive bool     `json:"recursive"`
	}
	if err := decodeFileJSON(request, &input, 256*1024); err != nil {
		writeFileError(writer, err)
		return
	}
	target, _ := fileTarget(request)
	first := ""
	if len(input.Paths) > 0 {
		first = input.Paths[0]
	}
	created, err := s.consumeFileTicket(request, "file.delete", target, first)
	if err == nil {
		for _, candidate := range input.Paths {
			if !created.AllowsPath(candidate) {
				err = apperr.New("PERMISSION_DENIED", "临时凭证不允许删除目标路径")
				break
			}
		}
	}
	if err == nil {
		if input.Recursive && !created.AllowRecursive {
			err = apperr.New("PERMISSION_DENIED", "临时凭证不允许递归删除目录")
		}
	}
	if err == nil {
		err = s.files.Delete(target, input.Paths, input.Recursive)
	}
	s.publishFileResult(created, target, input.Paths, err)
	if err != nil {
		writeFileError(writer, err)
		return
	}
	writeFileSuccess(writer, map[string]any{})
}

func (s *Server) consumeFileTicket(request *http.Request, scope string, target fileservice.Target, relative string) (ticket.Ticket, error) {
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if !strings.HasPrefix(authorization, "Bearer ") {
		return ticket.Ticket{}, apperr.New("UNAUTHENTICATED", "缺少文件临时凭证")
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	return s.tickets.ConsumeRestrictedFrom(token, scope, target.Type, target.ID, relative, request.Method, s.requestSourceIP(request))
}

// fileTarget 从请求中解析文件资源与相对路径。
// 路径优先读取 URL query 的 path 参数（百分号编码，支持中文等非 Latin-1 字符），
// 缺失时回退到 X-Prism-Path header（兼容旧客户端/旧面板，仅 ASCII 路径可用）。
func fileTarget(request *http.Request) (fileservice.Target, string) {
	relative := strings.TrimSpace(request.URL.Query().Get("path"))
	if relative == "" {
		relative = strings.TrimSpace(request.Header.Get(filePathHeader))
	}
	return fileservice.Target{
		Type: strings.TrimSpace(request.Header.Get(resourceTypeHeader)),
		ID:   strings.TrimSpace(request.Header.Get(resourceIDHeader)),
	}, relative
}

func (s *Server) publishFileResult(created ticket.Ticket, target fileservice.Target, paths []string, err error) {
	if created.ID == "" || created.OperationID == "" {
		return
	}
	s.hub.broadcast(protocol.Event("file.operation_result", map[string]any{
		"operation_id": created.OperationID, "ticket_id": created.ID, "scope": created.Scope,
		"resource_type": target.Type, "resource_id": target.ID, "paths": paths,
		"success": err == nil, "error": apperr.From(err),
	}))
}

func decodeFileJSON(request *http.Request, target any, maxBytes int64) error {
	defer request.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(request.Body, maxBytes+1))
	if err != nil {
		return apperr.Wrap("INVALID_REQUEST", "文件请求读取失败", err)
	}
	if int64(len(contents)) > maxBytes {
		return apperr.New("FILE_TOO_LARGE", "文件请求超过大小限制")
	}
	if err := json.Unmarshal(contents, target); err != nil {
		return apperr.Wrap("INVALID_REQUEST", "文件请求格式无效", err)
	}
	return nil
}

func writeFileSuccess(writer http.ResponseWriter, data any) {
	writeJSON(writer, http.StatusOK, map[string]any{"success": true, "data": data})
}

func writeFileError(writer http.ResponseWriter, err error) {
	if err == nil {
		err = apperr.New("INTERNAL", "文件操作失败")
	}
	apiError := apperr.From(err)
	status := http.StatusBadRequest
	switch apiError.Code {
	case "UNAUTHENTICATED", "TICKET_EXPIRED":
		status = http.StatusUnauthorized
	case "PERMISSION_DENIED", "PATH_ESCAPE":
		status = http.StatusForbidden
	case "FILE_NOT_FOUND", "INSTANCE_NOT_FOUND", "SERVER_NOT_FOUND":
		status = http.StatusNotFound
	case "FILE_EXISTS", "FILE_CHANGED", "INSTANCE_BUSY", "DIRECTORY_NOT_EMPTY":
		status = http.StatusConflict
	case "FILE_TOO_LARGE":
		status = http.StatusRequestEntityTooLarge
	case "TOO_MANY_REQUESTS":
		status = http.StatusTooManyRequests
	case "INTERNAL", "FILE_OPERATION_FAILED", "FILE_WRITE_FAILED":
		status = http.StatusInternalServerError
	}
	writeJSON(writer, status, map[string]any{"success": false, "error": apiError})
}

func writeFileMethodError(writer http.ResponseWriter, allow string) {
	writer.Header().Set("Allow", allow)
	writeJSON(writer, http.StatusMethodNotAllowed, map[string]any{"success": false,
		"error": apperr.New("METHOD_NOT_ALLOWED", "文件接口不支持当前请求方法")})
}

func setFileCORS(writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Origin") == "" {
		return
	}
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
	writer.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Prism-Resource-Type, X-Prism-Resource-ID, X-Prism-Path, X-Prism-Overwrite, X-Prism-Expected-Version, X-Prism-Upload-Offset, X-Prism-Upload-Final")
	writer.Header().Set("Access-Control-Expose-Headers", "Content-Disposition, Content-Length")
}
