package procutil

import "os/exec"

func ConfigureProcessGroup(cmd *exec.Cmd) { configureProcessGroup(cmd) }

func KillProcessTreeByPID(pid int) error { return killProcessTreeByPID(pid) }

func ShellCommand(command string) *exec.Cmd { return shellCommand(command) }

func PidAlive(pid int) bool { return pidAlive(pid) }

func WaitPID(pid int) error { return waitPID(pid) }

func CommandPID(cmd *exec.Cmd) int { return commandPID(cmd) }
