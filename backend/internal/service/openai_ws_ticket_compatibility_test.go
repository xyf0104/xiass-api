package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIWSManagedTicketRefreshChangesHandshakeCompatibility(t *testing.T) {
	account := ticketTestAccount(41)
	old := openAIWSAcquireRequest{Account: account, Owner: testOpenAIWSOwnership(), WSURL: "wss://example.com/v1/responses", Headers: http.Header{}}
	old.Headers.Set(openAIWSTurnStateHeader, "synthetic-old-account-ticket")
	next := old
	next.Headers = old.Headers.Clone()
	next.Headers.Set(openAIWSTurnStateHeader, "synthetic-new-account-ticket")
	require.NotEqual(t, normalizeOpenAIWSAcquireCompatibility(old), normalizeOpenAIWSAcquireCompatibility(next))
	require.False(t, sameOpenAIWSPrewarmTarget(old, next), "prewarming must not use a retired ticket snapshot")
	require.NotContains(t, fmt.Sprint(normalizeOpenAIWSAcquireCompatibility(next)), "synthetic-new-account-ticket")
	conn := &openAIWSConn{handshakeCompatibility: normalizeOpenAIWSAcquireCompatibility(old)}
	require.False(t, conn.matchesAcquireCompatibility(normalizeOpenAIWSAcquireCompatibility(next), false))
	require.True(t, conn.matchesAcquireCompatibility(normalizeOpenAIWSAcquireCompatibility(next), true), "an owner-verified response-ID continuation must preserve its existing handshake")
	next.Owner.upstream = "different-upstream"
	require.False(t, conn.matchesAcquireCompatibility(normalizeOpenAIWSAcquireCompatibility(next), true))
}

func TestOpenAIWSUnmanagedTurnStatePreservesExistingReuse(t *testing.T) {
	account := ticketTestAccount(42)
	account.Extra[OpenAICodexTicketEnabledExtraKey] = false
	a := normalizeOpenAIWSHandshakeCompatibility(account, http.Header{"X-Codex-Turn-State": {"client-a"}})
	b := normalizeOpenAIWSHandshakeCompatibility(account, http.Header{"X-Codex-Turn-State": {"client-b"}})
	require.Equal(t, a, b)
	account.Extra[OpenAICodexTicketEnabledExtraKey] = true
	managed := normalizeOpenAIWSHandshakeCompatibility(account, http.Header{"X-Codex-Turn-State": {"client-a"}})
	require.NotEqual(t, a, managed)
	require.Equal(t, managed, normalizeOpenAIWSHandshakeCompatibility(account, http.Header{"x-codex-turn-state": {"client-a"}}))
}

func TestOpenAIWSManagedTicketPoolDialsNewConnectionAfterRefresh(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	pool := newOpenAIWSConnPool(cfg)
	defer pool.Close()
	dialer := &openAIWSFakeDialer{}
	pool.setClientDialerForTest(dialer)
	req := openAIWSAcquireRequest{Account: ticketTestAccount(43), Owner: testOpenAIWSOwnership(), WSURL: "wss://example.com/v1/responses", Headers: http.Header{}}
	req.Account.Concurrency = 1
	req.Headers.Set(openAIWSTurnStateHeader, "synthetic-old-ticket")
	first, err := pool.Acquire(context.Background(), req)
	require.NoError(t, err)
	firstID := first.ConnID()
	first.Release()
	req.Headers = req.Headers.Clone()
	req.Headers.Set(openAIWSTurnStateHeader, "synthetic-refreshed-ticket")
	next, err := pool.Acquire(context.Background(), req)
	require.NoError(t, err)
	defer next.Release()
	require.NotEqual(t, firstID, next.ConnID())
	require.False(t, next.Reused())
}
