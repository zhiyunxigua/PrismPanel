package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"PrismPanel/internal/netgames"
	"PrismPanel/internal/store"
)

const (
	netGamesPreferenceNamespace = "net_games"
	netGamesPreferenceVersion   = 1
)

type netGamesPreference struct {
	DisplayGameCount int      `json:"display_game_count"`
	ForcedGameIDs    []string `json:"forced_game_ids"`
}

func (s *Server) handleNetGameSettings(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeSuccess(writer, map[string]any{"settings": s.netGames.Settings()})
	case http.MethodPut:
		var input netgames.Settings
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &input)
		}
		var settings netgames.Settings
		if err == nil {
			settings, err = s.netGames.UpdateSettings(input)
		}
		err = netGamesPublicError(err)
		s.record(request, "net_games.settings.update", "", input, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"settings": settings})
	default:
		methodNotAllowed(writer, "GET, PUT")
	}
}

func (s *Server) handleNetGameAccount(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		account, err := s.netGames.Account(request.Context())
		if err != nil {
			writeRequestError(writer, netGamesPublicError(err))
			return
		}
		writeSuccess(writer, map[string]any{"account": account})
	case http.MethodDelete:
		err := netGamesPublicError(s.netGames.DeleteAccount())
		s.record(request, "net_games.account.delete", "", nil, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{})
	default:
		methodNotAllowed(writer, "GET, DELETE")
	}
}

func (s *Server) handleNetGameAccountVerify(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	input.Email = strings.TrimSpace(input.Email)
	if err == nil && (input.Email == "" || strings.TrimSpace(input.Password) == "") {
		err = apiError("INVALID_REQUEST", "网易账号和密码不能为空")
	}
	var account netgames.AccountView
	if err == nil {
		account, err = s.netGames.SaveAccount(request.Context(), input.Email, input.Password)
	}
	err = netGamesPublicError(err)
	s.record(request, "net_games.account.verify", "", map[string]any{"email": input.Email}, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"account": account})
}

func (s *Server) handleNetGameCollect(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	run, err := s.netGames.CollectNow(request.Context(), "manual")
	err = netGamesPublicError(err)
	target := ""
	if run.ID != 0 {
		target = strconv.FormatUint(run.ID, 10)
	}
	s.record(request, "net_games.collect", target, map[string]any{"trigger_type": "manual"}, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"run": run})
}

func (s *Server) handleNetGameCollectorStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	status, err := s.netGames.CollectorStatus(request.Context())
	if err != nil {
		writeRequestError(writer, netGamesPublicError(err))
		return
	}
	writeSuccess(writer, map[string]any{"collector": status})
}

func (s *Server) handleNetGameSeries(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	preference, err := s.loadNetGamesPreference(request)
	from, to, windowErr := netGameWindow(request)
	if err == nil {
		err = windowErr
	}
	var series netgames.SeriesResponse
	if err == nil {
		series, err = s.netGames.Series(request.Context(), currentSession(request).User.ID,
			preference.DisplayGameCount, preference.ForcedGameIDs, from, to)
	}
	if err != nil {
		writeRequestError(writer, netGamesPublicError(err))
		return
	}
	writeSuccess(writer, series)
}

func (s *Server) handleNetGame(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/net-games/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[1] == "refresh-details" {
		s.handleNetGameRefreshDetails(writer, request, parts[0])
		return
	}
	if len(parts) != 1 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	preference, err := s.loadNetGamesPreference(request)
	from, to, windowErr := netGameWindow(request)
	if err == nil {
		err = windowErr
	}
	var game netgames.GameResponse
	if err == nil {
		game, err = s.netGames.Game(request.Context(), parts[0], preference.DisplayGameCount, preference.ForcedGameIDs, from, to)
	}
	if err != nil {
		writeRequestError(writer, netGamesPublicError(err))
		return
	}
	writeSuccess(writer, game)
}

