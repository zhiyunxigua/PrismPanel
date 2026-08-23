package game

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

type mcLaunchProfile struct {
	GameID             string // 当前版本 id（fabric 时为 fabric-loader-...）
	InheritsFrom       string // 空表示 base 版本
	MainClass          string
	MinecraftArguments string   // 旧版参数（1.13 前）
	GameArguments      []string // 新版 arguments.game 已过滤并规范化
	JVMArguments       []string // 新版 arguments.jvm 已过滤并规范化
	LibraryPaths       []string
	ClientJar          string
	AssetIndexID       string
	NativesDir         string
}

// ResolveMCLaunchProfile 从本地磁盘解析启动配置（支持 Fabric 继承 base）。
func ResolveMCLaunchProfile(versionID string) (*mcLaunchProfile, error) {
	mcDir, err := MCMinecraftDir(versionID)
	if err != nil {
		return nil, err
	}
	versionJSON, err := decodeVersionJSON(mcDir, versionID)
	if err != nil {
		// 区分「版本未安装」与「版本 JSON 损坏」，给出明确提示（参照 PCL2 McLaunchPrecheck）
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("游戏版本 %s 未安装，请先下载安装", versionID)
		}
		return nil, fmt.Errorf("游戏版本 %s 的版本 JSON 损坏，建议重新安装：%w", versionID, err)
	}
	inherits := versionJSON.InheritsFrom
	if inherits != "" {
		inherits = strings.TrimSpace(inherits)
	}
	profile := &mcLaunchProfile{
		GameID: versionID, InheritsFrom: inherits, MainClass: versionJSON.MainClass,
		MinecraftArguments: versionJSON.MinecraftArguments,
	}

	// 继承 base 的 client / assets / 库
	baseID := versionID
	if inherits != "" {
		baseID = inherits
	}
	baseDir, err := MCMinecraftDir(baseID)
	if err != nil {
		return nil, err
	}
	baseVersion, err := decodeVersionJSON(baseDir, baseID)
	if err != nil {
		return nil, fmt.Errorf("基础版本 %s 未安装：%w", baseID, err)
	}
	profile.ClientJar = filepath.Join(baseDir, "versions", baseID, baseID+".jar")
	profile.AssetIndexID = baseVersion.AssetIndex.ID
	if profile.AssetIndexID == "" {
		profile.AssetIndexID = baseID
	}
	profile.NativesDir = filepath.Join(baseDir, "versions", "natives")

	// base 库
	profile.LibraryPaths = append(profile.LibraryPaths, mcLibraryPaths(baseDir, baseVersion.Libraries)...)
	// base client jar
	profile.LibraryPaths = append(profile.LibraryPaths, profile.ClientJar)

	// 若为 Fabric，追加 Fabric 库并覆盖 mainClass / 参数
	if inherits != "" {
		profile.LibraryPaths = append(profile.LibraryPaths, fabricLibraryPaths(mcDir, versionJSON.Libraries)...)
	}

	// 参数：优先使用当前版本 arguments.game（Fabric 通常继承，为空时用 base 的）
	gameArgs := versionJSON.ArgumentGame
	if len(gameArgs) == 0 {
		gameArgs = baseVersion.ArgumentGame
	}
	if len(gameArgs) > 0 {
		profile.GameArguments = filterMCArguments(gameArgs)
	}
	jvmArgs := versionJSON.ArgumentJVM
	if len(jvmArgs) == 0 {
		jvmArgs = baseVersion.ArgumentJVM
	}
	if len(jvmArgs) > 0 {
		profile.JVMArguments = filterMCArguments(jvmArgs)
	}
	if profile.MainClass == "" {
		profile.MainClass = baseVersion.MainClass
	}
	if strings.TrimSpace(profile.MinecraftArguments) == "" {
		profile.MinecraftArguments = baseVersion.MinecraftArguments
	}
	if strings.TrimSpace(profile.MinecraftArguments) != "" && len(profile.GameArguments) == 0 {
		args, err := splitCommandLine(profile.MinecraftArguments)
		if err != nil {
			return nil, err
		}
		profile.GameArguments = args
	}
	return profile, nil
}

func mcLibraryPaths(mcDir string, libraries []mcLibrary) []string {
	var paths []string
	for _, library := range libraries {
		if !mcLibraryAllowed(library) {
			continue
		}
		if _, hasNative := library.Downloads.Classifiers[nativeClassifier()]; hasNative {
			continue
		}
		if library.Downloads.Artifact.Path == "" {
			continue
		}
		paths = append(paths, filepath.Join(mcDir, "libraries", filepath.FromSlash(library.Downloads.Artifact.Path)))
	}
	return paths
}

func fabricLibraryPaths(mcDir string, libraries []mcLibrary) []string {
	var paths []string
	for _, library := range libraries {
		if library.Name == "" {
			continue
		}
		paths = append(paths, filepath.Join(mcDir, "libraries", filepath.FromSlash(fabricLibraryPath(library.Name))))
	}
	return paths
}

type mcArgument struct {
	Rules []mcRule        `json:"rules"`
	Value json.RawMessage `json:"value"`
}

func filterMCArguments(raw []json.RawMessage) []string {
	var result []string
	for _, item := range raw {
		var argument mcArgument
		if err := json.Unmarshal(item, &argument); err == nil && len(argument.Rules) > 0 {
			if !mcRulesAllow(argument.Rules) {
				continue
			}
			var values []string
			if json.Unmarshal(argument.Value, &values) == nil {
				result = append(result, values...)
				continue
			}
			var single string
			if json.Unmarshal(argument.Value, &single) == nil {
				result = append(result, single)
			}
			continue
		}
		var plain string
		if json.Unmarshal(item, &plain) == nil {
			// 防御：版本 JSON 若以纯字符串形式声明 --demo（试玩参数），一律剔除
			if plain == "--demo" {
				continue
			}
			result = append(result, plain)
		}
	}
	return result
}

