//go:build windows

package updater

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestProcessWithoutConsoleDoesNotHideGUIWindow(t *testing.T) {
	command := newProcessWithoutConsoleCommand("PrismPanel.exe", nil)
	if command.SysProcAttr == nil {
		t.Fatal("Windows process attributes were not configured")
	}
	if command.SysProcAttr.HideWindow {
		t.Fatal("WinApp GUI window would be hidden after an update")
	}
	if command.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("CREATE_NO_WINDOW was not configured")
	}
}
