package plugins

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func ParseJAR(contents []byte, pluginTypes ...string) (map[string]Descriptor, Descriptor, error) {
	pluginType := normalizePluginType(pluginTypes)
	if pluginType == PluginTypeVelocity {
		return parseVelocityJAR(contents)
	}
	if pluginType == PluginTypeBungee {
		return parseBungeeJAR(contents)
	}
	if pluginType != PluginTypeSpigot {
		return nil, Descriptor{}, fmt.Errorf("unsupported plugin type %q", pluginType)
	}
	reader, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		return nil, Descriptor{}, fmt.Errorf("open plugin jar: %w", err)
	}
	descriptors := make(map[string]Descriptor)
	for _, entry := range reader.File {
		name := strings.TrimPrefix(strings.ReplaceAll(entry.Name, "\\", "/"), "/")
		if name != "plugin.yml" && name != "paper-plugin.yml" {
			continue
		}
		if entry.UncompressedSize64 > maxDescriptorSize {
			return nil, Descriptor{}, fmt.Errorf("%s exceeds 1 MiB", name)
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, Descriptor{}, err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, maxDescriptorSize+1))
		stream.Close()
		if readErr != nil {
			return nil, Descriptor{}, fmt.Errorf("read %s: %w", name, readErr)
		}
		if len(data) > maxDescriptorSize {
			return nil, Descriptor{}, fmt.Errorf("%s exceeds 1 MiB", name)
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
		return nil, Descriptor{}, errors.New("jar does not contain plugin.yml or paper-plugin.yml")
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
	for key, descriptor := range descriptors {
		descriptor.PluginType = PluginTypeSpigot
		descriptors[key] = descriptor
	}
	primary.PluginType = PluginTypeSpigot
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
	if (filename == "plugin.yml" || filename == "bungee.yml") && strings.TrimSpace(raw.Main) == "" {
		return Descriptor{}, fmt.Errorf("%s requires main", filename)
	}
	return Descriptor{
		File: filename, Name: strings.TrimSpace(raw.Name), Version: strings.TrimSpace(raw.Version),
		Main: strings.TrimSpace(raw.Main), Bootstrapper: strings.TrimSpace(raw.Bootstrapper),
		Loader: strings.TrimSpace(raw.Loader), APIVersion: strings.TrimSpace(raw.APIVersion),
		Authors: stringList(raw.Authors, raw.Author), Description: strings.TrimSpace(raw.Description),
		Website: strings.TrimSpace(raw.Website), Dependencies: stringList(raw.Depend, raw.Dependencies),
	}, nil
}

const (
	PluginTypeSpigot   = "spigot"
	PluginTypeVelocity = "velocity"
	PluginTypeBungee   = "bungee"
)

type velocityDescriptor struct {
	ID           string          `json:"id"`
	Version      string          `json:"version"`
	Main         string          `json:"main"`
	Authors      []string        `json:"authors"`
	Description  string          `json:"description"`
	URL          string          `json:"url"`
	Dependencies json.RawMessage `json:"dependencies"`
}

func normalizePluginType(values []string) string {
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return PluginTypeSpigot
	}
	return strings.ToLower(strings.TrimSpace(values[0]))
}

func ValidPluginType(value string) bool {
	switch value {
	case PluginTypeSpigot, PluginTypeVelocity, PluginTypeBungee:
		return true
	default:
		return false
	}
}

func parseVelocityJAR(contents []byte) (map[string]Descriptor, Descriptor, error) {
	data, err := readDescriptorFile(contents, "velocity-plugin.json")
	if err != nil {
		return nil, Descriptor{}, err
	}
	var raw velocityDescriptor
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, Descriptor{}, fmt.Errorf("decode velocity-plugin.json: %w", err)
	}
	if strings.TrimSpace(raw.ID) == "" || strings.TrimSpace(raw.Version) == "" || strings.TrimSpace(raw.Main) == "" {
		return nil, Descriptor{}, errors.New("velocity-plugin.json requires id, version and main")
	}
	descriptor := Descriptor{
		PluginType: PluginTypeVelocity, File: "velocity-plugin.json",
		Name: strings.TrimSpace(raw.ID), Version: strings.TrimSpace(raw.Version),
		Main: strings.TrimSpace(raw.Main), Authors: append([]string(nil), raw.Authors...),
		Description: strings.TrimSpace(raw.Description), Website: strings.TrimSpace(raw.URL),
		Dependencies: velocityDependencies(raw.Dependencies),
	}
	return map[string]Descriptor{"velocity": descriptor}, descriptor, nil
}

func parseBungeeJAR(contents []byte) (map[string]Descriptor, Descriptor, error) {
	data, err := readDescriptorFile(contents, "bungee.yml")
	if err != nil {
		return nil, Descriptor{}, err
	}
	descriptor, err := decodeDescriptor("bungee.yml", data)
	if err != nil {
		return nil, Descriptor{}, err
	}
	descriptor.PluginType = PluginTypeBungee
	return map[string]Descriptor{"bungee": descriptor}, descriptor, nil
}

func readDescriptorFile(contents []byte, target string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		return nil, fmt.Errorf("open plugin jar: %w", err)
	}
	for _, entry := range reader.File {
		name := strings.TrimPrefix(strings.ReplaceAll(entry.Name, string(rune(92)), "/"), "/")
		if name != target {
			continue
		}
		if entry.UncompressedSize64 > maxDescriptorSize {
			return nil, fmt.Errorf("%s exceeds 1 MiB", target)
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, maxDescriptorSize+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || len(data) > maxDescriptorSize {
			return nil, fmt.Errorf("read %s", target)
		}
		return data, nil
	}
	return nil, fmt.Errorf("jar does not contain %s", target)
}

func velocityDependencies(data json.RawMessage) []string {
	var objects []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(data, &objects) == nil {
		result := make([]string, 0, len(objects))
		for _, dependency := range objects {
			if id := strings.TrimSpace(dependency.ID); id != "" {
				result = append(result, id)
			}
		}
		return result
	}
	var mapping map[string]any
	if json.Unmarshal(data, &mapping) == nil {
		result := make([]string, 0, len(mapping))
		for id := range mapping {
			result = append(result, id)
		}
		return result
	}
	return nil
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
