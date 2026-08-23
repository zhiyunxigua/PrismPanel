package api

import (
	"encoding/json"
	"net/http"

	"PrismPanel/internal/netgames"
)

// handleMCSettings 读写国际版（Minecraft Java 版服务器监控）采集设置：
// GET/PUT /api/v1/net-games/settings。
// 设置项：mc_collection_interval_minutes（1-60 分钟）、history_retention_days（1-3650 天）。
func (s *Server) handleMCSettings(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		writeSuccess(writer, map[string]any{"settings": s.netGames.Settings()})
	case http.MethodPut:
		var input netgames.Settings
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &input)
		}
		if err == nil {
			err = input.Validate()
		}
		if err != nil {
			err = apiError("INVALID_REQUEST", err.Error())
		}
		var settings netgames.Settings
		if err == nil {
			settings, err = s.netGames.UpdateSettings(input)
		}
		err = publicError(err)
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
