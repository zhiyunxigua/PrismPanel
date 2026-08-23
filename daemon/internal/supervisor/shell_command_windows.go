//go:build windows

package supervisor

import (
	"os"
	"os/exec"
	"syscall"
)

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
