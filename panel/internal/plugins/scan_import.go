package plugins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (r *Repository) importFiles(report *ScanReport, pluginType string) {
	importDir := filepath.Join(r.typeRoot(pluginType), "import")
	entries, err := os.ReadDir(importDir)
	if err != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("read import directory: %v", err))
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jar") {
			continue
		}
		jarPath := filepath.Join(importDir, entry.Name())
		contents, _, readErr := readFileLimited(jarPath, maxPluginJARSize)
		if readErr != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("import %s: %v", entry.Name(), readErr))
			continue
		}
		base := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		configPath := filepath.Join(importDir, base+".zip")
		var config []byte
		if _, statErr := os.Stat(configPath); statErr == nil {
			config, _, readErr = readFileLimited(configPath, maxConfigBytes)
			if readErr != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("import %s config: %v", entry.Name(), readErr))
				continue
			}
		}
		result, uploadErr := r.Upload(UploadInput{
			PluginType: pluginType, JARFilename: entry.Name(),
			JAR: contents, ConfigZIP: config, Uploader: repositoryScanner,
		})
		if uploadErr != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("import %s: %v", entry.Name(), uploadErr))
			continue
		}
		if result.Duplicate {
			report.Duplicates++
		} else {
			report.Imported++
		}
		if err := markImported(jarPath); err != nil {
			report.Warnings = append(report.Warnings, fmt.Sprintf("mark %s imported: %v", entry.Name(), err))
		}
		if len(config) > 0 {
			if err := markImported(configPath); err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("mark %s imported: %v", filepath.Base(configPath), err))
			}
		}
	}
}

func markImported(path string) error {
	target := path + ".imported"
	for suffix := 1; ; suffix++ {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			return os.Rename(path, target)
		}
		target = fmt.Sprintf("%s.imported.%d", path, suffix)
	}
}
