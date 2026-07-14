//go:build !windows

package supervisor

import "os/exec"

func shellCommand(command string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", command)
}
