package supervisor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"PrismPanel-daemon/internal/atomicfile"
)

func writeServerPort(workspace string, port int) error {
	info, err := os.Stat(workspace)
	if err != nil {
		return fmt.Errorf("inspect workspace: %w", err)
	}
	if !info.IsDir() {
		return errors.New("workspace is not a directory")
	}
	path := filepath.Join(workspace, "server.properties")
	contents, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read server.properties: %w", err)
	}
	newline := "\n"
	if strings.Contains(string(contents), "\r\n") {
		newline = "\r\n"
	}
	normalized := strings.ReplaceAll(string(contents), "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	replaced := false
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "server-port=") {
			lines[index] = "server-port=" + strconv.Itoa(port)
			replaced = true
		}
	}
	if !replaced {
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, "server-port="+strconv.Itoa(port), "")
	}
	output := strings.Join(lines, newline)
	if err := atomicfile.WriteFile(path, []byte(output), 0o640); err != nil {
		return fmt.Errorf("write server.properties: %w", err)
	}
	return nil
}

func readServerPort(workspace string) (*int, error) {
	contents, err := os.ReadFile(filepath.Join(workspace, "server.properties"))
	if err != nil {
		return nil, err
	}
	normalized := strings.ReplaceAll(string(contents), "\r\n", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "server-port=") {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "server-port=")))
		if err != nil {
			return nil, err
		}
		return &value, nil
	}
	return nil, errors.New("server-port is not configured")
}
