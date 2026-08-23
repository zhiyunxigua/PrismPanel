package plugins

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"PrismPanel-daemon/internal/apperr"
	"PrismPanel-daemon/internal/model"
)

func (s *Service) Deploy(serverID, bundlePath string) (OperationResult, error) {
	server, err := s.servers.Get(serverID)
	if err != nil {
		return OperationResult{}, err
	}
	bundle, cleanup, err := prepareBundle(bundlePath)
	if err != nil {
		return OperationResult{}, apperr.Wrap("INVALID_PLUGIN_BUNDLE", "plugin bundle is invalid", err)
	}
	defer cleanup()
	if bundle.plugin.PluginType != model.PluginTypeForPlatform(server.Platform) {
		return OperationResult{}, apperr.New("PLUGIN_TYPE_MISMATCH", "plugin type does not match target server platform")
	}
	if bundle.manifest.Kind != "plugin" {
		return OperationResult{}, apperr.New("INVALID_PLUGIN_BUNDLE", "plugin deployment requires a plugin bundle")
	}
	targets, release, err := s.targets(serverID)
	if err != nil {
		return OperationResult{}, err
	}
	defer release()
	result := OperationResult{ServerID: serverID, PluginName: bundle.plugin.Name, Version: bundle.plugin.Version}
	var targetErrors []error
	for _, target := range targets {
		item, applyErr := s.applyOrQueue(target, pendingOperation{
			Type: "deploy", PluginType: bundle.plugin.PluginType, PluginName: bundle.plugin.Name,
			Directory: target.Directory,
		}, bundlePath,
			func() error { return deployArtifactToWorkspace(target.Workspace, target.Directory, bundle) })
		result.Targets = append(result.Targets, item)
		result.PendingRestart = result.PendingRestart || item.PendingRestart
		if applyErr != nil {
			targetErrors = append(targetErrors, fmt.Errorf("%s: %w", target.ID, applyErr))
		}
	}
	if len(targetErrors) > 0 {
		return result, apperr.Wrap("PLUGIN_DEPLOY_FAILED", "plugin deployment failed", errors.Join(targetErrors...))
	}
	result.Directory = model.ArtifactDirectory(server.Platform)
	return result, nil
}

func (s *Service) DeployConfig(serverID, bundlePath string) (OperationResult, error) {
	server, err := s.servers.Get(serverID)
	if err != nil {
		return OperationResult{}, err
	}
	bundle, cleanup, err := prepareBundle(bundlePath)
	if err != nil {
		return OperationResult{}, apperr.Wrap("INVALID_PLUGIN_BUNDLE", "plugin config bundle is invalid", err)
	}
	defer cleanup()
	if bundle.manifest.Kind != "config" {
		return OperationResult{}, apperr.New("INVALID_PLUGIN_BUNDLE", "config deployment requires a config bundle")
	}
	if bundle.plugin.PluginType != model.PluginTypeForPlatform(server.Platform) {
		return OperationResult{}, apperr.New("PLUGIN_TYPE_MISMATCH", "plugin type does not match target server platform")
	}
	targets, release, err := s.targets(serverID)
	if err != nil {
		return OperationResult{}, err
	}
	defer release()
	result := OperationResult{ServerID: serverID, PluginName: bundle.plugin.Name, Version: bundle.plugin.Version}
	var targetErrors []error
	for _, target := range targets {
		item, applyErr := s.applyOrQueue(target, pendingOperation{
			Type: "deploy_config", PluginType: bundle.plugin.PluginType, PluginName: bundle.plugin.Name,
		}, bundlePath,
			func() error { return deployConfigToWorkspace(target.Workspace, bundle) })
		result.Targets = append(result.Targets, item)
		result.PendingRestart = result.PendingRestart || item.PendingRestart
		if applyErr != nil {
			targetErrors = append(targetErrors, fmt.Errorf("%s: %w", target.ID, applyErr))
		}
	}
	if len(targetErrors) > 0 {
		return result, apperr.Wrap("PLUGIN_DEPLOY_FAILED", "plugin config deployment failed", errors.Join(targetErrors...))
	}
	return result, nil
}

