package api

import (
	"encoding/json"
	"net"
	"net/http"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/supervisor"
)

type pluginMessage struct {
	Type         string          `json:"type"`
	RequestID    string          `json:"request_id,omitempty"`
	Token        string          `json:"token,omitempty"`
	InstanceID   string          `json:"instance_id,omitempty"`
	SessionID    string          `json:"session_id,omitempty"`
	PID          int             `json:"pid,omitempty"`
	Platform     string          `json:"platform,omitempty"`
	Capabilities []string        `json:"capabilities,omitempty"`
	Success      bool            `json:"success,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
	Error        *apperr.Error   `json:"error,omitempty"`
}

func (s *Server) handlePlugin(writer http.ResponseWriter, request *http.Request) {
	if !isLoopbackRequest(request.RemoteAddr) {
		http.Error(writer, "plugin endpoint is local only", http.StatusForbidden)
		return
	}
	conn, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(1024 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var auth pluginMessage
	if err := conn.ReadJSON(&auth); err != nil || auth.Type != "auth" {
		writePluginAuthResult(conn, nil, apperr.New("UNAUTHENTICATED", "缺少插件实例凭据"))
		return
	}
	connection, err := s.supervisor.RegisterPlugin(
		auth.InstanceID, auth.SessionID, auth.Token, auth.PID, auth.Platform, auth.Capabilities,
	)
	if err != nil {
		writePluginAuthResult(conn, nil, err)
		return
	}
	defer connection.Close()
	if err := writePluginAuthResult(conn, map[string]any{
		"instance_id": auth.InstanceID, "session_id": auth.SessionID,
		"protocol_version": "2", "sample_interval_seconds": 5,
	}, nil); err != nil {
		return
	}
	go writePluginCommands(conn, connection)
	go s.supervisor.RestoreProxyBackends(auth.InstanceID)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(35 * time.Second))
		var message pluginMessage
		if err := conn.ReadJSON(&message); err != nil {
			return
		}
		switch message.Type {
		case "heartbeat":
			err = connection.Heartbeat()
		case "snapshot":
			var report supervisor.PluginReport
			if decodeErr := json.Unmarshal(message.Data, &report); decodeErr != nil {
				err = apperr.Wrap("INVALID_REQUEST", "插件状态格式无效", decodeErr)
			} else {
				err = connection.Update(report)
			}
		case "response":
			connection.HandleResponse(supervisor.PluginResponse{
				RequestID: message.RequestID,
				Success:   message.Success,
				Data:      message.Data,
				Error:     message.Error,
			})
			err = nil
		default:
			err = apperr.New("UNKNOWN_COMMAND", "不支持的插件消息")
		}
		if err != nil {
			return
		}
	}
}

func writePluginCommands(
	conn interface {
		WriteJSON(any) error
		Close() error
	},
	connection *supervisor.PluginConnection,
) {
	for {
		select {
		case message := <-connection.Outgoing():
			if err := conn.WriteJSON(message); err != nil {
				_ = conn.Close()
				return
			}
		case <-connection.Done():
			return
		}
	}
}

func writePluginAuthResult(conn interface{ WriteJSON(any) error }, data any, err error) error {
	if err != nil {
		return conn.WriteJSON(map[string]any{
			"type": "auth.result", "success": false, "error": apperr.From(err),
		})
	}
	return conn.WriteJSON(map[string]any{
		"type": "auth.result", "success": true, "data": data,
	})
}

func isLoopbackRequest(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
