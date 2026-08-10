package deployment

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/atomicfile"
	"PrismPanel-daemon/internal/model"
)

func (m *Manager) syncPluginConfigFiles(item *task, cfg model.ServerConfig, instanceID, targetWorkspace string) error {
	sourceRoot := filepath.Join(cfg.RootPath, cfg.ImageDirectory, "plugins")
	targetWorkspace = filepath.Clean(targetWorkspace)
	targetRoot := filepath.Join(targetWorkspace, "plugins")

	sourceInfo, err := os.Stat(sourceRoot)
	if err != nil || !sourceInfo.IsDir() {
		return apperr.New("INVALID_CONFIG", "镜像源 plugins 目录不存在或不可读取")
	}
	targetInfo, err := os.Stat(targetWorkspace)
	if err != nil || !targetInfo.IsDir() {
		return apperr.New("INVALID_CONFIG", "目标实例工作目录不存在或不可读取")
	}
	if err := os.MkdirAll(targetRoot, 0o750); err != nil {
		return apperr.Wrap("FILE_WRITE_FAILED", "无法创建目标 plugins 目录", err)
	}

	skip := func(relative string, entry fs.DirEntry) bool {
		if entry.Type()&os.ModeSymlink != 0 {
			return false
		}
		rootRelative := filepath.Join("plugins", relative)
		if isExcluded(rootRelative, entry.IsDir(), cfg.Exclude) {
			return true
		}
		if !entry.IsDir() && filepath.Dir(relative) == "." {
			return true
		}
		return !entry.IsDir() && !cfg.AllowsPluginConfigSync(entry.Name())
	}

	m.log(item, "info", "scanning_plugin_config", instanceID, "正在扫描镜像源插件配置")
	m.beginCopyProgress(item, "scanning_plugin_config", 0, 0)
	files, bytes, err := scanTree(item.context, sourceRoot, skip)
	if err != nil {
		return err
	}
	m.beginCopyProgress(item, "copying_plugin_config", files, bytes)
	m.log(item, "info", "copying_plugin_config", instanceID,
		fmt.Sprintf("正在同步 %d 个插件配置文件（%d 字节）", files, bytes))
	if err := m.copyTreeOverwrite(item.context, sourceRoot, targetRoot, skip, func(bytes int64, fileDone bool) {
		m.advanceCopyProgress(item, bytes, fileDone)
	}); err != nil {
		return err
	}
	m.setCopyStage(item, "finalizing")
	return nil
}

func (m *Manager) copyTreeOverwrite(
	ctx context.Context,
	sourceRoot string,
	destinationRoot string,
	skip func(relative string, entry fs.DirEntry) bool,
	progress func(bytes int64, fileDone bool),
) error {
	type copyJob struct {
		source      string
		destination string
		info        fs.FileInfo
	}
	workerContext, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan copyJob, m.copyConcurrency*2)
	var workers sync.WaitGroup
	var firstError error
	var errorOnce sync.Once
	recordError := func(err error) {
		if err == nil {
			return
		}
		errorOnce.Do(func() {
			firstError = err
			cancel()
		})
	}
	for worker := 0; worker < m.copyConcurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			buffer := make([]byte, 256*1024)
			for {
				select {
				case <-workerContext.Done():
					return
				case job, open := <-jobs:
					if !open {
						return
					}
					if _, err := copyFileAtomicWithBuffer(workerContext, job.source, job.destination, job.info, buffer, progress); err != nil {
						recordError(err)
						return
					}
				}
			}
		}()
	}
	walkErr := filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := workerContext.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed in plugin config sync: %s", relative)
		}
		if skip != nil && skip(relative, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		destination := filepath.Join(destinationRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type in plugin config sync: %s", relative)
		}
		select {
		case jobs <- copyJob{source: path, destination: destination, info: info}:
			return nil
		case <-workerContext.Done():
			return workerContext.Err()
		}
	})
	close(jobs)
	workers.Wait()
	if firstError != nil {
		return firstError
	}
	return walkErr
}

func copyFileAtomicWithBuffer(
	ctx context.Context,
	source string,
	destination string,
	info fs.FileInfo,
	buffer []byte,
	progress func(bytes int64, fileDone bool),
) (written int64, resultErr error) {
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return 0, err
	}
	sourceFile, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer sourceFile.Close()
	temp, err := os.CreateTemp(filepath.Dir(destination), ".prism-config-*")
	if err != nil {
		return 0, err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(info.Mode().Perm()); err != nil {
		_ = temp.Close()
		return 0, err
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = temp.Close()
			return written, err
		}
		count, readErr := sourceFile.Read(buffer)
		if count > 0 {
			wrote, writeErr := temp.Write(buffer[:count])
			written += int64(wrote)
			if wrote > 0 && progress != nil {
				progress(int64(wrote), false)
			}
			if writeErr != nil {
				_ = temp.Close()
				return written, writeErr
			}
			if wrote != count {
				_ = temp.Close()
				return written, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			_ = temp.Close()
			return written, readErr
		}
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return written, err
	}
	if err := temp.Close(); err != nil {
		return written, err
	}
	if err := atomicfile.Publish(tempPath, destination, true); err != nil {
		return written, err
	}
	if err := os.Chtimes(destination, time.Now(), info.ModTime()); err != nil {
		return written, err
	}
	if progress != nil {
		progress(0, true)
	}
	return written, nil
}