// DeployContent 部署通用内容包（kind = content，zip 顶层即服务端工作目录结构）。
// 覆盖策略：覆盖同名 + 保留额外（不删除目标额外文件）；逐文件事务式覆盖，失败回滚。
// backupSnapshot 为完全配置（full）高风险标记：部署前对目标工作目录做整目录 zip 快照，
// 备份路径写入操作结果供回滚。
func (s *Service) DeployContent(serverID, bundlePath string, backupSnapshot bool) (OperationResult, error) {
	server, err := s.servers.Get(serverID)
	if err != nil {
		return OperationResult{}, err
	}
	bundle, cleanup, err := prepareBundle(bundlePath)
	if err != nil {
		return OperationResult{}, apperr.Wrap("INVALID_PLUGIN_BUNDLE", "plugin content bundle is invalid", err)
	}
	defer cleanup()
	if bundle.manifest.Kind != "content" {
		return OperationResult{}, apperr.New("INVALID_PLUGIN_BUNDLE", "content deployment requires a content bundle")
	}
	if bundle.plugin.PluginType != model.PluginTypeForPlatform(server.Platform) {
		return OperationResult{}, apperr.New("PLUGIN_TYPE_MISMATCH", "plugin type does not match target server platform")
	}
	targets, release, err := s.targets(serverID)
	if err != nil {
		return OperationResult{}, err
	}
	defer release()
	result := OperationResult{ServerID: serverID, PluginName: bundle.plugin.Name, Version: bundle.plugin.Version}
	var targetErrors []error
	for _, target := range targets {
		var stats ContentDeployStats
		item, applyErr := s.applyOrQueue(target, pendingOperation{
			Type: "deploy_content", PluginType: bundle.plugin.PluginType, PluginName: bundle.plugin.Name,
			BackupSnapshot: backupSnapshot,
		}, bundlePath,
			func() error {
				var err error
				stats, err = deployContentToWorkspace(target.Workspace, bundle, deployContentOptions{
					BackupSnapshot: backupSnapshot, BackupDir: s.backupDir,
				})
				return err
			})
		item.Applied = stats.Applied
		item.Overwritten = stats.Overwritten
		item.Added = stats.Added
		item.BackupPath = stats.BackupPath
		result.Targets = append(result.Targets, item)
		result.PendingRestart = result.PendingRestart || item.PendingRestart
		if applyErr != nil {
			targetErrors = append(targetErrors, fmt.Errorf("%s: %w", target.ID, applyErr))
		}
	}
	if len(targetErrors) > 0 {
		return result, apperr.Wrap("PLUGIN_DEPLOY_FAILED", "plugin content deployment failed", errors.Join(targetErrors...))
	}
	return result, nil
}

func (s *Service) UploadInstance(instanceID, jarPath, originalFilename string, overwrite bool) (InstanceUploadResult, error) {
	snapshot, err := s.supervisor.Get(instanceID)
	if err != nil {
		return InstanceUploadResult{}, err
	}
	pluginType := model.PluginTypeForPlatform(snapshot.Platform)
	directory := model.ArtifactDirectory(snapshot.Platform)
	bundle, err := prepareUploadedJAR(jarPath, originalFilename, pluginType)
	if err != nil {
		return InstanceUploadResult{}, apperr.Wrap("INVALID_PLUGIN", "上传文件不是有效的服务端插件/模组", err)
	}
	result := InstanceUploadResult{
		InstanceID: instanceID, PluginType: bundle.plugin.PluginType,
		PluginName: bundle.plugin.Name, Version: bundle.plugin.Version,
		Directory: directory,
	}
	release, err := s.supervisor.ReserveDeployment([]string{instanceID})
	if err != nil {
		return result, err
	}
	defer release()

	// Keep conflict detection and installation under one reservation so
	// concurrent uploads cannot both pass the duplicate check.
	snapshot, err = s.supervisor.Get(instanceID)
	if err != nil {
		return result, err
	}
	pluginDir := filepath.Join(snapshot.Workspace, directory)
	existing, existingPath, err := findArtifactDetails(snapshot.Workspace, directory, bundle.plugin.Name, pluginType)
	if err != nil {
		return result, apperr.Wrap("PLUGIN_NAME_CONFLICT", "当前子服存在多个同名插件/模组文件", err)
	}
	if existingPath != "" {
		result.ExistingFile = existing.SourceFile
		result.ExistingVersion = existing.Version
		result.SourceFile = existing.SourceFile
		if !overwrite {
			result.Outcome = "conflict"
			return result, apperr.New("PLUGIN_EXISTS", "当前子服已安装同名插件/模组")
		}
		result.Replaced = true
	} else {
		result.SourceFile = sanitizeJARFilename(originalFilename, bundle.plugin.Name, bundle.plugin.Version)
		if pathExists(filepath.Join(pluginDir, result.SourceFile)) {
			return result, apperr.New("PLUGIN_FILE_CONFLICT", "插件目标文件名已被其他文件占用")
		}
	}

	target := operationTarget{
		ID: instanceID, Workspace: snapshot.Workspace, Running: snapshot.State == "running",
		PluginType: pluginType, Directory: directory,
	}
	operation := pendingOperation{
		Type: "upload", PluginType: pluginType,
		PluginName: bundle.plugin.Name, OriginalFilename: originalFilename,
		Directory: directory,
	}
	targetResult, applyErr := s.applyOrQueue(
		target, operation, jarPath, func() error {
			return deployArtifactToWorkspace(snapshot.Workspace, directory, bundle)
		},
	)
	result.PendingRestart = targetResult.PendingRestart
	if applyErr != nil {
		return result, apperr.Wrap("PLUGIN_UPLOAD_FAILED", "插件上传失败", applyErr)
	}
	if targetResult.Status == "pending" {
		result.Outcome = "queued"
	} else if result.Replaced {
		result.Outcome = "replaced"
	} else {
		result.Outcome = "installed"
	}
	return result, nil
}

