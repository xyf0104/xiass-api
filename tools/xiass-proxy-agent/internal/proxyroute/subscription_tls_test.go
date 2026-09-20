package proxyroute

import (
	"context"
	"strings"
	"testing"
)

const tlsSubscriptionFixture = "proxies:\n  - name: fixture\n    type: http\n    server: 127.0.0.1\n    port: 18443\n    tls: true\n    skip-cert-verify: true\n"

func TestSubscriptionTLSRequiresPerSourceConsent(t *testing.T) {
	source := Source{ID: "fixture", Input: tlsSubscriptionFixture}
	if _, err := LoadSources(context.Background(), []Source{source}); err == nil || !strings.Contains(err.Error(), "insecure TLS") {
		t.Fatalf("expected default TLS refusal, got %v", err)
	}
	source.AllowInsecureTLS = true
	nodes, err := LoadSources(context.Background(), []Source{source})
	if err != nil {
		t.Fatal(err)
	}
	defer CloseCandidates(nodes)
	if len(nodes) != 1 || !nodes[0].AllowInsecureTLS || nodes[0].Node["skip-cert-verify"] != true {
		t.Fatal("source consent or original node option lost")
	}
	if _, err := BuildCandidate(nodes[0].Node, 0, "fixture", "Fixture"); err == nil {
		t.Fatal("ordinary build implicitly accepted insecure TLS")
	}
	other := Source{ID: "other", Input: strings.ReplaceAll(tlsSubscriptionFixture, "18443", "18444")}
	if _, err := LoadSources(context.Background(), []Source{source, other}); err == nil {
		t.Fatal("one source consent leaked into another")
	}
	for _, extra := range []string{"    ca: /etc/passwd\n", "    dialer-proxy: other\n", "    nested:\n      private-key: /tmp/key\n"} {
		source.Input = tlsSubscriptionFixture + extra
		if _, err := LoadSources(context.Background(), []Source{source}); err == nil {
			t.Fatal("TLS consent allowed forbidden file/routing field")
		}
	}
	source.AllowInsecureTLS = false
	source.Input = tlsSubscriptionFixture + "    allow_insecure_tls: true\n"
	if _, err := LoadSources(context.Background(), []Source{source}); err == nil {
		t.Fatal("untrusted node field granted TLS consent")
	}
}
