package files

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"PrismPanel-daemon/internal/apperr"
)

const recycleBinName = ".prism-recycle-bin"

type RecycleEntry struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	OriginalPath string    `json:"original_path"`
	Type         string    `json:"type"`
	Size         int64     `json:"size"`
	Mode         uint32    `json:"mode"`
	DeletedAt    time.Time `json:"deleted_at"`
}

type recycleManifest struct {
	Name         string    `json:"name"`
	OriginalPath string    `json:"original_path"`
	Type         string    `json:"type"`
	Size         int64     `json:"size"`
	Mode         uint32    `json:"mode"`
	DeletedAt    time.Time `json:"deleted_at"`
}

func (s *Service) RecycleList(target Target) ([]RecycleEntry, error) {
	root, err := s.root(target)
	if err != nil {
		return nil, err
	}
	bin := filepath.Join(root, recycleBinName)
	entries, err := os.ReadDir(bin)
	if errors.Is(err, os.ErrNotExist) {
		return []RecycleEntry{}, nil
	}
	if err != nil {
		return nil, fileError(err, "回收站读取失败")
	}
	result := make([]RecycleEntry, 0, len(entries))
	for _, item := range entries {
		if !item.IsDir() {
			continue
		}
		manifest, err := readRecycleManifest(filepath.Join(bin, item.Name()))
		if err != nil {
			continue
		}
		result = append(result, RecycleEntry{ID: item.Name(), Name: manifest.Name,
			OriginalPath: manifest.OriginalPath, Type: manifest.Type, Size: manifest.Size,
			Mode: manifest.Mode, DeletedAt: manifest.DeletedAt})
	}
	// Keep the newest deletion first, matching BaoTa's recycle-bin view.
	sort.Slice(result, func(i, j int) bool { return result[i].DeletedAt.After(result[j].DeletedAt) })
	return result, nil
}

func (s *Service) RecycleRestore(target Target, id string) error {
	return s.mutate(target, func(root string) error {
		bin, err := ensureRecycleBin(root)
		if err != nil {
			return err
		}
		entryDir, manifest, err := locateRecycleEntry(bin, id)
		if err != nil {
			return err
		}
		original, err := securePath(root, manifest.OriginalPath, true)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(original); err == nil {
			return apperr.New("FILE_EXISTS", "恢复目标已存在")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fileError(err, "恢复目标检查失败")
		}
		if err := os.MkdirAll(filepath.Dir(original), 0o750); err != nil {
			return fileError(err, "恢复目录创建失败")
		}
		if err := os.Rename(filepath.Join(entryDir, "data"), original); err != nil {
			return apperr.Wrap("FILE_WRITE_FAILED", "文件恢复失败", err)
		}
		if err := os.RemoveAll(entryDir); err != nil {
			return apperr.Wrap("FILE_WRITE_FAILED", "回收站记录清理失败", err)
		}
		return nil
	})
}

func (s *Service) RecycleDelete(target Target, ids []string) error {
	if len(ids) == 0 || len(ids) > 100 {
		return apperr.New("INVALID_REQUEST", "回收站删除目标数量必须在 1 到 100 之间")
	}
	return s.mutate(target, func(root string) error {
		bin, err := ensureRecycleBin(root)
		if err != nil {
			return err
		}
		for _, id := range ids {
			entryDir, _, err := locateRecycleEntry(bin, id)
			if err != nil {
				return err
			}
			if err := os.RemoveAll(entryDir); err != nil {
				return apperr.Wrap("FILE_WRITE_FAILED", "回收站文件删除失败", err)
			}
		}
		return nil
	})
}

func (s *Service) RecycleClear(target Target) error {
	return s.mutate(target, func(root string) error {
		bin, err := ensureRecycleBin(root)
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(bin)
		if err != nil {
			return fileError(err, "回收站读取失败")
		}
		for _, item := range entries {
			if err := os.RemoveAll(filepath.Join(bin, item.Name())); err != nil {
				return apperr.Wrap("FILE_WRITE_FAILED", "回收站清空失败", err)
			}
		}
		return nil
	})
}

func moveToRecycleBin(root, relative string) error {
	_, err := moveToRecycleBinWithID(root, relative)
	return err
}

func moveToRecycleBinWithID(root, relative string) (string, error) {
	if relative == "." || isRecyclePath(relative) {
		return "", apperr.New("PATH_ESCAPE", "不能操作回收站目录")
	}
	target, err := securePath(root, relative, false)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", fileError(err, "删除目标读取失败")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", apperr.New("PATH_ESCAPE", "不能删除符号链接")
	}
	bin, err := ensureRecycleBin(root)
	if err != nil {
		return "", err
	}
	id, err := newRecycleID()
	if err != nil {
		return "", err
	}
	entryDir := filepath.Join(bin, id)
	if err := os.Mkdir(entryDir, 0o750); err != nil {
		return "", apperr.Wrap("FILE_WRITE_FAILED", "回收站目录创建失败", err)
	}
	manifest := recycleManifest{Name: filepath.Base(relative), OriginalPath: relative, Type: "file", Size: info.Size(), Mode: uint32(info.Mode().Perm()), DeletedAt: time.Now().UTC()}
	if info.IsDir() {
		manifest.Type = "directory"
		manifest.Size = directorySize(target)
	}
	raw, err := json.Marshal(manifest)
	if err == nil {
		err = os.WriteFile(filepath.Join(entryDir, "manifest.json"), raw, 0o640)
	}
	if err == nil {
		err = os.Rename(target, filepath.Join(entryDir, "data"))
	}
	if err != nil {
		_ = os.RemoveAll(entryDir)
		return "", apperr.Wrap("FILE_WRITE_FAILED", "移动到回收站失败", err)
	}
	return id, nil
}

