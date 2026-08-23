//go:build windows

package supervisor

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return killProcessTreeByPID(cmd.Process.Pid)
}

func killProcessTreeByPID(pid int) error {
	if pid <= 0 {
		return nil
	}
	killer := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	return killer.Run()
}

func signalProcessAlive(process *os.Process) bool {
	if process == nil {
		return false
	}
	return pidAlive(process.Pid)
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
