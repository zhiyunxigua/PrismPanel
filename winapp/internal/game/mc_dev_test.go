package game

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// enableDevModeForTest 开启开发者模式并重定向存储根目录到临时目录，返回清理函数。
func enableDevModeForTest(t *testing.T) {
	t.Helper()
	t.Setenv("PRISMPANEL_MC_DIR", t.TempDir())
	mcSettingsMu.Lock()
	old := mcSettingsCache
	mcSettingsCache = MCLauncherSettings{DevMode: true}
	mcSettingsLoaded = true
	mcSettingsMu.Unlock()
	t.Cleanup(func() {
		_ = DevLogClear()
		mcSettingsMu.Lock()
		mcSettingsCache = old
		mcSettingsLoaded = false
		mcSettingsMu.Unlock()
	})
}

func TestDevLogEntryFillsNewFields(t *testing.T) {
	enableDevModeForTest(t)
	SetDevLogAppVersion("1.2.3-test")

	started := time.Now()
	time.Sleep(2 * time.Millisecond)
	err := errors.New("测试错误: access_token=SECRETTOKEN123")
	DevLog("test-kind", "测试操作 detail=xxx", time.Since(started), err, DevLogOpt{Input: "version=1.20.4 server=127.0.0.1:25565"})

	entries := DevLogList()
	if len(entries) != 1 {
		t.Fatalf("期望 1 条日志，实际 %d", len(entries))
	}
	entry := entries[0]
	if entry.Kind != "test-kind" {
		t.Errorf("Kind = %q", entry.Kind)
	}
	if entry.AppVersion != "1.2.3-test" {
		t.Errorf("AppVersion = %q", entry.AppVersion)
	}
	if entry.GOOS != runtime.GOOS || entry.GOARCH != runtime.GOARCH {
		t.Errorf("GOOS/GOARCH = %q/%q", entry.GOOS, entry.GOARCH)
	}
	if entry.ElapsedMs <= 0 {
		t.Errorf("ElapsedMs = %d, 期望 > 0", entry.ElapsedMs)
	}
	if entry.Elapsed == "" {
		t.Error("Elapsed 字符串为空")
	}
	if !strings.Contains(entry.Input, "version=1.20.4") || !strings.Contains(entry.Input, "127.0.0.1:25565") {
		t.Errorf("Input = %q", entry.Input)
	}
	if entry.ErrorDetail == "" {
		t.Error("错误时 ErrorDetail 应包含堆栈明细")
	}
	if !strings.Contains(entry.ErrorDetail, "DevLog") {
		t.Errorf("ErrorDetail 应包含 DevLog 调用帧: %q", entry.ErrorDetail)
	}
}

func TestDevLogMasksSensitiveDetail(t *testing.T) {
	enableDevModeForTest(t)
	DevLog("mask-test", "login password=mysecret123 access_token=abc123def456 token=xyz", time.Millisecond, nil, DevLogOpt{Input: "password=plainpwd refresh_token=rt123"})

	entries := DevLogList()
	if len(entries) != 1 {
		t.Fatalf("期望 1 条日志，实际 %d", len(entries))
	}
	entry := entries[0]
	if strings.Contains(entry.Detail, "mysecret123") {
		t.Errorf("Detail 未脱敏: %q", entry.Detail)
	}
	if strings.Contains(entry.Detail, "abc123def456") {
		t.Errorf("Detail 未脱敏 access_token: %q", entry.Detail)
	}
	if strings.Contains(entry.Input, "plainpwd") {
		t.Errorf("Input 未脱敏: %q", entry.Input)
	}
	if !strings.Contains(entry.Detail, "***") {
		t.Errorf("Detail 应包含 ***: %q", entry.Detail)
	}
}

func TestDevLogMasksLongToken(t *testing.T) {
	enableDevModeForTest(t)
	longToken := strings.Repeat("A1b2C3d4E5f6", 8) // 96 字符 token 状字符串
	DevLog("token-test", "error occurred: "+longToken, time.Millisecond, errors.New("boom "+longToken))

	entries := DevLogList()
	if len(entries) != 1 {
		t.Fatalf("期望 1 条日志，实际 %d", len(entries))
	}
	if strings.Contains(entries[0].Detail, longToken) {
		t.Errorf("长 token 未脱敏: %q", entries[0].Detail)
	}
	if strings.Contains(entries[0].Error, longToken) {
		t.Errorf("错误信息中的长 token 未脱敏: %q", entries[0].Error)
	}
	if !strings.Contains(entries[0].ErrorDetail, "DevLog") {
		t.Errorf("ErrorDetail 应包含堆栈帧: %q", entries[0].ErrorDetail)
	}
}

