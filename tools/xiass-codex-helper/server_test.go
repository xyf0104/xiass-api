package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHelperServerApplyAndRestoreFlow(t *testing.T) {
	manager := NewConfigManager(t.TempDir())
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := newHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stopCount atomic.Int32
	var startCount atomic.Int32
	helper.stop = func(CodexInstallation) error {
		stopCount.Add(1)
		return nil
	}
	helper.start = func(CodexInstallation) error {
		startCount.Add(1)
		return nil
	}

	handler := helper.routes()

	statusResponse := getJSON(t, handler, "/api/status")
	connectURL, _ := statusResponse["connect_url"].(string)
	if !strings.HasPrefix(connectURL, "https://gateway.example.com/codex-helper/connect?") {
		t.Fatalf("connect_url = %q", connectURL)
	}

	applyBody := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	apply := postHelperJSON(t, handler, "/api/apply", helper.state, applyBody, http.StatusOK)
	if ok, _ := apply["ok"].(bool); !ok {
		t.Fatalf("apply response = %+v", apply)
	}
	backupID, _ := apply["backup_id"].(string)
	if backupID == "" {
		t.Fatal("apply response has no backup ID")
	}
	if stopCount.Load() != 1 || startCount.Load() != 1 {
		t.Fatalf("lifecycle counts after apply = stop %d, start %d", stopCount.Load(), startCount.Load())
	}

	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `[model_providers.official]`) {
		t.Fatal("apply endpoint did not write XIASS provider")
	}

	restoreBody, _ := json.Marshal(map[string]string{"backup_id": backupID})
	restore := postHelperJSON(t, handler, "/api/restore", helper.state, restoreBody, http.StatusOK)
	if ok, _ := restore["ok"].(bool); !ok {
		t.Fatalf("restore response = %+v", restore)
	}
	if stopCount.Load() != 2 || startCount.Load() != 2 {
		t.Fatalf("lifecycle counts after restore = stop %d, start %d", stopCount.Load(), startCount.Load())
	}
	restored, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != testOriginalConfig {
		t.Fatal("HTTP restore flow did not restore original config exactly")
	}
}

