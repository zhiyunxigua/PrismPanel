package game

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// fakeModrinthFetcher 用假依赖数据构造 fetcher：projects 为 project id → 版本（含 dependencies）。
// versionID 非空时校验与项目版本一致（模拟固定版本依赖）。
func fakeModrinthFetcher(projects map[string]*MCModrinthVersion) mcModrinthVersionFetcher {
	return func(ctx context.Context, projectID, versionID, gameVersion, loader string) (*MCModrinthVersion, error) {
		version, ok := projects[projectID]
		if !ok {
			return nil, fmt.Errorf("project %s not found", projectID)
		}
		if versionID != "" && versionID != version.ID {
			return nil, fmt.Errorf("project %s has no version %s", projectID, versionID)
		}
		return version, nil
	}
}

func fakeVersion(id string, deps ...MCModrinthDependency) *MCModrinthVersion {
	return &MCModrinthVersion{ID: id, VersionNumber: id, Files: []struct {
		Filename string `json:"filename"`
		URL      string `json:"url"`
		Size     int64  `json:"size"`
	}{{Filename: id + ".jar", URL: "https://example.test/" + id + ".jar"}}, Dependencies: deps}
}

func depsRequired(ids ...string) []MCModrinthDependency {
	var out []MCModrinthDependency
	for _, id := range ids {
		out = append(out, MCModrinthDependency{ProjectID: id, DependencyType: "required"})
	}
	return out
}

func idsOf(items []mcModrinthDependencyItem) []string {
	var ids []string
	for _, item := range items {
		ids = append(ids, item.ProjectID)
	}
	return ids
}

func joinIDs(ids []string) string {
	return strings.Join(ids, ",")
}

