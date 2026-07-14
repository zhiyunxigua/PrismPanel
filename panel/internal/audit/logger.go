package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	Timestamp time.Time `json:"timestamp"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target,omitempty"`
	Success   bool      `json:"success"`
	ErrorCode string    `json:"error_code,omitempty"`
	Detail    any       `json:"detail,omitempty"`
}

type Logger struct {
	path string
	mu   sync.Mutex
}

func NewLogger(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	return &Logger{path: path}, nil
}

func (l *Logger) Write(entry Entry) error {
	entry.Timestamp = time.Now().UTC()
	contents, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(contents, '\n')); err != nil {
		return fmt.Errorf("write audit log: %w", err)
	}
	return file.Sync()
}
