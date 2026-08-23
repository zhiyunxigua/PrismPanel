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
		}, bundlePath,
			func() error { return deployPluginToWorkspace(target.Workspace, bundle) })
		result.Targets = append(result.Targets, item)
		result.PendingRestart = result.PendingRestart || item.PendingRestart
		if applyErr != nil {
			targetErrors = append(targetErrors, fmt.Errorf("%s: %w", target.ID, applyErr))
		}
	}
	if len(targetErrors) > 0 {
		return result, apperr.Wrap("PLUGIN_DEPLOY_FAILED", "plugin deployment failed", errors.Join(targetErrors...))
	}
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

func (s *Service) UploadInstance(instanceID, jarPath, originalFilename string, overwrite bool) (InstanceUploadResult, error) {
	snapshot, err := s.supervisor.Get(instanceID)
	if err != nil {
		return InstanceUploadResult{}, err
	}
	pluginType := model.PluginTypeForPlatform(snapshot.Platform)
	bundle, err := prepareUploadedJAR(jarPath, originalFilename, pluginType)
	if err != nil {
		return InstanceUploadResult{}, apperr.Wrap("INVALID_PLUGIN", "上传文件不是有效的服务端插件", err)
	}
	result := InstanceUploadResult{
		InstanceID: instanceID, PluginType: bundle.plugin.PluginType,
		PluginName: bundle.plugin.Name, Version: bundle.plugin.Version,
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
	pluginDir := filepath.Join(snapshot.Workspace, "plugins")
	existing, existingPath, err := findPluginDetails(pluginDir, bundle.plugin.Name, pluginType)
	if err != nil {
		return result, apperr.Wrap("PLUGIN_NAME_CONFLICT", "当前子服存在多个同名插件文件", err)
	}
	if existingPath != "" {
		result.ExistingFile = existing.SourceFile
		result.ExistingVersion = existing.Version
		result.SourceFile = existing.SourceFile
		if !overwrite {
			result.Outcome = "conflict"
			return result, apperr.New("PLUGIN_EXISTS", "当前子服已安装同名插件")
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
		PluginType: pluginType,
	}
	operation := pendingOperation{
		Type: "upload", PluginType: pluginType,
		PluginName: bundle.plugin.Name, OriginalFilename: originalFilename,
	}
	targetResult, applyErr := s.applyOrQueue(
		target, operation, jarPath, func() error { return deployBundleToWorkspace(snapshot.Workspace, bundle) },
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
	targets, release, err := s.targetsForInstance(input.ServerID, input.InstanceID)
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
		operation := pendingOperation{Type: typeName, PluginName: input.PluginName}
		item, applyErr := s.applyOrQueue(target, operation, "", func() error {
			return setPluginEnabled(target.Workspace, input.PluginName, enabled)
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
	return result, nil
}

func (s *Service) Uninstall(input OperationInput) (OperationResult, error) {
	if strings.TrimSpace(input.PluginName) == "" {
		return OperationResult{}, apperr.New("INVALID_REQUEST", "plugin_name is required")
	}
	if input.DeleteConfig && !validDirectoryName(input.ConfigDirectory) {
		return OperationResult{}, apperr.New("INVALID_REQUEST", "config_directory is invalid")
	}
	targets, release, err := s.targetsForInstance(input.ServerID, input.InstanceID)
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
		}
		item, applyErr := s.applyOrQueue(target, operation, "", func() error {
			return uninstallPlugin(target.Workspace, input.PluginName, input.DeleteConfig, input.ConfigDirectory)
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
		switch operation.Type {
		case "deploy":
			bundle, cleanup, err := prepareBundle(bundlePath)
			if err != nil {
				return err
			}
			defer cleanup()
			return deployPluginToWorkspace(workspace, bundle)
		case "deploy_config":
			bundle, cleanup, err := prepareBundle(bundlePath)
			if err != nil {
				return err
			}
			defer cleanup()
			return deployConfigToWorkspace(workspace, bundle)
		case "upload":
			bundle, err := prepareUploadedJAR(bundlePath, operation.OriginalFilename, operation.PluginType)
			if err != nil {
				return err
			}
			return deployBundleToWorkspace(workspace, bundle)
		case "enable":
			return setPluginEnabled(workspace, operation.PluginName, true)
		case "disable":
			return setPluginEnabled(workspace, operation.PluginName, false)
		case "uninstall":
			return uninstallPlugin(workspace, operation.PluginName, operation.DeleteConfig, operation.ConfigDirectory)
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

func deployPluginToWorkspace(workspace string, bundle *preparedBundle) error {
	pluginDir := filepath.Join(workspace, "plugins")
	if err := os.MkdirAll(pluginDir, 0o750); err != nil {
		return err
	}
	existing, err := findPlugin(pluginDir, bundle.plugin.Name, bundle.plugin.PluginType)
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
	cleanupTransactionFiles(workspace, txn)
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

func setPluginEnabled(workspace, pluginName string, enabled bool) error {
	pluginDir := filepath.Join(workspace, "plugins")
	path, err := findPlugin(pluginDir, pluginName)
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

func uninstallPlugin(workspace, pluginName string, deleteConfig bool, configDirectory string) error {
	pluginDir := filepath.Join(workspace, "plugins")
	path, err := findPlugin(pluginDir, pluginName)
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
			return errors.New("config directory escapes plugins directory")
		}
		if err := os.RemoveAll(configPath); err != nil {
			return err
		}
	}
	return nil
}

func findPlugin(pluginDir, pluginName string, pluginTypes ...string) (string, error) {
	_, path, err := findPluginDetails(pluginDir, pluginName, pluginTypes...)
	return path, err
}

func findPluginDetails(pluginDir, pluginName string, pluginTypes ...string) (FilePlugin, string, error) {
	items, warnings := newScanCache().scan(filepath.Dir(pluginDir), pluginTypes...)
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

func cleanupTransactionFiles(workspace, marker string) {
	_ = filepath.WalkDir(filepath.Join(workspace, "plugins"), func(path string, entry fs.DirEntry, err error) error {
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
