//go:build !windows

package supervisor

import (
	"os"
	"os/exec"
	"syscall"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	return syscall.Kill(-pid, syscall.SIGKILL)
}

func signalProcessAlive(process *os.Process) bool {
	if process == nil {
		return false
	}
	err := process.Signal(syscall.Signal(0))
	return err == nil
}
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return signalProcessAlive(process)
}
