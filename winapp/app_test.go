package main

import (
	"path/filepath"
	"testing"
	"time"

	"PrismPanel-winapp/internal/application"
	"PrismPanel-winapp/internal/credentials"
	"PrismPanel-winapp/internal/settings"
)

func TestRuntimeConfigWaitsForStartup(t *testing.T) {
	app := &App{
		service: application.New(
			settings.NewStore(filepath.Join(t.TempDir(), "settings.json")),
			credentials.NewStore(),
		),
		startupDone: make(chan struct{}),
	}
	result := make(chan application.RuntimeConfig, 1)

	go func() {
		result <- app.RuntimeConfig()
	}()

	select {
	case <-result:
		t.Fatal("RuntimeConfig returned before startup completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(app.startupDone)
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("RuntimeConfig did not return after startup completed")
	}
}
