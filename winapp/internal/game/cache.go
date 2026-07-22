package game

import (
	"archive/zip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type CachePaths struct {
	Root      string
	Version   string
	Downloads string
	Base      string
	BaseMC    string
	Runtime   string
	Java      string
}

func DefaultCachePaths() (CachePaths, error) { return DefaultCachePathsForVersion("base") }

func DefaultCachePathsForVersion(versionLabel string) (CachePaths, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return CachePaths{}, fmt.Errorf("resolve user cache directory: %w", err)
	}
	root = filepath.Join(root, "PrismPanel", "game-cache")
	versionLabel = strings.TrimSpace(versionLabel)
	if versionLabel == "" {
		versionLabel = "base"
	}
	versionDir := filepath.Join(root, safePathSegment(versionLabel))
	return CachePaths{
		Root: root, Version: versionDir, Downloads: filepath.Join(versionDir, "downloads"),
		Base: versionDir, BaseMC: filepath.Join(versionDir, ".minecraft"), Runtime: filepath.Join(root, "runtime"),
		Java: filepath.Join(root, "java"),
	}, nil
}

func EnsureInstanceDirectories(instanceDir string) error { return EnsureModDirectories(instanceDir) }

type PackageDownload struct {
	Label       string
	URL         string
	MD5         string
	Destination string
	MD5File     string
	ExtractTo   string
}

func VersionDownloads(paths CachePaths, base, version MinecraftClientLibs) []PackageDownload {
	versionName := strings.TrimSpace(version.Version)
	if versionName == "" {
		versionName = fmt.Sprintf("%d", version.MCVersion)
	}
	return []PackageDownload{
		{Label: "base package", URL: base.URL, MD5: base.MD5, Destination: filepath.Join(paths.Downloads, "GameBase.zip"), MD5File: filepath.Join(paths.Base, "GAME_BASE.MD5"), ExtractTo: paths.Base},
		{Label: versionName + " package", URL: version.URL, MD5: version.MD5, Destination: filepath.Join(paths.Downloads, versionName+".zip"), MD5File: filepath.Join(paths.Base, versionName+".MD5"), ExtractTo: paths.Base},
		{Label: versionName + " libraries", URL: version.CoreLibURL, MD5: version.CoreLibMD5, Destination: filepath.Join(paths.Downloads, versionName+"_Lib.7z"), MD5File: filepath.Join(paths.Base, versionName+"_Lib.MD5")},
	}
}

func DownloadIfNeeded(ctx context.Context, item PackageDownload) (bool, error) {
	return DownloadIfNeededWithProgress(ctx, item, nil)
}

