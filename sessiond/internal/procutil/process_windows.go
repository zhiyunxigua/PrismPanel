//go:build windows

package procutil

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

const (
	createNewProcessGroup = 0x00000200
	processTerminate      = 0x0001
)

func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

func killProcessTreeByPID(pid int) error {
	if pid <= 0 {
		return nil
	}
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
	handle, err := syscall.OpenProcess(processTerminate|syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return nil
	}
	defer syscall.CloseHandle(handle)
	_ = syscall.TerminateProcess(handle, 1)
	_, _ = syscall.WaitForSingleObject(handle, 2000)
	return nil
}

func shellCommand(command string) *exec.Cmd {
	commandInterpreter := os.Getenv("ComSpec")
	if commandInterpreter == "" {
		commandInterpreter = "cmd.exe"
	}
	cmd := exec.Command(commandInterpreter)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: "cmd.exe /D /S /C " + string('"') + command + string('"'),
	}
	return cmd
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(handle)
	event, err := syscall.WaitForSingleObject(handle, 0)
	return err == nil && event == syscall.WAIT_TIMEOUT
}

func waitPID(pid int) error {
	for pidAlive(pid) {
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

func commandPID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}
