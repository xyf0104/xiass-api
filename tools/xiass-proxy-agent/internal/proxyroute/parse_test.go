package proxyroute

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseClashURIAndBase64(t *testing.T) {
	input := "socks5://user:secret@127.0.0.1:1080#private\nhttp://127.0.0.1:8080"
	yaml := "proxies:\n  - name: private-name\n    type: socks5\n    server: 127.0.0.1\n    port: 1080\n  - name: second\n    type: http\n    server: 127.0.0.1\n    port: 8080\nrules: ['MATCH,DIRECT']\n"
	for _, source := range []string{input, base64.StdEncoding.EncodeToString([]byte(input)), yaml} {
		nodes, err := Parse([]byte(source))
		if err != nil || len(nodes) != 2 {
			t.Fatalf("nodes=%d err=%v", len(nodes), err)
		}
	}
}

func TestParseRejectsUnsafeOrUnsupportedInput(t *testing.T) {
	for _, input := range []string{
		"",
		"unknown://secret@example.invalid:443",
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("accepted invalid input %q", input)
		}
	}
	for _, input := range []string{
		"proxies:\n  - type: wireguard\n    server: example.invalid\n    port: 443\n",
		"proxies:\n  - type: socks5\n    server: example.invalid\n    port: 443\n    skip-cert-verify: true\n",
	} {
		nodes, err := Parse([]byte(input))
		if err == nil {
			_, err = Summaries(nodes)
		}
		if err == nil {
			t.Fatalf("accepted invalid input %q", input)
		}
	}
	if _, err := Parse([]byte(strings.Repeat("a", MaxSubscriptionBytes+1))); err == nil {
		t.Fatal("accepted oversized input")
	}
}

func TestSummariesNeverExposeCredentialsOrEndpoints(t *testing.T) {
	nodes, err := Parse([]byte("socks5://alice:super-secret@proxy.example:1080"))
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := Summaries(nodes)
	if err != nil {
		t.Fatal(err)
	}
	summary := summaries[0]
	text := summary.Name + summary.Protocol
	if strings.Contains(text, "super-secret") || strings.Contains(text, "proxy.example") || strings.Contains(text, "alice") {
		t.Fatalf("summary leaked sensitive data: %q", text)
	}
}

func TestLoopbackAddressPolicy(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "[::1]:1234"} {
		if !IsLoopbackAddress(address) {
			t.Errorf("expected loopback: %s", address)
		}
	}
	for _, address := range []string{"0.0.0.0:1234", ":1234", "192.0.2.1:1234"} {
		if IsLoopbackAddress(address) {
			t.Errorf("accepted non-loopback: %s", address)
		}
	}
}
