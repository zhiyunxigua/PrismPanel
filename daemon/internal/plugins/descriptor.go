package plugins

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxDescriptorSize = 1024 * 1024

type rawDescriptor struct {
	Name         string `yaml:"name"`
	Version      string `yaml:"version"`
	Main         string `yaml:"main"`
	Bootstrapper string `yaml:"bootstrapper"`
	Loader       string `yaml:"loader"`
	APIVersion   string `yaml:"api-version"`
	Authors      any    `yaml:"authors"`
	Author       any    `yaml:"author"`
	Description  string `yaml:"description"`
	Website      string `yaml:"website"`
	Depend       any    `yaml:"depend"`
	Dependencies any    `yaml:"dependencies"`
}

func parseJAR(path string) (map[string]Descriptor, Descriptor, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, Descriptor{}, fmt.Errorf("open plugin jar: %w", err)
	}
	defer reader.Close()
	descriptors := make(map[string]Descriptor)
	for _, entry := range reader.File {
		name := strings.TrimPrefix(strings.ReplaceAll(entry.Name, "\\", "/"), "/")
		if name != "plugin.yml" && name != "paper-plugin.yml" {
			continue
		}
		if entry.UncompressedSize64 > maxDescriptorSize || entry.Mode()&os.ModeSymlink != 0 {
			return nil, Descriptor{}, fmt.Errorf("invalid descriptor %s", name)
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, Descriptor{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, maxDescriptorSize+1))
		stream.Close()
		if readErr != nil || len(data) > maxDescriptorSize {
			return nil, Descriptor{}, fmt.Errorf("read descriptor %s", name)
		}
		descriptor, err := decodeDescriptor(name, data)
		if err != nil {
			return nil, Descriptor{}, err
		}
		key := "bukkit"
		if name == "paper-plugin.yml" {
			key = "paper"
		}
		descriptors[key] = descriptor
	}
	if len(descriptors) == 0 {
		return nil, Descriptor{}, errors.New("jar has no plugin descriptor")
	}
	primary, exists := descriptors["bukkit"]
	if !exists {
		primary = descriptors["paper"]
	}
	for _, descriptor := range descriptors {
		if !strings.EqualFold(descriptor.Name, primary.Name) || descriptor.Version != primary.Version {
			return nil, Descriptor{}, errors.New("plugin descriptors disagree on name or version")
		}
	}
	return descriptors, primary, nil
}

func decodeDescriptor(filename string, data []byte) (Descriptor, error) {
	var raw rawDescriptor
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Descriptor{}, fmt.Errorf("decode %s: %w", filename, err)
	}
	if strings.TrimSpace(raw.Name) == "" || strings.TrimSpace(raw.Version) == "" {
		return Descriptor{}, fmt.Errorf("%s requires name and version", filename)
	}
	if filename == "plugin.yml" && strings.TrimSpace(raw.Main) == "" {
		return Descriptor{}, errors.New("plugin.yml requires main")
	}
	return Descriptor{
		File: filename, Name: strings.TrimSpace(raw.Name), Version: strings.TrimSpace(raw.Version),
		Main: strings.TrimSpace(raw.Main), Bootstrapper: strings.TrimSpace(raw.Bootstrapper),
		Loader: strings.TrimSpace(raw.Loader), APIVersion: strings.TrimSpace(raw.APIVersion),
		Authors: stringList(raw.Authors, raw.Author), Description: strings.TrimSpace(raw.Description),
		Website: strings.TrimSpace(raw.Website), Dependencies: stringList(raw.Depend, raw.Dependencies),
	}, nil
}

func stringList(values ...any) []string {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for _, value := range values {
		var items []any
		switch typed := value.(type) {
		case string:
			items = []any{typed}
		case []any:
			items = typed
		case []string:
			for _, item := range typed {
				items = append(items, item)
			}
		case map[string]any:
			for key := range typed {
				items = append(items, key)
			}
		}
		for _, item := range items {
			text := strings.TrimSpace(fmt.Sprint(item))
			key := strings.ToLower(text)
			if text == "" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, text)
		}
	}
	return result
}
