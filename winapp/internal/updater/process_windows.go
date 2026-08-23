//go:build windows

package updater

import (
	"errors"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func startProcessWithoutConsole(path string, args []string) error {
	command := newProcessWithoutConsoleCommand(path, args)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func newProcessWithoutConsoleCommand(path string, args []string) *exec.Cmd {
	command := exec.Command(path, args...)
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	return command
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	milliseconds := uint32(timeout / time.Millisecond)
	status, err := windows.WaitForSingleObject(handle, milliseconds)
	if err != nil {
		return err
	}
	if status == uint32(windows.WAIT_TIMEOUT) {
		return errors.New("等待旧版 WinApp 退出超时")
	}
	return nil
}
