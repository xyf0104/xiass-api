package control

import (
	"github.com/xyf0104/xiass-proxy-agent/internal/traffic"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestTrafficAPIAuthValidationAndCounts(t *testing.T) {
	m := NewManager()
	defer m.Close()
	store, err := traffic.Open(filepath.Join(t.TempDir(), "traffic.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.Traffic = store
	store.Register("jp", "Japan", "hysteria2")
	store.Add("jp", 100, 250)
	s, err := NewServer("test-token", m)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		path, method, token string
		status              int
	}{
		{"/v1/traffic", "GET", "", 401},
		{"/v1/traffic?days=30", "GET", "test-token", 200},
		{"/v1/traffic?days=31", "GET", "test-token", 400},
		{"/v1/traffic", "POST", "test-token", 405},
	} {
		r := httptest.NewRequest(tt.method, tt.path, nil)
		r.Header.Set("Authorization", "Bearer "+tt.token)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Fatalf("%s: %d", tt.path, w.Code)
		}
		if w.Code == http.StatusOK && !strings.Contains(w.Body.String(), `"total_bytes":350`) {
			t.Fatalf("wrong total: %s", w.Body)
		}
	}
}
