package game

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineUUID(t *testing.T) {
	first := offlineUUID("Steve")
	second := offlineUUID("Steve")
	if first == "" || first != second {
		t.Fatalf("offline uuid should be stable: %s vs %s", first, second)
	}
	if len(first) != 36 || strings.Count(first, "-") != 4 {
		t.Fatalf("offline uuid format invalid: %s", first)
	}
}

func TestFabricLibraryPath(t *testing.T) {
	path := fabricLibraryPath("net.fabricmc:fabric-loader:0.16.9")
	if path == "" || !strings.HasSuffix(path, "fabric-loader-0.16.9.jar") {
		t.Fatalf("fabric library path invalid: %s", path)
	}
	if !strings.Contains(strings.ReplaceAll(path, "\\", "/"), "net/fabricmc/fabric-loader") {
		t.Fatalf("fabric library path should use group directories: %s", path)
	}
}

func TestMCJavaRequirement(t *testing.T) {
	cases := map[string]string{
		"1.8.9":   "8",
		"1.12.2":  "8",
		"1.16.5":  "8",  // 1.12-1.16 官方要求 Java 8（PCL2 GetJavaRequirement）
		"1.17.1":  "16", // 1.17 官方要求 Java 16
		"1.19.2":  "17",
		"1.20.4":  "17", // 1.20.0-1.20.4 仍为 Java 17
		"1.20.6":  "21", // 1.20.5+ 需要 Java 21
		"1.21.4":  "21",
		"1.21.10": "21",
		"24w14a":  "21", // 23w31a（2023-08）后的快照需要 Java 21
	}
	for version, want := range cases {
		if got := mcJavaRequirement(version); got != want {
			t.Errorf("mcJavaRequirement(%q) = %q, want %q", version, got, want)
		}
	}
}

func TestBuildMCLaunchArgs(t *testing.T) {
	profile := &mcLaunchProfile{
		GameID: "1.21.4", MainClass: "net.minecraft.client.main.Main",
		LibraryPaths: []string{"lib/a.jar"}, ClientJar: "client.jar",
		AssetIndexID: "1.21.4", NativesDir: "natives",
		GameArguments: []string{"--username", "${auth_player_name}", "--version", "${version_name}"},
	}
	account := MCAccount{Mode: MCAuthOffline, Name: "Steve", UUID: offlineUUID("Steve"), AccessToken: "token"}
	args, err := BuildMCLaunchArgs(profile, account, MCLaunchRequest{ServerIP: "127.0.0.1", ServerPort: 25565, MaxMemoryMB: 2048}, "game", []string{"-Dcustom=1"}, 17)
	if err != nil {
		t.Fatalf("build args: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-Xmx2048M", "-cp", "net.minecraft.client.main.Main", "--username", "Steve", "-Dcustom=1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("launch args missing %q:\n%s", want, joined)
		}
	}
	// 1.20.2+ 自动进服用 QuickPlay 单参数 host:port，不再使用 --server/--port
	if !strings.Contains(joined, "--quickPlayMultiplayer 127.0.0.1:25565") {
		t.Errorf("1.20.2+ should use quickPlayMultiplayer:\n%s", joined)
	}
	if strings.Contains(joined, "--server") || strings.Contains(joined, "--port") {
		t.Errorf("1.20.2+ should not use --server/--port:\n%s", joined)
	}
}

