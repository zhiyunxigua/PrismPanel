package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type LaunchRequest struct {
	Server          ServerConfig
	Preparation     JoinPreparation
	Account         AccountState
	ProtocolVersion string
}

type LaunchResult struct {
	Preparation JoinPreparation `json:"preparation"`
	JavaPath    string          `json:"java_path"`
	PID         int             `json:"pid"`
	LogPath     string          `json:"log_path"`
}

type versionConfig struct {
	ID                 string `json:"id"`
	JVMArguments       string `json:"jvm_arguments"`
	ParameterArguments string `json:"parameter_arguments"`
	MinecraftArguments string `json:"minecraftArguments"`
	MainClass          string `json:"mainClass"`
}

func LaunchPreparedGame(ctx context.Context, request LaunchRequest, processes *ProcessManager, report func(stage, message string, percent float64)) (LaunchResult, error) {
	if processes == nil {
		return LaunchResult{}, errors.New("game process manager is not configured")
	}
	server := request.Server
	if processes.Running(server.ID) {
		return LaunchResult{}, ErrGameAlreadyRunning
	}
	if server.VersionLabel == "" {
		return LaunchResult{}, errors.New("game version label is required")
	}
	if request.Preparation.GameDir == "" {
		return LaunchResult{}, errors.New("game runtime directory is required")
	}

	report("launch", "\u6b63\u5728\u68c0\u67e5 Java", 96)
	javaPath, err := FindJava(server.Version)
	if err != nil {
		return LaunchResult{}, err
	}

	cfg, err := readVersionConfig(request.Preparation.GameDir, server.VersionLabel)
	if err != nil {
		return LaunchResult{}, err
	}
	if strings.TrimSpace(cfg.ID) == "" {
		cfg.ID = server.VersionLabel
	}

	report("launch", "\u6b63\u5728\u542f\u52a8\u672c\u5730\u8ba4\u8bc1\u670d\u52a1", 96.5)
	localServices, err := StartLocalLauncherServices(ctx, LocalLauncherServicesConfig{Server: server, Account: request.Account})
	if err != nil {
		return LaunchResult{}, err
	}
	servicesAttached := false
	defer func() {
		if !servicesAttached {
			localServices.Close()
		}
	}()

	report("launch", "\u6b63\u5728\u751f\u6210\u542f\u52a8\u53c2\u6570", 97)
	args, err := BuildLaunchArguments(LaunchArgumentInput{
		Config: cfg, Server: server, Account: request.Account, GameDir: request.Preparation.GameDir,
		LauncherControlPort: localServices.AuthPort(), LauncherPort: localServices.RPCPort(), ProtocolVersion: request.ProtocolVersion,
	})
	if err != nil {
		return LaunchResult{}, err
	}
	if err := ensureDefaultOptions(request.Preparation.GameDir); err != nil {
		return LaunchResult{}, err
	}

	logPath, err := DefaultGameLogPath(server)
	if err != nil {
		return LaunchResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return LaunchResult{}, err
	}

	report("launch", "\u6b63\u5728\u542f\u52a8 Java \u8fdb\u7a0b", 99)
	process, err := processes.Start(server.ID, ProcessStartRequest{
		JavaPath: javaPath,
		Args:     args,
		WorkDir:  request.Preparation.GameDir,
		LogPath:  logPath,
		Cleanup:  localServices.Close,
	})
	if err != nil {
		return LaunchResult{}, err
	}
	servicesAttached = true
	return LaunchResult{Preparation: request.Preparation, JavaPath: javaPath, PID: process.PID, LogPath: logPath}, nil
}

func readVersionConfig(gameDir, versionLabel string) (versionConfig, error) {
	path := filepath.Join(gameDir, "versions", versionLabel, versionLabel+".json")
	contents, err := os.ReadFile(path)
	if err != nil {
		return versionConfig{}, fmt.Errorf("read game version json %s: %w", path, err)
	}
	var cfg versionConfig
	if err := json.Unmarshal(contents, &cfg); err != nil {
		return versionConfig{}, fmt.Errorf("decode game version json: %w", err)
	}
	return cfg, nil
}

type LaunchArgumentInput struct {
	Config              versionConfig
	Server              ServerConfig
	Account             AccountState
	GameDir             string
	LauncherControlPort int
	LauncherPort        int
	ProtocolVersion     string
}

