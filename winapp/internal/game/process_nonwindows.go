//go:build !windows

package game

import "os/exec"

func hideProcessWindow(_ *exec.Cmd) {}