func mcRulesAllow(rules []mcRule) bool {
	if len(rules) == 0 {
		return true
	}
	allowed := false
	for _, rule := range rules {
		matches := rule.OS.Name == "" || rule.OS.Name == runtime.GOOS
		if rule.OS.Arch != "" {
			matches = matches && rule.OS.Arch == runtime.GOARCH
		}
		// features.is_demo_user：启动器永远不启动试玩（demo）账号，因此要求
		// is_demo_user=true 的规则（如 26.2+ 版本 JSON 中 "--demo" 参数的规则）
		// 一律不匹配，避免把 --demo 传进游戏导致离线模式进入试玩。
		if rule.Features != nil && rule.Features.IsDemoUser != nil && *rule.Features.IsDemoUser {
			matches = false
		}
		if !matches {
			continue
		}
		allowed = rule.Action == "allow"
	}
	return allowed
}

type mcRule struct {
	Action string `json:"action"`
	OS     struct {
		Name string `json:"name"`
		Arch string `json:"arch"`
	} `json:"os"`
	Features *struct {
		IsDemoUser *bool `json:"is_demo_user"`
	} `json:"features"`
}

// mcContainsNonASCII 判断字符串是否含非 ASCII 字符（中文用户名/路径等）。
func mcContainsNonASCII(s string) bool {
	for _, r := range s {
		if r > 0x7F {
			return true
		}
	}
	return false
}

// mcLaunchNeedsEncoding 判断本次启动是否需要注入编码参数：
// 用户名、游戏目录、natives 目录或 client jar 含非 ASCII 时注入（对齐 PCL2 的中文场景处理）。
func mcLaunchNeedsEncoding(account MCAccount, gameDir string, profile *mcLaunchProfile) bool {
	if mcContainsNonASCII(account.Name) || mcContainsNonASCII(gameDir) {
		return true
	}
	if profile != nil {
		if mcContainsNonASCII(profile.NativesDir) || mcContainsNonASCII(profile.ClientJar) {
			return true
		}
	}
	return false
}

// mcEncodingJVMArgs 中文/非 ASCII 场景的 JVM 编码参数（对齐 PCL2 ModLaunch.vb L1366-1389，#12）：
//   - Java <19：显式 -Dsun.stdout.encoding / -Dsun.stderr.encoding 为 UTF-8，防止控制台中文乱码；
//   - Java 18-20：额外 -Dfile.encoding=COMPAT（保持旧版 ANSI 行为，避免 JEP 400 默认改 UTF-8 破坏老版本）；
//   - Java 21+：默认 UTF-8，无需处理（返回空）。
//
// 注意：--username 等参数值由 Go 在 Windows 下以 UTF-16 原生传给进程（CreateProcessW），
// 不经本地代码页转换，JVM 侧得到的是完整 UTF-8 字符串；此处只补 stdout/stderr 与 file.encoding 一致性。
func mcEncodingJVMArgs(javaMajor int) []string {
	var args []string
	if javaMajor >= 21 {
		return nil // Java 21+ 默认 UTF-8
	}
	if javaMajor >= 18 {
		args = append(args, "-Dfile.encoding=COMPAT")
	}
	if javaMajor >= 19 {
		args = append(args, "-Dstdout.encoding=UTF-8", "-Dstderr.encoding=UTF-8")
	} else {
		args = append(args, "-Dsun.stdout.encoding=UTF-8", "-Dsun.stderr.encoding=UTF-8")
	}
	return args
}