func BuildLaunchArguments(input LaunchArgumentInput) ([]string, error) {
	jvmArgs, err := splitCommandLine(input.Config.JVMArguments)
	if err != nil {
		return nil, fmt.Errorf("parse jvm arguments: %w", err)
	}
	gameArgsText := input.Config.ParameterArguments
	if strings.TrimSpace(gameArgsText) == "" {
		gameArgsText = input.Config.MinecraftArguments
	}
	if strings.TrimSpace(gameArgsText) == "" {
		return nil, errors.New("game version json does not contain minecraft arguments")
	}
	gameArgs, err := splitCommandLine(gameArgsText)
	if err != nil {
		return nil, fmt.Errorf("parse minecraft arguments: %w", err)
	}

	mainClass := strings.TrimSpace(input.Config.MainClass)
	if mainClass == "" {
		mainClass = detectMainClass(jvmArgs)
	}
	if mainClass != "" {
		jvmArgs = removeExactArg(jvmArgs, mainClass)
	}
	jvmArgs = removeFlagWithValue(jvmArgs, "-Xmx")
	jvmArgs = append([]string{"-Xmx2048M"}, jvmArgs...)
	jvmArgs = appendOrReplaceJavaProperty(jvmArgs, "launcherControlPort", strconv.Itoa(input.LauncherControlPort))
	jvmArgs = appendOrReplaceJavaProperty(jvmArgs, "launcherGameId", localLauncherGameID(input.Server))
	if input.Account.UserID != "" {
		jvmArgs = appendOrReplaceJavaProperty(jvmArgs, "userId", input.Account.UserID)
	}
	if input.Account.UserToken != "" {
		jvmArgs = appendOrReplaceJavaProperty(jvmArgs, "Token", generateEncryptedToken(input.Account.UserToken))
	}
	jvmArgs = appendOrReplaceJavaProperty(jvmArgs, "Server", "RELEASE")
	nativesPath, runtimePath, err := nativePaths(input.GameDir, input.Server.VersionLabel)
	if err != nil {
		return nil, err
	}
	jvmArgs = appendOrReplaceJavaProperty(jvmArgs, "java.library.path", nativesPath)
	jvmArgs = appendOrReplaceJavaProperty(jvmArgs, "runtime_path", runtimePath)
	if mainClass != "" {
		jvmArgs = append(jvmArgs, mainClass)
	}

	uuid := generateRoleUUID(input.Server.Username, input.Account.UserID)
	replacements := map[string]string{
		"${auth_player_name}":  input.Server.Username,
		"${version_name}":      input.Server.VersionLabel,
		"${game_directory}":    ".",
		"${assets_root}":       "assets",
		"${assets_index_name}": input.Config.ID,
		"${auth_uuid}":         uuid,
		"${auth_access_token}": accessTokenForVersion(input.Server.Version),
		"${clientid}":          "0",
		"${auth_xuid}":         input.Account.UserID,
		"${user_type}":         "msa",
		"${version_type}":      "release",
	}
	gameArgs = replacePlaceholders(gameArgs, replacements)
	gameArgs = upsertFlagValue(gameArgs, "--gameDir", ".")
	gameArgs = upsertFlagValue(gameArgs, "--assetsDir", "assets")
	gameArgs = upsertFlagValue(gameArgs, "--server", input.Server.IP)
	gameArgs = upsertFlagValue(gameArgs, "--port", strconv.Itoa(input.Server.Port))
	gameArgs = upsertFlagValue(gameArgs, "--userProperties", buildUserProperties(input))
	gameArgs = upsertFlagValue(gameArgs, "--userPropertiesEx", buildUserPropertiesEx(input.ProtocolVersion))
	gameArgs = dropUnresolvedPlaceholderArgs(gameArgs)

	return append(jvmArgs, gameArgs...), nil
}

