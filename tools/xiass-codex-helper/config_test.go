package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testOriginalConfig = `model_provider = "official"
model = "gpt-old"
model_reasoning_effort = "ultra"
web_search = "cached"

[mcp_servers.example]
command = "example-mcp"

[desktop]
appearanceTheme = "system"

[model_providers.official]
name = "Official"
base_url = "https://example.com"
wire_api = "responses"
requires_openai_auth = true
`

func TestApplyAndRestorePreservesOriginalConfig(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	input := ApplyConfig{
		BaseURL: "https://gateway.example.com/v1/",
		APIKey:  "sk-test-1234567890",
		KeyName: "Codex",
	}
	result, err := manager.Apply(input)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.BackupID == "" || result.ConfigSHA == "" {
		t.Fatalf("Apply() result is incomplete: %+v", result)
	}

	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	for _, preserved := range []string{
		`model_reasoning_effort = "ultra"`,
		`[mcp_servers.example]`,
		`command = "example-mcp"`,
		`[desktop]`,
		`[model_providers.official]`,
	} {
		if !strings.Contains(text, preserved) {
			t.Errorf("updated config did not preserve %q", preserved)
		}
	}
	if count := strings.Count(text, "[model_providers.official]"); count != 1 {
		t.Fatalf("XIASS provider count = %d, want 1", count)
	}
	if !strings.Contains(text, `model_provider = "official"`) {
		t.Fatal("existing custom provider ID was not preserved")
	}
	if !strings.Contains(text, `http_headers = { "x-openai-actor-authorization" = "gateway.example.com" }`) {
		t.Fatal("actor authorization header does not match the working XIASS Codex configuration")
	}
	if strings.Contains(text, `x-openai-actor-authorization" = "https://`) {
		t.Fatal("actor authorization header must contain the XIASS hostname, not a URL")
	}
	if err := verifyManagedConfig(written, ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: input.APIKey}, "official"); err != nil {
		t.Fatalf("written config verification failed: %v", err)
	}

	backupBytes, err := os.ReadFile(manager.originalPath(result.BackupID))
	if err != nil {
		t.Fatal(err)
	}
	if string(backupBytes) != testOriginalConfig {
		t.Fatal("backup is not byte-for-byte identical to the original config")
	}

	restore, err := manager.Restore(result.BackupID)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restore.SafetyBackupID == "" {
		t.Fatal("Restore() did not create a pre-restore safety backup")
	}
	restored, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != testOriginalConfig {
		t.Fatal("restored config is not byte-for-byte identical to the original")
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	input := ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"}
	if _, err := manager.Apply(input); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(input); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(written), "[model_providers.codex_local_access]"); count != 1 {
		t.Fatalf("XIASS provider count after repeated apply = %d, want 1", count)
	}
}

func TestApplyRemovesLegacyXIASSProviderSection(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	original := `model_provider = "xiass"

[model_providers.xiass]
name = "XIASS API"
base_url = "https://old.example.com"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "old-secret"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"}); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(written)
	if strings.Contains(text, "[model_providers.xiass]") || strings.Contains(text, "old-secret") {
		t.Fatal("legacy XIASS provider section was not removed")
	}
	if strings.Count(text, "[model_providers.codex_local_access]") != 1 {
		t.Fatal("new stable provider section is missing")
	}
}

func TestUpgradeLegacyProviderReusesConnectionUnderStableID(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	original := `model_provider = "xiass"

[model_providers.xiass]
name = "XIASS API"
base_url = "https://gateway.example.com"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-test-1234567890"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	result, upgraded, err := manager.UpgradeLegacyProvider()
	if err != nil {
		t.Fatal(err)
	}
	if !upgraded || result.ProviderID != providerID {
		t.Fatalf("legacy upgrade result = upgraded %v, result %+v", upgraded, result)
	}
	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(written), "[model_providers.xiass]") || !strings.Contains(string(written), "[model_providers.codex_local_access]") {
		t.Fatal("legacy provider was not upgraded to the stable provider ID")
	}
}

func TestApplyRefusesInvalidExistingConfig(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	original := []byte("[broken\nvalue = true\n")
	if err := os.WriteFile(manager.ConfigPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"})
	if err == nil || !strings.Contains(err.Error(), "existing config.toml is invalid") {
		t.Fatalf("Apply() error = %v, want invalid existing config error", err)
	}
	after, readErr := os.ReadFile(manager.ConfigPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(original) {
		t.Fatal("invalid existing config was modified")
	}
}

func TestRestoreRejectsCorruptBackup(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manager.originalPath(result.BackupID), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Restore(result.BackupID); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Restore() error = %v, want checksum mismatch", err)
	}
	after, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("current config changed after corrupt backup restore attempt")
	}
}

func TestRestoreRemovesConfigCreatedByHelper(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	result, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Restore(result.BackupID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manager.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("config path still exists after restoring non-existent original: %v", err)
	}
	if _, err := os.Stat(filepath.Join(manager.BackupRoot, result.BackupID, "manifest.json")); err != nil {
		t.Fatal("original backup metadata was unexpectedly removed")
	}
}

func TestApplyRejectsSymbolicLinkConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires additional Windows privileges")
	}
	home := t.TempDir()
	target := filepath.Join(home, "actual-config.toml")
	if err := os.WriteFile(target, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewConfigManager(filepath.Join(home, ".codex"))
	if err := os.MkdirAll(filepath.Dir(manager.ConfigPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, manager.ConfigPath); err != nil {
		t.Fatal(err)
	}

	_, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-test-1234567890"})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Apply() error = %v, want symbolic link rejection", err)
	}
	after, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != testOriginalConfig {
		t.Fatal("symlink target changed after rejected apply")
	}
}

func TestEnsureConfigUnchangedDetectsExternalEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("model = \"before\"\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = \"after\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureConfigUnchanged(path, original, true); err == nil || !strings.Contains(err.Error(), "changed during") {
		t.Fatalf("ensureConfigUnchanged() error = %v, want concurrent edit rejection", err)
	}
}

func TestRollbackConfigErrorReportsUnverifiedRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config-as-directory")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	err := rollbackConfigError(errors.New("forced mutation failure"), path, []byte("model = \"before\"\n"), true, 0o600)
	var mutationErr *ConfigMutationError
	if !errors.As(err, &mutationErr) || mutationErr.RollbackErr == nil {
		t.Fatalf("rollbackConfigError() = %v, want ConfigMutationError with rollback failure", err)
	}
}