// BuildMCLaunchArgs 拼接 JVM 与游戏参数（参照 PCL2 的启动方式）。
// 关键点：版本 JSON 中 arguments.jvm 里的占位符（${classpath}、${natives_directory} 等）
// 也必须替换，否则会以字面量形式传给 Java，导致「找不到主类」等启动失败。
// javaMajor 为实际启动的 Java 大版本（用于 #12 中文用户名/路径的编码参数选择）。
func BuildMCLaunchArgs(profile *mcLaunchProfile, account MCAccount, req MCLaunchRequest, gameDir string, extraJVM []string, javaMajor int) ([]string, error) {
	if profile == nil {
		return nil, errors.New("launch profile is empty")
	}
	classpath := strings.Join(profile.LibraryPaths, string(os.PathListSeparator))

	replacements := map[string]string{
		"${auth_player_name}":    account.Name,
		"${version_name}":        profile.GameID,
		"${game_directory}":      gameDir,
		"${assets_root}":         filepath.Join(gameDir, "assets"),
		"${assets_index_name}":   profile.AssetIndexID,
		"${auth_uuid}":           account.UUID,
		"${auth_access_token}":   account.AccessToken,
		"${auth_session}":        account.AccessToken,
		"${clientid}":            "0",
		"${auth_xuid}":           "0",
		"${user_type}":           mcUserType(account),
		"${version_type}":        "release",
		"${natives_directory}":   profile.NativesDir,
		"${launcher_name}":       "PrismPanel",
		"${launcher_version}":    "1.0.0",
		"${classpath}":           classpath,
		"${classpath_separator}": string(os.PathListSeparator),
		"${library_directory}":   filepath.Join(gameDir, "libraries"),
		"${libraries_directory}": filepath.Join(gameDir, "libraries"),
		"${primary_jar}":         profile.ClientJar,
		"${user_properties}":     "{}",
		"${game_assets}":         filepath.Join(gameDir, "assets", "virtual", "legacy"),
	}

	// JVM 参数：直接采用版本 JSON 的 arguments.jvm（已按规则过滤），并替换其中的占位符。
	// 这样 ${classpath} 等会被正确替换，而不是被丢弃后手动补一个 -cp。
	jvmArgs := replacePlaceholders(profile.JVMArguments, replacements)
	// 防御（researcher 建议）：JVM 参数段残留的未解析占位符（如未来版本 JSON 引入新占位符）
	// 不应以字面量传给 Java——与游戏参数一致地丢弃（含其前置 --flag）。
	jvmArgs = dropUnresolvedPlaceholderArgs(jvmArgs)

	// 旧版（1.13 前）没有 arguments.jvm，使用经典 JVM 参数
	if len(jvmArgs) == 0 {
		jvmArgs = []string{
			"-XX:HeapDumpPath=MojangTricksIntelDriversForPerformance_javaw.exe_minecraft.exe.heapdump",
			"-Djava.library.path=" + profile.NativesDir,
			"-cp",
			classpath,
		}
	} else {
		// 兜底：确保 natives 与 classpath 参数存在（防止个别版本 JSON 缺失导致崩溃）
		if !hasArgPrefix(jvmArgs, "-Djava.library.path") {
			jvmArgs = append(jvmArgs, "-Djava.library.path="+profile.NativesDir)
		}
		if !hasArg(jvmArgs, "-cp") {
			jvmArgs = append(jvmArgs, "-cp", classpath)
		}
	}

	// 第三方认证服务器：注入 authlib-injector（位于最前）
	if isMCAuthlibThirdParty(account) {
		agent, err := mcAuthlibInjectorAgent(account)
		if err != nil {
			return nil, err
		}
		jvmArgs = append([]string{agent}, jvmArgs...)
	}

	// 内存：去掉 JSON 里可能存在的 -Xmx，统一用我们的值放在最前
	jvmArgs = removeFlagWithValue(jvmArgs, "-Xmx")
	jvmArgs = append([]string{"-Xmx" + strconv.Itoa(req.MaxMemoryMB) + "M"}, jvmArgs...)

	// 编码参数（#12，对齐 PCL2 ModLaunch.vb L1366-1389）：用户名/路径含非 ASCII 时，
	// 显式指定 stdout/stderr 编码为 UTF-8 并按 Java 大版本处理 file.encoding 兼容，防止中文乱码。
	// --username 值本身经 Windows UTF-16 原生参数传递（不经本地代码页），此处在主类前补 JVM 参数。
	if mcLaunchNeedsEncoding(account, gameDir, profile) {
		jvmArgs = append(jvmArgs, mcEncodingJVMArgs(javaMajor)...)
	}

	// 版本特定设置的额外 JVM 参数
	for _, arg := range extraJVM {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		jvmArgs = append(jvmArgs, arg)
	}

	// 参数去重（移植 PCL2 DeduplicateJavaArguments）：单参数完全重复删除；
	// 游戏参数键值对后者覆盖前者，JVM 参数仅去重完全相同的对。
	// 必须在追加主类之前做，避免把主类误当参数处理。
	jvmArgs = dedupeLaunchArgs(jvmArgs, true)

	// 主类（放在所有 JVM 参数之后）
	jvmArgs = append(jvmArgs, profile.MainClass)

	// 游戏参数：替换占位符、去掉未解析占位符、覆盖关键参数
	gameArgs := replacePlaceholders(profile.GameArguments, replacements)
	gameArgs = dropUnresolvedPlaceholderArgs(gameArgs)
	gameArgs = upsertFlagValue(gameArgs, "--gameDir", gameDir)
	gameArgs = upsertFlagValue(gameArgs, "--assetsDir", filepath.Join(gameDir, "assets"))
	gameArgs = upsertFlagValue(gameArgs, "--assetIndex", profile.AssetIndexID)
	gameArgs = upsertFlagValue(gameArgs, "--username", account.Name)
	gameArgs = upsertFlagValue(gameArgs, "--uuid", account.UUID)
	gameArgs = upsertFlagValue(gameArgs, "--accessToken", account.AccessToken)
	gameArgs = upsertFlagValue(gameArgs, "--userType", mcUserType(account))
	gameArgs = upsertFlagValue(gameArgs, "--version", profile.GameID)
	if req.Width > 0 {
		gameArgs = upsertFlagValue(gameArgs, "--width", strconv.Itoa(req.Width))
	}
	if req.Height > 0 {
		gameArgs = upsertFlagValue(gameArgs, "--height", strconv.Itoa(req.Height))
	}
	// 自动进服：1.20.2+（releaseTime > 2023-04-04）用 QuickPlay 单参数 host:port（参照 PCL2），
	// 老版本继续用 --server/--port。
	if req.ServerIP != "" {
		if mcSupportsQuickPlay(profile.GameID) {
			port := req.ServerPort
			if port <= 0 {
				port = 25565
			}
			gameArgs = upsertFlagValue(gameArgs, "--quickPlayMultiplayer", fmt.Sprintf("%s:%d", req.ServerIP, port))
		} else {
			gameArgs = upsertFlagValue(gameArgs, "--server", req.ServerIP)
			gameArgs = upsertFlagValue(gameArgs, "--port", strconv.Itoa(req.ServerPort))
		}
	}
	gameArgs = dedupeLaunchArgs(gameArgs, false)
	// 防御：无论规则如何处理，最终启动参数绝不允许出现 --demo（启动器不支持试玩账号，
	// 离线/正版用户传 --demo 都会进入试玩模式、无法进入多人游戏）。
	filtered := gameArgs[:0]
	for _, arg := range gameArgs {
		if arg == "--demo" {
			continue
		}
		filtered = append(filtered, arg)
	}
	gameArgs = filtered
	return append(jvmArgs, gameArgs...), nil
}

// mcSupportsQuickPlay 判断版本是否支持 QuickPlay（1.20.2+，对应 PCL2 的 ReleaseTime > 2023-04-04 判定）。
// 支持 fabric-loader-<loader>-<mc> 形式的版本 id。
func mcSupportsQuickPlay(versionID string) bool {
	id := mcVanillaVersion(versionID)
	id = strings.TrimPrefix(strings.TrimSpace(id), "v")
	if strings.HasPrefix(id, "1.") {
		parts := strings.Split(id, ".")
		if len(parts) >= 2 {
			if minor, err := strconv.Atoi(parts[1]); err == nil {
				if minor > 20 {
					return true
				}
				if minor == 20 && len(parts) >= 3 {
					if patch, err := strconv.Atoi(strings.SplitN(parts[2], "-", 2)[0]); err == nil {
						return patch >= 2
					}
				}
				return false
			}
		}
		return false
	}
	if strings.HasPrefix(id, "2.") {
		return true
	}
	// 快照：QuickPlay 自 23w31a（2023-08）起引入，23w01a~23w30a 的早期快照不支持。
	// 精确判定：year==23 时要求 week>=31；year>23 一律支持。
	if matched, _ := regexp.MatchString(`^[0-9]{2}w[0-9]{2}[a-z]$`, id); matched {
		if year, err := strconv.Atoi(id[:2]); err == nil {
			if year > 23 {
				return true
			}
			if year == 23 {
				if week, err := strconv.Atoi(id[3:5]); err == nil {
					return week >= 31
				}
			}
		}
	}
	return false
}

func mcUserType(account MCAccount) string {
	if account.Mode == MCAuthMicrosoft {
		return "msa"
	}
	return "legacy"
}

