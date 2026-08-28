package sessionproto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func SessionDir(dataDir string) string { return filepath.Join(dataDir, "sessions") }

func SocketPath(dataDir, instanceID string) string {
	return filepath.Join(SessionDir(dataDir), sanitizeInstanceID(instanceID)+".sock")
}

func StatePath(dataDir, instanceID string) string {
	return filepath.Join(SessionDir(dataDir), sanitizeInstanceID(instanceID)+".json")
}

func sanitizeInstanceID(instanceID string) string {
	value := strings.TrimSpace(instanceID)
	value = strings.ReplaceAll(value, string(filepath.Separator), "_")
	value = strings.ReplaceAll(value, "..", "_")
	if value == "" { return "instance" }
	return value
}

func WriteState(path string, state State) error {
	state.StartedAt = state.StartedAt.UTC()
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil { return err }
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil { return err }
	tmp, err := os.CreateTemp(filepath.Dir(path), ".prism-session-*")
	if err != nil { return err }
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o640); err != nil { _ = tmp.Close(); return err }
	if _, err := tmp.Write(payload); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Sync(); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }
	return os.Rename(tmpPath, path)
}

func ReadState(path string) (State, error) {
	var state State
	contents, err := os.ReadFile(path)
	if err != nil { return State{}, err }
	if err := json.Unmarshal(contents, &state); err != nil { return State{}, err }
	state.StartedAt = state.StartedAt.UTC()
	return state, nil
}

func RemoveState(path string) { _ = os.Remove(path) }

func FreshState(instanceID, sessionID, socket, token, pluginToken string, pid int) State {
	return State{InstanceID: instanceID, SessionID: sessionID, PID: pid, Socket: socket, Token: token, PluginToken: pluginToken, StartedAt: time.Now().UTC()}
}
