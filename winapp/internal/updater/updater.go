package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxExecutableSize = int64(256 * 1024 * 1024)

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Release struct {
	Version     string    `json:"version"`
	Platform    string    `json:"platform"`
	Arch        string    `json:"arch"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	BuiltAt     time.Time `json:"built_at"`
	Notes       string    `json:"notes"`
	PublishedAt time.Time `json:"published_at"`
	DownloadURL string    `json:"download_url"`
}

type Status struct {
	CurrentVersion  string   `json:"current_version"`
	UpdateAvailable bool     `json:"update_available"`
	Latest          *Release `json:"latest,omitempty"`
}

type apiEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func Check(ctx context.Context, panelURL, currentVersion string) (Status, error) {
	base, err := normalizePanelURL(panelURL)
	if err != nil {
		return Status{}, err
	}
	endpoint := *base
	endpoint.Path = "/api/v1/winapp/update"
	query := endpoint.Query()
	query.Set("version", strings.TrimSpace(currentVersion))
	query.Set("platform", "windows")
	query.Set("arch", "amd64")
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Status{}, err
	}
	request.Header.Set("User-Agent", "PrismPanel-WinApp/"+currentVersion)
	response, err := restrictedClient(base).Do(request)
	if err != nil {
		return Status{}, fmt.Errorf("检查 WinApp 更新失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("检查 WinApp 更新失败: HTTP %d", response.StatusCode)
	}
	var envelope apiEnvelope
	if err := json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&envelope); err != nil {
		return Status{}, fmt.Errorf("解析 WinApp 更新响应失败: %w", err)
	}
	if !envelope.Success {
		message := "Panel 拒绝了更新检查"
		if envelope.Error != nil && strings.TrimSpace(envelope.Error.Message) != "" {
			message = envelope.Error.Message
		}
		return Status{}, errors.New(message)
	}
	var status Status
	if err := json.Unmarshal(envelope.Data, &status); err != nil {
		return Status{}, fmt.Errorf("解析 WinApp 更新状态失败: %w", err)
	}
	if status.CurrentVersion != strings.TrimSpace(currentVersion) {
		return Status{}, errors.New("Panel 返回的当前版本不匹配")
	}
	if status.Latest != nil {
		if err := validateRelease(*status.Latest); err != nil {
			return Status{}, err
		}
		if status.UpdateAvailable && CompareVersions(status.Latest.Version, currentVersion) <= 0 {
			return Status{}, errors.New("Panel 返回的更新版本没有高于当前版本")
		}
	}
	return status, nil
}

func Download(ctx context.Context, panelURL string, release Release) (string, error) {
	if err := validateRelease(release); err != nil {
		return "", err
	}
	base, err := normalizePanelURL(panelURL)
	if err != nil {
		return "", err
	}
	downloadURL, err := base.Parse(release.DownloadURL)
	if err != nil || !sameOrigin(base, downloadURL) {
		return "", errors.New("WinApp 更新下载地址无效")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := restrictedClient(base).Do(request)
	if err != nil {
		return "", fmt.Errorf("下载 WinApp 更新失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 WinApp 更新失败: HTTP %d", response.StatusCode)
	}
	cacheRoot, err := updateCacheRoot()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(cacheRoot, release.Version)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	temporary, err := os.CreateTemp(directory, ".download-*.exe")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(response.Body, maxExecutableSize+1))
	closeErr := temporary.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written != release.Size || written > maxExecutableSize ||
		hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(release.SHA256) {
		return "", errors.New("下载的 WinApp 更新与发布清单不一致")
	}
	if err := validateWindowsExecutable(temporaryPath); err != nil {
		return "", err
	}
	target := filepath.Join(directory, "PrismPanel.exe")
	_ = os.Remove(target)
	if err := os.Rename(temporaryPath, target); err != nil {
		return "", err
	}
	return target, nil
}

func BeginApply(downloadedPath, targetPath string, waitPID int) error {
	downloadedPath, err := filepath.Abs(filepath.Clean(downloadedPath))
	if err != nil {
		return err
	}
	targetPath, err = filepath.Abs(filepath.Clean(targetPath))
	if err != nil {
		return err
	}
	if samePath(downloadedPath, targetPath) || waitPID <= 0 {
		return errors.New("WinApp 更新参数无效")
	}
	probe, err := os.CreateTemp(filepath.Dir(targetPath), ".PrismPanel-write-test-*")
	if err != nil {
		return fmt.Errorf("WinApp 安装目录不可写: %w", err)
	}
	probePath := probe.Name()
	if closeErr := probe.Close(); closeErr != nil {
		_ = os.Remove(probePath)
		return closeErr
	}
	_ = os.Remove(probePath)
	return startProcessWithoutConsole(downloadedPath, []string{
		"--apply-update", "--target", targetPath, "--wait-pid", strconv.Itoa(waitPID),
	})
}

func IsApplyMode(args []string) bool {
	for _, argument := range args {
		if argument == "--apply-update" {
			return true
		}
	}
	return false
}

func Apply(args []string) error {
	flags := flag.NewFlagSet("apply-update", flag.ContinueOnError)
	apply := flags.Bool("apply-update", false, "apply downloaded update")
	target := flags.String("target", "", "installed executable path")
	waitPID := flags.Int("wait-pid", 0, "process to wait for")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*apply || *waitPID <= 0 || strings.TrimSpace(*target) == "" {
		return errors.New("WinApp 更新参数无效")
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	targetPath, err := filepath.Abs(filepath.Clean(*target))
	if err != nil {
		return err
	}
	if samePath(source, targetPath) || !strings.EqualFold(filepath.Ext(targetPath), ".exe") {
		return errors.New("WinApp 更新目标无效")
	}
	if err := waitForProcessExit(*waitPID, 90*time.Second); err != nil {
		return err
	}
	if err := replaceAndRestart(source, targetPath); err != nil {
		_ = startProcessWithoutConsole(targetPath, nil)
		return err
	}
	return nil
}

func CleanupPrevious(executablePath string) {
	time.Sleep(3 * time.Second)
	_ = os.Remove(executablePath + ".old")
	cacheRoot, err := updateCacheRoot()
	if err == nil {
		_ = os.RemoveAll(cacheRoot)
	}
}

func RecordFailure(err error) {
	if err == nil {
		return
	}
	directory, pathErr := os.UserConfigDir()
	if pathErr != nil {
		return
	}
	path := filepath.Join(directory, "PrismPanel", "update-error.log")
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+" "+err.Error()+"\n"), 0o600)
}

func replaceAndRestart(source, target string) error {
	directory := filepath.Dir(target)
	staging, err := os.CreateTemp(directory, ".PrismPanel-update-*.exe")
	if err != nil {
		return fmt.Errorf("无法写入 WinApp 安装目录: %w", err)
	}
	stagingPath := staging.Name()
	defer os.Remove(stagingPath)
	input, err := os.Open(source)
	if err != nil {
		staging.Close()
		return err
	}
	_, copyErr := io.Copy(staging, input)
	input.Close()
	closeErr := staging.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	backup := target + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("备份旧版 WinApp 失败: %w", err)
	}
	if err := os.Rename(stagingPath, target); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("替换 WinApp 失败: %w", err)
	}
	if err := startProcessWithoutConsole(target, nil); err != nil {
		_ = os.Remove(target)
		_ = os.Rename(backup, target)
		return fmt.Errorf("启动新版 WinApp 失败: %w", err)
	}
	return nil
}

func validateRelease(release Release) error {
	if !versionPattern.MatchString(release.Version) || release.Platform != "windows" || release.Arch != "amd64" {
		return errors.New("Panel 返回的 WinApp 版本信息无效")
	}
	if release.Size <= 0 || release.Size > maxExecutableSize || len(release.SHA256) != 64 {
		return errors.New("Panel 返回的 WinApp 文件信息无效")
	}
	if _, err := hex.DecodeString(release.SHA256); err != nil {
		return errors.New("Panel 返回的 WinApp 文件校验值无效")
	}
	if strings.TrimSpace(release.DownloadURL) == "" {
		return errors.New("Panel 未提供 WinApp 更新下载地址")
	}
	return nil
}

func validateWindowsExecutable(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 2)
	if _, err := io.ReadFull(file, header); err != nil || string(header) != "MZ" {
		return errors.New("下载的更新不是有效的 Windows 可执行文件")
	}
	return nil
}

func normalizePanelURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(value), "/"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("Panel 地址无效")
	}
	parsed.Path = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func restrictedClient(base *url.URL) *http.Client {
	return &http.Client{
		Timeout: 15 * time.Minute,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if !sameOrigin(base, request.URL) {
				return errors.New("WinApp 更新下载不能重定向到其他服务器")
			}
			return nil
		},
	}
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

func updateCacheRoot() (string, error) {
	directory, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "PrismPanel", "updates"), nil
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func CompareVersions(left, right string) int {
	leftParts, leftOK := versionParts(left)
	rightParts, rightOK := versionParts(right)
	if !leftOK && !rightOK {
		return strings.Compare(left, right)
	}
	if !leftOK {
		return -1
	}
	if !rightOK {
		return 1
	}
	for index := range leftParts {
		if leftParts[index] < rightParts[index] {
			return -1
		}
		if leftParts[index] > rightParts[index] {
			return 1
		}
	}
	return 0
}

func versionParts(value string) ([3]uint64, bool) {
	var result [3]uint64
	if !versionPattern.MatchString(value) {
		return result, false
	}
	for index, part := range strings.Split(value, ".") {
		parsed, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return [3]uint64{}, false
		}
		result[index] = parsed
	}
	return result, true
}
