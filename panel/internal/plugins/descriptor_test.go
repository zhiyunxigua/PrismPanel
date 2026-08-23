package plugins

import (
	"reflect"
	"testing"
)

const fullFabricModJSON = `{
	"schemaVersion": 1,
	"id": "prismmod",
	"name": "Prism Mod",
	"version": "2.1.0",
	"authors": ["Alice", "Bob"],
	"description": "A test fabric mod",
	"environment": "client",
	"license": ["MIT", "Apache-2.0"],
	"icon": "assets/prismmod/icon.png",
	"contact": {"homepage": "https://example.com/prism"},
	"depends": {
		"minecraft": ">=1.20",
		"fabricloader": [">=0.14.9", "<=0.15.0"],
		"fabric-api": "*"
	},
	"suggests": {"jei": "*"},
	"entrypoints": {
		"main": ["com.example.prism.PrismMod"],
		"client": ["com.example.prism.client.PrismClient"],
		"server": ["com.example.prism.server.PrismServer"]
	}
}`

func TestParseFabricModJARCapturesDeepMetadata(t *testing.T) {
	descriptors, primary, err := ParseModJAR(
		testZIP(t, map[string]string{"fabric.mod.json": fullFabricModJSON}),
		"PrismMod-2.1.0.jar", PluginTypeFabric,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 || descriptors["fabric"].PluginType != PluginTypeFabric {
		t.Fatalf("unexpected fabric descriptors: %#v", descriptors)
	}
	if primary.ID != "prismmod" || primary.Name != "Prism Mod" || primary.Version != "2.1.0" {
		t.Fatalf("unexpected fabric descriptor identity: %#v", primary)
	}
	if !reflect.DeepEqual(primary.Dependencies, []string{"fabric-api", "fabricloader", "minecraft"}) {
		t.Fatalf("unexpected dependency keys: %#v", primary.Dependencies)
	}
	meta := primary.ModMetadata
	if meta == nil {
		t.Fatal("ModMetadata must be populated for fabric mods")
	}
	if meta.ID != "prismmod" || meta.SchemaVersion != 1 || meta.Environment != "client" {
		t.Fatalf("unexpected fabric mod identity metadata: %#v", meta)
	}
	if meta.License != "MIT, Apache-2.0" {
		t.Fatalf("unexpected license: %q", meta.License)
	}
	if meta.Icon != "assets/prismmod/icon.png" {
		t.Fatalf("unexpected icon: %q", meta.Icon)
	}
	expectedDepends := []ModDependency{
		{ID: "fabric-api", VersionRange: "*"},
		{ID: "fabricloader", VersionRange: ">=0.14.9 || <=0.15.0"},
		{ID: "minecraft", VersionRange: ">=1.20"},
	}
	if !reflect.DeepEqual(meta.Depends, expectedDepends) {
		t.Fatalf("unexpected depends: %#v", meta.Depends)
	}
	expectedSuggests := []ModDependency{{ID: "jei", VersionRange: "*"}}
	if !reflect.DeepEqual(meta.Suggests, expectedSuggests) {
		t.Fatalf("unexpected suggests: %#v", meta.Suggests)
	}
	expectedEntrypoints := []ModEntrypoint{
		{Kind: "main", Values: []string{"com.example.prism.PrismMod"}},
		{Kind: "client", Values: []string{"com.example.prism.client.PrismClient"}},
		{Kind: "server", Values: []string{"com.example.prism.server.PrismServer"}},
	}
	if !reflect.DeepEqual(meta.Entrypoints, expectedEntrypoints) {
		t.Fatalf("unexpected entrypoints: %#v", meta.Entrypoints)
	}
}

func TestParseFabricModJARHandlesMinimalAndStringForms(t *testing.T) {
	// license 字符串形式、environment 缺省、entrypoint 单字符串、depends 无版本约束。
	descriptors, primary, err := ParseModJAR(testZIP(t, map[string]string{
		"fabric.mod.json": `{
			"schemaVersion": 1,
			"id": "tiny",
			"version": "1.0",
			"license": "MIT",
			"depends": {"minecraft": "*"},
			"entrypoints": {"main": "com.example.tiny.Main"}
		}`,
	}), "tiny-1.0.jar", PluginTypeFabric)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 {
		t.Fatalf("unexpected descriptors: %#v", descriptors)
	}
	meta := primary.ModMetadata
	if meta.ID != "tiny" || meta.Environment != "" || meta.License != "MIT" {
		t.Fatalf("unexpected minimal metadata: %#v", meta)
	}
	if !reflect.DeepEqual(meta.Depends, []ModDependency{{ID: "minecraft", VersionRange: "*"}}) {
		t.Fatalf("unexpected depends: %#v", meta.Depends)
	}
	if !reflect.DeepEqual(meta.Entrypoints, []ModEntrypoint{{Kind: "main", Values: []string{"com.example.tiny.Main"}}}) {
		t.Fatalf("unexpected entrypoints: %#v", meta.Entrypoints)
	}
	if primary.Name != "tiny" {
		t.Fatalf("name must fall back to id: %#v", primary)
	}
}

func TestParsePaperJARMarksPaperType(t *testing.T) {
	descriptors, primary, err := ParseModJAR(
		testZIP(t, map[string]string{
			"plugin.yml": "name: PaperPlugin\nversion: 1.0\nmain: com.example.PaperPlugin\nauthors: [Tester]\n",
		}),
		"PaperPlugin-1.0.jar", PluginTypePaper,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 || descriptors["bukkit"].PluginType != PluginTypePaper {
		t.Fatalf("unexpected paper descriptors: %#v", descriptors)
	}
	if primary.PluginType != PluginTypePaper || primary.Name != "PaperPlugin" || primary.Version != "1.0" ||
		primary.Main != "com.example.PaperPlugin" {
		t.Fatalf("unexpected paper descriptor identity: %#v", primary)
	}
}

func TestParsePaperJARPrefersPluginYMLAndMarksPaper(t *testing.T) {
	descriptors, primary, err := ParseModJAR(
		testZIP(t, map[string]string{
			"plugin.yml":      "name: Both\nversion: 1.0\nmain: com.example.Both\nauthors: [Tester]\n",
			"paper-plugin.yml": "name: Both\nversion: 1.0\nmain: com.example.Both\n",
		}),
		"Both-1.0.jar", PluginTypePaper,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 2 || descriptors["bukkit"].PluginType != PluginTypePaper ||
		descriptors["paper"].PluginType != PluginTypePaper {
		t.Fatalf("unexpected paper descriptors: %#v", descriptors)
	}
	if primary.PluginType != PluginTypePaper || primary.Name != "Both" {
		t.Fatalf("unexpected paper primary: %#v", primary)
	}
}

func TestParseNeoForgeModJARMarksNeoForgeType(t *testing.T) {
	descriptors, primary, err := ParseModJAR(
		testZIP(t, map[string]string{
			"META-INF/mods.toml": "modLoader=\"javafml\"\nloaderVersion=\"[49,)\"\n\n[[mods]]\nmodId=\"neomod\"\nversion=\"1.0\"\ndisplayName=\"Neo Mod\"\n",
		}),
		"NeoMod-1.0.jar", PluginTypeNeoForge,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 || descriptors["forge"].PluginType != PluginTypeNeoForge {
		t.Fatalf("unexpected neoforge descriptors: %#v", descriptors)
	}
	if primary.PluginType != PluginTypeNeoForge || primary.ID != "neomod" || primary.Name != "Neo Mod" ||
		primary.Version != "1.0" {
		t.Fatalf("unexpected neoforge descriptor identity: %#v", primary)
	}
}

func TestParseNeoForgeModJARFilenameFallback(t *testing.T) {
	descriptors, primary, err := ParseModJAR(
		testZIP(t, map[string]string{"README.txt": "no toml"}),
		"my-neo-mod-2.0.0.jar", PluginTypeNeoForge,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptors) != 1 || primary.PluginType != PluginTypeNeoForge ||
		primary.Name != "my-neo-mod" || primary.Version != "2.0.0" {
		t.Fatalf("unexpected neoforge filename fallback: %#v", primary)
	}
}