func TestBuildMCLaunchArgsQuickPlayOldVersion(t *testing.T) {
	// 1.19.2（1.20.2 之前）：仍用 --server/--port
	profile := &mcLaunchProfile{
		GameID: "1.19.2", MainClass: "net.minecraft.client.main.Main",
		LibraryPaths: []string{"lib/a.jar"}, ClientJar: "client.jar",
		AssetIndexID: "1.19.2", NativesDir: "natives",
		GameArguments: []string{"--username", "${auth_player_name}"},
	}
	account := MCAccount{Mode: MCAuthOffline, Name: "Steve", UUID: offlineUUID("Steve"), AccessToken: "token"}
	args, err := BuildMCLaunchArgs(profile, account, MCLaunchRequest{ServerIP: "127.0.0.1", ServerPort: 25565, MaxMemoryMB: 2048}, "game", nil, 17)
	if err != nil {
		t.Fatalf("build args: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--server 127.0.0.1") || !strings.Contains(joined, "--port 25565") {
		t.Errorf("pre-1.20.2 should use --server/--port:\n%s", joined)
	}
	if strings.Contains(joined, "--quickPlayMultiplayer") {
		t.Errorf("pre-1.20.2 should not use quickPlayMultiplayer:\n%s", joined)
	}
}

func TestDedupeLaunchArgs(t *testing.T) {
	cases := []struct {
		name  string
		in    []string
		isJVM bool
		want  []string
	}{
		{"game pairs override keep first position", []string{"--width", "800", "--width", "1920", "--height", "480"}, false, []string{"--width", "1920", "--height", "480"}},
		{"jvm pairs keep different values", []string{"--width", "800", "--width", "1920"}, true, []string{"--width", "800", "--width", "1920"}},
		{"jvm identical pair dropped", []string{"--width", "800", "--width", "800"}, true, []string{"--width", "800"}},
		{"duplicate singles dropped", []string{"-Dfoo=bar", "-Dfoo=bar", "-cp", "x"}, false, []string{"-Dfoo=bar", "-cp", "x"}},
		{"negative value not treated as flag", []string{"-xPos", "23", "-xPos", "-50"}, false, []string{"-xPos", "-50"}},
		{"no dedup needed", []string{"--a", "1", "--b", "2"}, false, []string{"--a", "1", "--b", "2"}},
		{"single cp pair", []string{"-cp", "lib.jar"}, true, []string{"-cp", "lib.jar"}},
		{"tweakClass not overridden", []string{"--tweakClass", "A", "--tweakClass", "B"}, false, []string{"--tweakClass", "A", "--tweakClass", "B"}},
	}
	for _, c := range cases {
		got := dedupeLaunchArgs(c.in, c.isJVM)
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("%s: dedupe(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
}

func TestMCSupportsQuickPlay(t *testing.T) {
	cases := map[string]bool{
		"1.19.2":                       false,
		"1.20.1":                       false,
		"1.20.2":                       true,
		"1.20.6":                       true,
		"1.21.4":                       true,
		"fabric-loader-0.16.9-1.21.4":  true,
		"fabric-loader-0.14.24-1.20.1": false,
		"23w30a":                       false, // 23w31a 之前的快照不支持 QuickPlay
		"23w31a":                       true,
		"24w14a":                       true, // 24 年快照支持
		"22w45a":                       false,
	}
	for version, want := range cases {
		if got := mcSupportsQuickPlay(version); got != want {
			t.Errorf("mcSupportsQuickPlay(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestMaskLaunchArgs(t *testing.T) {
	args := []string{"--accessToken", "secret-token-123", "--username", "Steve"}
	masked := maskLaunchArgs(args, "secret-token-123")
	if strings.Contains(strings.Join(masked, " "), "secret-token-123") {
		t.Fatalf("token should be masked: %v", masked)
	}
	if !strings.Contains(masked[1], "***") {
		t.Fatalf("masked value should contain ***: %v", masked)
	}
}

func TestBuildMCLaunchArgsReplacesJVMPlaceholders(t *testing.T) {
	// 关键回归：arguments.jvm 中的 ${classpath} 等占位符必须被替换，
	// 否则会以字面量传给 Java 导致找不到主类（PCL 启动方式的核心点）。
	profile := &mcLaunchProfile{
		GameID: "1.21.4", MainClass: "net.minecraft.client.main.Main",
		LibraryPaths: []string{filepath.Join("libraries", "x.jar")}, ClientJar: filepath.Join("versions", "1.21.4", "1.21.4.jar"),
		AssetIndexID: "1.21.4", NativesDir: filepath.Join("versions", "natives"),
		JVMArguments: []string{
			"-Djava.library.path=${natives_directory}",
			"-Djna.tmpdir=${natives_directory}",
			"-Dminecraft.launcher.brand=${launcher_name}",
			"-Dminecraft.launcher.version=${launcher_version}",
			"-cp",
			"${classpath}",
			"-Dminecraft.client.jar=${primary_jar}",
			"-Dfuture.unknown=${future_placeholder_xyz}",
		},
		GameArguments: []string{"--accessToken", "${auth_access_token}"},
	}
	account := MCAccount{Mode: MCAuthOffline, Name: "Steve", UUID: offlineUUID("Steve"), AccessToken: "token"}
	args, err := BuildMCLaunchArgs(profile, account, MCLaunchRequest{MaxMemoryMB: 2048}, "game", nil, 17)
	if err != nil {
		t.Fatalf("build args: %v", err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "${") {
		t.Fatalf("launch args still contain unresolved placeholders:\n%s", joined)
	}
	for _, want := range []string{
		"-Djava.library.path=" + profile.NativesDir,
		"-Dminecraft.launcher.brand=PrismPanel",
		"-Dminecraft.client.jar=" + profile.ClientJar,
		"-cp", strings.Join(profile.LibraryPaths, string(os.PathListSeparator)),
		"net.minecraft.client.main.Main",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("launch args missing %q:\n%s", want, joined)
		}
	}
}

func TestMCLibraryRules(t *testing.T) {
	osx := struct {
		Action string `json:"action"`
		OS     struct {
			Name string `json:"name"`
			Arch string `json:"arch"`
		} `json:"os"`
		Features *struct {
			IsDemoUser *bool `json:"is_demo_user"`
		} `json:"features"`
	}{Action: "disallow", OS: struct {
		Name string `json:"name"`
		Arch string `json:"arch"`
	}{Name: "osx"}}
	// 仅有 disallow osx 的规则：windows 上不匹配 → 不被允许（与 Mojang 语义一致）。
	onlyOSX := mcLibrary{}
	onlyOSX.Rules = append(onlyOSX.Rules, osx)
	if mcLibraryAllowed(onlyOSX) {
		t.Error("library with only a disallow-osx rule should not be allowed")
	}
	// 显式 allow + disallow osx：windows 上应被允许。
	allowWindows := mcLibrary{}
	allowWindows.Rules = append(allowWindows.Rules, struct {
		Action string `json:"action"`
		OS     struct {
			Name string `json:"name"`
			Arch string `json:"arch"`
		} `json:"os"`
		Features *struct {
			IsDemoUser *bool `json:"is_demo_user"`
		} `json:"features"`
	}{Action: "allow", OS: struct {
		Name string `json:"name"`
		Arch string `json:"arch"`
	}{Name: ""}})
	allowWindows.Rules = append(allowWindows.Rules, osx)
	if !mcLibraryAllowed(allowWindows) {
		t.Error("library with allow + disallow-osx should be allowed on windows")
	}
	// 无规则 → 允许。
	if !mcLibraryAllowed(mcLibrary{}) {
		t.Error("library without rules should be allowed")
	}
}

func TestMCDeleteAndFabric(t *testing.T) {
	tempDir := t.TempDir()
	oldEnv, hadEnv := os.LookupEnv("PRISMPANEL_MC_DIR")
	os.Setenv("PRISMPANEL_MC_DIR", tempDir)
	defer func() {
		if hadEnv {
			os.Setenv("PRISMPANEL_MC_DIR", oldEnv)
		} else {
			os.Unsetenv("PRISMPANEL_MC_DIR")
		}
	}()

	// 与真实安装一致：Fabric 版本位于基础版本的 .minecraft/versions 内
	writeVersion := func(versionID, base string) {
		dir := filepath.Join(tempDir, safePathSegment(base), ".minecraft", "versions", versionID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := `{"id":"` + versionID + `","mainClass":"x"}`
		if err := os.WriteFile(filepath.Join(dir, versionID+".json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeVersion("1.21.4", "1.21.4")
	writeVersion("fabric-loader-0.16.9-1.21.4", "1.21.4")

	installed, err := InstalledMCVersions()
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(installed) != 2 {
		t.Fatalf("expected 2 installed versions, got %d", len(installed))
	}

	fabric, err := MCFabricInstalled("1.21.4")
	if err != nil {
		t.Fatal(err)
	}
	if !fabric {
		t.Error("1.21.4 should have fabric installed")
	}

	// 删除基础版本：Fabric 一并移除
	if err := DeleteMCVersion("1.21.4"); err != nil {
		t.Fatalf("delete version: %v", err)
	}
	fabric, err = MCFabricInstalled("1.21.4")
	if err != nil {
		t.Fatal(err)
	}
	if fabric {
		t.Error("1.21.4 should no longer have fabric installed after deleting it")
	}

	// 越界保护：恶意 id（如 ".."）会被清洗为安全段，不删除存储根或其父目录
	parent := filepath.Dir(tempDir)
	if err := DeleteMCVersion(".."); err != nil {
		t.Fatalf("DeleteMCVersion should be a safe no-op: %v", err)
	}
	if _, statErr := os.Stat(tempDir); statErr != nil {
		t.Fatalf("store root should still exist: %v", statErr)
	}
	if _, statErr := os.Stat(parent); statErr != nil {
		t.Fatalf("parent dir should still exist: %v", statErr)
	}
}

// ---- #12 中文用户名编码参数 ----

func TestMCContainsNonASCII(t *testing.T) {
	if mcContainsNonASCII("Steve") {
		t.Error("pure ASCII should be false")
	}
	if !mcContainsNonASCII("史蒂夫") {
		t.Error("Chinese username should be true")
	}
	if !mcContainsNonASCII("C:\\用户\\游戏") {
		t.Error("Chinese path should be true")
	}
}

func TestMCEncodingJVMArgs(t *testing.T) {
	cases := []struct {
		major int
		want  []string
	}{
		{8, []string{"-Dsun.stdout.encoding=UTF-8", "-Dsun.stderr.encoding=UTF-8"}},
		{11, []string{"-Dsun.stdout.encoding=UTF-8", "-Dsun.stderr.encoding=UTF-8"}},
		{17, []string{"-Dsun.stdout.encoding=UTF-8", "-Dsun.stderr.encoding=UTF-8"}},
		{18, []string{"-Dfile.encoding=COMPAT", "-Dsun.stdout.encoding=UTF-8", "-Dsun.stderr.encoding=UTF-8"}},
		{19, []string{"-Dfile.encoding=COMPAT", "-Dstdout.encoding=UTF-8", "-Dstderr.encoding=UTF-8"}},
		{20, []string{"-Dfile.encoding=COMPAT", "-Dstdout.encoding=UTF-8", "-Dstderr.encoding=UTF-8"}},
		{21, nil}, // Java 21+ 默认 UTF-8，无需处理
		{25, nil},
	}
	for _, c := range cases {
		got := mcEncodingJVMArgs(c.major)
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("mcEncodingJVMArgs(%d) = %v, want %v", c.major, got, c.want)
		}
	}
}

func indexOfArgs(args []string, target string) int {
	for i, arg := range args {
		if arg == target {
			return i
		}
	}
	return -1
}

func flagValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func TestBuildMCLaunchArgsChineseUsername(t *testing.T) {
	profile := &mcLaunchProfile{
		GameID: "1.21.4", MainClass: "net.minecraft.client.main.Main",
		LibraryPaths: []string{"lib/a.jar"}, ClientJar: "client.jar",
		AssetIndexID: "1.21.4", NativesDir: "natives",
		GameArguments: []string{"--username", "${auth_player_name}"},
	}
	account := MCAccount{Mode: MCAuthOffline, Name: "史蒂夫", UUID: offlineUUID("史蒂夫"), AccessToken: "token"}
	// #12：中文用户名在 Windows 下经 Go 原生 UTF-16 参数传递（不经本地代码页转换），
	// --username 值必须是完整 UTF-8 字符串；同时注入 UTF-8 编码 JVM 参数。
	args, err := BuildMCLaunchArgs(profile, account, MCLaunchRequest{MaxMemoryMB: 2048}, "game", nil, 17)
	if err != nil {
		t.Fatalf("build args: %v", err)
	}
	if got := flagValue(args, "--username"); got != "史蒂夫" {
		t.Errorf("--username should round-trip as UTF-8, got %q", got)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-Dsun.stdout.encoding=UTF-8", "-Dsun.stderr.encoding=UTF-8"} {
		if !strings.Contains(joined, want) {
			t.Errorf("java 17 with Chinese username should inject %s:\n%s", want, joined)
		}
	}
	// 编码参数必须在主类之前（JVM 参数）
	mainIdx := indexOfArgs(args, "net.minecraft.client.main.Main")
	if mainIdx < 0 {
		t.Fatalf("main class missing: %v", args)
	}
	for _, enc := range []string{"-Dsun.stdout.encoding=UTF-8", "-Dsun.stderr.encoding=UTF-8"} {
		if idx := indexOfArgs(args, enc); idx < 0 || idx > mainIdx {
			t.Errorf("encoding arg %s should precede main class (idx=%d main=%d)", enc, idx, mainIdx)
		}
	}
}

func TestBuildMCLaunchArgsChineseUsernameJava21(t *testing.T) {
	// Java 21+ 默认 UTF-8：不注入额外编码参数，但 --username 仍为完整 UTF-8。
	profile := &mcLaunchProfile{
		GameID: "1.21.4", MainClass: "net.minecraft.client.main.Main",
		LibraryPaths: []string{"lib/a.jar"}, ClientJar: "client.jar",
		AssetIndexID: "1.21.4", NativesDir: "natives",
		GameArguments: []string{"--username", "${auth_player_name}"},
	}
	account := MCAccount{Mode: MCAuthOffline, Name: "史蒂夫", UUID: offlineUUID("史蒂夫"), AccessToken: "token"}
	args, err := BuildMCLaunchArgs(profile, account, MCLaunchRequest{MaxMemoryMB: 2048}, "game", nil, 21)
	if err != nil {
		t.Fatalf("build args: %v", err)
	}
	if got := flagValue(args, "--username"); got != "史蒂夫" {
		t.Errorf("--username should round-trip as UTF-8, got %q", got)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "stdout.encoding") || strings.Contains(joined, "file.encoding") {
		t.Errorf("java 21 should rely on default UTF-8, no encoding args:\n%s", joined)
	}
}

func TestBuildMCLaunchArgsChinesePathEncoding(t *testing.T) {
	// 用户名 ASCII 但游戏目录含中文：同样注入编码参数（中文路径场景）。
	profile := &mcLaunchProfile{
		GameID: "1.21.4", MainClass: "net.minecraft.client.main.Main",
		LibraryPaths: []string{"lib/a.jar"}, ClientJar: "client.jar",
		AssetIndexID: "1.21.4", NativesDir: "natives",
		GameArguments: []string{"--username", "${auth_player_name}"},
	}
	account := MCAccount{Mode: MCAuthOffline, Name: "Steve", UUID: offlineUUID("Steve"), AccessToken: "token"}
	args, err := BuildMCLaunchArgs(profile, account, MCLaunchRequest{MaxMemoryMB: 2048}, "C:\\游戏\\minecraft", nil, 17)
	if err != nil {
		t.Fatalf("build args: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-Dsun.stdout.encoding=UTF-8") {
		t.Errorf("Chinese game dir should inject encoding args:\n%s", joined)
	}
}

func TestBuildMCLaunchArgsASCIINoEncoding(t *testing.T) {
	// 纯 ASCII 用户名 + ASCII 路径：不注入编码参数（保持原有启动行为）。
	profile := &mcLaunchProfile{
		GameID: "1.21.4", MainClass: "net.minecraft.client.main.Main",
		LibraryPaths: []string{"lib/a.jar"}, ClientJar: "client.jar",
		AssetIndexID: "1.21.4", NativesDir: "natives",
		GameArguments: []string{"--username", "${auth_player_name}"},
	}
	account := MCAccount{Mode: MCAuthOffline, Name: "Steve", UUID: offlineUUID("Steve"), AccessToken: "token"}
	args, err := BuildMCLaunchArgs(profile, account, MCLaunchRequest{MaxMemoryMB: 2048}, "game", nil, 17)
	if err != nil {
		t.Fatalf("build args: %v", err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "stdout.encoding") || strings.Contains(joined, "file.encoding") {
		t.Errorf("ASCII launch should not inject encoding args:\n%s", joined)
	}
}

// ---- #28 natives 启动前完整核对 ----

// writeNativesTestEnv 构造一个最小版本环境：versions/test/test.json 声明 natives classifier，
// archivePath 指向 libraries 下的 natives 压缩包；返回 mcDir / natives 目录 / 压缩包路径。
func writeNativesTestEnv(t *testing.T, archiveBytes []byte) (mcDir, nativesDir, archivePath string) {
	t.Helper()
	mcDir = t.TempDir()
	versionDir := filepath.Join(mcDir, "versions", "test")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	classifier := nativeClassifier()
	versionJSON := fmt.Sprintf(`{"id":"test","libraries":[{"name":"com.example:native-lib:1.0","downloads":{"classifiers":{"%s":{"path":"com/example/native-lib-1.0-natives.jar","url":"","size":0,"sha1":""}}}}]}`, classifier)
	if err := os.WriteFile(filepath.Join(versionDir, "test.json"), []byte(versionJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	archivePath = filepath.Join(mcDir, "libraries", "com", "example", "native-lib-1.0-natives.jar")
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if archiveBytes != nil {
		if err := os.WriteFile(archivePath, archiveBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nativesDir = filepath.Join(mcDir, "versions", "natives")
	return mcDir, nativesDir, archivePath
}

func makeNativeJar(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	file, err := writer.Create("test.dll")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestMCVerifyNativesExtractsMissing(t *testing.T) {
	content := []byte("native-binary-data-123456")
	jar := makeNativeJar(t, content)
	mcDir, nativesDir, _ := writeNativesTestEnv(t, jar)
	profile := &mcLaunchProfile{GameID: "test", NativesDir: nativesDir}
	if err := mcVerifyNatives(context.Background(), mcDir, profile); err != nil {
		t.Fatalf("verify should extract missing natives: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(nativesDir, "test.dll"))
	if err != nil {
		t.Fatalf("test.dll should exist after verify: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("extracted content mismatch: %q", got)
	}
}

func TestMCVerifyNativesRemovesStale(t *testing.T) {
	jar := makeNativeJar(t, []byte("data"))
	mcDir, nativesDir, _ := writeNativesTestEnv(t, jar)
	if err := os.MkdirAll(nativesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 版本升级遗留的旧版 dll（不在期望集合内）
	if err := os.WriteFile(filepath.Join(nativesDir, "old-version.dll"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := &mcLaunchProfile{GameID: "test", NativesDir: nativesDir}
	if err := mcVerifyNatives(context.Background(), mcDir, profile); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := os.Stat(filepath.Join(nativesDir, "old-version.dll")); !os.IsNotExist(err) {
		t.Errorf("stale file should be removed")
	}
	if _, err := os.Stat(filepath.Join(nativesDir, "test.dll")); err != nil {
		t.Errorf("expected native should exist: %v", err)
	}
}

func TestMCVerifyNativesReplacesWrongSize(t *testing.T) {
	content := []byte("0123456789") // 10 字节
	jar := makeNativeJar(t, content)
	mcDir, nativesDir, _ := writeNativesTestEnv(t, jar)
	if err := os.MkdirAll(nativesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nativesDir, "test.dll"), []byte("ab"), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := &mcLaunchProfile{GameID: "test", NativesDir: nativesDir}
	if err := mcVerifyNatives(context.Background(), mcDir, profile); err != nil {
		t.Fatalf("verify should re-extract mismatched natives: %v", err)
	}
	info, err := os.Stat(filepath.Join(nativesDir, "test.dll"))
	if err != nil {
		t.Fatalf("test.dll should exist: %v", err)
	}
	if info.Size() != int64(len(content)) {
		t.Errorf("test.dll size = %d, want %d", info.Size(), len(content))
	}
}

func TestMCVerifyNativesIdempotent(t *testing.T) {
	content := []byte("native-data")
	jar := makeNativeJar(t, content)
	mcDir, nativesDir, _ := writeNativesTestEnv(t, jar)
	profile := &mcLaunchProfile{GameID: "test", NativesDir: nativesDir}
	if err := mcVerifyNatives(context.Background(), mcDir, profile); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if err := mcVerifyNatives(context.Background(), mcDir, profile); err != nil {
		t.Fatalf("second verify should be a no-op: %v", err)
	}
	info, err := os.Stat(filepath.Join(nativesDir, "test.dll"))
	if err != nil {
		t.Fatalf("test.dll should exist: %v", err)
	}
	if info.Size() != int64(len(content)) {
		t.Errorf("test.dll size = %d, want %d", info.Size(), len(content))
	}
}

func TestMCVerifyNativesMissingArchiveNoURL(t *testing.T) {
	// 压缩包缺失且无下载地址 → 明确启动前错误
	mcDir, nativesDir, _ := writeNativesTestEnv(t, nil)
	profile := &mcLaunchProfile{GameID: "test", NativesDir: nativesDir}
	err := mcVerifyNatives(context.Background(), mcDir, profile)
	if err == nil {
		t.Fatal("expected error for missing natives archive without url")
	}
	if !strings.Contains(err.Error(), "缺失且无下载地址") {
		t.Errorf("error should mention missing archive: %v", err)
	}
}

func TestMCVerifyNativesCorruptArchiveNoURL(t *testing.T) {
	mcDir, nativesDir, archivePath := writeNativesTestEnv(t, []byte("not a zip at all"))
	if err := os.WriteFile(archivePath, []byte("corrupted"), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := &mcLaunchProfile{GameID: "test", NativesDir: nativesDir}
	err := mcVerifyNatives(context.Background(), mcDir, profile)
	if err == nil {
		t.Fatal("expected error for corrupt natives archive")
	}
	if !strings.Contains(err.Error(), "损坏") {
		t.Errorf("error should mention corrupt archive: %v", err)
	}
}

func TestMCVerifyNativesNoDeclaredNatives(t *testing.T) {
	// 版本未声明 natives classifier → 无需核对，直接通过
	mcDir := t.TempDir()
	versionDir := filepath.Join(mcDir, "versions", "test")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "test.json"), []byte(`{"id":"test","libraries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := &mcLaunchProfile{GameID: "test", NativesDir: filepath.Join(mcDir, "versions", "natives")}
	if err := mcVerifyNatives(context.Background(), mcDir, profile); err != nil {
		t.Fatalf("no declared natives should pass: %v", err)
	}
}
