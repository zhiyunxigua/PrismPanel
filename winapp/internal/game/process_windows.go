//go:build windows

package game

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func hideProcessWindow(command *exec.Cmd) {
	if command == nil {
		return
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.HideWindow = true
	command.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