// publishWithRecycle keeps the previous target recoverable while publishing a replacement.
func publishWithRecycle(root, relative string, publish func() error) error {
	id, err := moveToRecycleBinWithID(root, relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return publish()
		}
		return err
	}
	if err := publish(); err != nil {
		restoreErr := restoreRecycleEntry(root, id)
		if restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	return nil
}

func restoreRecycleEntry(root, id string) error {
	bin, err := ensureRecycleBin(root)
	if err != nil {
		return err
	}
	entryDir, manifest, err := locateRecycleEntry(bin, id)
	if err != nil {
		return err
	}
	original, err := securePath(root, manifest.OriginalPath, true)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(original); err == nil {
		return apperr.New("FILE_EXISTS", "回滚目标已存在")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fileError(err, "回滚目标检查失败")
	}
	if err := os.MkdirAll(filepath.Dir(original), 0o750); err != nil {
		return fileError(err, "回滚目录创建失败")
	}
	if err := os.Rename(filepath.Join(entryDir, "data"), original); err != nil {
		return apperr.Wrap("FILE_WRITE_FAILED", "覆盖失败后的文件恢复失败", err)
	}
	return os.RemoveAll(entryDir)
}

func ensureRecycleBin(root string) (string, error) {
	bin := filepath.Join(root, recycleBinName)
	info, err := os.Lstat(bin)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(bin, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
			return "", apperr.Wrap("FILE_WRITE_FAILED", "回收站目录创建失败", err)
		}
		info, err = os.Lstat(bin)
	}
	if err != nil {
		return "", fileError(err, "回收站目录不可用")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", apperr.New("PATH_ESCAPE", "回收站目录无效")
	}
	return bin, nil
}

func locateRecycleEntry(bin, id string) (string, recycleManifest, error) {
	if id == "" || strings.ContainsAny(id, `/\\`) || id == "." || id == ".." {
		return "", recycleManifest{}, apperr.New("INVALID_REQUEST", "回收站条目标识无效")
	}
	entryDir := filepath.Join(bin, id)
	entryInfo, err := os.Lstat(entryDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", recycleManifest{}, apperr.New("FILE_NOT_FOUND", "回收站条目不存在")
	}
	if err != nil {
		return "", recycleManifest{}, fileError(err, "回收站条目读取失败")
	}
	if entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.IsDir() {
		return "", recycleManifest{}, apperr.New("INVALID_REQUEST", "回收站条目无效")
	}
	manifest, err := readRecycleManifest(entryDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", recycleManifest{}, apperr.New("FILE_NOT_FOUND", "回收站条目不存在")
	}
	if err != nil {
		return "", recycleManifest{}, apperr.Wrap("FILE_READ_FAILED", "回收站条目无效", err)
	}
	if manifest.Type != "file" && manifest.Type != "directory" {
		return "", recycleManifest{}, apperr.New("INVALID_REQUEST", "回收站条目类型无效")
	}
	dataInfo, err := os.Lstat(filepath.Join(entryDir, "data"))
	if err != nil {
		return "", recycleManifest{}, apperr.Wrap("FILE_READ_FAILED", "回收站内容不可用", err)
	}
	if dataInfo.Mode()&os.ModeSymlink != 0 || (manifest.Type == "directory" && !dataInfo.IsDir()) ||
		(manifest.Type == "file" && !dataInfo.Mode().IsRegular()) {
		return "", recycleManifest{}, apperr.New("INVALID_REQUEST", "回收站内容类型无效")
	}
	return entryDir, manifest, nil
}

func readRecycleManifest(entryDir string) (recycleManifest, error) {
	raw, err := os.ReadFile(filepath.Join(entryDir, "manifest.json"))
	if err != nil {
		return recycleManifest{}, err
	}
	var manifest recycleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return recycleManifest{}, err
	}
	clean, err := normalizeRelative(manifest.OriginalPath)
	if err != nil || clean == "." || isRecyclePath(clean) || manifest.Name == "" {
		return recycleManifest{}, apperr.New("INVALID_REQUEST", "回收站原始路径无效")
	}
	manifest.OriginalPath = clean
	return manifest, nil
}

func newRecycleID() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", apperr.Wrap("FILE_WRITE_FAILED", "生成回收站标识失败", err)
	}
	return hex.EncodeToString(raw), nil
}

func directorySize(root string) int64 {
	var size int64
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil && info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return size
}

func isRecyclePath(relative string) bool {
	clean := filepath.ToSlash(relative)
	return clean == recycleBinName || strings.HasPrefix(clean, recycleBinName+"/")
}
