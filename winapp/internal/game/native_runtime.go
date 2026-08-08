package game

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"time"
)

const (
	fantnelRuntimeManifestURL  = "http://110.42.70.32:13423/api/fantnel/update/get?mode=static.win"
	fantnelRuntimeDownloadHost = "110.42.70.32:23148"
	maxRuntimeManifestSize     = 1 << 20
	maxNetEaseRuntimeSize      = 64 << 20
)

var (
	fantnelRuntimeSource = netEaseRuntimeSource{
		ManifestURL:  fantnelRuntimeManifestURL,
		DownloadHost: fantnelRuntimeDownloadHost,
	}
	netEaseRuntimeHTTPClient = &http.Client{
		Timeout: 45 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

type netEaseRuntimeSource struct {
	ManifestURL  string
	DownloadHost string
}

type fantnelUpdateResponse struct {
	Code int                 `json:"code"`
	Msg  string              `json:"msg"`
	Data []fantnelUpdateFile `json:"data"`
}

type fantnelUpdateFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

func InstallNetEaseRuntime(ctx context.Context) error {
	paths, err := DefaultCachePaths()
	if err != nil {
		return err
	}
	return installNetEaseRuntime(ctx, paths, netEaseRuntimeHTTPClient, fantnelRuntimeSource)
}

func installNetEaseRuntime(ctx context.Context, paths CachePaths, client *http.Client, source netEaseRuntimeSource) error {
	target := filepath.Join(paths.Root, "native-runtime", netEaseRuntimeDLL)
	checksumPath := target + ".sha256"

	item, err := fetchFantnelRuntimeManifest(ctx, client, source)
	if err != nil {
		matches, cacheErr := cachedRuntimeIsVerified(target, checksumPath)
		if matches {
			return nil
		}
		if cacheErr != nil {
			return errors.Join(fmt.Errorf("fetch Fantnel NetEase runtime manifest: %w", err), fmt.Errorf("inspect cached NetEase runtime: %w", cacheErr))
		}
		return fmt.Errorf("fetch Fantnel NetEase runtime manifest and no verified cache is available: %w", err)
	}

	matches, err := fileMatchesSHA256(target, item.Size, item.SHA256)
	if err != nil {
		return fmt.Errorf("inspect cached NetEase runtime: %w", err)
	}
	if matches {
		if err := validateAMD64PE(target); err != nil {
			matches = false
		}
	}
	if !matches {
		if err := downloadNetEaseRuntime(ctx, client, item, target); err != nil {
			return err
		}
	}
	if err := writeFileAtomically(checksumPath, []byte(strings.ToLower(item.SHA256)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write NetEase runtime checksum: %w", err)
	}
	return nil
}

func fetchFantnelRuntimeManifest(ctx context.Context, client *http.Client, source netEaseRuntimeSource) (fantnelUpdateFile, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.ManifestURL, nil)
	if err != nil {
		return fantnelUpdateFile{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return fantnelUpdateFile{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fantnelUpdateFile{}, fmt.Errorf("unexpected manifest status: %s", response.Status)
	}

	var payload fantnelUpdateResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxRuntimeManifestSize))
	if err := decoder.Decode(&payload); err != nil {
		return fantnelUpdateFile{}, fmt.Errorf("decode manifest: %w", err)
	}
	if payload.Code != 1 {
		return fantnelUpdateFile{}, fmt.Errorf("Fantnel manifest rejected request: code=%d message=%s", payload.Code, payload.Msg)
	}
	for _, item := range payload.Data {
		if pathpkg.Base(strings.ReplaceAll(item.Path, "\\", "/")) != netEaseRuntimeDLL {
			continue
		}
		checksum, err := validateRuntimeManifestFile(item, source)
		if err != nil {
			return fantnelUpdateFile{}, err
		}
		item.SHA256 = checksum
		return item, nil
	}
	return fantnelUpdateFile{}, fmt.Errorf("Fantnel manifest does not contain %s", netEaseRuntimeDLL)
}

func validateRuntimeManifestFile(item fantnelUpdateFile, source netEaseRuntimeSource) (string, error) {
	if item.Size <= 0 || item.Size > maxNetEaseRuntimeSize {
		return "", fmt.Errorf("invalid NetEase runtime size: %d", item.Size)
	}
	checksum, err := normalizeSHA256(item.SHA256)
	if err != nil {
		return "", fmt.Errorf("invalid NetEase runtime checksum: %w", err)
	}
	parsed, err := url.Parse(item.URL)
	if err != nil {
		return "", fmt.Errorf("parse NetEase runtime URL: %w", err)
	}
	if parsed.Scheme != "http" || !strings.EqualFold(parsed.Host, source.DownloadHost) || pathpkg.Base(parsed.Path) != netEaseRuntimeDLL {
		return "", fmt.Errorf("untrusted NetEase runtime URL: %s", item.URL)
	}
	return checksum, nil
}

func downloadNetEaseRuntime(ctx context.Context, client *http.Client, item fantnelUpdateFile, target string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download NetEase runtime: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download NetEase runtime: unexpected status %s", response.Status)
	}
	if response.ContentLength >= 0 && response.ContentLength != item.Size {
		return fmt.Errorf("download NetEase runtime: content length %d, want %d", response.ContentLength, item.Size)
	}

	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create NetEase runtime cache directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".netease-runtime-download-*.tmp")
	if err != nil {
		return fmt.Errorf("create NetEase runtime temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(temporary, hasher), io.LimitReader(response.Body, item.Size+1))
	if err != nil {
		return fmt.Errorf("download NetEase runtime body: %w", err)
	}
	if written != item.Size {
		return fmt.Errorf("download NetEase runtime size %d, want %d", written, item.Size)
	}
	actualChecksum := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualChecksum, item.SHA256) {
		return fmt.Errorf("download NetEase runtime checksum %s, want %s", actualChecksum, item.SHA256)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync NetEase runtime temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close NetEase runtime temporary file: %w", err)
	}
	if err := validateAMD64PE(temporaryPath); err != nil {
		return fmt.Errorf("validate downloaded NetEase runtime: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o644); err != nil {
		return fmt.Errorf("set NetEase runtime permissions: %w", err)
	}
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("install downloaded NetEase runtime: %w", err)
	}
	return nil
}

func cachedRuntimeIsVerified(target, checksumPath string) (bool, error) {
	contents, err := os.ReadFile(checksumPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fields := strings.Fields(string(contents))
	if len(fields) != 1 {
		return false, errors.New("cached checksum file is invalid")
	}
	checksum, err := normalizeSHA256(fields[0])
	if err != nil {
		return false, err
	}
	matches, err := fileMatchesSHA256(target, 0, checksum)
	if err != nil || !matches {
		return matches, err
	}
	if err := validateAMD64PE(target); err != nil {
		return false, err
	}
	return true, nil
}

func fileMatchesSHA256(filePath string, expectedSize int64, expectedChecksum string) (bool, error) {
	file, err := os.Open(filePath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		return false, nil
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return false, err
	}
	return strings.EqualFold(hex.EncodeToString(hasher.Sum(nil)), expectedChecksum), nil
}

func normalizeSHA256(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	decoded, err := hex.DecodeString(normalized)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("expected 64 hexadecimal characters")
	}
	return normalized, nil
}

func validateAMD64PE(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < 0x40 {
		return errors.New("file is too small for a PE header")
	}

	dosHeader := make([]byte, 0x40)
	if _, err := io.ReadFull(file, dosHeader); err != nil {
		return err
	}
	if string(dosHeader[:2]) != "MZ" {
		return errors.New("missing MZ header")
	}
	peOffset := int64(binary.LittleEndian.Uint32(dosHeader[0x3c:0x40]))
	if peOffset < 0 || peOffset+6 > info.Size() {
		return errors.New("invalid PE header offset")
	}
	peHeader := make([]byte, 6)
	if _, err := file.ReadAt(peHeader, peOffset); err != nil {
		return err
	}
	if string(peHeader[:4]) != "PE\x00\x00" {
		return errors.New("missing PE signature")
	}
	if machine := binary.LittleEndian.Uint16(peHeader[4:6]); machine != 0x8664 {
		return fmt.Errorf("PE architecture %#x is not AMD64", machine)
	}
	return nil
}

func writeFileAtomically(filePath string, contents []byte, mode os.FileMode) error {
	directory := filepath.Dir(filePath)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".netease-runtime-metadata-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporaryPath, mode); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filePath)
}
