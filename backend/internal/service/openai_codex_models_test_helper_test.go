package service

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newCodexModelsOAuthCacheServer(t *testing.T, body string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	original := chatgptCodexModelsURL
	chatgptCodexModelsURL = server.URL
	t.Cleanup(func() { chatgptCodexModelsURL = original })
	return server, calls
}

func expireCodexModelsManifestCache(s *OpenAIGatewayService, age time.Duration) {
	s.openAIModelsCache.mu.Lock()
	for key, entry := range s.openAIModelsCache.entries {
		entry.expiresAt = time.Now().Add(-age)
		entry.staleUntil = time.Now().Add(openAIModelsCacheStaleTTL - age)
		s.openAIModelsCache.entries[key] = entry
	}
	s.openAIModelsCache.mu.Unlock()
}
