package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeWorkspaceStateRepairsMacPathsAndProjects(t *testing.T) {
	state := map[string]any{
		"active-workspace-roots":         []any{`\Users\wufeng\Desktop\codex`},
		"electron-saved-workspace-roots": []any{`\Users\wufeng\Desktop\codex`, `\Users\wufeng\Desktop\codex\app`},
		"project-order":                  []any{"project-root", "project-app"},
		"selected-project":               map[string]any{"type": "local", "projectId": "project-root"},
		"local-projects": map[string]any{
			"project-root": map[string]any{"id": "project-root", "name": `\Users\wufeng\Desktop\codex`, "rootPaths": []any{}},
			"project-app":  map[string]any{"id": "project-app", "name": `\Users\wufeng\Desktop\codex\app`, "rootPaths": []any{}},
		},
		"thread-workspace-root-hints": map[string]any{"thread-a": `\Users\wufeng\Desktop\codex`},
		"thread-writable-roots":       map[string]any{"thread-a": []any{`\Users\wufeng\Desktop\codex`}},
	}

	changed, projects := normalizeWorkspaceState(state, "darwin")
	if !changed || projects != 2 {
		t.Fatalf("changed=%v projects=%d", changed, projects)
	}
	assertWorkspaceStringList(t, state["active-workspace-roots"], []string{"/Users/wufeng/Desktop/codex"})
	assertWorkspaceStringList(t, state["electron-saved-workspace-roots"], []string{"/Users/wufeng/Desktop/codex", "/Users/wufeng/Desktop/codex/app"})
	root := state["local-projects"].(map[string]any)["project-root"].(map[string]any)
	if root["name"] != "codex" {
		t.Fatalf("root project name = %v", root["name"])
	}
	assertWorkspaceStringList(t, root["rootPaths"], []string{"/Users/wufeng/Desktop/codex"})
	app := state["local-projects"].(map[string]any)["project-app"].(map[string]any)
	if app["name"] != "app" {
		t.Fatalf("app project name = %v", app["name"])
	}
	assertWorkspaceStringList(t, app["rootPaths"], []string{"/Users/wufeng/Desktop/codex/app"})
	if changed, _ := normalizeWorkspaceState(state, "darwin"); changed {
		t.Fatal("workspace repair is not idempotent")
	}
}

func TestNormalizeWorkspaceStateDoesNotRewriteWindowsPaths(t *testing.T) {
	state := map[string]any{
		"active-workspace-roots": []any{`C:\Users\wufeng\Desktop\codex`},
		"local-projects": map[string]any{
			"project-root": map[string]any{
				"name":      "codex",
				"rootPaths": []any{`C:\Users\wufeng\Desktop\codex`},
			},
		},
	}
	changed, projects := normalizeWorkspaceState(state, "windows")
	if changed || projects != 1 {
		t.Fatalf("changed=%v projects=%d", changed, projects)
	}
}

func TestRepairWorkspaceStateCreatesBackupAndFallback(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(home, ".codex-global-state.json")
	state := map[string]any{
		"active-workspace-roots": []any{`\Users\wufeng\Desktop\codex`},
		"local-projects": map[string]any{
			"project-root": map[string]any{"name": `\Users\wufeng\Desktop\codex`, "rootPaths": []any{}},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath+".bak", data, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := repairWorkspaceStateForOS(home, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Scanned || !result.Updated || result.ProjectCount != 1 || result.BackupID == "" {
		t.Fatalf("unexpected repair result: %+v", result)
	}
	if err := verifyWorkspaceState(statePath, "darwin"); err != nil {
		t.Fatal(err)
	}
	if err := verifyWorkspaceState(statePath+".bak", "darwin"); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(home, "xiass-helper", workspaceStateBackupDirName, result.BackupID)
	if _, err := os.Stat(filepath.Join(backupDir, ".codex-global-state.json")); err != nil {
		t.Fatalf("workspace backup missing: %v", err)
	}

	second, err := repairWorkspaceStateForOS(home, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if second.Updated || second.BackupID != "" {
		t.Fatalf("second repair was not idempotent: %+v", second)
	}
}

func assertWorkspaceStringList(t *testing.T, raw any, expected []string) {
	t.Helper()
	actual, ok := raw.([]string)
	if !ok {
		t.Fatalf("value type = %T, want []string", raw)
	}
	if len(actual) != len(expected) {
		t.Fatalf("list length = %d, want %d", len(actual), len(expected))
	}
	for index := range expected {
		if actual[index] != expected[index] {
			t.Fatalf("list[%d] = %q, want %q", index, actual[index], expected[index])
		}
	}
}