// dedupeLaunchArgs 参数去重（移植 PCL2 DeduplicateJavaArguments，ModLaunch.vb L1613-1651）：
//   - 单参数：完全重复的删除（保留第一个）；
//   - 键值对（"--flag value"）：游戏参数用新值覆盖旧值（保留首个位置，--tweakClass 除外）；
//     JVM 参数仅当键值完全相同才去重，不同值的重复对保留；
//   - "-xPos 23 -xPos -50" 这类负数值不会被误判为参数（- 开头但首个 - 后是数字）。
func dedupeLaunchArgs(args []string, isJVM bool) []string {
	result := make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		key := args[i]
		// 单参数判定：非 - 开头，或下一个缺失 / 下一个是参数样式（- 开头且首个 - 后的数字部分为 0）
		nextIsFlag := i+1 < len(args) && strings.HasPrefix(args[i+1], "-") && mcValAfterFirstDash(args[i+1]) == 0
		if !strings.HasPrefix(key, "-") || i+1 >= len(args) || nextIsFlag {
			i++
			if containsString(result, key) {
				continue
			}
			result = append(result, key)
			continue
		}
		// 以空格间隔的键值对
		value := args[i+1]
		i += 2
		found := false
		for j := 0; j < len(result); j++ {
			if result[j] != key {
				continue
			}
			if !isJVM && key != "--tweakClass" {
				// 游戏参数：用新值覆盖旧值（保持位置，避免多个相同键导致参数双双失效）
				result[j+1] = value
				found = true
				break
			}
			// JVM 参数：键和值完全相同才抛弃
			if j+1 < len(result) && result[j+1] == value {
				found = true
				break
			}
		}
		if !found {
			result = append(result, key, value)
		}
	}
	return result
}

// mcValAfterFirstDash 返回首个 - 之后字符串前导数字的解析值（对应 VB Val()）：
// "-width" → 0、"--foo" → 0、"-50" → 50、"-5abc" → 5。
func mcValAfterFirstDash(s string) int {
	idx := strings.Index(s, "-")
	if idx < 0 {
		return 0
	}
	num := 0
	for _, r := range s[idx+1:] {
		if r < '0' || r > '9' {
			break
		}
		num = num*10 + int(r-'0')
	}
	return num
}

// maskLaunchArgs 打码启动参数中的敏感值（accessToken/uuid 等），用于日志与前端展示（PCL2 FilterAccessToken 思路）。
func maskLaunchArgs(args []string, secrets ...string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	for i, arg := range args {
		masked := arg
		for _, secret := range secrets {
			if secret == "" {
				continue
			}
			masked = strings.ReplaceAll(masked, secret, "***")
		}
		out[i] = masked
	}
	return out
}

func hasArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func hasArgPrefix(args []string, prefix string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
} // FindMCJavaForVersion 按游戏版本选择 Java（本地缓存优先，其次环境变量/系统安装的 JDK）。
func FindMCJavaForVersion(versionID string) (string, error) {
	if cached := findJavaInStore(versionID); cached != "" {
		return cached, nil
	}
	required := mcJavaRequirement(versionID)
	javaExe := javaExeName()
	candidates := []string{}
	if settings, ok := loadCachedSettings(); ok && strings.TrimSpace(settings.DefaultJava) != "" {
		candidates = append(candidates, strings.TrimSpace(settings.DefaultJava))
	}
	if value := strings.TrimSpace(os.Getenv("PRISMPANEL_JAVA_PATH")); value != "" {
		candidates = append(candidates, value)
	}
	if value := strings.TrimSpace(os.Getenv("JAVA_HOME")); value != "" {
		candidates = append(candidates, filepath.Join(value, "bin", javaExe))
	}
	// 扫描官方启动器自带 runtime（装了官启的用户不再重复下载一份 Java）
	if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
		official := filepath.Join(local, "Packages", "Microsoft.4297127D64EC6_8wekyb3d8bbwe", "LocalCache", "Local", "runtime")
		if found := scanJavaRuntimeDirs(official, required); found != "" {
			return found, nil
		}
	}
	// 扫描自身下载的运行时缓存（其它组件的 Java 可能满足所需大版本）
	if root, err := mcJavaRoot(); err == nil {
		if found := scanJavaRuntimeDirs(root, required); found != "" {
			return found, nil
		}
	}
	// 扫描版本目录内的 runtime（部分用户手动放置）
	if mcDir, err := MCMinecraftDir(versionID); err == nil {
		if found := scanJavaRuntimeDirs(filepath.Join(mcDir, "runtime"), required); found != "" {
			return found, nil
		}
	}
	// 扫描常见 JDK 安装目录下的任意版本（满足所需大版本即可，避免无谓下载）
	for _, root := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		base := os.Getenv(root)
		if base == "" {
			continue
		}
		for _, sub := range []string{"Java", "Eclipse Adoptium", "Microsoft", "Zulu", "Amazon Corretto", "BellSoft"} {
			dir := filepath.Join(base, sub)
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				candidate := filepath.Join(dir, entry.Name(), "bin", javaExe)
				if mcJavaSatisfies(candidate, required) {
					return candidate, nil
				}
			}
		}
	}
	for _, candidate := range candidates {
		candidate = strings.Trim(strings.TrimSpace(candidate), "\"")
		if candidate == "" {
			continue
		}
		if mcJavaSatisfies(candidate, required) {
			return candidate, nil
		}
	}
	if path, err := exec.LookPath(javaExe); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("未找到本地 Java，将自动下载；也可设置 PRISMPANEL_JAVA_PATH 或 JAVA_HOME；版本 %s 需要 JDK %s", versionID, required)
}

// scanJavaRuntimeDirs 扫描 Java 运行时目录树（<root>/<组件>/bin 与 <root>/<平台>/<组件>/bin 两种布局），
// 返回第一个满足所需大版本的 java 可执行文件。
func scanJavaRuntimeDirs(root, required string) string {
	javaExe := javaExeName()
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		level1 := filepath.Join(root, entry.Name())
		if candidate := filepath.Join(level1, "bin", javaExe); mcJavaSatisfies(candidate, required) {
			return candidate
		}
		subs, err := os.ReadDir(level1)
		if err != nil {
			continue
		}
		for _, sub := range subs {
			if !sub.IsDir() {
				continue
			}
			candidate := filepath.Join(level1, sub.Name(), "bin", javaExe)
			if mcJavaSatisfies(candidate, required) {
				return candidate
			}
		}
	}
	return ""
}

