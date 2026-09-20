package control

import (
	"context"
	"testing"

	"github.com/xyf0104/xiass-proxy-agent/internal/proxyroute"
)

func TestSnapshotRetainsExplicitTLSConsent(t *testing.T) {
	nodes, err := proxyroute.LoadSources(context.Background(), []proxyroute.Source{{ID: "tls", Name: "Fixture", AllowInsecureTLS: true, Input: "proxies:\n  - name: fixture\n    type: http\n    server: 127.0.0.1\n    port: 18443\n    tls: true\n    skip-cert-verify: true\n"}})
	if err != nil {
		t.Fatal(err)
	}
	defer proxyroute.CloseCandidates(nodes)
	snapshot, err := encodeSnapshot(nodes)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeAndBuildSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer proxyroute.CloseCandidates(restored)
	if len(restored) != 1 || !restored[0].AllowInsecureTLS || restored[0].Node["skip-cert-verify"] != true {
		t.Fatal("TLS setting lost after restart")
	}
	nodes[0].AllowInsecureTLS = false
	snapshot, err = encodeSnapshot(nodes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeAndBuildSnapshot(snapshot); err == nil {
		t.Fatal("snapshot restored insecure node without consent")
	}
}
