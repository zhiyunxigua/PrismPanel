package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"PrismPanel-daemon/internal/model"
	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/supervisor"
)

type Service struct {
	supervisor *supervisor.Manager
	servers    *serverservice.Service
	cache      *scanCache
	pending    *pendingStore
	backupDir  string
	baselineMu sync.RWMutex
	baselines  map[string]map[string]string
	changes    map[string]map[string]struct{}
}

func NewService(manager *supervisor.Manager, servers *serverservice.Service, dataDir string) (*Service, error) {
	pending, err := newPendingStore(filepath.Join(dataDir, "plugin-pending"))
	if err != nil {
		return nil, err
	}
	backupDir := filepath.Join(dataDir, "plugin-backups")
	if err := os.MkdirAll(backupDir, 0o750); err != nil {
		return nil, err
	}
	service := &Service{
		supervisor: manager, servers: servers, cache: newScanCache(), pending: pending,
		backupDir: backupDir,
		baselines: make(map[string]map[string]string), changes: make(map[string]map[string]struct{}),
	}
	manager.SetBeforeStart(service.beforeStart)
	return service, nil
}

func (s *Service) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.scanRunningInstances()
			}
		}
	}()
}

func (s *Service) List(instanceID string) (ListResult, error) {
	snapshot, err := s.supervisor.Get(instanceID)
	if err != nil {
		return ListResult{}, err
	}
	directory := model.ArtifactDirectory(snapshot.Platform)
	kind := "plugin"
	var files []FilePlugin
	var warnings []string
	if model.IsModPlatform(snapshot.Platform) {
		// mods 目录可能混合 fabric/forge/neoforge 模组，按平台 mod 类型扫描，
		// 保证 neoforge 模组被标记为 neoforge 而非 forge。
		kind = "mod"
		files, warnings = s.cache.scanMods(snapshot.Workspace, model.ModTypeForPlatform(snapshot.Platform))
	} else {
		files, warnings = s.cache.scan(snapshot.Workspace, model.PluginTypeForPlatform(snapshot.Platform))
	}
	changes := map[string]struct{}{}
	if snapshot.State == supervisor.StateRunning {
		changes = s.instanceChanges(instanceID)
	}
	items, _ := merge(files, snapshot.Plugins, snapshot.PluginConnected, changes)
	items = filterModReporter(items)
	pending := hasPendingRestart(items) || snapshot.PluginOperationPending
	if snapshot.PluginConnected {
		s.supervisor.SetPluginRuntimeMismatch(instanceID, hasRuntimeMismatch(items))
	} else {
		pending = pending || snapshot.PluginPendingRestart
	}
	return ListResult{
		InstanceID: instanceID, PluginConnected: snapshot.PluginConnected,
		PendingRestart: pending, Items: items, Warnings: warnings,
		Directory: directory, Kind: kind,
	}, nil
}

// modReporterID 是 PrismPanel 自身上报客户端（prism-fabric-mod）的 mod id。
// 它属于基础设施而非用户 mod：不出现在 mods.list 中，避免被误启用/禁用或误判漂移。
const modReporterID = "prism-fabric"

