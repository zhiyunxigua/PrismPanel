//go:build !windows

package updater

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func startProcessWithoutConsole(path string, args []string) error {
	command := exec.Command(path, args...)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("waiting for previous process timed out")
}
