package supervisor

import "os/exec"

func ConfigureProcessGroup(cmd *exec.Cmd) {
	configureProcessGroup(cmd)
}

func KillProcessTreeByPID(pid int) error {
	return killProcessTreeByPID(pid)
}

func ShellCommand(command string) *exec.Cmd {
	return shellCommand(command)
}
