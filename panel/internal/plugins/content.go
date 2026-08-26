package plugins

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// 内容包类型：与 daemon 端约定一致（t2 接口约定）。
const (
	ContentTypeConfig = "config"
	ContentTypeFull   = "full"
)

// 内容包解压上限：完全配置可能包含 world 等大目录，
// 文件数与展开体积上限放宽（上传体积仍由 HTTP 层 maxPluginContentZIP 限制）。
const (
	maxContentFiles = 65536
	maxContentBytes = int64(4 * 1024 * 1024 * 1024)
)

const contentIndexSchemaVersion = 1

func validContentType(value string) bool {
	return value == ContentTypeConfig || value == ContentTypeFull
}

// contentIndexPath 返回制品下内容包版本索引路径（content.yaml）。
func contentIndexPath(artifactDir string) string {
	return filepath.Join(artifactDir, "content.yaml")
}

// loadContentIndex 读取制品的内容包版本索引；无索引时返回空索引。
func loadContentIndex(artifactDir string) (ContentIndex, error) {
	var index ContentIndex
	err := readYAML(contentIndexPath(artifactDir), &index)
	if errors.Is(err, os.ErrNotExist) {
		return ContentIndex{}, nil
	}
	if err != nil {
		return ContentIndex{}, err
	}
	return index, nil
}

// saveContentIndex 原子写内容包版本索引。
func saveContentIndex(artifactDir string, index ContentIndex) error {
	index.SchemaVersion = contentIndexSchemaVersion
	return atomicYAML(contentIndexPath(artifactDir), index)
}

// nextContentID 返回制品内下一个内容包版本号。
func nextContentID(index ContentIndex) int64 {
	var highest int64
	for _, version := range index.Versions {
		if version.ContentID > highest {
			highest = version.ContentID
		}
	}
	return highest + 1
}

// contentVersionDir 返回内容包版本的磁盘目录（content/<contentID>）。
func contentVersionDir(artifactDir string, contentID int64) string {
	return filepath.Join(artifactDir, "content", strconv.FormatInt(contentID, 10))
}

// currentContentDir 返回当前内容包的磁盘目录：优先 content/<ContentID>；
// 兼容旧版未版本化的 content/ 目录（ContentID 缺失或目录不存在时回退）。
func currentContentDir(artifactDir string, content *ContentSnapshot) (string, error) {
	if content == nil || !content.Present {
		return "", errors.New("plugin artifact has no content snapshot")
	}
	if content.ContentID > 0 {
		versioned := contentVersionDir(artifactDir, content.ContentID)
		if info, err := os.Stat(versioned); err == nil && info.IsDir() {
			return versioned, nil
		}
	}
	legacy := filepath.Join(artifactDir, "content")
	if info, err := os.Stat(legacy); err == nil && info.IsDir() {
		return legacy, nil
	}
	return "", fmt.Errorf("content directory missing for content_id %d", content.ContentID)
}

// extractContentZIP 将内容包 zip 解压到 destination（顶层即 destination 的内容）。
// 校验强度与 extractConfigZIP 一致：拒绝 `..`/绝对路径/符号链接/非普通文件（Zip Slip 防护）。
func extractContentZIP(contents []byte, destination string) (int, int64, error) {
	reader, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		return 0, 0, fmt.Errorf("open content zip: %w", err)
	}
	if len(reader.File) > maxContentFiles {
		return 0, 0, fmt.Errorf("content zip exceeds %d entries", maxContentFiles)
	}
	var files int
	var total int64
	for _, entry := range reader.File {
		name, err := cleanArchivePath(entry.Name)
		if err != nil {
			return 0, 0, err
		}
		if name == "" {
			continue
		}
		// manifest.yaml 为保留名：zip 顶层的 manifest.yaml 是 bundle manifest 的占位名
		//（打包与部署时均跳过，防与 bundle manifest 重名冲突），解压时不入库，
		// 使仓库快照（tree/files/sha256）与部署行为一致。
		if name == "manifest.yaml" {
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return 0, 0, fmt.Errorf("content zip contains symbolic link %s", name)
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return 0, 0, err
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return 0, 0, fmt.Errorf("content zip contains unsupported entry %s", name)
		}
		total += int64(entry.UncompressedSize64)
		if total > maxContentBytes {
			return 0, 0, fmt.Errorf("content zip exceeds %d bytes", maxContentBytes)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return 0, 0, err
		}
		source, err := entry.Open()
		if err != nil {
			return 0, 0, err
		}
		destinationFile, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
		if err != nil {
			source.Close()
			return 0, 0, err
		}
		written, copyErr := io.Copy(destinationFile, io.LimitReader(source, int64(entry.UncompressedSize64)+1))
		closeErr := destinationFile.Close()
		source.Close()
		if copyErr != nil || closeErr != nil || written != int64(entry.UncompressedSize64) {
			return 0, 0, fmt.Errorf("extract content entry %s", name)
		}
		files++
	}
	return files, total, nil
}

// contentTopLevelTree 记录内容包根目录的顶层结构（zip 顶层目录树，供前端预览）。
func contentTopLevelTree(root string) ([]ContentTreeEntry, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	tree := make([]ContentTreeEntry, 0, len(entries))
	for _, entry := range entries {
		item := ContentTreeEntry{Path: entry.Name()}
		if entry.IsDir() {
			item.Type = "dir"
		} else {
			item.Type = "file"
			info, err := entry.Info()
			if err != nil {
				return nil, err
			}
			item.Size = info.Size()
		}
		tree = append(tree, item)
	}
	sort.Slice(tree, func(left, right int) bool { return tree[left].Path < tree[right].Path })
	return tree, nil
}

// findJARInZIP 在内容包 zip（内存）中查找第一个可解析的 jar/mod 条目，
// 用于"仅内容包上传"（完全配置）时自动推导制品身份；找不到返回 found=false。
func findJARInZIP(contents []byte, pluginType string) (map[string]Descriptor, Descriptor, bool) {
	reader, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		return nil, Descriptor{}, false
	}
	candidates := make([]string, 0)
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() || !entry.Mode().IsRegular() {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name), ".jar") {
			candidates = append(candidates, entry.Name)
		}
	}
	sort.Strings(candidates)
	byName := make(map[string]*zip.File, len(reader.File))
	for _, entry := range reader.File {
		byName[entry.Name] = entry
	}
	for _, name := range candidates {
		entry := byName[name]
		if entry == nil || entry.UncompressedSize64 == 0 || entry.UncompressedSize64 > maxPluginJARSize {
			continue
		}
		source, err := entry.Open()
		if err != nil {
			continue
		}
		jarBytes, readErr := io.ReadAll(io.LimitReader(source, maxPluginJARSize+1))
		source.Close()
		if readErr != nil || int64(len(jarBytes)) > maxPluginJARSize {
			continue
		}
		descriptors, primary, err := ParseModJAR(jarBytes, filepath.Base(entry.Name), pluginType)
		if err != nil {
			continue
		}
		return descriptors, primary, true
	}
	return nil, Descriptor{}, false
}