func (s *Server) handleNetGameRefreshDetails(writer http.ResponseWriter, request *http.Request, gameID string) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	if !currentSession(request).User.IsSuperAdmin() {
		writeRequestError(writer, apiError("FORBIDDEN", "仅超级管理员可以刷新网络游戏详情"))
		return
	}
	game, err := s.netGames.RefreshDetails(request.Context(), gameID)
	err = netGamesPublicError(err)
	s.record(request, "net_games.details.refresh", gameID, map[string]any{"game_id": gameID}, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{"game": game})
}

func (s *Server) handleNetGamePreference(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		preference, err := s.loadNetGamesPreference(request)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"preference": preference})
	case http.MethodPut:
		var input netGamesPreference
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &input)
		}
		var preference netGamesPreference
		if err == nil {
			preference, err = normalizeNetGamesPreference(input)
		}
		if err == nil {
			encoded, encodeErr := json.Marshal(preference)
			if encodeErr != nil {
				err = encodeErr
			} else {
				_, err = s.store.UpsertUserPreference(request.Context(), currentSession(request).User.ID,
					netGamesPreferenceNamespace, netGamesPreferenceVersion, encoded)
			}
		}
		err = netGamesPublicError(err)
		s.record(request, "net_games.preference.update", "", preference, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"preference": preference})
	default:
		methodNotAllowed(writer, "GET, PUT")
	}
}

func (s *Server) loadNetGamesPreference(request *http.Request) (netGamesPreference, error) {
	preference := defaultNetGamesPreference()
	record, err := s.store.GetUserPreference(request.Context(), currentSession(request).User.ID, netGamesPreferenceNamespace)
	if errors.Is(err, store.ErrNotFound) {
		return preference, nil
	}
	if err != nil {
		return preference, err
	}
	if len(record.Settings) == 0 {
		return preference, nil
	}
	if err := json.Unmarshal(record.Settings, &preference); err != nil {
		return defaultNetGamesPreference(), apiError("INVALID_REQUEST", "网络游戏偏好设置格式无效")
	}
	return normalizeNetGamesPreference(preference)
}

func defaultNetGamesPreference() netGamesPreference {
	return netGamesPreference{DisplayGameCount: 10, ForcedGameIDs: []string{}}
}

func normalizeNetGamesPreference(input netGamesPreference) (netGamesPreference, error) {
	if input.DisplayGameCount == 0 {
		input.DisplayGameCount = 10
	}
	if input.DisplayGameCount < 1 || input.DisplayGameCount > 20 {
		return netGamesPreference{}, apiError("INVALID_REQUEST", "显示游戏数量必须在 1 到 20 之间")
	}
	seen := make(map[string]struct{}, len(input.ForcedGameIDs))
	forced := make([]string, 0, len(input.ForcedGameIDs))
	for _, raw := range input.ForcedGameIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if len(id) > 64 {
			return netGamesPreference{}, apiError("INVALID_REQUEST", "强制显示游戏 ID 不能超过 64 个字符")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		forced = append(forced, id)
	}
	if len(forced) > input.DisplayGameCount {
		return netGamesPreference{}, apiError("INVALID_REQUEST", "强制显示游戏数量不能超过显示游戏数量")
	}
	input.ForcedGameIDs = forced
	return input, nil
}

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

func netGamesPublicError(err error) error {
	if err == nil {
		return nil
	}
	var protocol *netgames.ProtocolError
	if errors.As(err, &protocol) {
		apiErr := apiError(protocol.Code, protocol.Message)
		apiErr.VerifyURL = protocol.VerifyURL
		return apiErr
	}
	if errors.Is(err, netgames.ErrCollectorBusy) {
		return apiError("CONFLICT", "网络游戏采集器正在运行")
	}
	if errors.Is(err, os.ErrNotExist) {
		return apiError("ACCOUNT_UNAVAILABLE", "尚未保存网易账号")
	}
	return publicError(err)
}
