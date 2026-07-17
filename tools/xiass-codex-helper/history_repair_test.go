package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryRepairSynchronizesAllProvidersAcrossLegacyAndCurrentStores(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	session := writeHistoryRollout(t, home, "sessions/2026/07/rollout-a.jsonl", "openai", "thread-a")
	archived := writeHistoryRollout(t, home, "archived_sessions/rollout-b.jsonl", "xiass", "thread-b")
	legacy := filepath.Join(home, "state_5.sqlite")
	current := filepath.Join(home, "sqlite", "state_5.sqlite")
	createHistoryDatabase(t, legacy, map[string]string{"thread-a": "openai", "thread-b": "xiass"})
	createHistoryDatabase(t, current, map[string]string{"thread-a": "openai"})

	repairer := NewHistoryRepairer(home)
	result, err := repairer.RepairCurrentProvider()
	if err != nil {
		t.Fatal(err)
	}
	if result.TargetProvider != "codex_local_access" {
		t.Fatalf("target provider = %q", result.TargetProvider)
	}
	if result.ScannedSessionFiles != 2 || result.UpdatedSessionFiles != 2 {
		t.Fatalf("session counts = scanned %d, updated %d", result.ScannedSessionFiles, result.UpdatedSessionFiles)
	}
	if result.ScannedDatabases != 2 || result.UpdatedDatabaseRows != 3 || result.ThreadCount != 3 {
		t.Fatalf("database result = %+v", result)
	}
	if result.BackupID == "" {
		t.Fatal("history repair did not create a backup")
	}
	assertHistoryRolloutProvider(t, session, "codex_local_access")
	assertHistoryRolloutProvider(t, archived, "codex_local_access")
	assertHistoryDatabaseProviders(t, legacy, map[string]int{"codex_local_access": 2})
	assertHistoryDatabaseProviders(t, current, map[string]int{"codex_local_access": 1})

	manifest := filepath.Join(repairer.BackupRoot, result.BackupID, "manifest.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Fatalf("history backup manifest is missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repairer.BackupRoot, result.BackupID, "database", "state_5.sqlite")); err != nil {
		t.Fatalf("legacy database backup is missing: %v", err)
	}

	second, err := repairer.RepairCurrentProvider()
	if err != nil {
		t.Fatal(err)
	}
	if second.UpdatedSessionFiles != 0 || second.UpdatedDatabaseRows != 0 || second.BackupID != "" {
		t.Fatalf("idempotent repair unexpectedly changed data: %+v", second)
	}
}

func TestHistoryRepairRejectsCorruptDatabaseBeforeChangingSessions(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "openai", "thread-a")
	before, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "sqlite"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "sqlite", "state_5.sqlite"), []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = NewHistoryRepairer(home).RepairCurrentProvider()
	if err == nil {
		t.Fatal("repair unexpectedly accepted a corrupt database")
	}
	after, readErr := os.ReadFile(session)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("session changed even though database validation failed")
	}
}

func TestHistoryRepairRejectsInvalidTarget(t *testing.T) {
	_, err := NewHistoryRepairer(t.TempDir()).Repair("bad provider\nvalue")
	if err == nil {
		t.Fatal("repair unexpectedly accepted an invalid provider ID")
	}
}

func TestHistoryRepairSynchronizesMissingProviderMarkers(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, providerID)
	session := writeHistoryRollout(t, home, "sessions/rollout-missing-provider.jsonl", "", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": ""})

	result, err := NewHistoryRepairer(home).RepairCurrentProvider()
	if err != nil {
		t.Fatal(err)
	}
	if result.UpdatedSessionFiles != 1 || result.UpdatedDatabaseRows != 1 {
		t.Fatalf("missing-provider repair result = %+v", result)
	}
	assertHistoryRolloutProvider(t, session, providerID)
	assertHistoryDatabase(t, databasePath, 1, providerID)
}

