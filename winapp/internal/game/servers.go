package game

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type ServerConfig struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	GameID       string    `json:"game_id,omitempty"`
	IP           string    `json:"ip"`
	Port         int       `json:"port"`
	Username     string    `json:"username"`
	Version      Version   `json:"version"`
	VersionLabel string    `json:"version_label"`
	ModDir       string    `json:"mod_dir"`
	CreatedAt    time.Time `json:"created_at"`
}

type ServerConfigInput struct {
	Name     string  `json:"name"`
	GameID   string  `json:"game_id"`
	IP       string  `json:"ip"`
	Port     int     `json:"port"`
	Username string  `json:"username"`
	Version  Version `json:"version"`
	ModDir   string  `json:"mod_dir"`
}

type JoinPreparation struct {
	Server     ServerConfig      `json:"server"`
	VersionDir string            `json:"version_dir"`
	RuntimeDir string            `json:"runtime_dir"`
	GameDir    string            `json:"game_dir"`
	Downloads  []PackageDownload `json:"downloads,omitempty"`
}

type ServerStore struct{ path string }

func NewServerStore(path string) ServerStore { return ServerStore{path: path} }

func DefaultServerStore() (ServerStore, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ServerStore{}, fmt.Errorf("resolve user config directory: %w", err)
	}
	return NewServerStore(filepath.Join(dir, "PrismPanel", "game-servers.json")), nil
}

func (s ServerStore) List() ([]ServerConfig, error) {
	servers, err := s.load()
	if err != nil {
		return nil, err
	}
	sort.Slice(servers, func(left, right int) bool { return servers[left].CreatedAt.After(servers[right].CreatedAt) })
	return servers, nil
}

func (s ServerStore) Create(input ServerConfigInput) (ServerConfig, error) {
	server, err := normalizeServerInput(input)
	if err != nil {
		return ServerConfig{}, err
	}
	servers, err := s.load()
	if err != nil {
		return ServerConfig{}, err
	}
	server.ID = serverID(server)
	server.CreatedAt = time.Now().UTC()
	servers = append(servers, server)
	if err := s.save(servers); err != nil {
		return ServerConfig{}, err
	}
	return server, nil
}

func (s ServerStore) Update(id string, input ServerConfigInput) (ServerConfig, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ServerConfig{}, errors.New("server id is required")
	}
	updated, err := normalizeServerInput(input)
	if err != nil {
		return ServerConfig{}, err
	}
	servers, err := s.load()
	if err != nil {
		return ServerConfig{}, err
	}
	found := false
	for index := range servers {
		if servers[index].ID != id {
			continue
		}
		updated.ID = servers[index].ID
		updated.CreatedAt = servers[index].CreatedAt
		servers[index] = updated
		found = true
		break
	}
	if !found {
		return ServerConfig{}, fmt.Errorf("server config not found: %s", id)
	}
	if err := s.save(servers); err != nil {
		return ServerConfig{}, err
	}
	return updated, nil
}

func NewTransientServer(input ServerConfigInput) (ServerConfig, error) {
	server, err := normalizeServerInput(input)
	if err != nil {
		return ServerConfig{}, err
	}
	server.ID = stableServerID(server)
	server.CreatedAt = time.Now().UTC()
	return server, nil
}

func NewTransientNetworkGame(input ServerConfigInput) (ServerConfig, error) {
	server, err := normalizeServerInputForLaunch(input, true)
	if err != nil {
		return ServerConfig{}, err
	}
	if strings.TrimSpace(server.GameID) == "" || server.GameID == localLauncherGameIDValue {
		return ServerConfig{}, errors.New("network game id is required")
	}
	server.ID = stableServerID(server)
	server.CreatedAt = time.Now().UTC()
	return server, nil
}
func (s ServerStore) Delete(id string) ([]ServerConfig, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("server id is required")
	}
	servers, err := s.load()
	if err != nil {
		return nil, err
	}
	kept := servers[:0]
	removed := false
	for _, server := range servers {
		if server.ID == id {
			removed = true
			continue
		}
		kept = append(kept, server)
	}
	if !removed {
		return nil, fmt.Errorf("server config not found: %s", id)
	}
	if err := s.save(kept); err != nil {
		return nil, err
	}
	sort.Slice(kept, func(left, right int) bool { return kept[left].CreatedAt.After(kept[right].CreatedAt) })
	return kept, nil
}