func DownloadIfNeededWithProgress(ctx context.Context, item PackageDownload, progress func(phase string, current, total int64)) (bool, error) {
	if item.URL == "" || item.MD5 == "" {
		return false, fmt.Errorf("%s download metadata is incomplete", item.Label)
	}
	if ok, err := md5FileMatches(item.MD5File, item.MD5); err != nil {
		return false, err
	} else if ok {
		if progress != nil {
			progress("cached", 1, 1)
		}
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(item.Destination), 0o755); err != nil {
		return false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return false, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("download %s failed: %s", item.Label, response.Status)
	}
	tmp := item.Destination + ".part"
	file, err := os.Create(tmp)
	if err != nil {
		return false, err
	}
	reader := io.Reader(response.Body)
	if progress != nil {
		reader = &progressReader{reader: response.Body, phase: "download", total: response.ContentLength, progress: progress}
	}
	hash := md5.New()
	_, copyErr := io.Copy(io.MultiWriter(file, hash), reader)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return false, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return false, closeErr
	}
	actual := strings.ToLower(hex.EncodeToString(hash.Sum(nil)))
	expected := strings.ToLower(strings.TrimSpace(item.MD5))
	if actual != expected {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("download %s md5 mismatch: expected %s, got %s", item.Label, expected, actual)
	}
	if err := os.Rename(tmp, item.Destination); err != nil {
		return false, err
	}
	if item.ExtractTo != "" && strings.EqualFold(filepath.Ext(item.Destination), ".zip") {
		completedEntries := 0
		if err := extractZipWithProgress(item.Destination, item.ExtractTo, func(done, total int) {
			completedEntries += done
			if progress != nil {
				progress("extract", int64(completedEntries), int64(total))
			}
		}); err != nil {
			return false, fmt.Errorf("extract %s: %w", item.Label, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(item.MD5File), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(item.MD5File, []byte(expected), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func InstallCoreLibraries(ctx context.Context, paths CachePaths, versionLabel string, item PackageDownload, progress func(phase string, current, total int64)) error {
	extractDir := coreLibraryExtractDir(item)
	if !directoryHasFiles(extractDir) {
		if item.Destination == "" {
			return fmt.Errorf("%s archive path is empty", item.Label)
		}
		if _, err := os.Stat(item.Destination); err != nil {
			return fmt.Errorf("core libraries archive is not available: %w", err)
		}
		if progress != nil {
			progress("extract", 0, 1)
		}
		if err := extractSevenZip(ctx, item.Destination, extractDir); err != nil {
			return fmt.Errorf("extract %s: %w", item.Label, err)
		}
		if progress != nil {
			progress("extract", 1, 1)
		}
	}
	return installCoreLibrariesFromDir(paths, versionLabel, extractDir, progress)
}

func coreLibraryExtractDir(item PackageDownload) string {
	if item.Destination == "" {
		return ""
	}
	return strings.TrimSuffix(item.Destination, filepath.Ext(item.Destination))
}

func extractSevenZip(ctx context.Context, source, target string) error {
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	if tool, err := exec.LookPath("7z"); err == nil {
		return runArchiveExtractor(ctx, tool, "x", "-y", "-o"+target, source)
	}
	if tool, err := exec.LookPath("7zz"); err == nil {
		return runArchiveExtractor(ctx, tool, "x", "-y", "-o"+target, source)
	}
	if tool, err := exec.LookPath("tar"); err == nil {
		return runArchiveExtractor(ctx, tool, "-xf", source, "-C", target)
	}
	return errorsNewNoExtractor()
}

func runArchiveExtractor(ctx context.Context, tool string, args ...string) error {
	command := exec.CommandContext(ctx, tool, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("%s failed: %s", filepath.Base(tool), message)
	}
	return nil
}

func errorsNewNoExtractor() error {
	return fmt.Errorf("no 7z extractor found; install 7z/7zz or ensure Windows tar.exe is available")
}

func installCoreLibrariesFromDir(paths CachePaths, versionLabel, sourceRoot string, progress func(phase string, current, total int64)) error {
	files, err := listRegularFiles(sourceRoot)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("core libraries directory is empty: %s", sourceRoot)
	}
	libraryFiles, err := libraryFilesByName(filepath.Join(paths.BaseMC, "libraries"))
	if err != nil {
		return err
	}
	versionDir := filepath.Join(paths.BaseMC, "versions", versionLabel)
	for index, file := range files {
		name := filepath.Base(file)
		if strings.EqualFold(filepath.Ext(name), ".jar") {
			for _, target := range libraryFiles[name] {
				if err := copyFile(file, target); err != nil {
					return fmt.Errorf("install core library %s: %w", name, err)
				}
			}
		} else {
			if err := copyFile(file, filepath.Join(versionDir, name)); err != nil {
				return fmt.Errorf("install core version file %s: %w", name, err)
			}
		}
		if progress != nil {
			progress("install", int64(index+1), int64(len(files)))
		}
	}
	return nil
}

func listRegularFiles(root string) ([]string, error) {
	var files []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return files, nil
}

func libraryFilesByName(root string) (map[string][]string, error) {
	files := make(map[string][]string)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			name := filepath.Base(path)
			files[name] = append(files[name], path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return files, nil
}

func directoryHasFiles(root string) bool {
	files, err := listRegularFiles(root)
	return err == nil && len(files) > 0
}

func md5FileMatches(path, expected string) (bool, error) {
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(string(contents)), strings.TrimSpace(expected)), nil
}

func extractZip(source, target string) error {
	return extractZipWithProgress(source, target, nil)
}

func extractZipWithProgress(source, target string, progress func(done, total int)) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer reader.Close()
	total := len(reader.File)
	for _, file := range reader.File {
		clean, err := cleanArchivePath(file.Name)
		if err != nil {
			return err
		}
		if clean == "." {
			if progress != nil {
				progress(1, total)
			}
			continue
		}
		destination := filepath.Join(target, filepath.FromSlash(clean))
		if !pathWithin(target, destination) {
			return fmt.Errorf("archive entry escapes target: %s", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(destination, 0o755); err != nil {
				return err
			}
			if progress != nil {
				progress(1, total)
			}
			continue
		}
		if file.Mode()&os.ModeSymlink != 0 || !file.Mode().IsRegular() {
			return fmt.Errorf("unsupported archive entry: %s", file.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		input, err := file.Open()
		if err != nil {
			return err
		}
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutputErr != nil {
			return closeOutputErr
		}
		if closeInputErr != nil {
			return closeInputErr
		}
		if progress != nil {
			progress(1, total)
		}
	}
	return nil
}

func cleanArchivePath(name string) (string, error) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	if name == "" {
		return ".", nil
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || filepath.IsAbs(clean) {
		return "", fmt.Errorf("unsafe archive path: %s", name)
	}
	return clean, nil
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

type progressReader struct {
	reader   io.Reader
	phase    string
	total    int64
	read     int64
	progress func(phase string, current, total int64)
}

func (r *progressReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if n > 0 {
		r.read += int64(n)
		r.progress(r.phase, r.read, r.total)
	}
	return n, err
}
