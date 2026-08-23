package plugins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// 内容包 CRUD：内容包作为独立可版本化资产（contentID 在制品内递增，类似 artifactID），
// 与 jar artifact 平行。编辑内容包 = 重新上传 zip = 新增版本并标记 current（保留旧版本）；
// 删除按版本粒度，删除 current 时回退到剩余最高版本；Manifest.Content 始终指向 current。

// AddContent 为制品新增一个内容包版本（contentID 递增并成为 current）。
// 用于「编辑内容包」与「添加同种配置」：同类型或不同类型均可，每次新增都是新版本。
func (r *Repository) AddContent(pluginID string, artifactID int64, contentType string, zipBytes []byte, pluginTypes ...string) (Manifest, error) {
	if !validContentType(contentType) {
		return Manifest{}, errors.New("content type must be config or full")
	}
	if len(zipBytes) == 0 {
		return Manifest{}, errors.New("content bundle is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	manifest, artifactDir, err := r.artifactLocked(pluginID, artifactID, pluginTypes...)
	if err != nil {
		return Manifest{}, err
	}
	index, err := loadContentIndex(artifactDir)
	if err != nil {
		return Manifest{}, err
	}
	contentID := nextContentID(index)
	staging, err := os.MkdirTemp(artifactDir, ".content-*")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(staging)
	if _, _, err := extractContentZIP(zipBytes, staging); err != nil {
		return Manifest{}, err
	}
	snapshot := ContentSnapshot{ContentID: contentID, Type: contentType, Present: true}
	snapshot.SHA256, snapshot.Files, snapshot.Size, err = treeHash(staging)
	if err != nil {
		return Manifest{}, err
	}
	if snapshot.Files == 0 {
		return Manifest{}, errors.New("content bundle contains no files")
	}
	snapshot.Tree, err = contentTopLevelTree(staging)
	if err != nil {
		return Manifest{}, err
	}
	contentDir := filepath.Join(artifactDir, "content")
	if err := os.MkdirAll(contentDir, 0o750); err != nil {
		return Manifest{}, err
	}
	finalDir := contentVersionDir(artifactDir, contentID)
	if err := os.Rename(staging, finalDir); err != nil {
		return Manifest{}, fmt.Errorf("publish content version: %w", err)
	}
	index.Current = contentID
	index.Versions = append(index.Versions, snapshot)
	if err := saveContentIndex(artifactDir, index); err != nil {
		return Manifest{}, err
	}
	manifest.Content = &snapshot
	if err := atomicYAML(filepath.Join(artifactDir, "manifest.yaml"), manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// ListContent 列出制品下全部内容包版本（升序）；无内容包返回空列表。
// 当前版本可通过 Manifest.Content 获取（调用方自行读取制品 manifest）。
func (r *Repository) ListContent(pluginID string, artifactID int64, pluginTypes ...string) ([]ContentSnapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, artifactDir, err := r.artifactLocked(pluginID, artifactID, pluginTypes...)
	if err != nil {
		return nil, err
	}
	index, err := loadContentIndex(artifactDir)
	if err != nil {
		return nil, err
	}
	return append([]ContentSnapshot(nil), index.Versions...), nil
}

// DeleteContent 删除制品下的一个内容包版本（快照与磁盘目录）。
// 若删除的是 current，回退到剩余最高版本；无剩余版本时 Manifest.Content 置空。
// 返回更新后的制品 manifest。
func (r *Repository) DeleteContent(pluginID string, artifactID int64, contentID int64, pluginTypes ...string) (Manifest, error) {
	if contentID < 1 {
		return Manifest{}, errors.New("invalid content id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	manifest, artifactDir, err := r.artifactLocked(pluginID, artifactID, pluginTypes...)
	if err != nil {
		return Manifest{}, err
	}
	index, err := loadContentIndex(artifactDir)
	if err != nil {
		return Manifest{}, err
	}
	kept := make([]ContentSnapshot, 0, len(index.Versions))
	found := false
	var nextCurrent int64
	for _, version := range index.Versions {
		if version.ContentID == contentID {
			found = true
			continue
		}
		kept = append(kept, version)
		if version.ContentID > nextCurrent {
			nextCurrent = version.ContentID
		}
	}
	if !found {
		return Manifest{}, os.ErrNotExist
	}
	if err := os.RemoveAll(contentVersionDir(artifactDir, contentID)); err != nil {
		return Manifest{}, err
	}
	index.Versions = kept
	if len(kept) == 0 {
		index.Current = 0
		_ = os.RemoveAll(filepath.Join(artifactDir, "content"))
		_ = os.Remove(contentIndexPath(artifactDir))
		manifest.Content = nil
	} else {
		index.Current = nextCurrent
		if err := saveContentIndex(artifactDir, index); err != nil {
			return Manifest{}, err
		}
		for _, version := range kept {
			if version.ContentID == nextCurrent {
				current := version
				manifest.Content = &current
				break
			}
		}
	}
	if err := atomicYAML(filepath.Join(artifactDir, "manifest.yaml"), manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// DeleteAllContent 删除制品下的全部内容包版本（快照与磁盘目录，含索引），
// Manifest.Content 置空。用于「仅删除内容包（某制品）」。
func (r *Repository) DeleteAllContent(pluginID string, artifactID int64, pluginTypes ...string) (Manifest, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	manifest, artifactDir, err := r.artifactLocked(pluginID, artifactID, pluginTypes...)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.RemoveAll(filepath.Join(artifactDir, "content")); err != nil {
		return Manifest{}, err
	}
	_ = os.Remove(contentIndexPath(artifactDir))
	manifest.Content = nil
	if err := atomicYAML(filepath.Join(artifactDir, "manifest.yaml"), manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// SetCurrentArtifact 切换制品 current（前端「切换 current 制品」/ 回滚到旧版本）：
// 仅修改 index.CurrentArtifactID，不影响任何制品内容。返回更新后的插件。
func (r *Repository) SetCurrentArtifact(pluginID string, artifactID int64, pluginTypes ...string) (Plugin, error) {
	if artifactID < 1 {
		return Plugin{}, errors.New("invalid artifact id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pluginType := normalizePluginType(pluginTypes)
	pluginDir := filepath.Join(r.typeRoot(pluginType), pluginID)
	index, err := r.loadIndexLocked(pluginDir)
	if err != nil {
		return Plugin{}, err
	}
	if _, err := r.loadManifestLocked(pluginDir, artifactID); err != nil {
		return Plugin{}, err
	}
	index.CurrentArtifactID = artifactID
	if err := atomicYAML(filepath.Join(pluginDir, "index.yaml"), index); err != nil {
		return Plugin{}, err
	}
	artifacts, err := r.loadArtifactsLocked(pluginDir)
	if err != nil {
		return Plugin{}, err
	}
	return buildPlugin(index, artifacts), nil
}

// DeleteArtifact 删除一个制品版本（jar + 其内容包与旧配置）。
// 删除后 current 回退到剩余最高版本；无剩余制品时整个仓库条目（插件/模组）被删除。
// 返回删除后的插件；条目被整体删除时返回 os.ErrNotExist。
func (r *Repository) DeleteArtifact(pluginID string, artifactID int64, pluginTypes ...string) (Plugin, error) {
	if artifactID < 1 {
		return Plugin{}, errors.New("invalid artifact id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pluginType := normalizePluginType(pluginTypes)
	pluginDir := filepath.Join(r.typeRoot(pluginType), pluginID)
	index, err := r.loadIndexLocked(pluginDir)
	if err != nil {
		return Plugin{}, err
	}
	artifactDir := filepath.Join(pluginDir, strconv.FormatInt(artifactID, 10))
	if _, err := os.Stat(artifactDir); err != nil {
		return Plugin{}, err
	}
	if err := os.RemoveAll(artifactDir); err != nil {
		return Plugin{}, err
	}
	artifacts, err := r.loadArtifactsLocked(pluginDir)
	if err != nil {
		return Plugin{}, err
	}
	if len(artifacts) == 0 {
		if err := os.RemoveAll(pluginDir); err != nil {
			return Plugin{}, err
		}
		return Plugin{}, os.ErrNotExist
	}
	// loadArtifactsLocked 按 ArtifactID 降序；剩余最高版本即 artifacts[0]。
	current := artifacts[0].ArtifactID
	index.CurrentArtifactID = current
	if next := nextArtifactID(artifacts); next > index.NextArtifactID {
		index.NextArtifactID = next
	}
	if err := atomicYAML(filepath.Join(pluginDir, "index.yaml"), index); err != nil {
		return Plugin{}, err
	}
	return buildPlugin(index, artifacts), nil
}

// DeletePlugin 删除整个仓库条目（插件/模组及其全部制品版本与内容包）。
func (r *Repository) DeletePlugin(pluginID string, pluginTypes ...string) error {
	if !pluginIDPattern.MatchString(pluginID) {
		return errors.New("invalid plugin id")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pluginDir := filepath.Join(r.typeRoot(normalizePluginType(pluginTypes)), pluginID)
	if _, err := os.Stat(pluginDir); err != nil {
		return err
	}
	return os.RemoveAll(pluginDir)
}
