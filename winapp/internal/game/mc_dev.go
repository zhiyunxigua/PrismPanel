package game

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// DevLogEntry 一条开发者模式操作日志。
type DevLogEntry struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`
	Detail  string    `json:"detail"`
	OK      bool      `json:"ok"`
	Error   string    `json:"error,omitempty"`
	Elapsed string    `json:"elapsed"`
}

const devLogMax = 500

var (
	devLogMu       sync.Mutex
	devLogRing     []DevLogEntry
	devLogFile     *os.File
	devLogFilePath string
	devLogEmitter  func(DevLogEntry)
)

// SetDevLogEmitter 设置开发者日志的新增回调（用于前端实时推送）。
func SetDevLogEmitter(fn func(DevLogEntry)) {
	devLogMu.Lock()
	devLogEmitter = fn
	devLogMu.Unlock()
}

// DevModeEnabled 当前是否开启开发者模式。
func DevModeEnabled() bool {
	settings, ok := loadCachedSettings()
	return ok && settings.DevMode
}

// DevLog 记录一条开发者日志（仅开发者模式开启时生效，写入 dev-mode.log 并回调推送）。
func DevLog(kind, detail string, elapsed time.Duration, err error) {
	if !DevModeEnabled() {
		return
	}
	entry := DevLogEntry{
		Time: time.Now(), Kind: kind, Detail: detail, OK: err == nil,
		Elapsed: elapsed.Round(time.Millisecond).String(),
	}
	if err != nil {
		entry.Error = err.Error()
	}
	devLogMu.Lock()
	devLogRing = append(devLogRing, entry)
	if len(devLogRing) > devLogMax {
		devLogRing = devLogRing[len(devLogRing)-devLogMax:]
	}
	emitter := devLogEmitter
	status := "OK"
	if err != nil {
		status = "FAIL: " + entry.Error
	}
	line := fmt.Sprintf("[%s] [%s] %s ... %s (%s)\n",
		entry.Time.Format("2006-01-02 15:04:05.000"), strings.ToUpper(entry.Kind), entry.Detail, status, entry.Elapsed)
	if devLogFilePath == "" {
		if root, err := mcStoreRoot(); err == nil {
			devLogFilePath = filepath.Join(root, "dev-mode.log")
		}
	}
	if devLogFilePath != "" {
		if devLogFile == nil {
			if file, err := os.OpenFile(devLogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
				devLogFile = file
			}
		}
		if devLogFile != nil {
			_, _ = devLogFile.WriteString(line)
		}
	}
	devLogMu.Unlock()
	if emitter != nil {
		emitter(entry)
	}
}

// DevLogList 返回最近的开发者日志（内存缓冲）。
func DevLogList() []DevLogEntry {
	devLogMu.Lock()
	out := append([]DevLogEntry(nil), devLogRing...)
	devLogMu.Unlock()
	return out
}

// DevLogClear 清空内存缓冲与日志文件。
func DevLogClear() error {
	devLogMu.Lock()
	defer devLogMu.Unlock()
	devLogRing = nil
	if devLogFile != nil {
		_ = devLogFile.Close()
		devLogFile = nil
	}
	if devLogFilePath != "" {
		if err := os.Remove(devLogFilePath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// DevLogPath 返回日志文件路径。
func DevLogPath() string {
	devLogMu.Lock()
	defer devLogMu.Unlock()
	if devLogFilePath == "" {
		if root, err := mcStoreRoot(); err == nil {
			devLogFilePath = filepath.Join(root, "dev-mode.log")
		}
	}
	return devLogFilePath
}

// OpenDevLog 用系统默认程序打开日志文件。
func OpenDevLog() error {
	path := DevLogPath()
	if path == "" {
		return errors.New("无法确定日志文件位置")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			return err
		}
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}
