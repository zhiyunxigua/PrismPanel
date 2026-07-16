package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	serverservice "PrismPanel-daemon/internal/server"
	"PrismPanel-daemon/internal/supervisor"
)

type Service struct {
	supervisor *supervisor.Manager
	servers    *serverservice.Service
	cache      *scanCache
	pending    *pendingStore
}

func NewService(manager *supervisor.Manager, servers *serverservice.Service, dataDir string) (*Service, error) {
	pending, err := newPendingStore(filepath.Join(dataDir, "plugin-pending"))
	if err != nil {
		return nil, err
	}
	service := &Service{supervisor: manager, servers: servers, cache: newScanCache(), pending: pending}
	manager.SetBeforeStart(service.applyPending)
	return service, nil
}

func (s *Service) List(instanceID string) (ListResult, error) {
	snapshot, err := s.supervisor.Get(instanceID)
	if err != nil {
		return ListResult{}, err
	}
	files, warnings := s.cache.scan(snapshot.Workspace)
	items, pending := merge(files, snapshot.Plugins, snapshot.PluginConnected)
	if snapshot.PluginConnected {
		s.supervisor.SetPluginPendingRestart(instanceID, pending)
	} else {
		pending = pending || snapshot.PluginPendingRestart
	}
	return ListResult{
		InstanceID: instanceID, PluginConnected: snapshot.PluginConnected,
		PendingRestart: pending, Items: items, Warnings: warnings,
	}, nil
}

type operationTarget struct {
	ID        string
	Workspace string
	Running   bool
	Image     bool
	Optional  bool
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
	targets := make([]operationTarget, 0, len(instances)+1)
	if cfg.Type == "mirror" {
		targets = append(targets, operationTarget{
			ID: "image", Workspace: filepath.Join(cfg.RootPath, cfg.ImageDirectory), Image: true,
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
		})
	}
	return targets, release, nil
}

func ensureWorkspace(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("workspace is unavailable: %s", path)
	}
	return nil
}

func merge(files []FilePlugin, runtime []supervisor.LoadedPlugin, connected bool) ([]Plugin, bool) {
	items := make([]Plugin, len(files))
	byFile := make(map[string]int)
	byName := make(map[string][]int)
	for index, file := range files {
		items[index] = Plugin{FilePlugin: file, FilePresent: true, Issues: make([]string, 0)}
		byFile[strings.ToLower(file.SourceFile)] = index
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
			if !exists {
				indexes := byName[strings.ToLower(loaded.Name)]
				if len(indexes) == 1 {
					index, exists = indexes[0], true
				}
			}
			if !exists {
				items = append(items, Plugin{
					FilePlugin: FilePlugin{Name: loaded.Name, Version: loaded.Version, Main: loaded.Main,
						Authors: append([]string(nil), loaded.Authors...), SourceFile: filepath.Base(loaded.SourceFile),
						SHA256: loaded.SHA256, Enabled: loaded.Enabled},
					Loaded: true, RuntimeVersion: loaded.Version, RuntimeMain: loaded.Main,
					RuntimeSHA256: loaded.SHA256, Status: "uninstall_pending_restart",
					Issues: []string{"runtime_plugin_file_missing"}, PendingRestart: true,
				})
				continue
			}
			matched[index] = struct{}{}
			item := &items[index]
			item.Loaded = true
			item.RuntimeVersion = loaded.Version
			item.RuntimeMain = loaded.Main
			item.RuntimeSHA256 = loaded.SHA256
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
			if item.SHA256 != "" && loaded.SHA256 != "" && item.SHA256 != loaded.SHA256 {
				item.Issues = append(item.Issues, "sha256_mismatch")
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
