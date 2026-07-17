package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
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
	var restartCount atomic.Int32
	helper.restart = func(CodexInstallation) error {
		restartCount.Add(1)
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
	if restartCount.Load() != 1 {
		t.Fatalf("restart count after apply = %d", restartCount.Load())
	}

	written, err := os.ReadFile(manager.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), `[model_providers.xiass]`) {
		t.Fatal("apply endpoint did not write XIASS provider")
	}

	restoreBody, _ := json.Marshal(map[string]string{"backup_id": backupID})
	restore := postHelperJSON(t, handler, "/api/restore", helper.state, restoreBody, http.StatusOK)
	if ok, _ := restore["ok"].(bool); !ok {
		t.Fatalf("restore response = %+v", restore)
	}
	if restartCount.Load() != 2 {
		t.Fatalf("restart count after restore = %d", restartCount.Load())
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
	helper.restart = func(CodexInstallation) error { return nil }
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
