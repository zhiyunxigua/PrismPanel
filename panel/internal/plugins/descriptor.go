package plugins

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
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
	return ParseModJAR(contents, "", pluginTypes...)
}

// ParseModJAR 解析插件/模组 jar；filename 用于 Forge 缺少 mods.toml 时的文件名回退。
func ParseModJAR(contents []byte, filename string, pluginTypes ...string) (map[string]Descriptor, Descriptor, error) {
	pluginType := normalizePluginType(pluginTypes)
	if pluginType == PluginTypeVelocity {
		return parseVelocityJAR(contents)
	}
	if pluginType == PluginTypeBungee {
		return parseBungeeJAR(contents)
	}
	if pluginType == PluginTypeFabric {
		return parseFabricModJAR(contents)
	}
	if pluginType == PluginTypeForge {
		return parseForgeModJAR(contents, filename)
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
	// PluginTypeFabric / PluginTypeForge 标记 mod 平台的服务端与仓库 mod 制品；
	// 与 daemon model 的平台枚举保持同一套取值（两任务共用约定）。
	PluginTypeFabric = "fabric"
	PluginTypeForge  = "forge"
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
	case PluginTypeSpigot, PluginTypeVelocity, PluginTypeBungee, PluginTypeFabric, PluginTypeForge:
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

// fabricModDescriptor 对应 jar 根目录的 fabric.mod.json（JSON 格式）。
type fabricModDescriptor struct {
	SchemaVersion int      `json:"schemaVersion"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Version       string   `json:"version"`
	Authors       []string `json:"authors"`
	Description   string   `json:"description"`
	Contact       struct {
		Homepage string `json:"homepage"`
		Sources  string `json:"sources"`
	} `json:"contact"`
	Depends map[string]any `json:"depends"`
}

func parseFabricModJAR(contents []byte) (map[string]Descriptor, Descriptor, error) {
	data, err := readDescriptorFile(contents, "fabric.mod.json")
	if err != nil {
		return nil, Descriptor{}, err
	}
	var raw fabricModDescriptor
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, Descriptor{}, fmt.Errorf("decode fabric.mod.json: %w", err)
	}
	id := strings.TrimSpace(raw.ID)
	version := strings.TrimSpace(raw.Version)
	if id == "" || version == "" {
		return nil, Descriptor{}, errors.New("fabric.mod.json requires id and version")
	}
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		name = id
	}
	website := strings.TrimSpace(raw.Contact.Homepage)
	if website == "" {
		website = strings.TrimSpace(raw.Contact.Sources)
	}
	descriptor := Descriptor{
		PluginType: PluginTypeFabric, File: "fabric.mod.json",
		Name: name, Version: version, Authors: append([]string(nil), raw.Authors...),
		Description: strings.TrimSpace(raw.Description), Website: website,
		Dependencies: fabricDependencyKeys(raw.Depends),
	}
	return map[string]Descriptor{"fabric": descriptor}, descriptor, nil
}

func parseForgeModJAR(contents []byte, filename string) (map[string]Descriptor, Descriptor, error) {
	data, err := readDescriptorFile(contents, "META-INF/mods.toml")
	if err == nil {
		descriptor, parseErr := parseForgeModsTOML(data)
		if parseErr == nil {
			descriptor.PluginType = PluginTypeForge
			return map[string]Descriptor{"forge": descriptor}, descriptor, nil
		}
	}
	if filename == "" {
		return nil, Descriptor{}, fmt.Errorf("jar does not contain META-INF/mods.toml: %w", err)
	}
	descriptor, fallbackErr := forgeModFilenameFallback(filename)
	if fallbackErr != nil {
		return nil, Descriptor{}, fallbackErr
	}
	return map[string]Descriptor{"forge": descriptor}, descriptor, nil
}

// parseForgeModsTOML 解析 META-INF/mods.toml 子集（与 daemon 端同一实现约定）。
func parseForgeModsTOML(data []byte) (Descriptor, error) {
	var descriptor Descriptor
	mods := make([]map[string]string, 0)
	deps := make(map[string][]string)
	dependencyOwner := ""
	currentMod := -1
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(stripTOMLComment(rawLine))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			section := strings.TrimSpace(line[2 : len(line)-2])
			if section == "mods" {
				mods = append(mods, make(map[string]string))
				currentMod = len(mods) - 1
				dependencyOwner = ""
				continue
			}
			if strings.HasPrefix(section, "dependencies.") {
				dependencyOwner = strings.TrimSpace(strings.TrimPrefix(section, "dependencies."))
				currentMod = -1
				continue
			}
			currentMod = -1
			dependencyOwner = ""
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentMod = -1
			dependencyOwner = ""
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if currentMod >= 0 && currentMod < len(mods) {
			mods[currentMod][key] = value
			continue
		}
		if dependencyOwner != "" && key == "modId" && value != "" {
			deps[dependencyOwner] = append(deps[dependencyOwner], value)
		}
	}
	if len(mods) == 0 {
		return descriptor, errors.New("mods.toml has no [[mods]] section")
	}
	primary := mods[0]
	modID := strings.TrimSpace(primary["modId"])
	version := strings.TrimSpace(primary["version"])
	if modID == "" || version == "" {
		return descriptor, errors.New("mods.toml [[mods]] requires modId and version")
	}
	name := strings.TrimSpace(primary["displayName"])
	if name == "" {
		name = modID
	}
	descriptor = Descriptor{
		PluginType: PluginTypeForge, File: "META-INF/mods.toml",
		Name: name, Version: version, Authors: splitTOMLList(primary["authors"]),
		Description: strings.TrimSpace(primary["description"]), Website: strings.TrimSpace(primary["displayURL"]),
		Dependencies: append([]string(nil), deps[modID]...),
	}
	return descriptor, nil
}

// forgeModFilenameFallback 在缺少 mods.toml 时从 jar 文件名推导 mod 名称与版本。
// 与 daemon/internal/plugins/descriptor_platform.go 的 forgeFilenameFallback 为双份对应实现，
// 保持一致，修改需同步。
func forgeModFilenameFallback(filename string) (Descriptor, error) {
	base := strings.TrimSpace(filepath.Base(filename))
	lower := strings.ToLower(base)
	if strings.HasSuffix(lower, ".jar.disabled") {
		base = base[:len(base)-len(".jar.disabled")]
	} else if strings.HasSuffix(lower, ".disabled") {
		base = base[:len(base)-len(".disabled")]
	} else if strings.HasSuffix(lower, ".jar") {
		base = base[:len(base)-len(".jar")]
	}
	if base == "" {
		return Descriptor{}, errors.New("cannot derive forge mod name from filename")
	}
	name, version := splitModNameVersion(base)
	if version == "" {
		version = "0.0.0"
	}
	return Descriptor{PluginType: PluginTypeForge, File: "filename-fallback", Name: name, Version: version}, nil
}

func splitModNameVersion(base string) (string, string) {
	index := strings.LastIndex(base, "-")
	if index > 0 && index < len(base)-1 {
		candidate := base[index+1:]
		if candidate[0] >= '0' && candidate[0] <= '9' && strings.ContainsAny(candidate, ". ") {
			return base[:index], candidate
		}
	}
	return base, ""
}

func fabricDependencyKeys(mapping map[string]any) []string {
	result := make([]string, 0, len(mapping))
	for key := range mapping {
		if key = strings.TrimSpace(key); key != "" {
			result = append(result, key)
		}
	}
	return result
}

func stripTOMLComment(line string) string {
	inQuote := byte(0)
	for index := 0; index < len(line); index++ {
		char := line[index]
		if char == '"' || char == '\'' {
			if inQuote == 0 {
				inQuote = char
			} else if inQuote == char {
				inQuote = 0
			}
			continue
		}
		if char == '#' && inQuote == 0 {
			return line[:index]
		}
	}
	return line
}

func splitTOMLList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = value[1 : len(value)-1]
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
