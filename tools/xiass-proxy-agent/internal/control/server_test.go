package control

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestControlAPIIsAuthenticatedAndRedactsRouteData(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	server, err := NewServer("test-token", manager)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	request, _ := http.NewRequest(http.MethodGet, testServer.URL+"/health", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", response.StatusCode)
	}
	response.Body.Close()

	payload := `{"input":"socks5://alice:super-secret@127.0.0.1:1080"}`
	request, _ = http.NewRequest(http.MethodPost, testServer.URL+"/v1/routes", strings.NewReader(payload))
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.StatusCode, body)
	}
	if strings.Contains(string(body), "super-secret") || strings.Contains(string(body), "alice") || strings.Contains(string(body), "127.0.0.1:1080") {
		t.Fatalf("route response leaked credentials or upstream endpoint: %s", body)
	}
	var route RouteInfo
	if err := json.Unmarshal(body, &route); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(route.SOCKS5, "127.0.0.1:") {
		t.Fatalf("unexpected local listener: %q", route.SOCKS5)
	}

	request, _ = http.NewRequest(http.MethodDelete, testServer.URL+"/v1/routes/"+route.ID, nil)
	request.Header.Set("X-XIASS-Proxy-Agent-Token", "test-token")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d", response.StatusCode)
	}
}

func TestSubscriptionPreviewAndSyncContract(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	server, err := NewServer("test-token", manager)
	if err != nil {
		t.Fatal(err)
	}
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	previewPayload := map[string]any{"sources": []map[string]any{{
		"id": "inline", "name": "Inline", "input": "http://user:fixture-secret@127.0.0.1:18101",
	}}}
	previewBody, _ := json.Marshal(previewPayload)
	responseBody, status := authenticatedJSON(t, testServer.URL+"/v1/subscriptions/preview", previewBody)
	if status != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", status, responseBody)
	}
	if bytes.Contains(responseBody, []byte("fixture-secret")) || bytes.Contains(responseBody, []byte("user")) {
		t.Fatalf("preview leaked node credentials: %s", responseBody)
	}
	var preview SubscriptionResult
	if err := json.Unmarshal(responseBody, &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Nodes) != 1 || preview.Snapshot == "" {
		t.Fatalf("unexpected preview response: %+v", preview)
	}

	address := freeAddress(t)
	syncPayload := map[string]any{
		"snapshot": preview.Snapshot, "selected_node_ids": []string{preview.Nodes[0].ID},
		"listen_addresses": map[string]string{preview.Nodes[0].ID: address}, "prune": false,
	}
	syncBody, _ := json.Marshal(syncPayload)
	responseBody, status = authenticatedJSON(t, testServer.URL+"/v1/subscriptions/sync", syncBody)
	if status != http.StatusOK {
		t.Fatalf("sync status=%d body=%s", status, responseBody)
	}
	var synced SubscriptionResult
	if err := json.Unmarshal(responseBody, &synced); err != nil {
		t.Fatal(err)
	}
	if len(synced.Nodes) != 1 || len(synced.Routes) != 1 || synced.Routes[0].SOCKS5 != address || synced.Routes[0].StableID != preview.Nodes[0].ID {
		t.Fatalf("unexpected sync response: %+v", synced)
	}
}

func authenticatedJSON(t *testing.T, endpoint string, body []byte) ([]byte, int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return responseBody, response.StatusCode
}