func TestDevLogKeepsOldFields(t *testing.T) {
	enableDevModeForTest(t)
	DevLog("old-test", "旧字段检查", 5*time.Millisecond, nil)

	entries := DevLogList()
	if len(entries) != 1 {
		t.Fatalf("期望 1 条日志，实际 %d", len(entries))
	}
	entry := entries[0]
	if entry.OK != true {
		t.Errorf("OK = %v", entry.OK)
	}
	if entry.Error != "" {
		t.Errorf("Error = %q, 期望空", entry.Error)
	}
	if entry.Elapsed == "" {
		t.Error("Elapsed 为空")
	}
	if entry.Time.IsZero() {
		t.Error("Time 为零值")
	}
}

func TestDevLogDisabledWhenDevModeOff(t *testing.T) {
	t.Setenv("PRISMPANEL_MC_DIR", t.TempDir())
	mcSettingsMu.Lock()
	old := mcSettingsCache
	mcSettingsCache = MCLauncherSettings{DevMode: false}
	mcSettingsLoaded = true
	mcSettingsMu.Unlock()
	t.Cleanup(func() {
		mcSettingsMu.Lock()
		mcSettingsCache = old
		mcSettingsLoaded = false
		mcSettingsMu.Unlock()
	})

	DevLog("off-test", "不应记录", time.Millisecond, nil)
	if got := DevLogList(); len(got) != 0 {
		t.Fatalf("开发者模式关闭时不应记录，实际 %d 条", len(got))
	}
}

func TestDevLogRingLimit(t *testing.T) {
	enableDevModeForTest(t)
	for index := 0; index < devLogMax+10; index++ {
		DevLog("ring-test", fmt.Sprintf("ring-%d", index), time.Millisecond, nil)
	}
	entries := DevLogList()
	if len(entries) != devLogMax {
		t.Fatalf("环形缓冲应保留 %d 条，实际 %d", devLogMax, len(entries))
	}
	// 最早一条（ring-0）应已被挤出。
	for _, entry := range entries {
		if entry.Detail == "ring-0" {
			t.Fatalf("最旧条目未被淘汰: %q", entry.Detail)
		}
	}
	// 最新一条（ring-%d, index=devLogMax+9）应存在。
	last := entries[len(entries)-1]
	if last.Detail != fmt.Sprintf("ring-%d", devLogMax+9) {
		t.Errorf("最新条目应为 ring-%d，实际 %q", devLogMax+9, last.Detail)
	}
}

func TestDevLogWritesFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PRISMPANEL_MC_DIR", root)
	mcSettingsMu.Lock()
	old := mcSettingsCache
	mcSettingsCache = MCLauncherSettings{DevMode: true}
	mcSettingsLoaded = true
	mcSettingsMu.Unlock()
	t.Cleanup(func() {
		_ = DevLogClear()
		mcSettingsMu.Lock()
		mcSettingsCache = old
		mcSettingsLoaded = false
		mcSettingsMu.Unlock()
	})

	DevLog("file-test", "写文件验证 app=0.0.1 windows/amd64", 3*time.Millisecond, nil)
	path := DevLogPath()
	if path == "" {
		t.Fatal("DevLogPath 为空")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}
	text := string(contents)
	if !strings.Contains(text, "[FILE-TEST]") {
		t.Errorf("日志文件缺少 kind: %q", text)
	}
	if !strings.Contains(text, "app=") || !strings.Contains(text, runtime.GOOS+"/"+runtime.GOARCH) {
		t.Errorf("日志文件缺少 app/环境信息: %q", text)
	}
}

func TestMaskSensitive(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"空字符串", "", ""},
		{"普通文本不变", "hello world", "hello world"},
		{"access_token 键值", "access_token=abcdef123456", "access_token=***"},
		{"password 键值", "password: secret", "password: ***"},
		{"refresh_token 键值", "refresh_token=rt000111", "refresh_token=***"},
		{"client_token 键值", "client_token=ct12345", "client_token=***"},
		{"长 token 无键名", strings.Repeat("Qw9x", 20), "***"},
		{"短字符串不动", "1.20.4", "1.20.4"},
		{"URL 查询参数", "https://example.com/auth?access_token=TOK&next=1", "https://example.com/auth?access_token=***&next=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := maskSensitive(tc.in)
			if got != tc.want {
				t.Errorf("maskSensitive(%q) = %q, 期望 %q", tc.in, got, tc.want)
			}
		})
	}
}
