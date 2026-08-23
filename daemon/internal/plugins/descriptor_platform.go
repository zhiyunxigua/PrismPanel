package plugins

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	PluginTypeSpigot   = "spigot"
	PluginTypeVelocity = "velocity"
	PluginTypeBungee   = "bungee"
	PluginTypeFabric   = "fabric"
	PluginTypeForge    = "forge"
)

// allPluginTypes 是 auto 探测时的候选顺序。插件描述符优先，mod 描述符在后，
// 避免包含 fabric.mod.json 的混合 jar 被误判。
var allPluginTypes = []string{PluginTypeSpigot, PluginTypeVelocity, PluginTypeBungee, PluginTypeFabric, PluginTypeForge}

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
	case PluginTypeSpigot, PluginTypeVelocity, PluginTypeBungee, PluginTypeFabric, PluginTypeForge:
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
	if pluginType == PluginTypeFabric {
		return parseFabricJAR(path)
	}
	if pluginType == PluginTypeForge {
		return parseForgeJAR(path)
	}
	if pluginType != "auto" {
		return nil, Descriptor{}, fmt.Errorf("unsupported plugin type %q", pluginType)
	}
	for _, candidate := range allPluginTypes {
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
		ID:           id,
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

// parseFabricJAR 解析 Fabric mod：读取 jar 根目录的 fabric.mod.json。
func parseFabricJAR(path string) (map[string]Descriptor, Descriptor, error) {
	data, err := readJAREntry(path, "fabric.mod.json")
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
		PluginType:   PluginTypeFabric,
		ID:           id,
		File:         "fabric.mod.json",
		Name:         name,
		Version:      version,
		Authors:      append([]string(nil), raw.Authors...),
		Description:  strings.TrimSpace(raw.Description),
		Website:      website,
		Dependencies: dependencyKeys(raw.Depends),
	}
	return map[string]Descriptor{"fabric": descriptor}, descriptor, nil
}

// parseForgeJAR 解析 Forge mod：优先读取 META-INF/mods.toml，
// 缺失时回退到 jar 文件名推导名称与版本。
func parseForgeJAR(path string) (map[string]Descriptor, Descriptor, error) {
	data, err := readJAREntry(path, "META-INF/mods.toml")
	if err == nil {
		descriptor, parseErr := parseForgeModsTOML(data)
		if parseErr == nil {
			descriptor.PluginType = PluginTypeForge
			return map[string]Descriptor{"forge": descriptor}, descriptor, nil
		}
	}
	descriptor, fallbackErr := forgeFilenameFallback(filepath.Base(path))
	if fallbackErr != nil {
		if err != nil {
			return nil, Descriptor{}, err
		}
		return nil, Descriptor{}, fallbackErr
	}
	return map[string]Descriptor{"forge": descriptor}, descriptor, nil
}

// parseForgeModsTOML 解析 META-INF/mods.toml 的子集：提取第一个 [[mods]]
// 块的 modId/version/displayName/authors/description/displayURL，以及
// [[dependencies.<modId>]] 下的依赖 modId 列表。
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
		PluginType:   PluginTypeForge,
		ID:           modID,
		File:         "META-INF/mods.toml",
		Name:         name,
		Version:      version,
		Authors:      splitTOMLList(primary["authors"]),
		Description:  strings.TrimSpace(primary["description"]),
		Website:      strings.TrimSpace(primary["displayURL"]),
		Dependencies: append([]string(nil), deps[modID]...),
	}
	return descriptor, nil
}

// forgeFilenameFallback 在缺少 mods.toml 时从 jar 文件名推导 mod 名称与版本。
// 与 panel/internal/plugins/descriptor.go 的 forgeModFilenameFallback 为双份对应实现，
// 保持一致，修改需同步。
func forgeFilenameFallback(filename string) (Descriptor, error) {
	base := strings.TrimSpace(filepath.Base(filename))
	lower := strings.ToLower(base)
	if strings.HasSuffix(lower, ".jar.disabled") {
		base = base[:len(base)-len(".jar.disabled")]
	} else if strings.HasSuffix(lower, ".disabled") {
		base = base[:len(base)-len(".disabled")]
	} else if strings.HasSuffix(lower, ".jar") {
		base = base[:len(base)-len(".jar")]
	}
	if strings.TrimSpace(base) == "" {
		return Descriptor{}, errors.New("cannot derive forge mod name from filename")
	}
	name, version := splitModNameVersion(base)
	if version == "" {
		version = "0.0.0"
	}
	return Descriptor{
		PluginType: PluginTypeForge, File: "filename-fallback",
		Name: name, Version: version,
	}, nil
}

// splitModNameVersion 将 "mod-name-1.2.3" 拆分为名称与版本；无法识别时版本为空。
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

// dependencyKeys 提取依赖映射的键列表（fabric.mod.json depends / suggests 等）。
func dependencyKeys(mapping map[string]any) []string {
	result := make([]string, 0, len(mapping))
	for key := range mapping {
		if key = strings.TrimSpace(key); key != "" {
			result = append(result, key)
		}
	}
	return result
}

// stripTOMLComment 去掉 TOML 行内注释（# 前的内容），忽略引号内的 #。
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

// splitTOMLList 拆分 TOML 字符串数组或逗号分隔字符串。
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