func nativePaths(gameDir, versionLabel string) (string, string, error) {
	gameDir = strings.TrimSpace(gameDir)
	if gameDir == "" {
		gameDir = "."
	}
	absGameDir, err := filepath.Abs(gameDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve game directory: %w", err)
	}
	nativesPath := filepath.Join(absGameDir, "versions", versionLabel, "natives-windows-x86_64")
	return nativesPath, filepath.Join(nativesPath, "runtime"), nil
}
func FindJava(version Version) (string, error) {
	candidates := javaCandidates(version)
	for _, candidate := range candidates {
		candidate = strings.Trim(strings.TrimSpace(candidate), "\"")
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath(javaExeName()); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("\u672a\u627e\u5230\u672c\u5730 Java\uff0c\u8bf7\u8bbe\u7f6e PRISMPANEL_JAVA_PATH \u6216 JAVA_HOME\uff1b\u5f53\u524d\u7248\u672c %s \u9700\u8981 %s", mustVersionLabel(version), javaRequirement(version))
}

func javaCandidates(version Version) []string {
	javaExe := javaExeName()
	var candidates []string
	if value := os.Getenv("PRISMPANEL_JAVA_PATH"); value != "" {
		candidates = append(candidates, value)
	}
	if value := os.Getenv("JAVA_HOME"); value != "" {
		candidates = append(candidates, filepath.Join(value, "bin", javaExe))
	}
	if home := os.Getenv("ProgramFiles"); home != "" {
		candidates = append(candidates,
			filepath.Join(home, "Java", "jdk-21", "bin", javaExe),
			filepath.Join(home, "Java", "jdk-17", "bin", javaExe),
		)
	}
	return candidates
}

func javaExeName() string {
	if runtime.GOOS == "windows" {
		return "java.exe"
	}
	return "java"
}

func javaRequirement(version Version) string {
	if version >= Version1_20_6 {
		return "JDK 21"
	}
	if version >= Version1_16 {
		return "JDK 17"
	}
	return "JRE 8"
}

func mustVersionLabel(version Version) string {
	label, err := VersionLabel(version)
	if err != nil {
		return fmt.Sprintf("%d", version)
	}
	return label
}

func ensureDefaultOptions(gameDir string) error {
	path := filepath.Join(gameDir, "options.txt")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, []byte("guiScale:2\nlang:zh_cn\nmaxFps:120\n"), 0o644)
}

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

func appendOrReplaceJavaProperty(args []string, key, value string) []string {
	prefix := "-D" + key + "="
	for i, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			args[i] = prefix + value
			return args
		}
	}
	return append(args, prefix+value)
}

func containsArg(args []string, value string) bool {
	for _, arg := range args {
		if arg == value {
			return true
		}
	}
	return false
}

func detectMainClass(args []string) string {
	for _, arg := range args {
		if strings.Contains(arg, "/") || strings.Contains(arg, "\\") || strings.Contains(arg, string(os.PathSeparator)) {
			continue
		}
		if strings.Contains(arg, ".") && !strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") {
			return arg
		}
	}
	return ""
}

func removeExactArg(args []string, value string) []string {
	out := args[:0]
	for _, arg := range args {
		if arg == value {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func localLauncherGameID(server ServerConfig) string {
	if value := strings.TrimSpace(server.GameID); value != "" {
		return value
	}
	return "0"
}

func buildUserProperties(input LaunchArgumentInput) string {
	protocolVersion := strings.TrimSpace(input.ProtocolVersion)
	if protocolVersion == "" {
		protocolVersion = "0"
	}
	uid := numericJSONValue(input.Account.UserID)
	gameID := numericJSONValue(localLauncherGameID(input.Server))
	encoded, _ := json.Marshal(map[string]any{
		"uid":           []any{uid, 0},
		"gameid":        []any{gameID, 0},
		"launcherport":  []any{input.LauncherPort, 0},
		"filterkey":     []any{randomLowerAlpha(32), "0"},
		"filterpath":    []any{"", "0"},
		"timedelta":     []any{0, 0},
		"launchversion": []any{protocolVersion, "0"},
	})
	return string(encoded)
}

func buildUserPropertiesEx(protocolVersion string) string {
	protocolVersion = strings.TrimSpace(protocolVersion)
	if protocolVersion == "" {
		protocolVersion = "0"
	}
	encoded, _ := json.Marshal(map[string]any{
		"GameType":        2,
		"channel":         "netease",
		"isFilter":        true,
		"launcherVersion": protocolVersion,
		"timedelta":       0,
	})
	return string(encoded)
}

func numericJSONValue(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return json.Number("0")
	}
	if _, err := strconv.ParseInt(value, 10, 64); err == nil {
		return json.Number(value)
	}
	return value
}

func randomLowerAlpha(length int) string {
	return strings.ToLower(randomUpperAlnum(length))
}

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

func accessTokenForVersion(version Version) string {
	if version >= Version1_18 {
		return "0"
	}
	return randomUpperAlnum(32)
}
