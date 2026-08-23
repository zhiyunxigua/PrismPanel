package game

import (
	"errors"
	"os"
	"runtime"
	"strings"
)

// 本文件只保留国际版（mc_launch.go 等）启动路径共用的参数/路径助手。

// javaExeName 返回当前平台 Java 可执行文件名。
func javaExeName() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

// ensureDefaultOptions 在游戏目录不存在 options.txt 时写入默认选项。
func ensureDefaultOptions(gameDir string) error {
	path := gameDir + string(os.PathSeparator) + "options.txt"
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, []byte("guiScale:2\nlang:zh_cn\nmaxFps:120\n"), 0o644)
}

// splitCommandLine 按空格拆分命令行，保留引号包裹的片段。
func splitCommandLine(input string) ([]string, error) {
	var args []string
	var current strings.Builder
	inQuote := false
	escaped := false
	quote := rune(0)
	for _, r := range input {
		if escaped {
			current.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			current.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			if inQuote && r == quote {
				inQuote = false
				quote = 0
			} else if !inQuote {
				inQuote = true
				quote = r
			} else {
				current.WriteRune(r)
			}
			continue
		}
		if !inQuote && (r == ' ' || r == '\t' || r == '\r' || r == '\n') {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if inQuote {
		return nil, errors.New("unclosed quote")
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args, nil
}

// removeFlagWithValue 移除形如 "-Xmx" value 或 "-Xmx2G" 的参数。
func removeFlagWithValue(args []string, prefix string) []string {
	out := args[:0]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == prefix && i+1 < len(args) {
			i++
			continue
		}
		if strings.HasPrefix(arg, prefix) {
			continue
		}
		out = append(out, arg)
	}
	return out
}

// replacePlaceholders 用替换表替换参数中的 ${...} 占位符。
func replacePlaceholders(args []string, replacements map[string]string) []string {
	out := make([]string, len(args))
	for i, arg := range args {
		for from, to := range replacements {
			arg = strings.ReplaceAll(arg, from, to)
		}
		out[i] = arg
	}
	return out
}

// upsertFlagValue 设置 "--flag value"：已存在则替换值，否则追加到末尾。
func upsertFlagValue(args []string, flag, value string) []string {
	for i, arg := range args {
		if arg == flag {
			if i+1 < len(args) {
				args[i+1] = value
				return args
			}
			return append(args, value)
		}
	}
	return append(args, flag, value)
}

// dropUnresolvedPlaceholderArgs 删除仍含 ${...} 的参数（连同一并删除其前置 --flag）。
func dropUnresolvedPlaceholderArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.Contains(arg, "${") {
			if i > 0 && strings.HasPrefix(args[i-1], "--") && len(out) > 0 && out[len(out)-1] == args[i-1] {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}