func (s *Service) SetEnabled(input OperationInput, enabled bool) (OperationResult, error) {
	if strings.TrimSpace(input.PluginName) == "" {
		return OperationResult{}, apperr.New("INVALID_REQUEST", "plugin_name is required")
	}
	targets, release, err := s.targets(input.ServerID)
	if err != nil {
		return OperationResult{}, err
	}
	defer release()
	typeName := "disable"
	if enabled {
		typeName = "enable"
	}
	result := OperationResult{ServerID: input.ServerID, PluginName: input.PluginName}
	var targetErrors []error
	for _, target := range targets {
		operation := pendingOperation{Type: typeName, PluginName: input.PluginName, Directory: target.Directory}
		item, applyErr := s.applyOrQueue(target, operation, "", func() error {
			return setArtifactEnabled(target.Workspace, target.Directory, input.PluginName, enabled)
		})
		result.Targets = append(result.Targets, item)
		result.PendingRestart = result.PendingRestart || item.PendingRestart
		if applyErr != nil {
			targetErrors = append(targetErrors, fmt.Errorf("%s: %w", target.ID, applyErr))
		}
	}
	if len(targetErrors) > 0 {
		return result, apperr.Wrap("PLUGIN_OPERATION_FAILED", "plugin state change failed", errors.Join(targetErrors...))
	}
	if len(targets) > 0 {
		result.Directory = targets[0].Directory
	}
	return result, nil
}

func (s *Service) Uninstall(input OperationInput) (OperationResult, error) {
	if strings.TrimSpace(input.PluginName) == "" {
		return OperationResult{}, apperr.New("INVALID_REQUEST", "plugin_name is required")
	}
	if input.DeleteConfig && !validDirectoryName(input.ConfigDirectory) {
		return OperationResult{}, apperr.New("INVALID_REQUEST", "config_directory is invalid")
	}
	targets, release, err := s.targets(input.ServerID)
	if err != nil {
		return OperationResult{}, err
	}
	defer release()
	result := OperationResult{ServerID: input.ServerID, PluginName: input.PluginName}
	var targetErrors []error
	for _, target := range targets {
		operation := pendingOperation{
			Type: "uninstall", PluginName: input.PluginName,
			DeleteConfig: input.DeleteConfig, ConfigDirectory: input.ConfigDirectory,
			Directory: target.Directory,
		}
		item, applyErr := s.applyOrQueue(target, operation, "", func() error {
			return uninstallArtifact(target.Workspace, target.Directory, input.PluginName, input.DeleteConfig, input.ConfigDirectory)
		})
		result.Targets = append(result.Targets, item)
		result.PendingRestart = result.PendingRestart || item.PendingRestart
		if applyErr != nil {
			targetErrors = append(targetErrors, fmt.Errorf("%s: %w", target.ID, applyErr))
		}
	}
	if len(targetErrors) > 0 {
		return result, apperr.Wrap("PLUGIN_OPERATION_FAILED", "plugin uninstall failed", errors.Join(targetErrors...))
	}
	if len(targets) > 0 {
		result.Directory = targets[0].Directory
	}
	return result, nil
}

