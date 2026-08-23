package plugins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var repositoryScanner = Uploader{Username: "local-import", DisplayName: "Local repository scan"}

func (r *Repository) Rescan() (ScanReport, error) {
	report := ScanReport{Warnings: make([]string, 0)}
	r.mu.Lock()
	if err := r.repairLocked(&report); err != nil {
		r.mu.Unlock()
		return report, err
	}
	r.mu.Unlock()

	for _, pluginType := range repositoryTypes {
		r.importFiles(&report, pluginType)
	}
	plugins, err := r.List()
	if err != nil {
		return report, err
	}
	report.Plugins = plugins
	return report, nil
}

func (r *Repository) repairLocked(report *ScanReport) error {
	for _, pluginType := range repositoryTypes {
		entries, err := os.ReadDir(r.typeRoot(pluginType))
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "import" || !pluginIDPattern.MatchString(entry.Name()) {
				continue
			}
			if err := r.repairPluginLocked(pluginType, entry.Name(), report); err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s/%s: %v", pluginType, entry.Name(), err))
			}
		}
	}
	return nil
}

func (r *Repository) repairPluginLocked(pluginType, pluginID string, report *ScanReport) error {
	pluginDir := filepath.Join(r.typeRoot(pluginType), pluginID)
	entries, err := os.ReadDir(pluginDir)
	if err != nil {
		return err
	}
	ids := make([]int64, 0)
	for _, entry := range entries {
		id, parseErr := strconv.ParseInt(entry.Name(), 10, 64)
		if entry.IsDir() && parseErr == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	if len(ids) == 0 {
		return errors.New("repository has no valid artifacts")
	}

	index, indexErr := r.loadIndexLocked(pluginDir)
	nextID := ids[len(ids)-1] + 1
	manifests := make([]Manifest, 0, len(ids))
	for _, id := range ids {
		artifactDir := filepath.Join(pluginDir, strconv.FormatInt(id, 10))
		manifest, readErr := r.loadManifestLocked(pluginDir, id)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s/%d invalid manifest: %v", pluginID, id, readErr))
			continue
		}
		manifest.PluginType = pluginType
		observed, observeErr := inspectArtifact(pluginID, id, artifactDir, manifest)
		if observeErr != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s/%d unreadable: %v", pluginID, id, observeErr))
			continue
		}
		if errors.Is(readErr, os.ErrNotExist) {
			if err := atomicYAML(filepath.Join(artifactDir, "manifest.yaml"), observed); err != nil {
				return fmt.Errorf("rebuild artifact %d manifest: %w", id, err)
			}
			report.RebuiltManifests++
			manifests = append(manifests, observed)
			continue
		}
		if artifactMatches(manifest, observed) {
			manifests = append(manifests, manifest)
			continue
		}
		recovered, recoveredID, recoverErr := r.recoverChangedArtifactLocked(
			pluginDir, artifactDir, nextID, observed,
		)
		if recoverErr != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s/%d recovery failed: %v", pluginID, id, recoverErr))
			continue
		}
		nextID = recoveredID + 1
		manifests = append(manifests, recovered)
		report.RecoveredChanges++
	}
	if len(manifests) == 0 {
		return errors.New("repository has no readable artifacts")
	}
	sort.Slice(manifests, func(left, right int) bool { return manifests[left].ArtifactID > manifests[right].ArtifactID })
	name := manifests[0].Name
	currentID := manifests[0].ArtifactID
	if indexErr == nil && index.CurrentArtifactID > 0 {
		for _, manifest := range manifests {
			if manifest.ArtifactID == index.CurrentArtifactID {
				currentID = manifest.ArtifactID
				break
			}
		}
	}
	for _, manifest := range manifests {
		if !strings.EqualFold(manifest.Name, name) {
			return fmt.Errorf("artifact name conflict: %s and %s", name, manifest.Name)
		}
	}
	index = Index{
		SchemaVersion: manifestSchemaVersion, PluginID: pluginID, PluginType: pluginType,
		Name: name, AutoInstall: index.AutoInstall,
		CurrentArtifactID: currentID, NextArtifactID: nextID,
	}
	return atomicYAML(filepath.Join(pluginDir, "index.yaml"), index)
}