// filterModReporter 从合并结果中移除 PrismPanel 自身上报客户端条目。
func filterModReporter(items []Plugin) []Plugin {
	filtered := make([]Plugin, 0, len(items))
	for _, item := range items {
		if item.ID == modReporterID {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func hasPendingRestart(items []Plugin) bool {
	for _, item := range items {
		if item.PendingRestart {
			return true
		}
	}
	return false
}

func hasRuntimeMismatch(items []Plugin) bool {
	for _, item := range items {
		if !item.PendingRestart {
			continue
		}
		for _, issue := range item.Issues {
			if issue != "file_changed_since_start" {
				return true
			}
		}
	}
	return false
}

type operationTarget struct {
	ID         string
	Workspace  string
	Running    bool
	Image      bool
	Optional   bool
	PluginType string
	Directory  string
}

func (s *Service) targets(serverID string) ([]operationTarget, func(), error) {
	cfg, err := s.servers.Get(serverID)
	if err != nil {
		return nil, nil, err
	}
	instances := cfg.Instances()
	ids := make([]string, len(instances))
	for index, instance := range instances {
		ids[index] = instance.InstanceID
	}
	release, err := s.supervisor.ReserveDeployment(ids)
	if err != nil {
		return nil, nil, err
	}
	directory := model.ArtifactDirectory(cfg.Platform)
	targets := make([]operationTarget, 0, len(instances)+1)
	if cfg.Type == "mirror" {
		targets = append(targets, operationTarget{
			ID: "image", Workspace: filepath.Join(cfg.RootPath, cfg.ImageDirectory), Image: true,
			PluginType: model.PluginTypeForPlatform(cfg.Platform), Directory: directory,
		})
	}
	for _, instance := range instances {
		snapshot, snapshotErr := s.supervisor.Get(instance.InstanceID)
		if snapshotErr != nil {
			release()
			return nil, nil, snapshotErr
		}
		targets = append(targets, operationTarget{
			ID: instance.InstanceID, Workspace: instance.Workspace,
			Running: snapshot.State == supervisor.StateRunning, Optional: cfg.Type == "mirror",
			PluginType: model.PluginTypeForPlatform(cfg.Platform), Directory: directory,
		})
	}
	return targets, release, nil
}

func ensureWorkspace(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("workspace is unavailable: %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("workspace is unavailable: %s", path)
	}
	return nil
}

func merge(files []FilePlugin, runtime []supervisor.LoadedPlugin, connected bool, changedFiles ...map[string]struct{}) ([]Plugin, bool) {
	changes := map[string]struct{}{}
	if len(changedFiles) > 0 && changedFiles[0] != nil {
		changes = changedFiles[0]
	}
	items := make([]Plugin, len(files))
	byFile := make(map[string]int)
	byID := make(map[string]int)
	byName := make(map[string][]int)
	for index, file := range files {
		items[index] = Plugin{FilePlugin: file, FilePresent: true, Issues: make([]string, 0)}
		byFile[strings.ToLower(file.SourceFile)] = index
		if file.ID != "" {
			byID[strings.ToLower(file.ID)] = index
		}
		name := strings.ToLower(file.Name)
		byName[name] = append(byName[name], index)
	}
	for _, indexes := range byName {
		if len(indexes) < 2 {
			continue
		}
		for _, index := range indexes {
			items[index].Status = "conflict"
			items[index].Issues = append(items[index].Issues, "duplicate_plugin_name")
		}
	}

	matched := make(map[int]struct{})
	if connected {
		for _, loaded := range runtime {
			index, exists := byFile[strings.ToLower(filepath.Base(loaded.SourceFile))]
			if !exists && loaded.ID != "" {
				index, exists = byID[strings.ToLower(loaded.ID)]
			}
			if !exists {
				indexes := byName[strings.ToLower(loaded.Name)]
				if len(indexes) == 1 {
					index, exists = indexes[0], true
				}
			}
			if !exists {
				if strings.TrimSpace(loaded.SourceFile) == "" {
					items = append(items, Plugin{
						FilePlugin: FilePlugin{ID: loaded.ID, Name: loaded.Name, Version: loaded.Version,
							Main:    loaded.Main,
							Authors: append([]string(nil), loaded.Authors...), Enabled: loaded.Enabled},
						Loaded: true, RuntimeVersion: loaded.Version, RuntimeMain: loaded.Main, Status: "runtime_only",
						Issues: []string{"runtime_plugin_source_unavailable"},
					})
					continue
				}
				items = append(items, Plugin{
					FilePlugin: FilePlugin{ID: loaded.ID, Name: loaded.Name, Version: loaded.Version,
						Main:    loaded.Main,
						Authors: append([]string(nil), loaded.Authors...), SourceFile: filepath.Base(loaded.SourceFile),
						Enabled: loaded.Enabled},
					Loaded: true, RuntimeVersion: loaded.Version, RuntimeMain: loaded.Main,
					Status: "uninstall_pending_restart",
					Issues: []string{"runtime_plugin_file_missing"}, PendingRestart: true,
				})
				continue
			}
			matched[index] = struct{}{}
			item := &items[index]
			item.Loaded = true
			item.RuntimeVersion = loaded.Version
			item.RuntimeMain = loaded.Main
			if !item.Enabled {
				item.Status = "disabled_pending_restart"
				item.Issues = append(item.Issues, "disabled_file_still_loaded")
				item.PendingRestart = true
			}
			if item.Version != loaded.Version {
				item.Issues = append(item.Issues, "version_mismatch")
				item.PendingRestart = true
			}
			if item.Main != "" && loaded.Main != "" && item.Main != loaded.Main {
				item.Issues = append(item.Issues, "main_class_mismatch")
				item.PendingRestart = true
			}
			if item.Status == "" {
				if item.PendingRestart {
					item.Status = "update_pending_restart"
				} else {
					item.Status = "loaded"
				}
			}
		}
	}
	pending := false
	for index := range items {
		item := &items[index]
		if _, changed := changes[strings.ToLower(item.SourceFile)]; changed {
			item.Issues = append(item.Issues, "file_changed_since_start")
			item.PendingRestart = true
			if item.FilePresent && item.Loaded {
				item.Status = "update_pending_restart"
			} else if item.FilePresent && item.Enabled {
				item.Status = "install_pending_restart"
			}
		}
		if item.Status == "conflict" {
			item.PendingRestart = connected
		}
		if item.Status == "" {
			if !connected {
				if item.Enabled {
					item.Status = "file_enabled"
				} else {
					item.Status = "file_disabled"
				}
			} else if _, exists := matched[index]; !exists {
				if item.Enabled {
					item.Status = "not_loaded"
				} else {
					item.Status = "disabled"
				}
			}
		}
		pending = pending || item.PendingRestart
	}
	sort.Slice(items, func(left, right int) bool {
		return strings.ToLower(items[left].Name) < strings.ToLower(items[right].Name)
	})
	return items, pending
}

func (s *Service) beforeStart(instanceID, workspace string) error {
	if err := s.applyPending(instanceID, workspace); err != nil {
		return err
	}
	directory := "plugins"
	if snapshot, err := s.supervisor.Get(instanceID); err == nil {
		directory = model.ArtifactDirectory(snapshot.Platform)
	}
	baseline, err := scanEnabledHashes(workspace, directory)
	if err != nil {
		return fmt.Errorf("scan %s before start: %w", directory, err)
	}
	s.baselineMu.Lock()
	s.baselines[instanceID] = baseline
	delete(s.changes, instanceID)
	s.baselineMu.Unlock()
	s.supervisor.SetPluginFilesChanged(instanceID, false)
	return nil
}

func (s *Service) scanRunningInstances() {
	for _, snapshot := range s.supervisor.List() {
		if snapshot.State != supervisor.StateRunning {
			continue
		}
		baseline, exists := s.instanceBaseline(snapshot.InstanceID)
		if !exists {
			continue
		}
		current, err := scanEnabledHashes(snapshot.Workspace, model.ArtifactDirectory(snapshot.Platform))
		if err != nil {
			continue
		}
		changes := changedPluginFiles(baseline, current)
		s.baselineMu.Lock()
		s.changes[snapshot.InstanceID] = changes
		s.baselineMu.Unlock()
		s.supervisor.SetPluginFilesChanged(snapshot.InstanceID, len(changes) > 0)
	}
}

func (s *Service) instanceBaseline(instanceID string) (map[string]string, bool) {
	s.baselineMu.RLock()
	baseline, exists := s.baselines[instanceID]
	s.baselineMu.RUnlock()
	return baseline, exists
}

func (s *Service) instanceChanges(instanceID string) map[string]struct{} {
	s.baselineMu.RLock()
	defer s.baselineMu.RUnlock()
	result := make(map[string]struct{}, len(s.changes[instanceID]))
	for name := range s.changes[instanceID] {
		result[name] = struct{}{}
	}
	return result
}

func changedPluginFiles(baseline, current map[string]string) map[string]struct{} {
	result := make(map[string]struct{})
	for name, digest := range baseline {
		if current[name] != digest {
			result[name] = struct{}{}
		}
	}
	for name, digest := range current {
		if baseline[name] != digest {
			result[name] = struct{}{}
		}
	}
	return result
}