// mcJavaSatisfies 校验可执行文件存在且 Java 大版本满足要求。
func mcJavaSatisfies(candidate, required string) bool {
	if info, err := os.Stat(candidate); err != nil || info.IsDir() {
		return false
	}
	version, err := detectJavaVersion(candidate)
	if err != nil {
		// 无法探测版本时按存在处理（避免因探测失败而跳过可用 Java）
		return true
	}
	return version >= javaMajorNumber(required)
}

func javaMajorNumber(required string) int {
	value, err := strconv.Atoi(required)
	if err != nil {
		return 17
	}
	return value
}

// detectJavaVersion 运行 java -version 解析大版本号；失败返回错误。
func detectJavaVersion(javaPath string) (int, error) {
	out, err := exec.Command(javaPath, "-version").CombinedOutput()
	if err != nil {
		return 0, err
	}
	text := string(out)
	// 形如 "openjdk version \"25.0.4\" 2025" / "java version \"1.8.0_402\"" / "21.0.2"
	matches := javaVersionPattern.FindStringSubmatch(text)
	if len(matches) < 2 {
		return 0, fmt.Errorf("无法解析 Java 版本: %s", text)
	}
	versionText := matches[1]
	if strings.HasPrefix(versionText, "1.") {
		parts := strings.Split(versionText, ".")
		if len(parts) >= 2 {
			if major, err := strconv.Atoi(parts[1]); err == nil {
				return major, nil
			}
		}
	}
	parts := strings.Split(versionText, ".")
	if len(parts) >= 1 {
		if major, err := strconv.Atoi(parts[0]); err == nil {
			return major, nil
		}
	}
	return 0, fmt.Errorf("无法解析 Java 版本: %s", versionText)
}

var javaVersionPattern = regexp.MustCompile(`version "([^"]+)"`)

// mcJavaRequirement 返回该版本所需的 Java 大版本（8/16/17/21...）。
// 优先使用版本 JSON 的 javaVersion.majorVersion（权威），否则按 MC 版本号估算
// （对齐 PCL2 GetJavaRequirement：1.20.5+→21、1.18pre2+→17、1.17+→16、1.12+→8）。
func mcJavaRequirement(versionID string) string {
	if major, _ := mcRequiredJavaInfo(versionID); major > 0 {
		return strconv.Itoa(major)
	}
	mcVersion := mcVanillaVersion(versionID)
	trimmed := strings.TrimPrefix(strings.TrimSpace(mcVersion), "v")
	if strings.HasPrefix(trimmed, "1.") {
		parts := strings.Split(trimmed, ".")
		if len(parts) >= 2 {
			if minor, err := strconv.Atoi(parts[1]); err == nil {
				if minor >= 21 {
					return "21"
				}
				if minor == 20 {
					// 1.20.5+ 需要 Java 21，1.20.0-1.20.4 需要 Java 17
					if len(parts) >= 3 {
						if patch, err := strconv.Atoi(strings.SplitN(parts[2], "-", 2)[0]); err == nil && patch >= 5 {
							return "21"
						}
					}
					return "17"
				}
				if minor >= 18 {
					return "17"
				}
				if minor == 17 {
					return "16"
				}
				return "8" // 1.12-1.16（含 1.16.5）
			}
		}
	}
	if strings.HasPrefix(trimmed, "2.") {
		return "21"
	}
	// 快照（如 24w14a）：23w31a（2023-08）后需要 Java 21
	if matched, _ := regexp.MatchString(`^[0-9]{2}w[0-9]{2}[a-z]$`, trimmed); matched {
		if year, err := strconv.Atoi(trimmed[:2]); err == nil && year >= 23 {
			return "21"
		}
	}
	return "17"
}

// mcVanillaVersion 从版本 id 中提取原版 MC 版本号（兼容 fabric-loader-<loader>-<mc>）。
func mcVanillaVersion(versionID string) string {
	id := strings.TrimSpace(versionID)
	if index := strings.LastIndex(id, "-"); index >= 0 {
		suffix := id[index+1:]
		// 尾部段形如 x.y 或 x.y.z 时视为 MC 版本
		parts := strings.Split(suffix, ".")
		if len(parts) >= 2 {
			allNumeric := true
			for _, part := range parts {
				if _, err := strconv.Atoi(part); err != nil {
					allNumeric = false
					break
				}
			}
			if allNumeric {
				return suffix
			}
		}
	}
	return id
}

