//go:build windows

package config

import (
	"os"
	"path/filepath"
)

func defaultSessionSocket() string {
	return filepath.Join(os.Getenv("ProgramData"), "PrismPanel", "sessiond", "session.sock")
}

func defaultSessionTokenFile() string {
	return filepath.Join(os.Getenv("ProgramData"), "PrismPanel", "sessiond", "token")
}
