package supervisor

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPluginRequestMatchesResponse(t *testing.T) {
	connection := &PluginConnection{
		capabilities: map[string]struct{}{"proxy.backends": {}},
		outgoing:     make(chan PluginRequest, 1),
		done:         make(chan struct{}),
		pending:      make(map[string]chan PluginResponse),
	}
	go func() {
		request := <-connection.Outgoing()
		data, _ := json.Marshal(ProxyBackendResult{Revision: 4, Applied: 2, Removed: 1})
		connection.HandleResponse(PluginResponse{
			RequestID: request.RequestID,
			Success:   true,
			Data:      data,
		})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var result ProxyBackendResult
	if err := connection.Request(ctx, "proxy.backends.replace", ProxyBackendCatalog{Revision: 4}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Revision != 4 || result.Applied != 2 || result.Removed != 1 {
		t.Fatalf("unexpected response: %#v", result)
	}
}

func TestPluginRequestRequiresCapability(t *testing.T) {
	connection := &PluginConnection{
		capabilities: map[string]struct{}{},
		outgoing:     make(chan PluginRequest, 1),
		done:         make(chan struct{}),
		pending:      make(map[string]chan PluginResponse),
	}
	if err := connection.Request(context.Background(), "player.transfer", map[string]string{}, nil); err == nil {
		t.Fatal("expected missing capability to be rejected")
	}
}