func TestHelperServerRejectsMissingStateAndForeignBaseURL(t *testing.T) {
	helper, err := newHelperServer(NewConfigManager(t.TempDir()), "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	handler := helper.routes()

	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	postHelperJSON(t, handler, "/api/apply", "", body, http.StatusForbidden)
	foreign := []byte(`{"base_url":"https://evil.example","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	postHelperJSON(t, handler, "/api/apply", helper.state, foreign, http.StatusBadRequest)
}

func TestHelperServerSelectsSiteAtRuntime(t *testing.T) {
	helper, err := newHelperServer(NewConfigManager(t.TempDir()), "", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	handler := helper.routes()

	before := getJSON(t, handler, "/api/status")
	if before["connect_url"] != "" || before["site_url"] != "" {
		t.Fatalf("unconfigured status = %+v", before)
	}

	selected := postHelperJSON(
		t,
		handler,
		"/api/site",
		helper.state,
		[]byte(`{"site_url":"https://gateway.example.com/"}`),
		http.StatusOK,
	)
	connectURL, _ := selected["connect_url"].(string)
	if !strings.HasPrefix(connectURL, "https://gateway.example.com/codex-helper/connect?") {
		t.Fatalf("runtime connect_url = %q", connectURL)
	}
	if selected["site_url"] != "https://gateway.example.com" {
		t.Fatalf("runtime site_url = %v", selected["site_url"])
	}
}

func TestHelperServerSelectsCodexAppAtRuntime(t *testing.T) {
	helper, err := newHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation { return CodexInstallation{} }
	helper.selectApp = func() (CodexInstallation, error) {
		return CodexInstallation{
			AppPath:    `C:\Program Files\Codex`,
			Executable: `C:\Program Files\Codex\Codex.exe`,
			Found:      true,
		}, nil
	}
	handler := helper.routes()

	selected := postHelperJSON(t, handler, "/api/select-app", helper.state, []byte(`{}`), http.StatusOK)
	if ok, _ := selected["ok"].(bool); !ok {
		t.Fatalf("select app response = %+v", selected)
	}
	status := getJSON(t, handler, "/api/status")
	codex, _ := status["codex"].(map[string]any)
	if found, _ := codex["found"].(bool); !found {
		t.Fatalf("selected Codex app was not retained: %+v", status)
	}
	if codex["executable"] != `C:\Program Files\Codex\Codex.exe` {
		t.Fatalf("selected Codex executable = %v", codex["executable"])
	}
}

func TestHelperIndexRendersUsableSessionState(t *testing.T) {
	const state = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1"
	helper, err := newHelperServer(NewConfigManager(t.TempDir()), "", state)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Host = "127.0.0.1:43123"
	response := httptest.NewRecorder()
	helper.routes().ServeHTTP(response, request)
	body := response.Body.String()
	if !strings.Contains(body, `name="xiass-helper-state" content="`+state+`"`) {
		t.Fatal("helper session state was not rendered as a plain meta attribute")
	}
	if strings.Contains(body, `content="&`) || strings.Contains(body, `content="\&quot;`) {
		t.Fatal("helper session state contains an extra escaped quote layer")
	}
	if !strings.Contains(body, `value="`+defaultXIASSAPIURL+`"`) {
		t.Fatal("helper index does not render the default XIASS API URL")
	}
}

func TestHelperManualHistoryRepairStopsRepairsAndStarts(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	createHistoryDatabase(t, filepath.Join(home, "state_5.sqlite"), map[string]string{"thread-a": "xiass"})
	helper, err := newHelperServer(NewConfigManager(home), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stopped, started atomic.Int32
	helper.stop = func(CodexInstallation) error { stopped.Add(1); return nil }
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }

	response := postHelperJSON(t, helper.routes(), "/api/repair-history", helper.state, []byte(`{}`), http.StatusOK)
	if ok, _ := response["ok"].(bool); !ok {
		t.Fatalf("repair response = %+v", response)
	}
	if stopped.Load() != 1 || started.Load() != 1 {
		t.Fatalf("lifecycle counts = stop %d, start %d", stopped.Load(), started.Load())
	}
	assertHistoryRolloutProvider(t, session, "codex_local_access")
	assertHistoryDatabase(t, filepath.Join(home, "state_5.sqlite"), 1, "codex_local_access")
}

func TestHelperApplyRollsBackConfigWhenHistoryValidationFails(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "sqlite"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "sqlite", "state_5.sqlite"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := newHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stopped, started atomic.Int32
	helper.stop = func(CodexInstallation) error { stopped.Add(1); return nil }
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }

	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	response := postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)
	if ok, _ := response["ok"].(bool); ok {
		t.Fatalf("apply unexpectedly succeeded: %+v", response)
	}
	if stopped.Load() != 1 || started.Load() != 1 {
		t.Fatalf("lifecycle counts = stop %d, start %d", stopped.Load(), started.Load())
	}
	restored, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != testOriginalConfig {
		t.Fatal("configuration was not rolled back after history validation failed")
	}
}

func TestHelperRestoreMissingOriginalConfigAlignsConversationsWithOfficialProvider(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "codex_local_access", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "codex_local_access"})
	helper, err := newHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation { return CodexInstallation{Found: true, AppPath: "/test/Codex.app"} }
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	handler := helper.routes()

	apply := postHelperJSON(t, handler, "/api/apply", helper.state, []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`), http.StatusOK)
	backupID, _ := apply["backup_id"].(string)
	if backupID == "" {
		t.Fatal("apply did not return a backup ID")
	}
	restoreBody, _ := json.Marshal(map[string]string{"backup_id": backupID})
	postHelperJSON(t, handler, "/api/restore", helper.state, restoreBody, http.StatusOK)
	if _, err := os.Stat(manager.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("config.toml still exists after restoring a missing original: %v", err)
	}
	assertHistoryRolloutProvider(t, session, "openai")
	assertHistoryDatabase(t, databasePath, 1, "openai")
}

