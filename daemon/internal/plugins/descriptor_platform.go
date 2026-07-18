package plugins

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	PluginTypeSpigot   = "spigot"
	PluginTypeVelocity = "velocity"
	PluginTypeBungee   = "bungee"
)

type velocityDescriptor struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	Main         string          `json:"main"`
	Authors      []string        `json:"authors"`
	Description  string          `json:"description"`
	URL          string          `json:"url"`
	Dependencies json.RawMessage `json:"dependencies"`
}

func requestedPluginType(values []string) string {
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return "auto"
	}
	return strings.ToLower(strings.TrimSpace(values[0]))
}

func validPluginType(value string) bool {
	switch value {
	case PluginTypeSpigot, PluginTypeVelocity, PluginTypeBungee:
		return true
	default:
		return false
	}
}

func parseJAR(path string, pluginTypes ...string) (map[string]Descriptor, Descriptor, error) {
	pluginType := requestedPluginType(pluginTypes)
	if pluginType == PluginTypeSpigot {
		descriptors, primary, err := parseSpigotJAR(path)
		return markPluginType(descriptors, primary, err, PluginTypeSpigot)
	}
	if pluginType == PluginTypeVelocity {
		return parseVelocityJAR(path)
	}
	if pluginType == PluginTypeBungee {
		return parseBungeeJAR(path)
	}
	if pluginType != "auto" {
		return nil, Descriptor{}, fmt.Errorf("unsupported plugin type %q", pluginType)
	}
	for _, candidate := range []string{PluginTypeSpigot, PluginTypeVelocity, PluginTypeBungee} {
		descriptors, primary, err := parseJAR(path, candidate)
		if err == nil {
			return descriptors, primary, nil
		}
	}
	return nil, Descriptor{}, errors.New("jar has no supported plugin descriptor")
}

func markPluginType(
	descriptors map[string]Descriptor,
	primary Descriptor,
	err error,
	pluginType string,
) (map[string]Descriptor, Descriptor, error) {
	if err != nil {
		return nil, Descriptor{}, err
	}
	for key, descriptor := range descriptors {
		descriptor.PluginType = pluginType
		descriptors[key] = descriptor
	}
	primary.PluginType = pluginType
	return descriptors, primary, nil
}

func parseVelocityJAR(path string) (map[string]Descriptor, Descriptor, error) {
	data, err := readJAREntry(path, "velocity-plugin.json")
	if err != nil {
		return nil, Descriptor{}, err
	}
	var raw velocityDescriptor
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, Descriptor{}, fmt.Errorf("decode velocity-plugin.json: %w", err)
	}
	id := strings.TrimSpace(raw.ID)
	version := strings.TrimSpace(raw.Version)
	mainClass := strings.TrimSpace(raw.Main)
	if id == "" || version == "" || mainClass == "" {
		return nil, Descriptor{}, errors.New("velocity-plugin.json requires id, version and main")
	}
	descriptor := Descriptor{
		PluginType:   PluginTypeVelocity,
		File:         "velocity-plugin.json",
		Name:         id,
		Version:      version,
		Main:         mainClass,
		Authors:      append([]string(nil), raw.Authors...),
		Description:  strings.TrimSpace(raw.Description),
		Website:      strings.TrimSpace(raw.URL),
		Dependencies: velocityDependencies(raw.Dependencies),
	}
	return map[string]Descriptor{"velocity": descriptor}, descriptor, nil
}

func parseBungeeJAR(path string) (map[string]Descriptor, Descriptor, error) {
	data, err := readJAREntry(path, "bungee.yml")
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

func readJAREntry(path, target string) ([]byte, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open plugin jar: %w", err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		name := strings.TrimPrefix(strings.ReplaceAll(entry.Name, "\\", "/"), "/")
		if name != target {
			continue
		}
		if entry.UncompressedSize64 > maxDescriptorSize || entry.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("invalid descriptor %s", target)
		}
		stream, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, maxDescriptorSize+1))
		closeErr := stream.Close()
		if readErr != nil || closeErr != nil || len(data) > maxDescriptorSize {
			return nil, fmt.Errorf("read descriptor %s", target)
		}
		return data, nil
	}
	return nil, fmt.Errorf("jar has no %s", target)
}

func velocityDependencies(data json.RawMessage) []string {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
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
