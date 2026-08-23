package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultCopyConcurrency(t *testing.T) {
	if got := Default().Files.CopyConcurrency; got != 4 {
		t.Fatalf("default files.copy_concurrency = %d, want 4", got)
	}
}

func TestLoadKeepsCopyConcurrencyDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.yaml")
	contents := `server:
    listen: 127.0.0.1
    port: 24444
storage:
    data_dir: data
files:
    max_edit_file_size: 5242880
process:
    console_buffer_lines: 2000
    shutdown_timeout_seconds: 90
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Files.CopyConcurrency != 4 {
		t.Fatalf("loaded files.copy_concurrency = %d, want 4", cfg.Files.CopyConcurrency)
	}
}

func TestCopyConcurrencyValidation(t *testing.T) {
	for _, value := range []int{0, 65} {
		cfg := Default()
		cfg.Files.CopyConcurrency = value
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected files.copy_concurrency %d to be rejected", value)
		}
	}
}

func TestHTTPSPublicURLWithTLSReverseProxy(t *testing.T) {
	cfg := Default()
	cfg.Server.PublicURL = "https://daemon.example.com"
	cfg.SSL.Enabled = false

	if err := cfg.Validate(); err != nil {
		t.Fatalf("HTTPS public URL behind TLS reverse proxy was rejected: %v", err)
	}
}

func TestDefaultSessionManagerEndpoints(t *testing.T) {
	cfg := Default()
	if cfg.Process.SessionSocket == "" {
		t.Fatal("default session socket is empty")
	}
	if cfg.Process.SessionTokenFile == "" {
		t.Fatal("default session token file is empty")
	}
	if cfg.SessionSocket() == "" || cfg.SessionTokenFile() == "" {
		t.Fatalf("resolved session endpoints are empty: socket=%q token=%q", cfg.SessionSocket(), cfg.SessionTokenFile())
	}
}

func TestDefaultSessionOrphanTimeout(t *testing.T) {
	cfg := Default()
	if cfg.Process.SessionOrphanTimeoutSec != 180 {
		t.Fatalf("default session orphan timeout = %d", cfg.Process.SessionOrphanTimeoutSec)
	}
	cfg.Process.SessionOrphanTimeoutSec = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected zero session orphan timeout to be rejected")
	}
}