// LaunchMC 国际版启动：合并实例目录、拼接参数、启动进程。
func LaunchMC(ctx context.Context, req MCLaunchRequest, account MCAccount, processes *ProcessManager, report func(stage, message string, percent float64)) (LaunchResult, error) {
	// 全局默认内存（请求未指定时）
	if req.MaxMemoryMB <= 0 {
		if settings, ok := loadCachedSettings(); ok && settings.DefaultMemoryMB > 0 {
			req.MaxMemoryMB = settings.DefaultMemoryMB
		}
	}
	req = req.normalized()
	if processes == nil {
		return LaunchResult{}, errors.New("game process manager is not configured")
	}
	if processes.Running(mcProcessID(req.VersionID)) {
		return LaunchResult{}, ErrGameAlreadyRunning
	}

	// 读取版本特定设置并作为默认值（请求中未显式指定时生效）
	var extraJVM []string
	var versionJavaPath string
	var versionWidth, versionHeight int
	launchVersion := req.VersionID
	if settings, ok, err := LoadMCVersionSettings(req.VersionID); err == nil && ok {
		if settings.ServerIP != "" {
			req.ServerIP = settings.ServerIP
		}
		if settings.ServerPort > 0 {
			req.ServerPort = settings.ServerPort
		}
		if settings.MaxMemoryMB > 0 {
			req.MaxMemoryMB = settings.MaxMemoryMB
		}
		if settings.InstanceDir != "" {
			req.InstanceDir = settings.InstanceDir
		}
		if settings.JVMArgs != "" {
			if args, err := splitCommandLine(settings.JVMArgs); err == nil {
				extraJVM = args
			}
		}
		versionJavaPath = strings.TrimSpace(settings.JavaPath)
		versionWidth, versionHeight = settings.Width, settings.Height
		if versionWidth > 0 {
			req.Width = versionWidth
		}
		if versionHeight > 0 {
			req.Height = versionHeight
		}
		// 每版本启动版本：显式 LaunchVersion 优先，否则 UseFabric 自动用该基础的 Fabric 子版本。
		// 注意：req.VersionID 保持用户选择的版本不变（作为进程标识），launchVersion 才是真正解析的版本。
		if strings.TrimSpace(settings.LaunchVersion) != "" {
			launchVersion = strings.TrimSpace(settings.LaunchVersion)
		} else if settings.UseFabric && !strings.HasPrefix(req.VersionID, "fabric-loader-") {
			if fabric := MCFabricVersionFor(req.VersionID); fabric != "" {
				launchVersion = fabric
			}
		}
	}
	if report != nil {
		report("prepare", "正在解析启动配置", 60)
	}
	profile, err := ResolveMCLaunchProfile(launchVersion)
	if err != nil {
		return LaunchResult{}, err
	}
	mcDir, err := MCMinecraftDir(launchVersion)
	if err != nil {
		return LaunchResult{}, err
	}
	// 预检测（参照 PCL2 McLaunchPrecheck）：路径含 !/; 会导致 Java 参数解析异常，直接拒绝启动
	if strings.ContainsAny(mcDir, "!;") {
		return LaunchResult{}, fmt.Errorf("游戏目录路径包含非法字符 ! 或 ;（%s），请更换游戏目录后再启动", mcDir)
	}
	if strings.TrimSpace(req.InstanceDir) != "" && strings.ContainsAny(req.InstanceDir, "!;") {
		return LaunchResult{}, fmt.Errorf("实例目录路径包含非法字符 ! 或 ;（%s）", req.InstanceDir)
	}
	// 启动前完整性检查（参照 PCL2 DlClientFix）：关键文件缺失时提示补全，避免 Java 直接报类路径错误
	if missing := mcMissingLaunchFiles(mcDir, profile); len(missing) > 0 {
		if report != nil {
			report("prepare", fmt.Sprintf("检测到 %d 个缺失文件，正在补全", len(missing)), 62)
		}
		if err := mcCompleteLaunchFiles(ctx, mcDir, profile, missing); err != nil {
			return LaunchResult{}, fmt.Errorf("启动前补全文件失败（缺失 %d 个文件）：%w", len(missing), err)
		}
		if stillMissing := mcMissingLaunchFiles(mcDir, profile); len(stillMissing) > 0 {
			return LaunchResult{}, fmt.Errorf("仍有 %d 个关键文件缺失，建议重新安装该版本", len(stillMissing))
		}
	}
	// 启动前 natives 完整核对（对齐 PCL2 McLaunchNatives L1657-1718，报告 #28 完整版）：
	// 按"文件名+大小"核对每个 dll/dylib/so，清理版本升级遗留的残留文件，
	// 不匹配则清掉后从压缩包重解压；核对失败给出明确启动前错误。
	if report != nil {
		report("prepare", "正在核对原生库完整性", 64)
	}
	if err := mcVerifyNatives(ctx, mcDir, profile); err != nil {
		return LaunchResult{}, err
	}
	if strings.TrimSpace(req.InstanceDir) != "" {
		if err := mergeInstanceDir(req.InstanceDir, mcDir); err != nil {
			return LaunchResult{}, err
		}
	}
	if err := ensureDefaultOptions(mcDir); err != nil {
		return LaunchResult{}, err
	}
	if report != nil {
		report("launch", "正在准备 Java 运行时", 80)
	}
	// 优先使用版本指定 Java > 本地/系统 Java；都没有时才自动下载运行时（避免无谓下载卡住）
	javaPath := versionJavaPath
	if javaPath != "" && !mcJavaSatisfies(javaPath, mcJavaRequirement(launchVersion)) {
		javaPath = ""
	}
	if javaPath == "" {
		var err error
		javaPath, err = FindMCJavaForVersion(launchVersion)
		if err != nil {
			if ensureErr := EnsureMCJava(ctx, launchVersion, report); ensureErr != nil {
				return LaunchResult{}, errors.Join(err, ensureErr)
			}
			javaPath, err = FindMCJavaForVersion(launchVersion)
			if err != nil {
				return LaunchResult{}, err
			}
		}
	}
	// 第三方认证：确保 authlib-injector 就绪（在拼参数前下载好 jar）
	if isMCAuthlibThirdParty(account) {
		if report != nil {
			report("launch", "正在准备第三方认证组件", 85)
		}
		if _, err := EnsureMCAuthlibInjector(ctx); err != nil {
			return LaunchResult{}, err
		}
	}
	// 实际 Java 大版本（用于 #12 编码参数选择）：能探测到就用实际值，否则退回版本需求估算
	javaMajor := javaMajorNumber(mcJavaRequirement(launchVersion))
	if detected, err := detectJavaVersion(javaPath); err == nil {
		javaMajor = detected
	}
	args, err := BuildMCLaunchArgs(profile, account, req, mcDir, extraJVM, javaMajor)
	if err != nil {
		return LaunchResult{}, err
	}
	server := ServerConfig{
		ID: mcProcessID(req.VersionID), IP: req.ServerIP, Port: req.ServerPort,
		Username: account.Name, VersionLabel: req.VersionID,
	}
	logPath, err := DefaultGameLogPath(server)
	if err != nil {
		return LaunchResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return LaunchResult{}, err
	}
	// 启动参数打码日志（参照 PCL2 FilterAccessToken）：token 打码后写入日志文件 + report 展示，
	// 排查启动问题时可直接复现启动命令。
	maskedArgs := maskLaunchArgs(args, account.AccessToken, account.UUID)
	maskedCmd := strings.Join(append([]string{javaPath}, maskedArgs...), " ")
	if report != nil {
		report("launch-cmd", maskedCmd, 95)
	}
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		_, _ = fmt.Fprintf(f, "[PrismPanel] 启动命令: %s\n", maskedCmd)
		_ = f.Close()
	}
	if report != nil {
		report("launch", "正在启动游戏", 95)
	}
	// 参照 PCL2：把 APPDATA 环境变量指向游戏目录，便于 MC/LWJGL 定位 .minecraft 与 natives。
	env := []string{
		"APPDATA=" + mcDir,
		"appdata=" + mcDir,
	}
	process, err := processes.Start(server.ID, ProcessStartRequest{
		JavaPath: javaPath, Args: args, WorkDir: mcDir, LogPath: logPath, Env: env,
	})
	if err != nil {
		return LaunchResult{}, err
	}
	return LaunchResult{
		Preparation: JoinPreparation{Server: server, GameDir: mcDir},
		JavaPath:    javaPath, PID: process.PID, LogPath: logPath,
	}, nil
}

