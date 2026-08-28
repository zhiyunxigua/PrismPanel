//go:build windows

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func Install(configPath string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	if strings.TrimSpace(configPath) == "" {
		configPath = filepath.Join(os.Getenv("ProgramData"), "PrismPanel", "sessiond", "sessiond.yaml")
	}
	bin := fmt.Sprintf(`"%s" --config "%s"`, executable, configPath)
	cmd := exec.Command("sc.exe", "create", "prism-sessiond", "binPath=", bin, "start=", "auto", "DisplayName=", "Prism Session Manager")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sc create: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("sc.exe", "start", "prism-sessiond").CombinedOutput(); err != nil {
		return fmt.Errorf("sc start: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func Uninstall() error {
	_ = exec.Command("sc.exe", "stop", "prism-sessiond").Run()
	return exec.Command("sc.exe", "delete", "prism-sessiond").Run()
}
