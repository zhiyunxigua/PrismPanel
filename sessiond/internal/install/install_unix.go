//go:build !windows

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const unitPath = "/etc/systemd/system/prism-sessiond.service"

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
		configPath = "/etc/prism-sessiond/sessiond.yaml"
	}
	unit := fmt.Sprintf(`[Unit]
Description=Prism Session Manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s --config %s
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
`, executable, configPath)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	if output, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("systemctl", "enable", "--now", "prism-sessiond").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable --now prism-sessiond: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func Uninstall() error {
	_ = exec.Command("systemctl", "disable", "--now", "prism-sessiond").Run()
	return os.Remove(unitPath)
}
