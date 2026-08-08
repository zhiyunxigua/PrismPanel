//go:build !linux

package firewall

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestUnsupportedBackendReportsPlatformReason(t *testing.T) {
	status := (unsupportedBackend{}).Status(context.Background())
	if status.Supported {
		t.Fatal("unsupported platform backend reported supported")
	}
	if !strings.Contains(status.Reason, runtime.GOOS) {
		t.Fatalf("reason %q does not mention platform %q", status.Reason, runtime.GOOS)
	}
}
