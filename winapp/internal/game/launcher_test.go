package game

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestBuildLaunchArgumentsUsesServerRuntimeAndConnection(t *testing.T) {
	args, err := BuildLaunchArguments(LaunchArgumentInput{
		Config: versionConfig{
			ID:                 "26",
			JVMArguments:       "-Xmx2G -Djava.library.path=versions\\1.21.8\\natives-windows-x86_64 -cp libraries\\a.jar cpw.mods.bootstraplauncher.BootstrapLauncher",
			ParameterArguments: "--username ${auth_player_name} --gameDir . --assetsDir assets --assetIndex 26 --uuid ${auth_uuid} --accessToken ${auth_access_token} --clientId ${clientid} --xuid ${auth_xuid} --userType msa --versionType release --launchTarget neoforgeclient",
		},
		Server:  ServerConfig{ID: "server-a", IP: "127.0.0.1", Port: 25565, Username: "Steve", Version: Version1_21_8, VersionLabel: "1.21.8"},
		Account: AccountState{UserID: "123456", UserToken: "token-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertArgValue(t, args, "--gameDir", ".")
	assertArgValue(t, args, "--assetsDir", "assets")
	assertArgValue(t, args, "--server", "127.0.0.1")
	assertArgValue(t, args, "--port", "25565")
	assertArgValue(t, args, "--username", "Steve")
	if slices.Contains(args, "${clientid}") {
		t.Fatalf("unresolved placeholder was not removed: %v", args)
	}
	if !containsPrefix(args, "-DToken=") {
		t.Fatalf("encrypted NetEase token was not added: %v", args)
	}
	if containsPrefixValue(args, "-DToken=", "token-value") {
		t.Fatalf("raw NetEase token leaked into launch arguments")
	}
	libraryPath := propertyValue(t, args, "java.library.path")
	if !filepath.IsAbs(libraryPath) {
		t.Fatalf("java.library.path must be absolute for NetEase native loader: %s", libraryPath)
	}
	runtimePath := propertyValue(t, args, "runtime_path")
	if !filepath.IsAbs(runtimePath) {
		t.Fatalf("runtime_path must be absolute for NetEase native loader: %s", runtimePath)
	}
}

func TestBuildLaunchArgumentsPlacesLauncherPropertiesBeforeMainClass(t *testing.T) {
	args, err := BuildLaunchArguments(LaunchArgumentInput{
		Config: versionConfig{
			ID:                 "26",
			MainClass:          "cpw.mods.bootstraplauncher.BootstrapLauncher",
			JVMArguments:       "-Xmx2G -cp libraries\\a.jar cpw.mods.bootstraplauncher.BootstrapLauncher",
			ParameterArguments: "--username ${auth_player_name} --gameDir . --assetsDir assets --assetIndex 26 --uuid ${auth_uuid} --accessToken ${auth_access_token} --launchTarget neoforgeclient",
		},
		Server:              ServerConfig{ID: "server-a", IP: "127.0.0.1", Port: 25565, Username: "Steve", Version: Version1_21_8, VersionLabel: "1.21.8"},
		Account:             AccountState{UserID: "123456", UserToken: "token-value"},
		LauncherControlPort: 34567,
		LauncherPort:        45678,
		ProtocolVersion:     "1.2.3",
	})
	if err != nil {
		t.Fatal(err)
	}
	mainIndex := indexOfArg(args, "cpw.mods.bootstraplauncher.BootstrapLauncher")
	if mainIndex < 0 {
		t.Fatalf("main class not found: %v", args)
	}
	controlIndex := indexOfArgPrefix(args, "-DlauncherControlPort=")
	if controlIndex < 0 || controlIndex > mainIndex {
		t.Fatalf("launcherControlPort must be before main class: %v", args)
	}
	assertArgValue(t, args, "--userPropertiesEx", `{"GameType":2,"channel":"netease","isFilter":true,"launcherVersion":"1.2.3","timedelta":0}`)
	userProperties := valueAfterArg(t, args, "--userProperties")
	var decoded map[string][]any
	if err := json.Unmarshal([]byte(userProperties), &decoded); err != nil {
		t.Fatalf("userProperties is not JSON: %s err=%v", userProperties, err)
	}
	launcherPort := decoded["launcherport"]
	if len(launcherPort) != 2 || launcherPort[0].(float64) != 45678 {
		t.Fatalf("launcherport mismatch: %s", userProperties)
	}
}

func TestSplitCommandLineKeepsQuotedClasspath(t *testing.T) {
	args, err := splitCommandLine(`-Xmx2G -cp "libraries/a.jar;libraries/b.jar" main.Class --name "Steve Jobs"`)
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"-Xmx2G", "-cp", "libraries/a.jar;libraries/b.jar", "main.Class", "--name", "Steve Jobs"}
	if strings.Join(args, "\x00") != strings.Join(expected, "\x00") {
		t.Fatalf("args mismatch\nwant: %q\n got: %q", expected, args)
	}
}

func valueAfterArg(t *testing.T, args []string, key string) string {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key {
			return args[i+1]
		}
	}
	t.Fatalf("%s not found in args[%d]: %v", key, len(args), args)
	return ""
}

func indexOfArg(args []string, value string) int {
	for i, arg := range args {
		if arg == value {
			return i
		}
	}
	return -1
}

func indexOfArgPrefix(args []string, prefix string) int {
	for i, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return i
		}
	}
	return -1
}

