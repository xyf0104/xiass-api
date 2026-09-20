package proxyroute

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadSourcesUsesUpstreamDedupAndFirstAttribution(t *testing.T) {
	input := "http://127.0.0.1:18001"
	candidates, err := LoadSources(context.Background(), []Source{
		{ID: "first", Name: "First", Input: input},
		{ID: "second", Name: "Second", Input: input},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer CloseCandidates(candidates)
	if len(candidates) != 1 {
		t.Fatalf("candidates=%d", len(candidates))
	}
	if candidates[0].SourceID != "first" || candidates[0].SourceName != "First" {
		t.Fatalf("unexpected attribution: %+v", candidates[0])
	}
	if candidates[0].ID == "" || candidates[0].ID != candidates[0].Outbound.StableID {
		t.Fatal("upstream stable identity was not retained")
	}
}

func TestLoadSourcesExpandsURLsAndPassesUserAgentAndFilters(t *testing.T) {
	var userAgents []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgents = append(userAgents, r.UserAgent())
		_, _ = w.Write([]byte("proxies:\n  - name: keep\n    type: http\n    server: 127.0.0.1\n    port: 18001\n  - name: drop\n    type: http\n    server: 127.0.0.1\n    port: 18002\n"))
	}))
	defer server.Close()

	candidates, err := LoadSources(context.Background(), []Source{{
		ID: "remote", URL: server.URL + "\n" + server.URL, UserAgent: "XIASS-Test/1", ExcludeKeywords: []string{"drop"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer CloseCandidates(candidates)
	if len(candidates) != 1 || candidates[0].Name != "keep" {
		t.Fatalf("unexpected filtered candidates: %+v", candidates)
	}
	if len(userAgents) != 2 || userAgents[0] != "XIASS-Test/1" || userAgents[1] != "XIASS-Test/1" {
		t.Fatalf("user agents=%v", userAgents)
	}
}

func TestLoadSourcesLimitsAndInputExclusivity(t *testing.T) {
	sources := make([]Source, MaxSubscriptionSources+1)
	for index := range sources {
		sources[index] = Source{ID: strings.Repeat("a", index%3+1), Input: "http://127.0.0.1:18001"}
	}
	if _, err := LoadSources(context.Background(), sources); err == nil {
		t.Fatal("accepted too many sources")
	}
	if _, err := LoadSources(context.Background(), []Source{{
		ID: "mixed", URL: "https://example.invalid/sub", Input: "http://127.0.0.1:18001",
	}}); err == nil {
		t.Fatal("accepted combined url and input")
	}
}
