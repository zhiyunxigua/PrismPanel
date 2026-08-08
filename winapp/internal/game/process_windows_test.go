//go:build windows

package game

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHideProcessWindow(t *testing.T) {
	command := exec.Command("java.exe")
	hideProcessWindow(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow {
		t.Fatal("child process window was not hidden")
	}
	if command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("CREATE_NO_WINDOW was not configured")
	}
}
