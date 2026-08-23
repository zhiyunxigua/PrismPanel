package api

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

var proxyUpgrader = websocket.Upgrader{
	ReadBufferSize: 4096, WriteBufferSize: 4096,
	CheckOrigin: func(request *http.Request) bool {
		origin := request.Header.Get("Origin")
		if origin == "" {
			return true
		}
		parsed, err := url.Parse(origin)
		return err == nil && strings.EqualFold(parsed.Host, request.Host)
	},
}

func (s *Server) handleConsoleProxy(writer http.ResponseWriter, request *http.Request) {
	nodeID := strings.TrimSpace(request.URL.Query().Get("node_id"))
	instanceID := strings.TrimSpace(request.URL.Query().Get("instance_id"))
	if nodeID == "" || instanceID == "" {
		return
	}
	if err := s.authorizeInstance(request, "console.read", instanceID); err != nil {
		return
	}
	frontend, err := proxyUpgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	defer frontend.Close()
	daemonURL, err := s.connections.ConsoleURL(nodeID)
	if err != nil {
		_ = frontend.WriteJSON(map[string]any{"type": "proxy.error", "message": err.Error()})
		return
	}
	backend, response, err := websocket.DefaultDialer.DialContext(request.Context(), daemonURL, nil)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		_ = frontend.WriteJSON(map[string]any{"type": "proxy.error", "message": "守护进程控制台连接失败"})
		return
	}
	defer backend.Close()
	done := make(chan struct{}, 2)
	go relayWebSocket(frontend, backend, done)
	go relayWebSocket(backend, frontend, done)
	<-done
}

func relayWebSocket(destination, source *websocket.Conn, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	for {
		messageType, contents, err := source.ReadMessage()
		if err != nil {
			return
		}
		if err := destination.WriteMessage(messageType, contents); err != nil {
			return
		}
	}
}
