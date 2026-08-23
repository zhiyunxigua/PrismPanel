package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"PrismPanel/internal/netgames"
	"PrismPanel/internal/store"
)

// mcServerInputJSON 是创建/更新国际服的请求体。
// address 支持 "host" / "host:port" / "[ipv6]:port"；也可用 host+port 分开展示。
type mcServerInputJSON struct {
	Name    string  `json:"name"`
	Address string  `json:"address"`
	Host    string  `json:"host"`
	Port    *uint16 `json:"port,omitempty"`
	Enabled *bool   `json:"enabled"`
	Note    string  `json:"note"`
}

func (s *Server) handleMCServers(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		servers, err := s.netGames.MCServers(request.Context())
		if err != nil {
			writeRequestError(writer, mcPublicError(err))
			return
		}
		writeSuccess(writer, map[string]any{"servers": servers})
	case http.MethodPost:
		if !currentSession(request).User.IsSuperAdmin() {
			writeRequestError(writer, apiError("FORBIDDEN", "仅超级管理员可以管理国际版服务器"))
			return
		}
		input, err := parseMCServerInput(request)
		var server store.MCServer
		if err == nil {
			server, err = s.netGames.CreateMCServer(request.Context(), input)
		}
		err = mcPublicError(err)
		s.record(request, "net_games.mc.create", strconv.FormatUint(server.ID, 10), input, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"server": server})
	default:
		methodNotAllowed(writer, "GET, POST")
	}
}

func (s *Server) handleMCServer(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/net-games/mc-servers/"), "/")
	if path == "" || strings.Contains(path, "/") {
		http.NotFound(writer, request)
		return
	}
	id, err := strconv.ParseUint(path, 10, 64)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	switch request.Method {
	case http.MethodGet:
		server, err := s.netGames.MCServer(request.Context(), id)
		if err != nil {
			writeRequestError(writer, mcPublicError(err))
			return
		}
		writeSuccess(writer, map[string]any{"server": server})
	case http.MethodPut:
		if !currentSession(request).User.IsSuperAdmin() {
			writeRequestError(writer, apiError("FORBIDDEN", "仅超级管理员可以管理国际版服务器"))
			return
		}
		input, err := parseMCServerInput(request)
		var server store.MCServer
		if err == nil {
			server, err = s.netGames.UpdateMCServer(request.Context(), id, input)
		}
		err = mcPublicError(err)
		s.record(request, "net_games.mc.update", path, input, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"server": server})
	case http.MethodDelete:
		if !currentSession(request).User.IsSuperAdmin() {
			writeRequestError(writer, apiError("FORBIDDEN", "仅超级管理员可以管理国际版服务器"))
			return
		}
		err := mcPublicError(s.netGames.DeleteMCServer(request.Context(), id))
		s.record(request, "net_games.mc.delete", path, map[string]any{"id": id}, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{})
	default:
		methodNotAllowed(writer, "GET, PUT, DELETE")
	}
}

func (s *Server) handleMCServerSeries(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	keys := splitMCServerKeys(request.URL.Query().Get("servers"))
	from, to, err := netGameWindow(request)
	var series netgames.MCServerSeriesResponse
	if err == nil {
		series, err = s.netGames.MCServerSeries(request.Context(), keys, from, to)
	}
	if err != nil {
		writeRequestError(writer, mcPublicError(err))
		return
	}
	writeSuccess(writer, series)
}

func (s *Server) handleMCServerSummary(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	summary, err := s.netGames.MCServerSummary(request.Context())
	if err != nil {
		writeRequestError(writer, mcPublicError(err))
		return
	}
	writeSuccess(writer, summary)
}

func (s *Server) handleMCServerCollect(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	summary, err := s.netGames.CollectMCNow(request.Context())
	err = mcPublicError(err)
	s.record(request, "net_games.mc.collect", "", map[string]any{"trigger_type": "manual"}, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"summary": summary})
}

func parseMCServerInput(request *http.Request) (store.MCServerInput, error) {
	var raw mcServerInputJSON
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &raw)
	}
	if err != nil {
		return store.MCServerInput{}, apiError("INVALID_REQUEST", "请求体格式无效")
	}
	input := store.MCServerInput{
		Name: strings.TrimSpace(raw.Name),
		Note: strings.TrimSpace(raw.Note),
	}
	if raw.Enabled != nil {
		input.Enabled = raw.Enabled
	}
	address := strings.TrimSpace(raw.Address)
	if address != "" {
		input.Host = address
	} else {
		host := strings.TrimSpace(raw.Host)
		if host == "" {
			return store.MCServerInput{}, apiError("INVALID_REQUEST", "服务器地址不能为空")
		}
		input.Host = host
		if raw.Port != nil {
			if *raw.Port < 1 || *raw.Port > 65535 {
				return store.MCServerInput{}, apiError("INVALID_REQUEST", "端口必须在 1 到 65535 之间")
			}
			input.Port = *raw.Port
		}
	}
	return input, nil
}

func splitMCServerKeys(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			keys = append(keys, value)
		}
	}
	return keys
}

func mcPublicError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return apiError("NOT_FOUND", "国际版服务器不存在")
	case errors.Is(err, store.ErrConflict):
		return apiError("CONFLICT", err.Error())
	case errors.Is(err, netgames.ErrMCCollectorBusy):
		return apiError("CONFLICT", "国际服采集器正在运行")
	default:
		return apiError("INVALID_REQUEST", err.Error())
	}
}

// netGameWindow 解析查询时间窗口参数（from/to 或 hours），用于趋势查询。
func netGameWindow(request *http.Request) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	fromText := strings.TrimSpace(request.URL.Query().Get("from"))
	toText := strings.TrimSpace(request.URL.Query().Get("to"))
	if fromText == "" && toText == "" {
		hours := queryInt(request, "hours", 24)
		if hours < 1 || hours > 24*3650 {
			return time.Time{}, time.Time{}, apiError("INVALID_REQUEST", "查询时间范围无效")
		}
		return now.Add(-time.Duration(hours) * time.Hour), now, nil
	}
	to := now
	if toText != "" {
		parsed, err := time.Parse(time.RFC3339, toText)
		if err != nil {
			return time.Time{}, time.Time{}, apiError("INVALID_REQUEST", "to 必须是 RFC3339 时间")
		}
		to = parsed.UTC()
	}
	from := to.Add(-24 * time.Hour)
	if fromText != "" {
		parsed, err := time.Parse(time.RFC3339, fromText)
		if err != nil {
			return time.Time{}, time.Time{}, apiError("INVALID_REQUEST", "from 必须是 RFC3339 时间")
		}
		from = parsed.UTC()
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, apiError("INVALID_REQUEST", "from 必须早于 to")
	}
	return from, to, nil
}
