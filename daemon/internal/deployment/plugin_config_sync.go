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

// syncConfigFiles 同步镜像源配置到目标实例。directories 为显式指定的同步根
// （由前端自适应检测传入）；为空时使用服务器配置的 config_sync_directories。
func (m *Manager) syncConfigFiles(item *task, cfg model.ServerConfig, instanceID, targetWorkspace string, directories []string) error {
	targetWorkspace = filepath.Clean(targetWorkspace)
	targetInfo, err := os.Stat(targetWorkspace)
	if err != nil || !targetInfo.IsDir() {
		return apperr.New("INVALID_CONFIG", "目标实例工作目录不存在或不可读取")
	}
	if len(directories) == 0 {
		directories = cfg.ConfigSyncDirectories
	}
	if len(directories) == 0 {
		// 防御性兜底：配置未经过 Normalize（例如单元测试直接构造）时只同步 plugins。
		directories = []string{"plugins"}
	}
	for _, directory := range directories {
		if err := item.context.Err(); err != nil {
			return err
		}
		if err := m.syncConfigDirectory(item, cfg, instanceID, targetWorkspace, directory); err != nil {
			return err
		}
	}
	m.setCopyStage(item, "finalizing")
	return nil
}

func (m *Manager) syncConfigDirectory(item *task, cfg model.ServerConfig, instanceID, targetWorkspace, directory string) error {
	sourceRoot := filepath.Join(cfg.RootPath, cfg.ImageDirectory, directory)
	targetRoot := filepath.Join(targetWorkspace, directory)

	sourceInfo, err := os.Stat(sourceRoot)
	if err != nil || !sourceInfo.IsDir() {
		return apperr.New("INVALID_CONFIG", fmt.Sprintf("镜像源 %s 目录不存在或不可读取", directory))
	}
	if err := os.MkdirAll(targetRoot, 0o750); err != nil {
		return apperr.Wrap("FILE_WRITE_FAILED", fmt.Sprintf("无法创建目标 %s 目录", directory), err)
	}

	skip := func(relative string, entry fs.DirEntry) bool {
		if entry.Type()&os.ModeSymlink != 0 {
			return false
		}
		rootRelative := filepath.Join(directory, relative)
		if isExcluded(rootRelative, entry.IsDir(), cfg.Exclude) {
			return true
		}
		if !entry.IsDir() {
			if !cfg.AllowsPluginConfigSync(entry.Name()) {
				return true
			}
			// plugins 目录只同步插件子目录内的配置文件，跳过根级文件（如插件 jar、散落的配置）；
			// 其他同步根（如 config）允许根级白名单文件，mod 服的 config 目录通常是散文件。
			if directory == "plugins" && filepath.Dir(relative) == "." {
				return true
			}
		}
		return false
	}

	m.log(item, "info", "scanning_config", instanceID, fmt.Sprintf("正在扫描镜像源 %s 配置", directory))
	m.beginCopyProgress(item, "scanning_config", 0, 0)
	files, bytes, err := scanTree(item.context, sourceRoot, skip)
	if err != nil {
		return err
	}
	m.beginCopyProgress(item, "copying_config", files, bytes)
	m.log(item, "info", "copying_config", instanceID,
		fmt.Sprintf("正在同步 %s：%d 个配置文件（%d 字节）", directory, files, bytes))
	if err := m.copyTreeOverwrite(item.context, sourceRoot, targetRoot, skip, func(bytes int64, fileDone bool) {
		m.advanceCopyProgress(item, bytes, fileDone)
	}); err != nil {
		return err
	}
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

// ---- 配置同步自适应检测 ----

// ConfigSyncDirInfo 单个同步根目录的检测结果。
type ConfigSyncDirInfo struct {
	Name      string `json:"name"`
	Exists    bool   `json:"exists"`
	FileCount int    `json:"file_count"`
}

// ConfigSyncIssue 检测出的异常/无法确定情况（前端弹窗说明）。
type ConfigSyncIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ConfigSyncDetectResult 配置同步自适应检测结果。
type ConfigSyncDetectResult struct {
	Platform              string             `json:"platform"`
	ConfiguredDirectories []string           `json:"configured_directories"`
	Directories           []ConfigSyncDirInfo `json:"directories"`
	Recommended           []string           `json:"recommended"`
	Issues                []ConfigSyncIssue  `json:"issues"`
}

// DetectConfigSyncDirs 检测镜像源中可同步的配置目录，供前端自适应选择：
//   - 插件服（paper/spigot）：配置在 plugins/ 下，默认候选 ["plugins"]；
//   - mod 服（fabric/forge）：mod 配置在 config/ 下，默认候选 ["config","plugins"]；
//   - 以服务器配置的 config_sync_directories 覆盖默认候选；
//   - 检测实际存在的目录并给出推荐；缺失关键目录时返回 issues（前端弹窗说明）。
func (m *Manager) DetectConfigSyncDirs(serverID string) (ConfigSyncDetectResult, error) {
	cfg, err := m.servers.Get(serverID)
	if err != nil {
		return ConfigSyncDetectResult{}, err
	}
	if cfg.Type != "mirror" {
		return ConfigSyncDetectResult{}, apperr.New("INVALID_STATE", "只有镜像服务器组支持配置同步检测")
	}
	candidates := append([]string(nil), cfg.ConfigSyncDirectories...)
	if len(candidates) == 0 {
		if model.IsModPlatform(cfg.Platform) {
			candidates = append([]string(nil), model.ModConfigSyncDirectories...)
		} else {
			candidates = append([]string(nil), model.DefaultConfigSyncDirectories...)
		}
	}
	imageRoot := filepath.Join(cfg.RootPath, cfg.ImageDirectory)
	result := ConfigSyncDetectResult{
		Platform:              cfg.Platform,
		ConfiguredDirectories: append([]string(nil), cfg.ConfigSyncDirectories...),
	}
	for _, directory := range candidates {
		root := filepath.Join(imageRoot, directory)
		info, statErr := os.Stat(root)
		exists := statErr == nil && info.IsDir()
		fileCount := 0
		if exists {
			fileCount = countSyncableFiles(root, directory, cfg)
		}
		result.Directories = append(result.Directories, ConfigSyncDirInfo{
			Name: directory, Exists: exists, FileCount: fileCount,
		})
		if exists {
			result.Recommended = append(result.Recommended, directory)
		}
	}

	hasDir := func(name string) bool {
		for _, dir := range result.Directories {
			if dir.Name == name && dir.Exists {
				return true
			}
		}
		return false
	}
	hasConfig, hasPlugins := hasDir("config"), hasDir("plugins")
	switch {
	case model.IsModPlatform(cfg.Platform) && !hasConfig && hasPlugins:
		result.Issues = append(result.Issues, ConfigSyncIssue{
			Code: "MOD_CONFIG_DIR_MISSING",
			Message: "mod 服的配置通常在镜像源 config/ 目录下，但未检测到 config/ 目录；当前仅能同步 plugins/。" +
				"如需同步 mod 配置，请先在镜像源根目录创建 config/ 目录并放入配置文件。",
		})
	case model.IsModPlatform(cfg.Platform) && !hasConfig && !hasPlugins:
		result.Issues = append(result.Issues, ConfigSyncIssue{
			Code: "NO_CONFIG_DIR_FOUND",
			Message: "镜像源未检测到任何可同步的配置目录（config/ 与 plugins/ 均不存在），无法确定配置位置。" +
				"mod 配置应放在镜像源的 config/ 目录下。",
		})
	case !model.IsModPlatform(cfg.Platform) && !hasPlugins:
		result.Issues = append(result.Issues, ConfigSyncIssue{
			Code: "PLUGINS_DIR_MISSING",
			Message: "插件服镜像源未检测到 plugins/ 目录，插件配置无法同步。" +
				"请先在镜像源根目录创建 plugins/ 目录并放入插件配置。",
		})
	}
	if len(result.Recommended) == 0 {
		result.Issues = append(result.Issues, ConfigSyncIssue{
			Code: "NO_CONFIG_DIR_FOUND",
			Message: "镜像源未检测到任何可同步的配置目录，无法确定配置位置。",
		})
	}
	return result, nil
}

// countSyncableFiles 统计镜像源某目录中符合白名单的可同步文件数（上限保护）。
func countSyncableFiles(root, directory string, cfg model.ServerConfig) int {
	skip := func(relative string, entry fs.DirEntry) bool {
		if entry.IsDir() {
			return false
		}
		if !cfg.AllowsPluginConfigSync(entry.Name()) {
			return true
		}
		// 与同步语义一致：plugins 根跳过根级文件（插件 jar），config 根允许根级白名单文件
		if directory == "plugins" && filepath.Dir(relative) == "." {
			return true
		}
		return false
	}
	files, _, err := scanTree(context.Background(), root, skip)
	if err != nil {
		return 0
	}
	return int(files)
}