func (s *Service) applyOrQueue(target operationTarget, operation pendingOperation, bundlePath string, apply func() error) (TargetResult, error) {
	if err := ensureWorkspace(target.Workspace); err != nil {
		if target.Optional && errors.Is(err, os.ErrNotExist) {
			return TargetResult{Target: target.ID, Status: "inherited_from_image"}, nil
		}
		if target.Optional {
			return TargetResult{Target: target.ID, Status: "failed", Message: err.Error()}, err
		}
		if mkdirErr := os.MkdirAll(target.Workspace, 0o750); mkdirErr != nil {
			err = errors.Join(err, mkdirErr)
			return TargetResult{Target: target.ID, Status: "failed", Message: err.Error()}, err
		}
		if workspaceErr := ensureWorkspace(target.Workspace); workspaceErr != nil {
			return TargetResult{Target: target.ID, Status: "failed", Message: workspaceErr.Error()}, workspaceErr
		}
	}
	if err := apply(); err != nil {
		if target.Running && !target.Image && retryableFileError(err) {
			if queueErr := s.pending.enqueue(target.ID, operation, bundlePath); queueErr != nil {
				return TargetResult{Target: target.ID, Status: "failed", Message: errors.Join(err, queueErr).Error()}, errors.Join(err, queueErr)
			}
			s.supervisor.SetPluginPendingRestart(target.ID, true)
			return TargetResult{Target: target.ID, Status: "pending", PendingRestart: true, Message: err.Error()}, nil
		}
		return TargetResult{Target: target.ID, Status: "failed", Message: err.Error()}, err
	}
	if !target.Image {
		s.supervisor.SetPluginPendingRestart(target.ID, target.Running)
	}
	return TargetResult{Target: target.ID, Status: "applied", PendingRestart: target.Running}, nil
}

func (s *Service) applyPending(instanceID, workspace string) error {
	err := s.pending.apply(instanceID, func(operation pendingOperation, bundlePath string) error {
		directory := operation.Directory
		if directory == "" {
			directory = "plugins"
		}
		switch operation.Type {
		case "deploy":
			bundle, cleanup, err := prepareBundle(bundlePath)
			if err != nil {
				return err
			}
			defer cleanup()
			return deployArtifactToWorkspace(workspace, directory, bundle)
		case "deploy_config":
			bundle, cleanup, err := prepareBundle(bundlePath)
			if err != nil {
				return err
			}
			defer cleanup()
			return deployConfigToWorkspace(workspace, bundle)
		case "deploy_content":
			bundle, cleanup, err := prepareBundle(bundlePath)
			if err != nil {
				return err
			}
			defer cleanup()
			_, err = deployContentToWorkspace(workspace, bundle, deployContentOptions{
				BackupSnapshot: operation.BackupSnapshot, BackupDir: s.backupDir,
			})
			return err
		case "upload":
			bundle, err := prepareUploadedJAR(bundlePath, operation.OriginalFilename, operation.PluginType)
			if err != nil {
				return err
			}
			return deployArtifactToWorkspace(workspace, directory, bundle)
		case "enable":
			return setArtifactEnabled(workspace, directory, operation.PluginName, true)
		case "disable":
			return setArtifactEnabled(workspace, directory, operation.PluginName, false)
		case "uninstall":
			return uninstallArtifact(workspace, directory, operation.PluginName, operation.DeleteConfig, operation.ConfigDirectory)
		default:
			return fmt.Errorf("unknown pending plugin operation: %s", operation.Type)
		}
	})
	if err == nil {
		s.supervisor.SetPluginPendingRestart(instanceID, false)
	}
	return err
}

func deployBundleToWorkspace(workspace string, bundle *preparedBundle) error {
	if err := deployPluginToWorkspace(workspace, bundle); err != nil {
		return err
	}
	if !bundle.manifest.Config.Present {
		return nil
	}
	return deployConfigToWorkspace(workspace, bundle)
}

// deployPluginToWorkspace 部署到 plugins 目录（Bukkit 系插件的固定目录）。
func deployPluginToWorkspace(workspace string, bundle *preparedBundle) error {
	return deployArtifactToWorkspace(workspace, "plugins", bundle)
}

// deployModToWorkspace 部署到 mods 目录（Fabric/Forge 模组目录）。
func deployModToWorkspace(workspace string, bundle *preparedBundle) error {
	return deployArtifactToWorkspace(workspace, "mods", bundle)
}

