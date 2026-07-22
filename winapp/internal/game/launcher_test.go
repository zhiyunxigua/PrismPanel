package game

import (
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
