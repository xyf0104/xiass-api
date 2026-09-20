package turnstate

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func syntheticTokenValue(blocks int, issued time.Time) string {
	raw := make([]byte, 57+16*blocks)
	raw[0] = 0x80
	binary.BigEndian.PutUint64(raw[1:9], uint64(issued.Unix()))
	for i := 9; i < len(raw); i++ {
		raw[i] = byte(blocks + i)
	}
	return base64.URLEncoding.EncodeToString(raw)
}

func TestParseRecognizesExactEnvelopeShape(t *testing.T) {
	issued := time.Date(2026, time.September, 20, 8, 30, 0, 0, time.UTC)
	for _, blocks := range []int{10, 11, 12, 13} {
		value := syntheticTokenValue(blocks, issued)
		token, err := Parse(value)
		if err != nil {
			t.Fatalf("Parse(%d blocks): %v", blocks, err)
		}
		if token.Value != value || token.Blocks != blocks || !token.Issued.Equal(issued) {
			t.Fatalf("Parse(%d blocks) = %#v", blocks, token)
		}
		if len(token.Fingerprint) != 16 {
			t.Fatalf("fingerprint length = %d, want 16", len(token.Fingerprint))
		}
	}
}

func TestPolicyAcceptsOnlyConfiguredBlocksAndIssuedTTL(t *testing.T) {
	issued := time.Date(2026, time.September, 20, 8, 30, 0, 0, time.UTC)
	personal, err := Parse(syntheticTokenValue(10, issued))
	if err != nil {
		t.Fatal(err)
	}
	team, err := Parse(syntheticTokenValue(12, issued))
	if err != nil {
		t.Fatal(err)
	}
	personalPolicy := Policy{Blocks: 10, TTL: time.Hour}
	teamPolicy := Policy{Blocks: 12, TTL: time.Hour}
	now := issued.Add(15 * time.Minute)
	if !personalPolicy.Accept(personal, now) || personalPolicy.Accept(team, now) {
		t.Fatal("personal policy did not enforce exact 10-block shape")
	}
	if !teamPolicy.Accept(team, now) || teamPolicy.Accept(personal, now) {
		t.Fatal("team policy did not enforce exact 12-block shape")
	}
	if personalPolicy.Accept(personal, issued.Add(time.Hour-30*time.Second)) {
		t.Fatal("policy accepted token at conservative expiry boundary")
	}
	future, err := Parse(syntheticTokenValue(10, now.Add(31*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if personalPolicy.Accept(future, now) {
		t.Fatal("policy accepted token issued more than 30 seconds in the future")
	}
}

func TestParseRejectsMalformedEnvelope(t *testing.T) {
	issued := time.Date(2026, time.September, 20, 8, 30, 0, 0, time.UTC)
	valid := syntheticTokenValue(10, issued)
	cases := []string{
		"",
		valid + "=",
		valid + " ===",
		strings.Repeat("A", 292),
	}
	for _, value := range cases {
		if _, err := Parse(value); err == nil {
			t.Fatalf("Parse(%q) unexpectedly succeeded", value)
		}
	}
}