// deployArtifactToWorkspace 泛化部署：将 bundle 的 jar 写入 <workspace>/<directory>。
func deployArtifactToWorkspace(workspace, directory string, bundle *preparedBundle) error {
	pluginDir := filepath.Join(workspace, directory)
	if err := os.MkdirAll(pluginDir, 0o750); err != nil {
		return err
	}
	existing, err := findArtifact(workspace, directory, bundle.plugin.Name, bundle.plugin.PluginType)
	if err != nil {
		return err
	}
	filename := sanitizeJARFilename(bundle.manifest.Artifact.OriginalFilename, bundle.plugin.Name, bundle.plugin.Version)
	if existing != "" {
		filename = filepath.Base(existing)
	}
	target := filepath.Join(pluginDir, filename)
	if existing == "" && pathExists(target) {
		return errors.New("plugin target filename is already occupied")
	}
	txn := fmt.Sprintf(".prism-plugin-%d", time.Now().UnixNano())
	backup := target + txn + ".backup"
	temp := target + txn + ".new"
	if err := copyFile(bundle.jarPath, temp); err != nil {
		return err
	}
	hadTarget := pathExists(target)
	if hadTarget {
		if err := os.Rename(target, backup); err != nil {
			_ = os.Remove(temp)
			return err
		}
	}
	if err := os.Rename(temp, target); err != nil {
		if hadTarget {
			_ = os.Rename(backup, target)
		}
		return err
	}
	_ = os.Remove(backup)
	cleanupArtifactFiles(workspace, directory, txn)
	return nil
}

func deployConfigToWorkspace(workspace string, bundle *preparedBundle) error {
	if !bundle.manifest.Config.Present {
		return errors.New("plugin config bundle has no config snapshot")
	}
	pluginDir := filepath.Join(workspace, "plugins")
	pluginPath, err := findPlugin(pluginDir, bundle.plugin.Name, bundle.plugin.PluginType)
	if err != nil {
		return err
	}
	if pluginPath == "" {
		return errors.New("plugin file not found")
	}
	txn := fmt.Sprintf(".prism-plugin-%d", time.Now().UnixNano())
	configRoot := filepath.Join(pluginDir, bundle.manifest.Config.Directory)
	rollbacks := make([]func(), 0)
	err = filepath.WalkDir(bundle.configPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, relErr := filepath.Rel(bundle.configPath, path)
		if relErr != nil {
			return relErr
		}
		destination := filepath.Join(configRoot, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return err
		}
		configTemp := destination + txn + ".new"
		configBackup := destination + txn + ".backup"
		if err := copyFile(path, configTemp); err != nil {
			return err
		}
		hadConfig := pathExists(destination)
		if hadConfig {
			if err := os.Rename(destination, configBackup); err != nil {
				_ = os.Remove(configTemp)
				return err
			}
		}
		if err := os.Rename(configTemp, destination); err != nil {
			if hadConfig {
				_ = os.Rename(configBackup, destination)
			}
			return err
		}
		rollbacks = append(rollbacks, func() {
			_ = os.Remove(destination)
			if hadConfig {
				_ = os.Rename(configBackup, destination)
			}
		})
		return nil
	})
	if err != nil {
		for index := len(rollbacks) - 1; index >= 0; index-- {
			rollbacks[index]()
		}
		return err
	}
	cleanupTransactionFiles(workspace, txn)
	return nil
}

// setPluginEnabled 启停 plugins 目录下的插件（保持既有测试调用兼容）。
func setPluginEnabled(workspace, pluginName string, enabled bool) error {
	return setArtifactEnabled(workspace, "plugins", pluginName, enabled)
}

// setModEnabled 启停 mods 目录下的模组（.jar.disabled 重命名约定，Fabric/Forge 均适用）。
func setModEnabled(workspace, pluginName string, enabled bool) error {
	return setArtifactEnabled(workspace, "mods", pluginName, enabled)
}

// setArtifactEnabled 泛化启停：在 <workspace>/<directory> 下重命名 .jar ↔ .jar.disabled。
func setArtifactEnabled(workspace, directory, pluginName string, enabled bool) error {
	path, err := findArtifact(workspace, directory, pluginName)
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("plugin file not found")
	}
	disabled := strings.HasSuffix(strings.ToLower(path), ".jar.disabled")
	if enabled == !disabled {
		return nil
	}
	target := path
	if disabled {
		target = path[:len(path)-len(".disabled")]
	}
	if !enabled {
		target = path + ".disabled"
	}
	if pathExists(target) {
		return errors.New("plugin target filename already exists")
	}
	return os.Rename(path, target)
}