func TestHelperRestoreLegacyXIASSBackupUpgradesConfigAndHistoryForward(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	legacyConfig := `model_provider = "xiass"

[model_providers.xiass]
name = "XIASS API"
base_url = "https://gateway.example.com"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-test-1234567890"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "xiass"})
	apply, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-new-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	helper, err := newHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation { return CodexInstallation{Found: true, AppPath: "/test/Codex.app"} }
	helper.stop = func(CodexInstallation) error { return nil }
	helper.start = func(CodexInstallation) error { return nil }
	body, _ := json.Marshal(map[string]string{"backup_id": apply.BackupID})
	postHelperJSON(t, helper.routes(), "/api/restore", helper.state, body, http.StatusOK)
	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), `model_provider = "xiass"`) || !strings.Contains(string(config), `model_provider = "codex_local_access"`) {
		t.Fatal("restored legacy configuration was not upgraded forward")
	}
	assertHistoryRolloutProvider(t, session, "codex_local_access")
	assertHistoryDatabase(t, databasePath, 1, "codex_local_access")
}

func TestHelperRejectsConcurrentLifecycleOperationBeforeStoppingCodex(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	helper, err := newHelperServer(NewConfigManager(home), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stopped atomic.Int32
	helper.stop = func(CodexInstallation) error { stopped.Add(1); return nil }
	helper.start = func(CodexInstallation) error { return nil }
	release, err := acquireLifecycleLock(home)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	postHelperJSON(t, helper.routes(), "/api/repair-history", helper.state, []byte(`{}`), http.StatusConflict)
	if stopped.Load() != 0 {
		t.Fatal("Codex was stopped even though another helper held the lifecycle lock")
	}
}

func TestHelperStopFailureChangesNothing(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "official", "thread-a")
	beforeSession, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	helper, err := newHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return errors.New("cannot verify process state") }
	var started atomic.Int32
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }
	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)

	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != testOriginalConfig {
		t.Fatal("config changed after stop failure")
	}
	afterSession, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeSession, afterSession) {
		t.Fatal("session changed after stop failure")
	}
	backups, err := manager.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 || started.Load() != 0 {
		t.Fatalf("stop failure created backups or started Codex: backups=%d starts=%d", len(backups), started.Load())
	}
}

func TestHelperRetriesCodexLaunchAfterTransientFailure(t *testing.T) {
	helper, err := newHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	installation := CodexInstallation{Found: true, AppPath: "/test/Codex.app"}
	helper.detect = func() CodexInstallation { return installation }
	var attempts atomic.Int32
	helper.start = func(CodexInstallation) error {
		if attempts.Add(1) == 1 {
			return errors.New("transient launch failure")
		}
		return nil
	}
	if err := helper.startWithRetry(installation); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("launch attempts = %d, want 2", attempts.Load())
	}
}

func TestHelperDoesNotStartCodexAfterHistoryRollbackFailure(t *testing.T) {
	home := t.TempDir()
	writeHistoryConfig(t, home, "codex_local_access")
	helper, err := newHelperServer(NewConfigManager(home), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.repairHistory = func() (HistoryRepairResult, error) {
		return HistoryRepairResult{}, &HistoryRepairApplyError{
			Cause:       errors.New("forced repair failure"),
			RollbackErr: errors.New("forced rollback failure"),
		}
	}
	var started atomic.Int32
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }
	response := postHelperJSON(t, helper.routes(), "/api/repair-history", helper.state, []byte(`{}`), http.StatusInternalServerError)
	if started.Load() != 0 {
		t.Fatal("Codex was started after history rollback failed")
	}
	message, _ := response["message"].(string)
	if !strings.Contains(message, "保持关闭") {
		t.Fatalf("unsafe rollback response is unclear: %q", message)
	}
}

func TestHelperDoesNotStartCodexAfterApplyConfigRollbackFailure(t *testing.T) {
	helper, err := newHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.applyConfig = func(ApplyConfig) (ApplyResult, error) {
		return ApplyResult{}, &ConfigMutationError{
			Cause:       errors.New("forced config failure"),
			RollbackErr: errors.New("forced rollback failure"),
		}
	}
	var started atomic.Int32
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }
	body, err := json.Marshal(map[string]string{
		"base_url": defaultXIASSAPIURL,
		"api_key":  "sk-test-1234567890",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)
	if started.Load() != 0 {
		t.Fatal("Codex was started after apply configuration rollback failed")
	}
	if message, _ := response["message"].(string); !strings.Contains(message, "保持关闭") {
		t.Fatalf("unsafe apply rollback response is unclear: %q", message)
	}
}

func TestHelperDoesNotStartCodexAfterRestoreConfigRollbackFailure(t *testing.T) {
	helper, err := newHelperServer(NewConfigManager(t.TempDir()), defaultXIASSAPIURL, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	helper.restoreConfig = func(string) (RestoreResult, error) {
		return RestoreResult{}, &ConfigMutationError{
			Cause:       errors.New("forced restore failure"),
			RollbackErr: errors.New("forced rollback failure"),
		}
	}
	var started atomic.Int32
	helper.start = func(CodexInstallation) error { started.Add(1); return nil }
	response := postHelperJSON(t, helper.routes(), "/api/restore", helper.state, []byte(`{"backup_id":"test-backup"}`), http.StatusInternalServerError)
	if started.Load() != 0 {
		t.Fatal("Codex was started after restore configuration rollback failed")
	}
	if message, _ := response["message"].(string); !strings.Contains(message, "保持关闭") {
		t.Fatalf("unsafe restore rollback response is unclear: %q", message)
	}
}

func TestLocalHTTPServerAllowsLongLifecycleOperations(t *testing.T) {
	server := newLocalHTTPServer(http.NotFoundHandler())
	if server.WriteTimeout < 2*time.Minute {
		t.Fatalf("WriteTimeout = %v, want at least 2 minutes", server.WriteTimeout)
	}
}

func TestHelperApplyRestoresOriginalStateWhenNewStateCannotStart(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	helper, err := newHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	var starts atomic.Int32
	helper.start = func(CodexInstallation) error {
		if starts.Add(1) <= 2 {
			return errors.New("new state cannot start")
		}
		return nil
	}
	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-test-1234567890","key_name":"Codex"}`)
	response := postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)
	if restarted, _ := response["restarted"].(bool); !restarted {
		t.Fatalf("original safe state was not restarted: %+v", response)
	}
	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != testOriginalConfig {
		t.Fatal("original config was not restored after two launch failures")
	}
}