func propertyValue(t *testing.T, args []string, key string) string {
	t.Helper()
	prefix := "-D" + key + "="
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	t.Fatalf("property %s not found in args[%d]: %v", key, len(args), args)
	return ""
}

func assertArgValue(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key {
			if args[i+1] != value {
				t.Fatalf("%s value mismatch: want %q got %q in %v", key, value, args[i+1], args)
			}
			return
		}
	}
	t.Fatalf("%s not found in args[%d]: %v", key, len(args), args)
}

func containsPrefix(args []string, prefix string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}

func containsPrefixValue(args []string, prefix, value string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) && strings.Contains(arg, value) {
			return true
		}
	}
	return false
}

func TestGenerateRoleUUIDIsStableAndHex(t *testing.T) {
	first := generateRoleUUID("Steve", strconv.FormatUint(123456, 10))
	second := generateRoleUUID("Steve", strconv.FormatUint(123456, 10))
	if first != second {
		t.Fatalf("uuid should be stable: %s != %s", first, second)
	}
	if len(first) != 32 {
		t.Fatalf("uuid should be 32 hex chars: %s", first)
	}
}

func TestResetRuntimeDirectoryCleansOnlyRuntimeChild(t *testing.T) {
	root := t.TempDir()
	paths := CachePaths{Runtime: root}
	target := filepath.Join(root, "server-a")
	stale := filepath.Join(target, "mods", "old.jar")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ResetRuntimeDirectory(paths, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file should be removed, stat err=%v", err)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("runtime target should be recreated, info=%v err=%v", info, err)
	}
}

func TestResetRuntimeDirectoryRejectsUnsafeTargets(t *testing.T) {
	root := t.TempDir()
	paths := CachePaths{Runtime: root}
	if err := ResetRuntimeDirectory(paths, root); err == nil {
		t.Fatal("runtime root must not be removable")
	}
	outside := filepath.Join(t.TempDir(), "server-a")
	if err := ResetRuntimeDirectory(paths, outside); err == nil {
		t.Fatal("outside target must not be removable")
	}
}

func TestInstallCoreLibrariesFromDirInstallsDatAndOverwritesJar(t *testing.T) {
	root := t.TempDir()
	paths := CachePaths{BaseMC: filepath.Join(root, ".minecraft")}
	source := filepath.Join(root, "downloads", "2_Lib")
	loaderTarget := filepath.Join(paths.BaseMC, "libraries", "net", "neoforged", "fancymodloader", "loader", "9.0.18", "loader-9.0.18.jar")
	if err := os.MkdirAll(filepath.Dir(loaderTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loaderTarget, []byte("old-loader"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "1.21.8.dat"), []byte("dat-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "loader-9.0.18.jar"), []byte("new-loader"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installCoreLibrariesFromDir(paths, "1.21.8", source, nil); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(paths.BaseMC, "versions", "1.21.8", "1.21.8.dat"), "dat-content")
	assertFileContent(t, loaderTarget, "new-loader")
}

func TestEnsureNetEaseNativeRuntimeCopiesCachedDLL(t *testing.T) {
	root := t.TempDir()
	paths := CachePaths{Root: root, BaseMC: filepath.Join(root, ".minecraft")}
	runtimeDir := filepath.Join(root, "runtime", "server-a")
	nativesPath := filepath.Join(runtimeDir, "versions", "1.21.8", "natives-windows-x86_64")
	if err := os.MkdirAll(nativesPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cachedDLL := filepath.Join(root, "native-runtime", netEaseRuntimeDLL)
	if err := os.MkdirAll(filepath.Dir(cachedDLL), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachedDLL, []byte("netease-native"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureNetEaseNativeRuntime(paths, runtimeDir, "1.21.8"); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(nativesPath, "runtime", netEaseRuntimeDLL), "netease-native")
}

func TestEnsureNetEaseNativeRuntimeReportsMissingDLL(t *testing.T) {
	root := t.TempDir()
	paths := CachePaths{Root: root, BaseMC: filepath.Join(root, ".minecraft")}
	runtimeDir := filepath.Join(root, "runtime", "server-a")
	nativesPath := filepath.Join(runtimeDir, "versions", "1.21.8", "natives-windows-x86_64")
	if err := os.MkdirAll(nativesPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureNetEaseNativeRuntime(paths, runtimeDir, "1.21.8"); err == nil {
		t.Fatal("missing NetEase runtime DLL should be reported before launching")
	}
}
func assertFileContent(t *testing.T, path string, expected string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != expected {
		t.Fatalf("%s content mismatch: want %q got %q", path, expected, string(contents))
	}
}
