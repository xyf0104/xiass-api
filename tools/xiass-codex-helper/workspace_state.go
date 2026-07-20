package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const workspaceStateBackupDirName = "workspace-state-backups"

type WorkspaceStateRepairResult struct {
	Scanned      bool   `json:"scanned"`
	Updated      bool   `json:"updated"`
	ProjectCount int    `json:"project_count"`
	BackupID     string `json:"backup_id,omitempty"`
}

func repairWorkspaceState(codexHome string) (WorkspaceStateRepairResult, error) {
	return repairWorkspaceStateForOS(codexHome, runtime.GOOS)
}

func repairWorkspaceStateForOS(codexHome, goos string) (WorkspaceStateRepairResult, error) {
	result := WorkspaceStateRepairResult{}
	statePath := filepath.Join(codexHome, ".codex-global-state.json")
	data, err := os.ReadFile(statePath)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("read Codex workspace state: %w", err)
	}
	info, err := os.Stat(statePath)
	if err != nil || !info.Mode().IsRegular() {
		return result, errors.New("Codex workspace state is not a regular file")
	}

	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		return result, fmt.Errorf("parse Codex workspace state: %w", err)
	}
	result.Scanned = true
	changed, projectCount := normalizeWorkspaceState(state, goos)
	result.ProjectCount = projectCount
	if !changed {
		return result, nil
	}

	backupID := time.Now().UTC().Format("20060102T150405.000000000Z")
	backupDir := filepath.Join(codexHome, "xiass-helper", workspaceStateBackupDirName, backupID)
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return result, fmt.Errorf("create workspace state backup: %w", err)
	}
	if err := copyRegularFile(statePath, filepath.Join(backupDir, filepath.Base(statePath))); err != nil {
		return result, fmt.Errorf("backup workspace state: %w", err)
	}
	backupPath := statePath + ".bak"
	backupExisted := false
	if _, err := os.Stat(backupPath); err == nil {
		backupExisted = true
		if err := copyRegularFile(backupPath, filepath.Join(backupDir, filepath.Base(backupPath))); err != nil {
			return result, fmt.Errorf("backup workspace state fallback: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return result, fmt.Errorf("inspect workspace state fallback: %w", err)
	}

	updated, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return result, fmt.Errorf("encode repaired workspace state: %w", err)
	}
	updated = append(updated, '\n')
	if err := writeFileAtomic(statePath, updated, info.Mode().Perm()); err != nil {
		return result, fmt.Errorf("write repaired workspace state: %w", err)
	}
	if err := writeFileAtomic(backupPath, updated, info.Mode().Perm()); err != nil {
		rollbackErr := restoreWorkspaceStateBackup(statePath, backupPath, backupDir, backupExisted)
		if rollbackErr != nil {
			return result, fmt.Errorf("write workspace state fallback: %v; rollback failed: %w", err, rollbackErr)
		}
		return result, fmt.Errorf("write workspace state fallback; repaired state was rolled back: %w", err)
	}
	if err := verifyWorkspaceState(statePath, goos); err != nil {
		rollbackErr := restoreWorkspaceStateBackup(statePath, backupPath, backupDir, backupExisted)
		if rollbackErr != nil {
			return result, fmt.Errorf("verify repaired workspace state: %v; rollback failed: %w", err, rollbackErr)
		}
		return result, fmt.Errorf("verify repaired workspace state; changes were rolled back: %w", err)
	}

	result.Updated = true
	result.BackupID = backupID
	return result, nil
}