func mcProcessID(versionID string) string {
	return "mc-" + safePathSegment(versionID)
}

func mergeInstanceDir(instanceDir, gameDir string) error {
	for _, name := range []string{"mods", "config", "resourcepacks", "shaderpacks", "saves"} {
		source := filepath.Join(instanceDir, name)
		if !directoryExists(source) {
			continue
		}
		if err := copyDirectory(source, filepath.Join(gameDir, name)); err != nil {
			return fmt.Errorf("merge %s: %w", name, err)
		}
	}
	return nil
}

// mcCompleteFile 启动补全计划中的单个文件（本地路径 + 下载信息）。
type mcCompleteFile struct {
	path string
	url  string
	size int64
	sha1 string
}

// mcLaunchFilePlan 从版本 JSON（含 Fabric 继承链）构建 本地路径 → 下载信息 映射，
// 供启动前补全使用。覆盖 client jar、资源索引、artifact 库与 fabric（name+url）库。
func mcLaunchFilePlan(mcDir string, profile *mcLaunchProfile) map[string]mcCompleteFile {
	plan := map[string]mcCompleteFile{}
	ids := []string{profile.GameID}
	if profile.InheritsFrom != "" && profile.InheritsFrom != profile.GameID {
		ids = append(ids, profile.InheritsFrom)
	}
	for _, id := range ids {
		version, err := decodeVersionJSON(mcDir, id)
		if err != nil {
			continue
		}
		if client := version.Downloads.Client; client.URL != "" {
			p := filepath.Join(mcDir, "versions", id, id+".jar")
			plan[p] = mcCompleteFile{path: p, url: client.URL, size: client.Size, sha1: client.SHA1}
		}
		if version.AssetIndex.ID != "" || version.AssetIndex.URL != "" {
			idxID := version.AssetIndex.ID
			if idxID == "" {
				idxID = id
			}
			p := filepath.Join(mcDir, "assets", "indexes", idxID+".json")
			plan[p] = mcCompleteFile{path: p, url: version.AssetIndex.URL, size: version.AssetIndex.Size, sha1: version.AssetIndex.SHA1}
		}
		for _, lib := range version.Libraries {
			a := lib.Downloads.Artifact
			if a.URL == "" || a.Path == "" {
				continue
			}
			p := filepath.Join(mcDir, "libraries", filepath.FromSlash(a.Path))
			plan[p] = mcCompleteFile{path: p, url: a.URL, size: a.Size, sha1: a.SHA1}
		}
	}
	// Fabric profile 形式库（name+url，无 downloads.artifact）
	if contents, err := os.ReadFile(filepath.Join(mcDir, "versions", profile.GameID, profile.GameID+".json")); err == nil {
		var fp fabricProfile
		if json.Unmarshal(contents, &fp) == nil {
			for _, lib := range fp.Libraries {
				if lib.Name == "" {
					continue
				}
				if u := fabricLibraryURL(lib.Name, lib.URL); u != "" {
					p := filepath.Join(mcDir, "libraries", filepath.FromSlash(fabricLibraryPath(lib.Name)))
					plan[p] = mcCompleteFile{path: p, url: u}
				}
			}
		}
	}
	return plan
}

// mcMissingLaunchFiles 返回启动必需但缺失的关键文件列表（client jar、资源索引、库）。
// 资源索引仅在该版本 JSON 声明了 assetIndex 时才要求存在（远古版本无资源索引系统）。
// natives 不在此列：由 mcVerifyNatives 在启动前做按"文件名+大小"的完整核对（含清残留/重解压）。
func mcMissingLaunchFiles(mcDir string, profile *mcLaunchProfile) []string {
	plan := mcLaunchFilePlan(mcDir, profile)
	var missing []string
	add := func(p string) {
		if p != "" && !fileExists(p) {
			missing = append(missing, p)
		}
	}
	add(profile.ClientJar)
	indexPath := filepath.Join(mcDir, "assets", "indexes", profile.AssetIndexID+".json")
	if _, declared := plan[indexPath]; declared {
		add(indexPath)
	}
	for _, p := range profile.LibraryPaths {
		add(p)
	}
	return missing
}

