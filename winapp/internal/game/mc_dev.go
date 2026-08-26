package game

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
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

	// ---- 新增字段（向后兼容：旧字段不变，新字段均可选）----
	AppVersion  string `json:"app_version,omitempty"`  // 应用版本（main.appVersion）
	GOOS        string `json:"goos,omitempty"`         // 操作系统 runtime.GOOS
	GOARCH      string `json:"goarch,omitempty"`       // 架构 runtime.GOARCH
	Input       string `json:"input,omitempty"`        // 操作输入摘要（版本ID/服务器IP/端口/文件名等，已脱敏）
	ElapsedMs   int64  `json:"elapsed_ms,omitempty"`   // 耗时毫秒数值（Elapsed 字符串保留）
	ErrorDetail string `json:"error_detail,omitempty"` // 错误堆栈/明细（err.Error() 之外的补充）
}

// DevLogOpt DevLog 的可选参数。
type DevLogOpt struct {
	Input string // 操作输入摘要（脱敏后写入）
}

const devLogMax = 500

var (
	devLogMu       sync.Mutex
	devLogRing     []DevLogEntry
	devLogFile     *os.File
	devLogFilePath string
	devLogEmitter  func(DevLogEntry)
	devLogVersion  string
)

// SetDevLogAppVersion 设置应用版本号（startup 时注入，写入每条日志）。
func SetDevLogAppVersion(version string) {
	devLogMu.Lock()
	devLogVersion = version
	devLogMu.Unlock()
}

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
// opts 可选：携带操作输入摘要等上下文。
func DevLog(kind, detail string, elapsed time.Duration, err error, opts ...DevLogOpt) {
	if !DevModeEnabled() {
		return
	}
	var input string
	if len(opts) > 0 {
		input = opts[0].Input
	}
	entry := DevLogEntry{
		Time: time.Now(), Kind: kind, Detail: maskSensitive(detail), OK: err == nil,
		Elapsed: elapsed.Round(time.Millisecond).String(),
	}
	devLogMu.Lock()
	entry.AppVersion = devLogVersion
	devLogMu.Unlock()
	entry.GOOS = runtime.GOOS
	entry.GOARCH = runtime.GOARCH
	entry.ElapsedMs = elapsed.Milliseconds()
	if input != "" {
		entry.Input = maskSensitive(input)
	}
	if err != nil {
		entry.Error = maskSensitive(err.Error())
		entry.ErrorDetail = maskSensitive(captureStack(4096))
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
	line := fmt.Sprintf("[%s] [%s] %s ... %s (%s) app=%s %s/%s",
		entry.Time.Format("2006-01-02 15:04:05.000"), strings.ToUpper(entry.Kind), entry.Detail, status, entry.Elapsed,
		entry.AppVersion, entry.GOOS, entry.GOARCH)
	if entry.Input != "" {
		line += " input=" + entry.Input
	}
	line += "\n"
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

// captureStack 捕获当前 goroutine 的堆栈（截断到 limit 字节），用于错误明细。
func captureStack(limit int) string {
	stack := debug.Stack()
	if len(stack) > limit {
		stack = stack[:limit]
	}
	return strings.TrimSpace(string(stack))
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
	var result error
	if devLogFilePath != "" {
		if err := os.Remove(devLogFilePath); err != nil && !os.IsNotExist(err) {
			result = err
		}
		// 清空后重置缓存路径：游戏目录可能已变化，下次写入/定位时按当前目录重新计算。
		devLogFilePath = ""
	}
	return result
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

// ---- 敏感信息脱敏 ----

var (
	// JSON 风格键值："password": "secret"
	sensitiveJSONRe = regexp.MustCompile(`(?i)"(access_token|refresh_token|client_token|clientsecret|password|passwd|secret|authorization|apikey|api_key|token)"\s*:\s*"[^"]*"`)
	// 键值对风格：password=secret / password: secret / access_token=TOK&...
	sensitiveKeyRe = regexp.MustCompile(`(?i)(access_token|refresh_token|client_token|clientsecret|password|passwd|secret|authorization|apikey|api_key|token)(\s*[=:]\s*)[^\s,&"']+`)
	tokenLikeRe    = regexp.MustCompile(`[A-Za-z0-9_\-.]{48,}`)
)

// maskSensitive 脱敏字符串中的敏感信息：
//   - "password": "secret" 形式的 JSON 键值
//   - access_token/refresh_token/client_token/password/token 等键值对（= 或 : 形式）
//   - 长度 >= 48 的连续 token 状字符串（覆盖 JWT/accessToken 等无键名情形）
func maskSensitive(value string) string {
	if value == "" {
		return ""
	}
	value = sensitiveJSONRe.ReplaceAllString(value, `"$1":"***"`)
	value = sensitiveKeyRe.ReplaceAllString(value, "$1$2***")
	value = tokenLikeRe.ReplaceAllString(value, "***")
	return value
}