func (s ServerStore) Get(id string) (ServerConfig, error) {
	servers, err := s.load()
	if err != nil {
		return ServerConfig{}, err
	}
	for _, server := range servers {
		if server.ID == id {
			return server, nil
		}
	}
	return ServerConfig{}, fmt.Errorf("server config not found: %s", id)
}

func (s ServerStore) load() ([]ServerConfig, error) {
	contents, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return []ServerConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read local game servers: %w", err)
	}
	var servers []ServerConfig
	if err := json.Unmarshal(contents, &servers); err != nil {
		return nil, fmt.Errorf("decode local game servers: %w", err)
	}
	return servers, nil
}

func (s ServerStore) save(servers []ServerConfig) error {
	contents, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		return fmt.Errorf("encode local game servers: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create local game servers directory: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(s.path, contents, 0o600); err != nil {
		return fmt.Errorf("write local game servers: %w", err)
	}
	return nil
}

func normalizeServerInput(input ServerConfigInput) (ServerConfig, error) {
	return normalizeServerInputForLaunch(input, true)
}

func normalizeServerInputForLaunch(input ServerConfigInput, requireModDir bool) (ServerConfig, error) {
	name := strings.TrimSpace(input.Name)
	ip := strings.TrimSpace(input.IP)
	username := strings.TrimSpace(input.Username)
	modDir := strings.TrimSpace(input.ModDir)
	gameID := strings.TrimSpace(input.GameID)
	if name == "" {
		return ServerConfig{}, errors.New("server name is required")
	}
	if ip == "" {
		ip = "127.0.0.1"
	}
	if input.Port <= 0 {
		input.Port = 25565
	}
	if input.Port > 65535 {
		return ServerConfig{}, errors.New("server port must be between 1 and 65535")
	}
	if username == "" {
		return ServerConfig{}, errors.New("role username is required")
	}
	if strings.TrimSpace(gameID) == "" {
		return ServerConfig{}, errors.New("network game id is required")
	}
	if err := ValidateNetGameID(gameID); err != nil {
		return ServerConfig{}, err
	}
	if modDir != "" {
		modDir = cleanOptionalPath(modDir)
	}
	if requireModDir && modDir == "" {
		return ServerConfig{}, errors.New("custom resource directory is required")
	}
	if err := input.Version.Validate(); err != nil || input.Version == VersionBase {
		return ServerConfig{}, errors.New("game version is required")
	}
	label, err := VersionLabel(input.Version)
	if err != nil {
		return ServerConfig{}, err
	}
	return ServerConfig{
		Name: name, GameID: gameID, IP: ip, Port: input.Port, Username: username,
		Version: input.Version, VersionLabel: label, ModDir: modDir,
	}, nil
}

func cleanOptionalPath(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return filepath.Clean(value)
}

func stableServerID(server ServerConfig) string {
	source := strings.Join([]string{
		server.Name, server.GameID, server.IP, strconv.Itoa(server.Port), server.Username,
		fmt.Sprintf("%d", server.Version), server.ModDir,
	}, "\x00")
	hash := sha256.Sum256([]byte(source))
	return hex.EncodeToString(hash[:12])
}
func serverID(server ServerConfig) string {
	source := strings.Join([]string{
		server.Name, server.GameID, server.IP, strconv.Itoa(server.Port), server.Username,
		fmt.Sprintf("%d", server.Version), server.ModDir, time.Now().UTC().Format(time.RFC3339Nano),
	}, "\x00")
	hash := sha256.Sum256([]byte(source))
	return hex.EncodeToString(hash[:12])
}