func TestHelperRestoreReturnsToPreRestoreStateWhenSelectedStateCannotStart(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	if err := os.WriteFile(manager.ConfigPath, []byte(testOriginalConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	apply, err := manager.Apply(ApplyConfig{BaseURL: "https://gateway.example.com", APIKey: "sk-current-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	preRestore, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	helper, err := newHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	var starts atomic.Int32
	helper.start = func(CodexInstallation) error {
		if starts.Add(1) <= 2 {
			return errors.New("selected restore state cannot start")
		}
		return nil
	}
	body, _ := json.Marshal(map[string]string{"backup_id": apply.BackupID})
	response := postHelperJSON(t, helper.routes(), "/api/restore", helper.state, body, http.StatusInternalServerError)
	if restarted, _ := response["restarted"].(bool); !restarted {
		t.Fatalf("pre-restore safe state was not restarted: %+v", response)
	}
	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(config, preRestore) {
		t.Fatal("pre-restore config was not restored after two launch failures")
	}
}

func TestHelperApplyLaunchFailureRestoresLegacyHistorySnapshot(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	legacyConfig := `model_provider = "xiass"

[model_providers.xiass]
name = "XIASS API"
base_url = "https://gateway.example.com"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-old-1234567890"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "xiass"})
	helper, err := newHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	helper.stop = func(CodexInstallation) error { return nil }
	var starts atomic.Int32
	helper.start = func(CodexInstallation) error {
		if starts.Add(1) <= 2 {
			return errors.New("new state cannot start")
		}
		return nil
	}
	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-new-1234567890","key_name":"Codex"}`)
	postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)
	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != legacyConfig {
		t.Fatal("legacy config was not restored after launch failure")
	}
	assertHistoryRolloutProvider(t, session, "xiass")
	assertHistoryDatabase(t, databasePath, 1, "xiass")
}

func TestHelperNeverRollsBackWhileCodexExitCannotBeConfirmed(t *testing.T) {
	home := t.TempDir()
	manager := NewConfigManager(home)
	legacyConfig := `model_provider = "xiass"

[model_providers.xiass]
name = "XIASS API"
base_url = "https://gateway.example.com"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "sk-old-1234567890"
`
	if err := os.WriteFile(manager.ConfigPath, []byte(legacyConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	session := writeHistoryRollout(t, home, "sessions/rollout-a.jsonl", "xiass", "thread-a")
	databasePath := filepath.Join(home, "state_5.sqlite")
	createHistoryDatabase(t, databasePath, map[string]string{"thread-a": "xiass"})
	helper, err := newHelperServer(manager, "https://gateway.example.com", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNO1")
	if err != nil {
		t.Fatal(err)
	}
	helper.detect = func() CodexInstallation {
		return CodexInstallation{Found: true, Running: true, AppPath: "/test/Codex.app"}
	}
	var stops atomic.Int32
	helper.stop = func(CodexInstallation) error {
		if stops.Add(1) == 1 {
			return nil
		}
		return errors.New("cannot confirm Codex exited")
	}
	helper.start = func(CodexInstallation) error { return errors.New("launch detection failed") }
	body := []byte(`{"base_url":"https://gateway.example.com","api_key":"sk-new-1234567890","key_name":"Codex"}`)
	response := postHelperJSON(t, helper.routes(), "/api/apply", helper.state, body, http.StatusInternalServerError)
	message, _ := response["message"].(string)
	if !strings.Contains(message, "未执行回滚") {
		t.Fatalf("unsafe rollback refusal is unclear: %q", message)
	}
	config, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `model_provider = "codex_local_access"`) {
		t.Fatal("new aligned config was unexpectedly rolled back while Codex might be running")
	}
	assertHistoryRolloutProvider(t, session, "codex_local_access")
	assertHistoryDatabase(t, databasePath, 1, "codex_local_access")
}

func getJSON(t *testing.T, handler http.Handler, target string) map[string]any {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Host = "127.0.0.1:43123"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func postHelperJSON(t *testing.T, handler http.Handler, target, state string, body []byte, wantStatus int) map[string]any {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	request.Host = "127.0.0.1:43123"
	request.Header.Set("Content-Type", "application/json")
	if state != "" {
		request.Header.Set("X-XIASS-Helper-State", state)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d, payload = %+v", response.Code, wantStatus, payload)
	}
	return payload
}
