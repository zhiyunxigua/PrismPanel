package winappupdates

import (
	"archive/zip"
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
	"sync"
	"time"
)

const (
	manifestSchemaVersion = 1
	maxBundleSize         = int64(300 * 1024 * 1024)
	maxExecutableSize     = int64(256 * 1024 * 1024)
	maxManifestSize       = int64(64 * 1024)
	maxReleaseNotesRunes  = 20000
)

var (
	versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	ErrNoRelease   = errors.New("no WinApp release is published")
)

type BuildManifest struct {
	SchemaVersion int       `json:"schema_version"`
	Product       string    `json:"product"`
	Platform      string    `json:"platform"`
	Arch          string    `json:"arch"`
	Version       string    `json:"version"`
	File          string    `json:"file"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	BuiltAt       time.Time `json:"built_at"`
}

type Uploader struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
}

type Release struct {
	BuildManifest
	Notes       string    `json:"notes"`
	PublishedBy Uploader  `json:"published_by"`
	PublishedAt time.Time `json:"published_at"`
}

type Repository struct {
	root string
	mu   sync.RWMutex
}

func NewRepository(root string) (*Repository, error) {
	absolute, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(absolute, "releases"), 0o750); err != nil {
		return nil, fmt.Errorf("create WinApp update repository: %w", err)
	}
	return &Repository{root: absolute}, nil
}

func (r *Repository) Publish(bundle io.Reader, size int64, notes string, uploader Uploader) (Release, error) {
	if bundle == nil || size <= 0 || size > maxBundleSize {
		return Release{}, fmt.Errorf("WinApp release bundle must be between 1 byte and %d bytes", maxBundleSize)
	}
	notes = strings.TrimSpace(notes)
	if len([]rune(notes)) > maxReleaseNotesRunes {
		return Release{}, fmt.Errorf("release notes cannot exceed %d characters", maxReleaseNotesRunes)
	}
	temporaryBundle, err := os.CreateTemp(r.root, ".bundle-*.zip")
	if err != nil {
		return Release{}, err
	}
	temporaryPath := temporaryBundle.Name()
	defer os.Remove(temporaryPath)
	written, copyErr := io.Copy(temporaryBundle, io.LimitReader(bundle, maxBundleSize+1))
	closeErr := temporaryBundle.Close()
	if copyErr != nil {
		return Release{}, copyErr
	}
	if closeErr != nil {
		return Release{}, closeErr
	}
	if written != size || written > maxBundleSize {
		return Release{}, errors.New("WinApp release bundle size does not match the upload")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	manifest, err := inspectBundle(temporaryPath)
	if err != nil {
		return Release{}, err
	}
	if latest, latestErr := r.latestLocked(); latestErr == nil {
		if CompareVersions(manifest.Version, latest.Version) <= 0 {
			return Release{}, fmt.Errorf("release version must be newer than %s", latest.Version)
		}
	} else if !errors.Is(latestErr, ErrNoRelease) {
		return Release{}, latestErr
	}

	finalDirectory := filepath.Join(r.root, "releases", manifest.Version)
	if _, err := os.Stat(finalDirectory); err == nil {
		return Release{}, errors.New("release version already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return Release{}, err
	}
	staging, err := os.MkdirTemp(filepath.Join(r.root, "releases"), ".release-*")
	if err != nil {
		return Release{}, err
	}
	defer os.RemoveAll(staging)
	if err := extractExecutable(temporaryPath, filepath.Join(staging, manifest.File), manifest); err != nil {
		return Release{}, err
	}
	release := Release{
		BuildManifest: manifest, Notes: notes, PublishedBy: uploader, PublishedAt: time.Now().UTC(),
	}
	if err := writeJSONFile(filepath.Join(staging, "release.json"), release); err != nil {
		return Release{}, err
	}
	if err := os.Rename(staging, finalDirectory); err != nil {
		return Release{}, fmt.Errorf("publish WinApp release: %w", err)
	}
	if err := writeJSONAtomic(filepath.Join(r.root, "latest.json"), release); err != nil {
		_ = os.RemoveAll(finalDirectory)
		return Release{}, err
	}
	return release, nil
}

func (r *Repository) Latest() (Release, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.latestLocked()
}

func (r *Repository) latestLocked() (Release, error) {
	var release Release
	if err := readJSONFile(filepath.Join(r.root, "latest.json"), &release); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Release{}, ErrNoRelease
		}
		return Release{}, err
	}
	if err := validateManifest(release.BuildManifest); err != nil {
		return Release{}, fmt.Errorf("invalid latest WinApp release: %w", err)
	}
	return release, nil
}

func (r *Repository) List() ([]Release, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entries, err := os.ReadDir(filepath.Join(r.root, "releases"))
	if err != nil {
		return nil, err
	}
	releases := make([]Release, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !versionPattern.MatchString(entry.Name()) {
			continue
		}
		var release Release
		if err := readJSONFile(filepath.Join(r.root, "releases", entry.Name(), "release.json"), &release); err == nil {
			releases = append(releases, release)
		}
	}
	sort.Slice(releases, func(left, right int) bool {
		return CompareVersions(releases[left].Version, releases[right].Version) > 0
	})
	return releases, nil
}

func (r *Repository) Artifact(version string) (Release, string, error) {
	version = strings.TrimSpace(version)
	if !versionPattern.MatchString(version) {
		return Release{}, "", errors.New("invalid release version")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var release Release
	directory := filepath.Join(r.root, "releases", version)
	if err := readJSONFile(filepath.Join(directory, "release.json"), &release); err != nil {
		return Release{}, "", err
	}
	if release.Version != version || release.File != "PrismPanel.exe" {
		return Release{}, "", errors.New("release metadata does not match the requested version")
	}
	return release, filepath.Join(directory, release.File), nil
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

func inspectBundle(path string) (BuildManifest, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return BuildManifest{}, fmt.Errorf("open WinApp release bundle: %w", err)
	}
	defer archive.Close()
	if len(archive.File) != 2 {
		return BuildManifest{}, errors.New("WinApp release bundle must contain only manifest.json and PrismPanel.exe")
	}
	var manifestFile, executable *zip.File
	for _, item := range archive.File {
		if item.FileInfo().IsDir() || filepath.Base(item.Name) != item.Name {
			return BuildManifest{}, errors.New("WinApp release bundle cannot contain directories")
		}
		switch item.Name {
		case "manifest.json":
			manifestFile = item
		case "PrismPanel.exe":
			executable = item
		default:
			return BuildManifest{}, fmt.Errorf("unexpected release bundle file: %s", item.Name)
		}
	}
	if manifestFile == nil || executable == nil {
		return BuildManifest{}, errors.New("WinApp release bundle is incomplete")
	}
	manifestContents, err := readZipFile(manifestFile, maxManifestSize)
	if err != nil {
		return BuildManifest{}, err
	}
	var manifest BuildManifest
	if err := json.Unmarshal(manifestContents, &manifest); err != nil {
		return BuildManifest{}, fmt.Errorf("decode WinApp release manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return BuildManifest{}, err
	}
	if int64(executable.UncompressedSize64) != manifest.Size || manifest.Size > maxExecutableSize {
		return BuildManifest{}, errors.New("WinApp executable size does not match the manifest")
	}
	return manifest, nil
}

func validateManifest(manifest BuildManifest) error {
	if manifest.SchemaVersion != manifestSchemaVersion || manifest.Product != "PrismPanel" ||
		manifest.Platform != "windows" || manifest.Arch != "amd64" || manifest.File != "PrismPanel.exe" {
		return errors.New("WinApp release manifest targets an unsupported product or platform")
	}
	if !versionPattern.MatchString(manifest.Version) {
		return errors.New("WinApp release version must use MAJOR.MINOR.PATCH")
	}
	if manifest.Size <= 0 || manifest.Size > maxExecutableSize {
		return errors.New("WinApp executable size is invalid")
	}
	if len(manifest.SHA256) != 64 {
		return errors.New("WinApp executable SHA-256 is invalid")
	}
	if _, err := hex.DecodeString(manifest.SHA256); err != nil {
		return errors.New("WinApp executable SHA-256 is invalid")
	}
	if manifest.BuiltAt.IsZero() {
		return errors.New("WinApp release build time is required")
	}
	return nil
}

func extractExecutable(bundlePath, target string, manifest BuildManifest) error {
	archive, err := zip.OpenReader(bundlePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	var source *zip.File
	for _, item := range archive.File {
		if item.Name == manifest.File {
			source = item
			break
		}
	}
	if source == nil {
		return errors.New("WinApp executable is missing from the release bundle")
	}
	input, err := source.Open()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, maxExecutableSize+1))
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != manifest.Size || hex.EncodeToString(hash.Sum(nil)) != strings.ToLower(manifest.SHA256) {
		return errors.New("WinApp executable does not match the release manifest")
	}
	file, err := os.Open(target)
	if err != nil {
		return err
	}
	defer file.Close()
	header := make([]byte, 2)
	if _, err := io.ReadFull(file, header); err != nil || string(header) != "MZ" {
		return errors.New("WinApp release does not contain a Windows executable")
	}
	return nil
}

func readZipFile(source *zip.File, limit int64) ([]byte, error) {
	if int64(source.UncompressedSize64) > limit {
		return nil, errors.New("release manifest is too large")
	}
	input, err := source.Open()
	if err != nil {
		return nil, err
	}
	defer input.Close()
	contents, err := io.ReadAll(io.LimitReader(input, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > limit {
		return nil, errors.New("release manifest is too large")
	}
	return contents, nil
}

func readJSONFile(path string, output any) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(contents, output)
}

func writeJSONFile(path string, value any) error {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(contents, '\n'), 0o640)
}

func writeJSONAtomic(path string, value any) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".latest-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	contents, err := json.MarshalIndent(value, "", "  ")
	if err == nil {
		_, err = temporary.Write(append(contents, '\n'))
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	backupPath := path + ".old"
	_ = os.Remove(backupPath)
	if err := os.Rename(path, backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Rename(backupPath, path)
		return err
	}
	_ = os.Remove(backupPath)
	return nil
}