func TestHistoryRepairNeverMigratesStableProviderBackToLegacyXIASS(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "xiass")
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "codex_local_access", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "codex_local_access"})
	result, err := NewHistoryRepairer(home).RepairCurrentProvider()
	if err != nil {
		t.Fatal(err)
	}
	if result.UpdatedSessionFiles != 0 || result.UpdatedDatabaseRows != 0 {
		t.Fatalf("legacy target unexpectedly rewrote stable history: %+v", result)
	}
	assertHistoryRolloutProvider(t, session, "codex_local_access")
	assertHistoryDatabase(t, databasePath, 1, "codex_local_access")
}

func TestHistoryRepairRollsBackSessionAndDatabaseAfterWriteFailure(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "xiass"})
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER reject_provider_update BEFORE UPDATE OF model_provider ON threads BEGIN SELECT RAISE(FAIL, 'blocked by test'); END`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}

	result, err := NewHistoryRepairer(home).RepairCurrentProvider()
	if err == nil {
		t.Fatal("repair unexpectedly succeeded despite a forced database write failure")
	}
	if result.BackupID == "" {
		t.Fatal("failed repair did not leave a recovery backup")
	}
	after, readErr := os.ReadFile(session)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("session metadata was not restored after database failure")
	}
	assertHistoryDatabase(t, databasePath, 1, "xiass")
}

func TestHistoryRepairUpdatesEverySessionMetadataRecordAcrossLargeLines(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	path := filepath.Join(home, "sessions", "rollout-multi.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	metadata := func(id string) []byte {
		data, err := json.Marshal(map[string]any{
			"type":    "session_meta",
			"payload": map[string]any{"id": id, "model_provider": "xiass", "cwd": home},
		})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	content := append(metadata("thread-a"), '\n')
	content = append(content, bytes.Repeat([]byte("x"), historyMetadataMax+1024)...)
	content = append(content, '\n')
	content = append(content, metadata("thread-a")...)
	content = append(content, '\n')
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	createHistoryDatabase(t, filepath.Join(home, "state_5.sqlite"), map[string]string{"thread-a": "xiass"})

	result, err := NewHistoryRepairer(home).RepairCurrentProvider()
	if err != nil {
		t.Fatal(err)
	}
	if result.UpdatedSessionFiles != 1 {
		t.Fatalf("updated session files = %d, want 1", result.UpdatedSessionFiles)
	}
	for _, line := range []int{0, 2} {
		provider, err := sessionProviderAtLine(path, line)
		if err != nil {
			t.Fatal(err)
		}
		if provider != "codex_local_access" {
			t.Fatalf("line %d provider = %q", line, provider)
		}
	}
}

func TestHistoryRepairCreatesCoherentSnapshotForWALDatabase(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "xiass"})
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("PRAGMA journal_mode = WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("PRAGMA wal_autocheckpoint = 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("INSERT INTO threads (id, rollout_path, model_provider) VALUES (?, ?, ?)", "thread-b", "rollout-thread-b.jsonl", "xiass"); err != nil {
		t.Fatal(err)
	}

	repairer := NewHistoryRepairer(home)
	result, err := repairer.RepairCurrentProvider()
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(repairer.BackupRoot, result.BackupID, "database", "state_5.sqlite")
	assertHistoryDatabase(t, snapshot, 2, "xiass")
	assertHistoryDatabase(t, databasePath, 2, "codex_local_access")
}

func TestHistoryRestoreRejectsTamperedDatabaseBackupBeforeChangingSessions(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, providerID)
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", legacyProviderID, "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": legacyProviderID})

	repairer := NewHistoryRepairer(home)
	result, err := repairer.RepairCurrentProvider()
	if err != nil {
		t.Fatal(err)
	}
	if result.BackupID == "" {
		t.Fatal("history repair did not create a rollback backup")
	}
	manifest := readHistoryManifest(t, repairer, result.BackupID)
	if len(manifest.DatabaseFiles) != 1 {
		t.Fatalf("database backup count = %d, want 1", len(manifest.DatabaseFiles))
	}
	backupPath := filepath.Join(repairer.BackupRoot, result.BackupID, filepath.FromSlash(manifest.DatabaseFiles[0].BackupPath))
	if err := os.WriteFile(backupPath, []byte("corrupt snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := repairer.RestoreBackup(result.BackupID); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("RestoreBackup() error = %v, want checksum mismatch", err)
	}
	assertHistoryRolloutProvider(t, session, providerID)
	assertHistoryDatabaseProviders(t, databasePath, map[string]int{providerID: 1})
}

func TestHistoryRepairDetectsThreadIdentityReplacementWithSameRowCount(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "xiass"})
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`CREATE TRIGGER replace_thread_identity AFTER UPDATE OF model_provider ON threads BEGIN
		DELETE FROM threads WHERE id = NEW.id;
		INSERT INTO threads (id, rollout_path, model_provider) VALUES ('thread-replacement', 'rollout-replacement.jsonl', NEW.model_provider);
	END`)
	if closeErr := database.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewHistoryRepairer(home).RepairCurrentProvider()
	if err == nil {
		t.Fatal("repair did not detect a changed thread identity set")
	}
	assertHistoryRolloutProvider(t, session, "xiass")
	assertHistoryDatabaseProviders(t, databasePath, map[string]int{"xiass": 1})
	database, err = sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var id string
	if err := database.QueryRow("SELECT id FROM threads").Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != "thread-a" {
		t.Fatalf("thread identity was not restored: %q", id)
	}
}

func TestHistoryRepairRecoversInterruptedApplyingManifest(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "openai")
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "xiass"})
	repairer := NewHistoryRepairer(home)
	plan, err := repairer.buildPlan("codex_local_access", []string{"xiass"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := repairer.createBackup(plan)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Status = historyStatusApplying
	if err := repairer.writeBackupManifest(manifest); err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Sessions {
		if err := replaceSessionMetadataLine(change, change.OriginalLine, change.UpdatedLine); err != nil {
			t.Fatal(err)
		}
	}
	for _, database := range plan.Databases {
		if _, err := updateDatabaseProvider(database, "codex_local_access", []string{"xiass"}); err != nil {
			t.Fatal(err)
		}
	}
	assertHistoryRolloutProvider(t, session, "codex_local_access")

	if _, err := repairer.RepairCurrentProvider(); err != nil {
		t.Fatal(err)
	}
	assertHistoryRolloutProvider(t, session, "openai")
	assertHistoryDatabase(t, databasePath, 1, "openai")
	data, err := os.ReadFile(filepath.Join(repairer.BackupRoot, manifest.ID, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recovered historyBackupManifest
	if err := json.Unmarshal(data, &recovered); err != nil {
		t.Fatal(err)
	}
	if recovered.Status != historyStatusRolledBack {
		t.Fatalf("recovered manifest status = %q", recovered.Status)
	}
}

func TestInterruptedRecoveryNeverOverwritesNewerConversations(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "openai")
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "xiass"})
	repairer := NewHistoryRepairer(home)
	plan, err := repairer.buildPlan("codex_local_access", []string{"xiass"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := repairer.createBackup(plan)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Status = historyStatusApplying
	if err := repairer.writeBackupManifest(manifest); err != nil {
		t.Fatal(err)
	}
	for _, change := range plan.Sessions {
		if err := replaceSessionMetadataLine(change, change.OriginalLine, change.UpdatedLine); err != nil {
			t.Fatal(err)
		}
	}
	for _, database := range plan.Databases {
		if _, err := updateDatabaseProvider(database, "codex_local_access", []string{"xiass"}); err != nil {
			t.Fatal(err)
		}
	}
	newSession := writeHistoryRollout(t, home, "sessions/rollout-new.jsonl", "openai", "thread-new")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	_, insertErr := database.Exec("INSERT INTO threads (id, rollout_path, model_provider) VALUES (?, ?, ?)", "thread-new", newSession, "openai")
	closeErr := database.Close()
	if insertErr != nil {
		t.Fatal(insertErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	_, err = repairer.RepairCurrentProvider()
	if err == nil {
		t.Fatal("interrupted recovery overwrote newer data instead of stopping")
	}
	var repairErr *HistoryRepairApplyError
	if !errors.As(err, &repairErr) || repairErr.RollbackErr == nil {
		t.Fatalf("interrupted recovery error is not marked unsafe: %v", err)
	}
	assertHistoryRolloutProvider(t, session, "codex_local_access")
	assertHistoryRolloutProvider(t, newSession, "openai")
	assertHistoryDatabaseProviders(t, databasePath, map[string]int{"codex_local_access": 1, "openai": 1})
}

func TestHistoryRepairRealDataReadOnlyPlan(t *testing.T) {
	home := os.Getenv("XIASS_TEST_CODEX_HOME")
	if home == "" {
		t.Skip("set XIASS_TEST_CODEX_HOME to run the read-only real-data check")
	}
	allowImmutableHistoryReadForTests = true
	t.Cleanup(func() { allowImmutableHistoryReadForTests = false })
	target, err := readCurrentProvider(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	repairer := NewHistoryRepairer(home)
	sources, err := repairer.discoverSourceProviders(target)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := repairer.buildPlan(target, sources)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ScannedFiles == 0 || plan.ThreadCount == 0 {
		t.Fatalf("real-data plan found no conversations: files=%d thread_rows=%d", plan.ScannedFiles, plan.ThreadCount)
	}
	t.Logf("provider=%s rollout_files=%d database_count=%d thread_rows=%d pending_rollout_repairs=%d", target, plan.ScannedFiles, len(plan.Databases), plan.ThreadCount, len(plan.Sessions))
}

func writeHistoryConfig(t *testing.T, home, provider string) {
	t.Helper()
	content := []byte("model_provider = \"" + provider + "\"\n")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeHistoryRollout(t *testing.T, home, relative, provider, threadID string) string {
	t.Helper()
	path := filepath.Join(home, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(map[string]any{
		"type": "session_meta",
		"payload": map[string]any{
			"id":             threadID,
			"model_provider": provider,
			"cwd":            filepath.Join(home, "workspace"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	content := append(metadata, []byte("\n{\"type\":\"event_msg\",\"payload\":{\"type\":\"user_message\"}}\n")...)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func createHistoryDatabase(t *testing.T, path string, providers map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		rollout_path TEXT NOT NULL,
		model_provider TEXT NOT NULL,
		has_user_event INTEGER NOT NULL DEFAULT 1,
		cwd TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	for id, provider := range providers {
		if _, err := database.Exec("INSERT INTO threads (id, rollout_path, model_provider) VALUES (?, ?, ?)", id, "rollout-"+id+".jsonl", provider); err != nil {
			t.Fatal(err)
		}
	}
}

func assertHistoryRolloutProvider(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line, _, _ := bytes.Cut(data, []byte("\n"))
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		t.Fatal(err)
	}
	payload := record["payload"].(map[string]any)
	if payload["model_provider"] != expected {
		t.Fatalf("rollout provider = %v, want %q", payload["model_provider"], expected)
	}
}

func assertHistoryDatabase(t *testing.T, path string, expectedCount int, expectedProvider string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count, mismatched int
	if err := database.QueryRow("SELECT COUNT(*) FROM threads").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM threads WHERE model_provider <> ?", expectedProvider).Scan(&mismatched); err != nil {
		t.Fatal(err)
	}
	if count != expectedCount || mismatched != 0 {
		t.Fatalf("database %s has count=%d mismatched=%d", path, count, mismatched)
	}
}

func assertHistoryDatabaseProviders(t *testing.T, path string, expected map[string]int) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query("SELECT model_provider, COUNT(*) FROM threads GROUP BY model_provider")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	actual := map[string]int{}
	for rows.Next() {
		var provider string
		var count int
		if err := rows.Scan(&provider, &count); err != nil {
			t.Fatal(err)
		}
		actual[provider] = count
	}
	if len(actual) != len(expected) {
		t.Fatalf("database providers = %+v, want %+v", actual, expected)
	}
	for provider, count := range expected {
		if actual[provider] != count {
			t.Fatalf("database providers = %+v, want %+v", actual, expected)
		}
	}
}

func readHistoryManifest(t *testing.T, repairer *HistoryRepairer, backupID string) historyBackupManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repairer.BackupRoot, backupID, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest historyBackupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}
