package application

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"PrismPanel-winapp/internal/settings"
)

func TestConfigurePanelURLValidatesPersistsAndStartsProxy(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/auth/status" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"data":{"initialized":true}}`))
	}))
	defer panel.Close()

	store := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	service := New(store)
	runtime, err := service.ConfigurePanelURL(context.Background(), panel.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close(context.Background()) }()
	if !runtime.Configured || runtime.Mode != "winapp" || runtime.APIBaseURL == "" || runtime.ProxySession == "" {
		t.Fatalf("unexpected runtime config: %#v", runtime)
	}
	stored, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.PanelURL != panel.URL {
		t.Fatalf("stored Panel URL = %q", stored.PanelURL)
	}
}