func normalizeWorkspaceState(state map[string]any, goos string) (bool, int) {
	changed := false
	for _, key := range []string{"active-workspace-roots", "electron-saved-workspace-roots"} {
		normalized, updated := normalizeWorkspacePathList(state[key], goos)
		if updated {
			state[key] = normalized
			changed = true
		}
	}
	if order, ok := state["project-order"].([]any); ok {
		normalized, updated := normalizeWorkspacePathList(order, goos)
		if updated {
			state["project-order"] = normalized
			changed = true
		}
	}
	if selected, ok := state["selected-project"].(string); ok {
		if normalized, updated := normalizeWorkspacePath(selected, goos); updated {
			state["selected-project"] = normalized
			changed = true
		}
	}

	projectCount := 0
	if projects, ok := state["local-projects"].(map[string]any); ok {
		for _, raw := range projects {
			project, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			projectCount++
			roots, rootsChanged := normalizeWorkspacePathList(project["rootPaths"], goos)
			if len(roots) == 0 {
				if name, ok := project["name"].(string); ok {
					if inferred, pathLike := normalizeWorkspaceProjectPath(name, goos); pathLike {
						roots = []string{inferred}
						rootsChanged = true
					}
				}
			}
			if rootsChanged {
				project["rootPaths"] = roots
				changed = true
			}
			if len(roots) > 0 {
				if name, ok := project["name"].(string); ok && workspaceProjectNameIsPath(name, goos) {
					base := filepath.Base(roots[0])
					if base != "" && base != "." && name != base {
						project["name"] = base
						changed = true
					}
				}
			}
		}
	}

	if hints, ok := state["thread-workspace-root-hints"].(map[string]any); ok {
		for threadID, raw := range hints {
			value, ok := raw.(string)
			if !ok {
				continue
			}
			if normalized, updated := normalizeWorkspacePath(value, goos); updated {
				hints[threadID] = normalized
				changed = true
			}
		}
	}
	if writable, ok := state["thread-writable-roots"].(map[string]any); ok {
		for threadID, raw := range writable {
			normalized, updated := normalizeWorkspacePathList(raw, goos)
			if updated {
				writable[threadID] = normalized
				changed = true
			}
		}
	}
	return changed, projectCount
}

func normalizeWorkspacePathList(raw any, goos string) ([]string, bool) {
	var values []string
	switch items := raw.(type) {
	case []any:
		values = make([]string, 0, len(items))
		for _, item := range items {
			value, ok := item.(string)
			if ok {
				values = append(values, value)
			}
		}
	case []string:
		values = append([]string(nil), items...)
	case nil:
		return nil, false
	default:
		return nil, false
	}

	changed := false
	for index, value := range values {
		if normalized, updated := normalizeWorkspacePath(value, goos); updated {
			values[index] = normalized
			changed = true
		}
	}
	return values, changed
}

func normalizeWorkspacePath(value, goos string) (string, bool) {
	if goos != "darwin" {
		return value, false
	}
	trimmed := strings.TrimSpace(value)
	normalized := strings.ReplaceAll(trimmed, `\`, "/")
	if strings.HasPrefix(normalized, "/") {
		normalized = filepath.Clean(normalized)
	}
	return normalized, normalized != value
}

func normalizeWorkspaceProjectPath(value, goos string) (string, bool) {
	normalized, _ := normalizeWorkspacePath(value, goos)
	if goos == "darwin" && filepath.IsAbs(normalized) {
		return normalized, true
	}
	return "", false
}

func workspaceProjectNameIsPath(value, goos string) bool {
	_, ok := normalizeWorkspaceProjectPath(value, goos)
	return ok
}

func verifyWorkspaceState(path, goos string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	changed, _ := normalizeWorkspaceState(state, goos)
	if changed {
		return errors.New("workspace state still contains invalid project paths")
	}
	return nil
}

func restoreWorkspaceStateBackup(statePath, fallbackPath, backupDir string, fallbackExisted bool) error {
	if err := copyRegularFile(filepath.Join(backupDir, filepath.Base(statePath)), statePath); err != nil {
		return err
	}
	if fallbackExisted {
		return copyRegularFile(filepath.Join(backupDir, filepath.Base(fallbackPath)), fallbackPath)
	}
	if err := os.Remove(fallbackPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