// uninstallPlugin 卸载 plugins 目录下的插件（保持既有测试调用兼容）。
func uninstallPlugin(workspace, pluginName string, deleteConfig bool, configDirectory string) error {
	return uninstallArtifact(workspace, "plugins", pluginName, deleteConfig, configDirectory)
}

// uninstallMod 卸载 mods 目录下的模组。
func uninstallMod(workspace, pluginName string, deleteConfig bool, configDirectory string) error {
	return uninstallArtifact(workspace, "mods", pluginName, deleteConfig, configDirectory)
}

// uninstallArtifact 泛化卸载：删除 <workspace>/<directory> 下的制品文件。
func uninstallArtifact(workspace, directory, pluginName string, deleteConfig bool, configDirectory string) error {
	pluginDir := filepath.Join(workspace, directory)
	path, err := findArtifact(workspace, directory, pluginName)
	if err != nil {
		return err
	}
	if path != "" {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	if deleteConfig {
		if !validDirectoryName(configDirectory) {
			return errors.New("config directory is invalid")
		}
		configPath := filepath.Join(pluginDir, configDirectory)
		if filepath.Dir(configPath) != pluginDir {
			return errors.New("config directory escapes artifacts directory")
		}
		if err := os.RemoveAll(configPath); err != nil {
			return err
		}
	}
	return nil
}

// findPlugin 在 plugins 目录下按名称查找插件文件（保持既有调用兼容）。
func findPlugin(pluginDir, pluginName string, pluginTypes ...string) (string, error) {
	_, path, err := findArtifactDetails(filepath.Dir(pluginDir), "plugins", pluginName, pluginTypes...)
	return path, err
}

// findArtifact 在 <workspace>/<directory> 下按名称查找制品文件。
func findArtifact(workspace, directory, pluginName string, pluginTypes ...string) (string, error) {
	_, path, err := findArtifactDetails(workspace, directory, pluginName, pluginTypes...)
	return path, err
}

// findArtifactDetails 泛化查找：扫描 <workspace>/<directory> 并按名称匹配单个制品。
func findArtifactDetails(workspace, directory, pluginName string, pluginTypes ...string) (FilePlugin, string, error) {
	items, warnings := newScanCache().scanDirectory(workspace, directory, pluginTypes...)
	if len(warnings) > 0 && len(items) == 0 {
		return FilePlugin{}, "", errors.New(warnings[0])
	}
	matches := make([]FilePlugin, 0)
	for _, item := range items {
		if strings.EqualFold(item.Name, pluginName) {
			matches = append(matches, item)
		}
	}
	if len(matches) > 1 {
		return FilePlugin{}, "", errors.New("multiple plugin files use the same name")
	}
	if len(matches) == 0 {
		return FilePlugin{}, "", nil
	}
	pluginDir := filepath.Join(workspace, directory)
	return matches[0], filepath.Join(pluginDir, matches[0].SourceFile), nil
}

func sanitizeJARFilename(original, name, version string) string {
	filename := filepath.Base(strings.TrimSpace(original))
	if !strings.HasSuffix(strings.ToLower(filename), ".jar") || filename == ".jar" {
		filename = name + "-" + version + ".jar"
	}
	filename = strings.Map(func(value rune) rune {
		switch value {
		case '<', '>', ':', 34, '/', 92, '|', '?', '*':
			return '-'
		}
		if value < 32 {
			return '-'
		}
		return value
	}, filename)
	return filename
}

// cleanupTransactionFiles 清理 plugins 目录下的部署事务残留文件。
func cleanupTransactionFiles(workspace, marker string) {
	cleanupArtifactFiles(workspace, "plugins", marker)
}

// cleanupArtifactFiles 泛化清理：删除 <workspace>/<directory> 下包含标记的临时文件。
func cleanupArtifactFiles(workspace, directory, marker string) {
	_ = filepath.WalkDir(filepath.Join(workspace, directory), func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.Contains(entry.Name(), marker) {
			_ = os.Remove(path)
		}
		return nil
	})
}

func pathExists(path string) bool { _, err := os.Lstat(path); return err == nil }

func retryableFileError(err error) bool {
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.Errno(5), syscall.Errno(13), syscall.Errno(32), syscall.Errno(33):
			return true
		}
	}
	return false
}
