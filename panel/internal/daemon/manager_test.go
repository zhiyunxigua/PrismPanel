package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestManagerMaintainsMultipleConnections(t *testing.T) {
	first := fakeDaemon(t, "node-a", "token-a")
	defer first.Close()
	second := fakeDaemon(t, "node-b", "token-b")
	defer second.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewManager(nilLogger(), nil)
	manager.Start(ctx, []ConnectionDefinition{
		{PanelNodeID: "panel-a", BaseURL: first.URL, Token: "token-a", Enabled: true},
		{PanelNodeID: "panel-b", BaseURL: second.URL, Token: "token-b", Enabled: true},
	})
	defer manager.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		left, right := manager.Status("panel-a"), manager.Status("panel-b")
		if left.State == "ONLINE" && right.State == "ONLINE" {
			if left.NodeID != "node-a" || right.NodeID != "node-b" {
				t.Fatalf("statuses crossed: %#v %#v", left, right)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("connections did not become online: %#v %#v", manager.Status("panel-a"), manager.Status("panel-b"))
}

func fakeDaemon(t *testing.T, nodeID, token string) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/ws/control" {
			http.NotFound(writer, request)
			return
		}
		conn, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var auth envelope
		if err := conn.ReadJSON(&auth); err != nil {
			return
		}
		success := auth.Type == "auth" && auth.Token == token
		metadata, _ := json.Marshal(Metadata{
			NodeID: nodeID, Version: "test", ProtocolVersion: "1",
			Capabilities: []string{"server.manage"},
		})
		result := envelope{Type: "auth.result", Success: &success, Data: metadata}
		if !success {
			result.Error = &APIError{Code: "UNAUTHENTICATED", Message: "invalid token"}
		}
		if err := conn.WriteJSON(result); err != nil || !success {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
}

func nilLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