func PrepareServerRuntime(server ServerConfig) (JoinPreparation, error) {
	return PrepareLaunchRuntime(server, NewLocalLaunchProfile(server, ""))
}

func PrepareLaunchRuntime(server ServerConfig, profile LaunchProfile) (JoinPreparation, error) {
	profile = profile.normalized(server, "")
	if profile.UseCustomResourceDir {
		if err := EnsureModDirectories(server.ModDir); err != nil {
			return JoinPreparation{}, err
		}
	}
	paths, err := DefaultCachePathsForVersion(server.VersionLabel)
	if err != nil {
		return JoinPreparation{}, err
	}
	if !directoryExists(paths.BaseMC) {
		return JoinPreparation{}, fmt.Errorf("game version %s is not installed at %s", server.VersionLabel, paths.Version)
	}
	runtimeDir := RuntimeDirectory(paths, server)
	if err := ResetRuntimeDirectory(paths, runtimeDir); err != nil {
		return JoinPreparation{}, err
	}
	if err := copyDirectory(paths.BaseMC, runtimeDir); err != nil {
		return JoinPreparation{}, fmt.Errorf("copy base minecraft files: %w", err)
	}
	if profile.Kind == LaunchKindNetGame {
		gameRoot := filepath.Join(paths.Game, safePathSegment(profile.LauncherGameID()), ".minecraft")
		if directoryExists(gameRoot) {
			if err := copyDirectory(gameRoot, runtimeDir); err != nil {
				return JoinPreparation{}, fmt.Errorf("copy network game component: %w", err)
			}
		}
		if err := installCachedCoreMods(paths, profile.LauncherGameID(), filepath.Join(runtimeDir, "mods")); err != nil {
			return JoinPreparation{}, err
		}
	}
	if err := ensureNetEaseNativeRuntime(paths, runtimeDir, server.VersionLabel); err != nil {
		return JoinPreparation{}, err
	}
	if profile.UseCustomResourceDir {
		if err := mergeModDirectory(server.ModDir, runtimeDir); err != nil {
			return JoinPreparation{}, err
		}
	}
	return JoinPreparation{Server: server, VersionDir: paths.Version, RuntimeDir: runtimeDir, GameDir: runtimeDir}, nil
}

func installCachedCoreMods(paths CachePaths, gameID, targetModsPath string) error {
	sourceRoot := filepath.Join(paths.GameMods, safePathSegment(gameID))
	if !directoryExists(sourceRoot) {
		return nil
	}
	files, err := filesWithExtension(sourceRoot, ".jar")
	if err != nil {
		return err
	}
	for _, source := range files {
		target := filepath.Join(targetModsPath, filepath.Base(source))
		if err := copyFile(source, target); err != nil {
			return fmt.Errorf("copy core mod %s: %w", filepath.Base(source), err)
		}
	}
	return nil
}

const netEaseRuntimeDLL = "api-ms-win-crt-utility-l1-1-1.dll"

func ensureNetEaseNativeRuntime(paths CachePaths, runtimeDir, versionLabel string) error {
	nativesPath := filepath.Join(runtimeDir, "versions", versionLabel, "natives-windows-x86_64")
	if !directoryExists(nativesPath) {
		return nil
	}
	runtimePath := filepath.Join(nativesPath, "runtime")
	target := filepath.Join(runtimePath, netEaseRuntimeDLL)
	source, err := findNetEaseRuntimeDLL(paths, runtimeDir, versionLabel)
	if err != nil {
		return err
	}
	if samePath(source, target) {
		return nil
	}
	if err := os.MkdirAll(runtimePath, 0o755); err != nil {
		return fmt.Errorf("create NetEase native runtime directory: %w", err)
	}
	if err := copyFile(source, target); err != nil {
		return fmt.Errorf("install NetEase native runtime DLL: %w", err)
	}
	return nil
}