func TestMCModrinthResolveDependenciesOrder(t *testing.T) {
	// A 依赖 B（required）与 C（optional）；B 依赖 D。
	// 期望：依赖在前、本体在后 → D,B,A；optional 的 C 不装。
	projects := map[string]*MCModrinthVersion{
		"A": fakeVersion("vA", append(depsRequired("B"), MCModrinthDependency{ProjectID: "C", DependencyType: "optional"})...),
		"B": fakeVersion("vB", depsRequired("D")...),
		"C": fakeVersion("vC"),
		"D": fakeVersion("vD"),
	}
	items, err := mcModrinthResolveDependencies(context.Background(), fakeModrinthFetcher(projects), "A", "", "1.21.4", "fabric", map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := joinIDs(idsOf(items)); got != "D,B,A" {
		t.Errorf("resolve order = %s, want D,B,A", got)
	}
}

func TestMCModrinthResolveDependenciesCycle(t *testing.T) {
	// A ↔ B 相互依赖：防环，不无限递归；每个项目只出现一次。
	projects := map[string]*MCModrinthVersion{
		"A": fakeVersion("vA", depsRequired("B")...),
		"B": fakeVersion("vB", depsRequired("A")...),
	}
	items, err := mcModrinthResolveDependencies(context.Background(), fakeModrinthFetcher(projects), "A", "", "1.21.4", "fabric", map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("resolve cycle should terminate: %v", err)
	}
	got := joinIDs(idsOf(items))
	if got != "B,A" {
		t.Errorf("resolve cycle = %s, want B,A", got)
	}
}

func TestMCModrinthResolveDependenciesDedup(t *testing.T) {
	// A 依赖 B 与 C；B 依赖 C → C 只安装一次。
	projects := map[string]*MCModrinthVersion{
		"A": fakeVersion("vA", depsRequired("B", "C")...),
		"B": fakeVersion("vB", depsRequired("C")...),
		"C": fakeVersion("vC"),
	}
	items, err := mcModrinthResolveDependencies(context.Background(), fakeModrinthFetcher(projects), "A", "", "1.21.4", "fabric", map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ids := idsOf(items)
	if len(ids) != 3 {
		t.Fatalf("expected 3 items (C,B,A), got %d: %v", len(ids), ids)
	}
	count := 0
	for _, id := range ids {
		if id == "C" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("C should be installed once, got %d times: %v", count, ids)
	}
}

func TestMCModrinthResolveDependenciesPinnedVersion(t *testing.T) {
	// 依赖声明了 version_id（作者固定版本）：必须使用该版本而非自行挑选。
	projects := map[string]*MCModrinthVersion{
		"A": fakeVersion("vA", MCModrinthDependency{ProjectID: "B", VersionID: "vB-pinned", DependencyType: "required"}),
		"B": fakeVersion("vB-pinned"),
	}
	fetch := func(ctx context.Context, projectID, versionID, gameVersion, loader string) (*MCModrinthVersion, error) {
		// 若忽略固定版本（versionID 为空），B 会解析到 vB-pinned 之外（此处直接报错模拟）
		if projectID == "B" && versionID != "vB-pinned" {
			return nil, fmt.Errorf("pinned version ignored: %s", versionID)
		}
		return fakeModrinthFetcher(projects)(ctx, projectID, versionID, gameVersion, loader)
	}
	items, err := mcModrinthResolveDependencies(context.Background(), fetch, "A", "", "1.21.4", "fabric", map[string]bool{}, 0)
	if err != nil {
		t.Fatalf("resolve pinned version: %v", err)
	}
	ids := idsOf(items)
	if joinIDs(ids) != "B,A" {
		t.Errorf("resolve pinned = %s, want B,A", joinIDs(ids))
	}
	for _, item := range items {
		if item.ProjectID == "B" && item.Version.ID != "vB-pinned" {
			t.Errorf("B should resolve to pinned version vB-pinned, got %s", item.Version.ID)
		}
	}
}

func TestMCModrinthResolveDependenciesDepthLimit(t *testing.T) {
	// 6 层依赖链（A1→A2→…→A7）超过深度上限 5 → 明确错误。
	projects := map[string]*MCModrinthVersion{}
	for i := 1; i <= 6; i++ {
		next := fmt.Sprintf("A%d", i+1)
		projects[fmt.Sprintf("A%d", i)] = fakeVersion(fmt.Sprintf("vA%d", i), depsRequired(next)...)
	}
	projects["A7"] = fakeVersion("vA7")
	_, err := mcModrinthResolveDependencies(context.Background(), fakeModrinthFetcher(projects), "A1", "", "1.21.4", "fabric", map[string]bool{}, 0)
	if err == nil {
		t.Fatal("expected depth limit error")
	}
	if !strings.Contains(err.Error(), "依赖链超过") {
		t.Errorf("error should mention depth limit: %v", err)
	}
}

func TestMCModrinthResolveDependenciesMissingDependency(t *testing.T) {
	// 依赖的项目不存在（fetch 报错）→ 错误信息指明哪个依赖失败。
	projects := map[string]*MCModrinthVersion{
		"A": fakeVersion("vA", depsRequired("ghost")...),
	}
	_, err := mcModrinthResolveDependencies(context.Background(), fakeModrinthFetcher(projects), "A", "", "1.21.4", "fabric", map[string]bool{}, 0)
	if err == nil {
		t.Fatal("expected error for missing dependency")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should mention missing dependency id: %v", err)
	}
}

func TestMCModrinthResolveDependenciesSharedVisited(t *testing.T) {
	// visited 在调用间共享：同一项目再次解析直接跳过（去重集合持久）。
	projects := map[string]*MCModrinthVersion{
		"A": fakeVersion("vA"),
		"B": fakeVersion("vB"),
	}
	visited := map[string]bool{}
	first, err := mcModrinthResolveDependencies(context.Background(), fakeModrinthFetcher(projects), "A", "", "1.21.4", "fabric", visited, 0)
	if err != nil {
		t.Fatalf("resolve A: %v", err)
	}
	second, err := mcModrinthResolveDependencies(context.Background(), fakeModrinthFetcher(projects), "A", "", "1.21.4", "fabric", visited, 0)
	if err != nil {
		t.Fatalf("resolve A again: %v", err)
	}
	if len(first) != 1 || len(second) != 0 {
		t.Errorf("shared visited should dedupe across calls: first=%v second=%v", idsOf(first), idsOf(second))
	}
}
