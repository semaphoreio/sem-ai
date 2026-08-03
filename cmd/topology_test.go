package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression: critical-path/topology returned every block with empty
// dependencies because fetchTopology only tried to fetch the pipeline YAML as
// a workflow artifact (which doesn't exist). It must also read the pipeline
// YAML from the local working tree, where the dependencies actually live.
func TestLocalPipelineDeps(t *testing.T) {
	dir := t.TempDir()
	semDir := filepath.Join(dir, ".semaphore")
	if err := os.MkdirAll(semDir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `version: v1.0
blocks:
  - name: Build
    dependencies: []
  - name: Test
    dependencies: ["Build"]
  - name: Deploy
    dependencies: ["Test"]
`
	if err := os.WriteFile(filepath.Join(semDir, "semaphore.yml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	deps := localPipelineDeps(semDir, "semaphore.yml")
	if deps == nil {
		t.Fatal("expected deps from local YAML, got nil")
	}
	if len(deps["Build"]) != 0 {
		t.Errorf("Build deps = %v, want []", deps["Build"])
	}
	if len(deps["Test"]) != 1 || deps["Test"][0] != "Build" {
		t.Errorf("Test deps = %v, want [Build]", deps["Test"])
	}
	if len(deps["Deploy"]) != 1 || deps["Deploy"][0] != "Test" {
		t.Errorf("Deploy deps = %v, want [Test]", deps["Deploy"])
	}

	// The critical path through this chain is the full Build->Test->Deploy.
	blocks := []blockTopology{
		{Name: "Build", Dependencies: deps["Build"]},
		{Name: "Test", Dependencies: deps["Test"]},
		{Name: "Deploy", Dependencies: deps["Deploy"]},
	}
	if got := computeCriticalPath(blocks); len(got) != 3 {
		t.Errorf("critical path = %v, want 3 blocks (Build->Test->Deploy)", got)
	}

	// Missing file → nil (graceful).
	if localPipelineDeps(filepath.Join(dir, "nope"), "semaphore.yml") != nil {
		t.Error("expected nil for missing YAML")
	}
}

func TestParseYAMLDependenciesBasic(t *testing.T) {
	deps := parseYAMLDependencies([]byte(`
blocks:
  - name: Build
    dependencies: []
  - name: Test
    dependencies:
      - Build
  - name: Deploy
    dependencies:
      - Test
`))
	if deps == nil {
		t.Fatal("expected non-nil deps")
	}
	if len(deps["Build"]) != 0 {
		t.Errorf("Build deps = %v, want []", deps["Build"])
	}
	if len(deps["Test"]) != 1 || deps["Test"][0] != "Build" {
		t.Errorf("Test deps = %v, want [Build]", deps["Test"])
	}
	if len(deps["Deploy"]) != 1 || deps["Deploy"][0] != "Test" {
		t.Errorf("Deploy deps = %v, want [Test]", deps["Deploy"])
	}
}

func TestParseYAMLDependenciesDiamond(t *testing.T) {
	deps := parseYAMLDependencies([]byte(`
blocks:
  - name: Root
    dependencies: []
  - name: Left
    dependencies: [Root]
  - name: Right
    dependencies: [Root]
  - name: Merge
    dependencies: [Left, Right]
`))
	if len(deps["Root"]) != 0 {
		t.Errorf("Root deps = %v, want []", deps["Root"])
	}
	if len(deps["Merge"]) != 2 {
		t.Errorf("Merge should have 2 deps, got %v", deps["Merge"])
	}
}

func TestParseYAMLDependenciesExplicitEmpty(t *testing.T) {
	deps := parseYAMLDependencies([]byte(`
blocks:
  - name: Standalone
    dependencies: []
`))
	if deps["Standalone"] == nil {
		t.Error("explicit empty deps should be [], not nil")
	}
	if len(deps["Standalone"]) != 0 {
		t.Errorf("got %v, want []", deps["Standalone"])
	}
}

func TestParseYAMLDependenciesOmittedField(t *testing.T) {
	deps := parseYAMLDependencies([]byte(`
blocks:
  - name: Standalone
`))
	if deps == nil {
		t.Fatal("expected non-nil")
	}
	if deps["Standalone"] == nil {
		t.Error("omitted deps field should default to [], not nil")
	}
}

func TestParseYAMLDependenciesEdgeCases(t *testing.T) {
	if parseYAMLDependencies([]byte(``)) != nil {
		t.Error("empty YAML should return nil")
	}
	if parseYAMLDependencies([]byte(`{not: [valid`)) != nil {
		t.Error("invalid YAML should return nil")
	}
	if parseYAMLDependencies([]byte(`version: v1.0`)) != nil {
		t.Error("YAML without blocks should return nil")
	}
}

// --- computeCriticalPath (real exported function) ---

func TestCriticalPathLinear(t *testing.T) {
	path := computeCriticalPath([]blockTopology{
		{Name: "A", Dependencies: []string{}},
		{Name: "B", Dependencies: []string{"A"}},
		{Name: "C", Dependencies: []string{"B"}},
		{Name: "D", Dependencies: []string{"C"}},
	})
	if len(path) != 4 {
		t.Fatalf("got length %d, want 4; path = %v", len(path), path)
	}
	for i, want := range []string{"A", "B", "C", "D"} {
		if path[i] != want {
			t.Errorf("path[%d] = %q, want %q", i, path[i], want)
		}
	}
}

func TestCriticalPathDiamond(t *testing.T) {
	path := computeCriticalPath([]blockTopology{
		{Name: "Root", Dependencies: []string{}},
		{Name: "Left", Dependencies: []string{"Root"}},
		{Name: "Right", Dependencies: []string{"Root"}},
		{Name: "Merge", Dependencies: []string{"Left", "Right"}},
	})
	if len(path) != 3 {
		t.Errorf("diamond path length = %d, want 3; path = %v", len(path), path)
	}
	if path[0] != "Root" || path[len(path)-1] != "Merge" {
		t.Errorf("path should start with Root and end with Merge, got %v", path)
	}
}

func TestCriticalPathIsolated(t *testing.T) {
	path := computeCriticalPath([]blockTopology{
		{Name: "A", Dependencies: []string{}},
		{Name: "B", Dependencies: []string{}},
		{Name: "C", Dependencies: []string{}},
	})
	if len(path) != 1 {
		t.Errorf("isolated blocks should have depth 1, got %d", len(path))
	}
}

func TestCriticalPathSingle(t *testing.T) {
	path := computeCriticalPath([]blockTopology{
		{Name: "Only", Dependencies: []string{}},
	})
	if len(path) != 1 || path[0] != "Only" {
		t.Errorf("got %v, want [Only]", path)
	}
}

func TestCriticalPathLongVsShort(t *testing.T) {
	path := computeCriticalPath([]blockTopology{
		{Name: "A", Dependencies: []string{}},
		{Name: "B", Dependencies: []string{"A"}},
		{Name: "C", Dependencies: []string{"B"}},
		{Name: "D", Dependencies: []string{"C"}},
		{Name: "E", Dependencies: []string{"A"}}, // short branch
	})
	if len(path) != 4 {
		t.Errorf("should pick long branch (4), got %d; path = %v", len(path), path)
	}
}

func TestCriticalPathEmpty(t *testing.T) {
	path := computeCriticalPath([]blockTopology{})
	if len(path) != 0 {
		t.Errorf("empty input should return empty path, got %v", path)
	}
}

// --- computeBlastRadius (real exported function) ---

func has(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func TestBlastRadiusRootFailure(t *testing.T) {
	roots, casc := computeBlastRadius([]blockTopology{
		{Name: "Build", Result: "failed", Dependencies: []string{}},
		{Name: "Test", Result: "stopped", Dependencies: []string{"Build"}},
		{Name: "Deploy", Result: "stopped", Dependencies: []string{"Test"}},
	})
	if len(roots) != 1 || roots[0] != "Build" {
		t.Errorf("roots = %v, want [Build]", roots)
	}
	if !has(casc, "Test") || !has(casc, "Deploy") {
		t.Errorf("cascading = %v, want [Test, Deploy]", casc)
	}
}

func TestBlastRadiusPassedUpstream(t *testing.T) {
	roots, casc := computeBlastRadius([]blockTopology{
		{Name: "Build", Result: "passed", Dependencies: []string{}},
		{Name: "Test", Result: "failed", Dependencies: []string{"Build"}},
		{Name: "Deploy", Result: "stopped", Dependencies: []string{"Test"}},
	})
	if len(roots) != 1 || roots[0] != "Test" {
		t.Errorf("roots = %v, want [Test]", roots)
	}
	if has(casc, "Test") {
		t.Error("Test is a root failure, not cascading")
	}
	if !has(casc, "Deploy") {
		t.Errorf("Deploy should cascade, got %v", casc)
	}
}

func TestBlastRadiusAllPassed(t *testing.T) {
	roots, casc := computeBlastRadius([]blockTopology{
		{Name: "Build", Result: "passed", Dependencies: []string{}},
		{Name: "Test", Result: "passed", Dependencies: []string{"Build"}},
	})
	if len(roots) != 0 || len(casc) != 0 {
		t.Errorf("all passed should have no failures; roots=%v casc=%v", roots, casc)
	}
}

func TestBlastRadiusMultipleRoots(t *testing.T) {
	roots, casc := computeBlastRadius([]blockTopology{
		{Name: "Unit", Result: "failed", Dependencies: []string{}},
		{Name: "Integration", Result: "failed", Dependencies: []string{}},
		{Name: "Deploy", Result: "stopped", Dependencies: []string{"Unit", "Integration"}},
	})
	if len(roots) != 2 {
		t.Errorf("expected 2 roots, got %v", roots)
	}
	if !has(casc, "Deploy") {
		t.Errorf("Deploy should cascade, got %v", casc)
	}
}

func TestBlastRadiusFailedWithFailedDep(t *testing.T) {
	// Downstream failed but its dep also failed — NOT a root failure
	roots, _ := computeBlastRadius([]blockTopology{
		{Name: "Upstream", Result: "failed", Dependencies: []string{}},
		{Name: "Downstream", Result: "failed", Dependencies: []string{"Upstream"}},
	})
	if has(roots, "Downstream") {
		t.Error("Downstream has a failed dep, should not be a root failure")
	}
	if !has(roots, "Upstream") {
		t.Error("Upstream should be a root failure")
	}
}

func TestBlastRadiusCanceled(t *testing.T) {
	_, casc := computeBlastRadius([]blockTopology{
		{Name: "Build", Result: "failed", Dependencies: []string{}},
		{Name: "Test", Result: "canceled", Dependencies: []string{"Build"}},
	})
	if !has(casc, "Test") {
		t.Errorf("canceled with failed dep should cascade, got %v", casc)
	}
}

// --- topoBlocksFromMap ---

func TestTopoBlocksFromMapTyped(t *testing.T) {
	topo := map[string]interface{}{
		"blocks": []blockTopology{
			{Name: "A", State: "done", Result: "passed", Dependencies: []string{"B"}},
		},
	}
	result := topoBlocksFromMap(topo)
	if len(result) != 1 || result[0].Name != "A" {
		t.Errorf("got %v, want [{A ...}]", result)
	}
}

func TestTopoBlocksFromMapInterface(t *testing.T) {
	topo := map[string]interface{}{
		"blocks": []interface{}{
			map[string]interface{}{
				"name": "Build", "state": "done", "result": "passed",
				"dependencies": []interface{}{},
			},
			map[string]interface{}{
				"name": "Test", "state": "done", "result": "failed",
				"dependencies": []interface{}{"Build"},
			},
		},
	}
	result := topoBlocksFromMap(topo)
	if len(result) != 2 {
		t.Fatalf("got %d blocks, want 2", len(result))
	}
	if result[1].Dependencies[0] != "Build" {
		t.Errorf("Test dep = %v, want [Build]", result[1].Dependencies)
	}
}

func TestTopoBlocksFromMapNil(t *testing.T) {
	if r := topoBlocksFromMap(map[string]interface{}{"blocks": nil}); len(r) != 0 {
		t.Errorf("nil blocks should return empty, got %v", r)
	}
	if r := topoBlocksFromMap(map[string]interface{}{"other": "val"}); len(r) != 0 {
		t.Errorf("missing key should return empty, got %v", r)
	}
}