func findNetEaseRuntimeDLL(paths CachePaths, runtimeDir, versionLabel string) (string, error) {
	for _, candidate := range netEaseRuntimeDLLCandidates(paths, runtimeDir, versionLabel) {
		if fileExists(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("missing NetEase native runtime DLL %s; place it at %s or set PRISMPANEL_NETEASE_RUNTIME_DLL", netEaseRuntimeDLL, filepath.Join(paths.Root, "native-runtime", netEaseRuntimeDLL))
}

func netEaseRuntimeDLLCandidates(paths CachePaths, runtimeDir, versionLabel string) []string {
	var candidates []string
	if value := strings.TrimSpace(os.Getenv("PRISMPANEL_NETEASE_RUNTIME_DLL")); value != "" {
		candidates = append(candidates, value)
	}
	if value := strings.TrimSpace(os.Getenv("PRISMPANEL_NETEASE_RUNTIME_DIR")); value != "" {
		candidates = append(candidates, filepath.Join(value, netEaseRuntimeDLL))
	}
	candidates = append(candidates,
		filepath.Join(paths.Root, "native-runtime", netEaseRuntimeDLL),
		filepath.Join(paths.BaseMC, "versions", versionLabel, "natives-windows-x86_64", "runtime", netEaseRuntimeDLL),
		filepath.Join(runtimeDir, "versions", versionLabel, "natives-windows-x86_64", "runtime", netEaseRuntimeDLL),
	)
	if executable, err := os.Executable(); err == nil {
		executableDir := filepath.Dir(executable)
		candidates = append(candidates,
			filepath.Join(executableDir, "resources", netEaseRuntimeDLL),
			filepath.Join(executableDir, "assets", netEaseRuntimeDLL),
			filepath.Join(executableDir, netEaseRuntimeDLL),
		)
	}
	return candidates
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
func EnsureModDirectories(modDir string) error {
	modDir = strings.TrimSpace(modDir)
	if modDir == "" {
		return errors.New("mod directory is required")
	}
	for _, name := range []string{"mods", "config", "resourcepacks", "shaderpacks"} {
		if err := os.MkdirAll(filepath.Join(modDir, name), 0o755); err != nil {
			return fmt.Errorf("create mod directory %s: %w", name, err)
		}
	}
	return nil
}

func ResetRuntimeDirectory(paths CachePaths, runtimeDir string) error {
	root, err := filepath.Abs(paths.Runtime)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(runtimeDir)
	if err != nil {
		return err
	}
	if samePath(root, target) || !pathWithin(root, target) {
		return fmt.Errorf("refuse to clean unsafe runtime directory: %s", runtimeDir)
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("clean runtime directory: %w", err)
	}
	return os.MkdirAll(target, 0o755)
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func RuntimeDirectory(paths CachePaths, server ServerConfig) string {
	gameID := safeRuntimeSegment(server.GameID)
	roleName := safeRuntimeSegment(server.Username)
	return filepath.Join(paths.Runtime, gameID+"-"+roleName)
}

func safeRuntimeSegment(value string) string {
	cleaned := strings.Map(func(char rune) rune {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '.' || char == '_' || char == '-' {
			return char
		}
		return '-'
	}, strings.TrimSpace(value))
	cleaned = strings.Trim(cleaned, "-._")
	if cleaned == "" {
		return "game"
	}
	return cleaned
}

func mergeModDirectory(sourceRoot, gameDir string) error {
	for _, name := range []string{"mods", "config", "resourcepacks", "shaderpacks"} {
		source := filepath.Join(sourceRoot, name)
		if directoryExists(source) {
			if err := copyDirectory(source, filepath.Join(gameDir, name)); err != nil {
				return fmt.Errorf("copy %s: %w", name, err)
			}
		}
	}
	return nil
}

func copyDirectory(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("source is not a directory: %s", source)
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(target, 0o755)
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		return copyFile(path, destination)
	})
}

func copyFile(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(target)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

var unsafePathSegment = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func safePathSegment(value string) string {
	cleaned := strings.Trim(unsafePathSegment.ReplaceAllString(value, "-"), "-._")
	if cleaned == "" {
		return "server"
	}
	return cleaned
}