// mcCompleteLaunchFiles 补全缺失的启动文件：库/资源索引按版本 JSON 下载地址重新下载（并发）。
// natives 的核对/重解压由 mcVerifyNatives 单独负责（按"文件名+大小"核对并清残留）。
func mcCompleteLaunchFiles(ctx context.Context, mcDir string, profile *mcLaunchProfile, missing []string) error {
	plan := mcLaunchFilePlan(mcDir, profile)
	var toDownload []mcCompleteFile
	for _, p := range missing {
		if file, ok := plan[p]; ok && file.url != "" {
			toDownload = append(toDownload, file)
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, mcEffectiveConcurrency())
	var mu sync.Mutex
	var firstErr error
	for _, file := range toDownload {
		wg.Add(1)
		sem <- struct{}{}
		go func(f mcCompleteFile) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := downloadURLChecked(ctx, f.url, f.path, f.size, f.sha1); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(file)
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return nil
}

// mcNativeFile 单个原生文件的期望（解压后的目标文件名 + 大小），
// 对齐 PCL2 McLaunchNatives（ModLaunch.vb L1657-1718）的"按名+大小核对"。
type mcNativeFile struct {
	name string
	size int64
}

// mcNativesArchive 一个 natives 压缩包及其期望内容。来源与 mcLaunchFilePlan 一致：
// 版本 JSON（含继承链）libraries 的 downloads.classifiers[<平台>]。
type mcNativesArchive struct {
	archivePath string
	url         string
	size        int64
	sha1        string
	files       []mcNativeFile
}

// mcNativesPlan 收集当前版本（含继承链）声明的 natives classifier 压缩包。
// 与 mcLaunchFilePlan 使用同一来源（decodeVersionJSON 的 libraries classifiers），
// 保证"核对/重解压"与"安装时解压"来自同一批压缩包。
func mcNativesPlan(mcDir string, profile *mcLaunchProfile) ([]mcNativesArchive, error) {
	if profile == nil {
		return nil, nil
	}
	ids := []string{profile.GameID}
	if profile.InheritsFrom != "" && profile.InheritsFrom != profile.GameID {
		ids = append(ids, profile.InheritsFrom)
	}
	classifier := nativeClassifier()
	var archives []mcNativesArchive
	seen := map[string]bool{}
	for _, id := range ids {
		version, err := decodeVersionJSON(mcDir, id)
		if err != nil {
			continue
		}
		for _, lib := range version.Libraries {
			if !mcLibraryAllowed(lib) {
				continue
			}
			a, ok := lib.Downloads.Classifiers[classifier]
			if !ok || a.Path == "" {
				continue
			}
			archivePath := filepath.Join(mcDir, "libraries", filepath.FromSlash(a.Path))
			if seen[archivePath] {
				continue
			}
			seen[archivePath] = true
			archives = append(archives, mcNativesArchive{
				archivePath: archivePath,
				url:         a.URL,
				size:        a.Size,
				sha1:        a.SHA1,
			})
		}
	}
	return archives, nil
}

// nativeFilesInArchive 列出压缩包内的原生文件条目（dll/dylib/so，目标名取 basename，
// 与 extractZipFiltered 的解压目标一致）。
func nativeFilesInArchive(path string) ([]mcNativeFile, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var files []mcNativeFile
	for _, file := range reader.File {
		if !isNativeLibraryFile(file.Name) {
			continue
		}
		clean, err := cleanArchivePath(file.Name)
		if err != nil {
			continue
		}
		files = append(files, mcNativeFile{
			name: filepath.Base(filepath.FromSlash(clean)),
			size: int64(file.UncompressedSize64),
		})
	}
	return files, nil
}

func zipOpens(path string) bool {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return false
	}
	_ = reader.Close()
	return true
}

// nativeFileMatches 按 PCL2 语义核对目标原生文件：存在且大小一致。
func nativeFileMatches(path string, wantSize int64) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Size() == wantSize
}

// mcVerifyNatives 启动前 natives 完整核对（对齐 PCL2 McLaunchNatives，ModLaunch.vb L1657-1718）：
//  1. 核对每个 natives 压缩包内的 dll/dylib/so 是否已解压且"文件名+大小"一致；
//  2. 清理 natives 目录中不属于当前期望集合的残留文件（版本升级遗留的旧版 dll）；
//  3. 压缩包缺失/损坏时按 classifier 下载信息补全（size/sha1 校验）；
//  4. 有不匹配文件时清掉后从压缩包重新解压，仍不一致给出明确启动前错误。
func mcVerifyNatives(ctx context.Context, mcDir string, profile *mcLaunchProfile) error {
	if profile == nil || profile.NativesDir == "" {
		return nil
	}
	archives, err := mcNativesPlan(mcDir, profile)
	if err != nil {
		return err
	}
	if len(archives) == 0 {
		return nil // 版本未声明 natives classifier（如自定义 JSON），无需核对
	}
	// 1) 压缩包缺失/损坏 → 按 classifier 下载信息补全
	for i := range archives {
		a := &archives[i]
		if fileExists(a.archivePath) && zipOpens(a.archivePath) {
			continue
		}
		missing := !fileExists(a.archivePath)
		if a.url == "" {
			if missing {
				return fmt.Errorf("natives 压缩包 %s 缺失且无下载地址，请重新安装该版本", filepath.Base(a.archivePath))
			}
			return fmt.Errorf("natives 压缩包 %s 损坏且无下载地址，请重新安装该版本", filepath.Base(a.archivePath))
		}
		if err := downloadURLChecked(ctx, a.url, a.archivePath, a.size, a.sha1); err != nil {
			return fmt.Errorf("下载 natives 压缩包 %s 失败：%w", filepath.Base(a.archivePath), err)
		}
		if !zipOpens(a.archivePath) {
			return fmt.Errorf("natives 压缩包 %s 损坏，请重新安装该版本", filepath.Base(a.archivePath))
		}
	}
	// 2) 读取期望文件集合（名+大小）
	expected := map[string]int64{}
	for i := range archives {
		a := &archives[i]
		files, err := nativeFilesInArchive(a.archivePath)
		if err != nil {
			return fmt.Errorf("读取 natives 压缩包 %s 失败：%w", filepath.Base(a.archivePath), err)
		}
		a.files = files
		for _, f := range files {
			expected[f.name] = f.size
		}
	}
	if len(expected) == 0 {
		return fmt.Errorf("natives 压缩包内容异常（未发现 dll/dylib/so），请重新安装该版本")
	}
	// 3) 清理残留：natives 目录中不属于期望集合的文件（版本升级遗留）
	if entries, err := os.ReadDir(profile.NativesDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if _, ok := expected[entry.Name()]; !ok {
				_ = os.Remove(filepath.Join(profile.NativesDir, entry.Name()))
			}
		}
	}
	// 4) 核对：存在且大小一致 → 通过；否则清掉不匹配文件后重新解压
	mismatched := false
	for _, a := range archives {
		for _, f := range a.files {
			target := filepath.Join(profile.NativesDir, f.name)
			if !nativeFileMatches(target, f.size) {
				mismatched = true
				_ = os.Remove(target)
			}
		}
	}
	if mismatched {
		for _, a := range archives {
			if err := extractZipFiltered(a.archivePath, profile.NativesDir); err != nil {
				return fmt.Errorf("重新解压 natives %s 失败：%w", filepath.Base(a.archivePath), err)
			}
		}
		// 5) 复核对：仍不一致 → 明确启动前错误
		var stillBad []string
		for _, a := range archives {
			for _, f := range a.files {
				if !nativeFileMatches(filepath.Join(profile.NativesDir, f.name), f.size) {
					stillBad = append(stillBad, f.name)
				}
			}
		}
		if len(stillBad) > 0 {
			return fmt.Errorf("natives 完整性校验失败：%s 无法补全，建议重新安装该版本", strings.Join(stillBad, "、"))
		}
	}
	return nil
}
