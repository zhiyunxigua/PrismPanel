package plugins

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	maxConfigFiles = 4096
	maxConfigBytes = int64(512 * 1024 * 1024)
)

func atomicYAML(path string, value any) error {
	contents, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o640); err == nil {
		_, err = temp.Write(contents)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(path)
		return os.Rename(tempPath, path)
	}
	return nil
}

func readYAML(path string, value any) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(contents, value)
}

func extractConfigZIP(contents []byte, destination string) (int, int64, error) {
	reader, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		return 0, 0, fmt.Errorf("open config zip: %w", err)
	}
	if len(reader.File) > maxConfigFiles {
		return 0, 0, fmt.Errorf("config zip exceeds %d entries", maxConfigFiles)
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
		if entry.Mode()&os.ModeSymlink != 0 {
			return 0, 0, fmt.Errorf("config zip contains symbolic link %s", name)
		}
		target := filepath.Join(destination, filepath.FromSlash(name))
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o750); err != nil {
				return 0, 0, err
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			return 0, 0, fmt.Errorf("config zip contains unsupported entry %s", name)
		}
		total += int64(entry.UncompressedSize64)
		if total > maxConfigBytes {
			return 0, 0, fmt.Errorf("config zip exceeds %d bytes", maxConfigBytes)
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
			return 0, 0, fmt.Errorf("extract config entry %s", name)
		}
		files++
	}
	return files, total, nil
}

func cleanArchivePath(value string) (string, error) {
	value = strings.TrimPrefix(strings.ReplaceAll(value, "\\", "/"), "/")
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." {
		return "", nil
	}
	if filepath.IsAbs(filepath.FromSlash(value)) || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsRune(clean, 0) {
		return "", fmt.Errorf("config path escapes root: %s", value)
	}
	return clean, nil
}

func copyTree(source, destination string) (int, int64, error) {
	var files int
	var total int64
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("config contains symbolic link %s", relative)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("config contains unsupported entry %s", relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
		if err != nil {
			input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		files++
		total += info.Size()
		return nil
	})
	return files, total, err
}

func treeHash(root string) (string, int, int64, error) {
	type fileEntry struct {
		path string
		size int64
	}
	entries := make([]fileEntry, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("config contains unsupported entry %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries = append(entries, fileEntry{path: filepath.ToSlash(relative), size: info.Size()})
		return nil
	})
	if err != nil {
		return "", 0, 0, err
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].path < entries[right].path })
	hash := sha256.New()
	var total int64
	for _, entry := range entries {
		_, _ = io.WriteString(hash, entry.path)
		_, _ = hash.Write([]byte{0})
		file, err := os.Open(filepath.Join(root, filepath.FromSlash(entry.path)))
		if err != nil {
			return "", 0, 0, err
		}
		_, copyErr := io.Copy(hash, file)
		file.Close()
		if copyErr != nil {
			return "", 0, 0, copyErr
		}
		total += entry.size
	}
	return hex.EncodeToString(hash.Sum(nil)), len(entries), total, nil
}
