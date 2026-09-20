package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func fakeCodexTicketState(n int) string {
	for blocks := 1; blocks <= 64; blocks++ {
		if base64.URLEncoding.EncodedLen(57+16*blocks) == n {
			return syntheticCodexTicketState(blocks, time.Now(), byte(blocks))
		}
	}
	if n < 6 {
		return strings.Repeat("A", n)
	}
	return "gAAAAA" + strings.Repeat("B", n-6)
}

func syntheticCodexTicketState(blocks int, issued time.Time, fill byte) string {
	raw := make([]byte, 57+16*blocks)
	raw[0] = 0x80
	binary.BigEndian.PutUint64(raw[1:9], uint64(issued.Unix()))
	for i := 9; i < len(raw); i++ {
		raw[i] = fill
	}
	return base64.URLEncoding.EncodeToString(raw)
}

func completedCodexTicketStream(model string) string {
	return "data: {\"type\":\"response.created\",\"response\":{\"model\":" + jsonString(model) + "}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"pong\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"model\":" + jsonString(model) + "}}\n\n"
}

func ticketTestAccount(id int64) *Account {
	return &Account{
		ID:          id,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "tok", "chatgpt_account_id": "acc-1"},
		Extra:       map[string]any{OpenAICodexTicketEnabledExtraKey: true},
	}
}

func TestOpenAICodexTicketIsDisabledUntilAccountExplicitlyOptsIn(t *testing.T) {
	account := ticketTestAccount(40)
	delete(account.Extra, OpenAICodexTicketEnabledExtraKey)
	require.False(t, OpenAICodexTicketEnabledForAccount(account))

	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled: true, FailClosed: true, Models: []string{"gpt-6-astra"},
	}, nil)
	headers := http.Header{}
	headers.Set(openAICodexTurnStateHeader, "client-state")
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", headers))
	require.Equal(t, "client-state", headers.Get(openAICodexTurnStateHeader))
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	require.False(t, OpenAICodexTicketStatuses(account, svc.openAICodexTicketConfig(), time.Now())[0].Blocked)

	account.Extra[OpenAICodexTicketEnabledExtraKey] = "true"
	require.False(t, OpenAICodexTicketEnabledForAccount(account), "only a dedicated boolean opt-in is accepted")
	account.Extra[OpenAICodexTicketEnabledExtraKey] = true
	require.True(t, OpenAICodexTicketEnabledForAccount(account))
}

func TestRefreshOpenAICodexTicketRejectsDisabledAccount(t *testing.T) {
	account := ticketTestAccount(43)
	delete(account.Extra, OpenAICodexTicketEnabledExtraKey)
	upstream := &httpUpstreamRecorder{}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true}, upstream)
	svc.accountRepo = &codexTicketRefreshRepo{accounts: []Account{*account}}

	_, err := svc.RefreshOpenAICodexTicket(context.Background(), account.ID, "gpt-6-astra")
	require.ErrorIs(t, err, ErrOpenAICodexTicketAccountDisabled)
	require.Empty(t, upstream.requests)
}

func TestOpenAICodexTicketRefreshIgnoresTypedNilRepository(t *testing.T) {
	var repo *codexTicketRefreshRepo
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true}, nil)
	svc.accountRepo = repo

	require.NotPanics(t, func() {
		svc.refreshOpenAICodexTickets(context.Background())
	})
	_, err := svc.RefreshOpenAICodexTicket(context.Background(), 43, "gpt-6-astra")
	require.EqualError(t, err, "codex ticket refresh is unavailable")
}

type embeddedNilCodexTicketRepo struct {
	AccountRepository
}

func TestOpenAICodexTicketRefreshIgnoresNilEmbeddedRepository(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true}, nil)
	svc.accountRepo = &embeddedNilCodexTicketRepo{}

	require.NotPanics(t, func() {
		svc.refreshOpenAICodexTickets(context.Background())
	})
	_, err := svc.RefreshOpenAICodexTicket(context.Background(), 43, "gpt-6-astra")
	require.ErrorContains(t, err, "codex ticket account repository is unavailable")
}

func ticketTestService(t *testing.T, cfg config.OpenAICodexTicketConfig, upstream HTTPUpstream) *OpenAIGatewayService {
	t.Helper()
	return &OpenAIGatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{OpenAICodexTicket: cfg},
		},
		httpUpstream: upstream,
	}
}

func TestApplyOpenAICodexTicket_ReplacesHeader(t *testing.T) {
	state := fakeCodexTicketState(292)
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:      true,
		TargetLength: 292,
		TTLSeconds:   3600,
		FailClosed:   true,
	}, nil)
	account := ticketTestAccount(41)
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID:  41,
		Model:      "gpt-6-astra",
		State:      state,
		Length:     292,
		CapturedAt: time.Now(),
		ExpiresAt:  time.Now().Add(time.Hour),
	})

	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, fakeCodexTicketState(312))
	err := svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h)
	require.NoError(t, err)
	require.Equal(t, state, h.Get(openAICodexTurnStateHeader))
	require.Equal(t, 292, len(h.Get(openAICodexTurnStateHeader)))
}

func TestApplyOpenAICodexTicket_DoesNotReuseOtherModelOrAccount(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:         true,
		TargetLength:    292,
		TTLSeconds:      3600,
		FailClosed:      true,
		HarvestProxyURL: "socks5h://harvest",
	}, &httpUpstreamRecorder{err: io.EOF})
	a := ticketTestAccount(41)
	b := ticketTestAccount(42)
	astra := fakeCodexTicketState(292)
	svc.storeOpenAICodexTicket(context.Background(), a, &openAICodexTicket{
		AccountID:  41,
		Model:      "gpt-6-astra",
		State:      astra,
		Length:     292,
		CapturedAt: time.Now(),
		ExpiresAt:  time.Now().Add(time.Hour),
	})

	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, "keep-ungated")
	err := svc.applyOpenAICodexTicket(context.Background(), a, "gpt-5.5", h)
	require.NoError(t, err)
	require.Equal(t, "keep-ungated", h.Get(openAICodexTurnStateHeader))
	require.False(t, svc.openAICodexTicketBlocksAccount(a, "gpt-5.5"))
	require.True(t, svc.openAICodexTicketBlocksAccount(b, "gpt-6-astra"))
	require.False(t, svc.openAICodexTicketBlocksAccount(a, "gpt-6-astra"))

	h = http.Header{}
	err = svc.applyOpenAICodexTicket(context.Background(), b, "gpt-6-astra", h)
	require.ErrorIs(t, err, ErrOpenAICodexTicketUnavailable)
	require.Empty(t, h.Get(openAICodexTurnStateHeader))
}

func TestLookupOpenAICodexTicket_PrefersNewerExtra(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 292, TTLSeconds: 3600}, nil)
	account := ticketTestAccount(41)
	oldState := fakeCodexTicketState(292)
	newState := syntheticCodexTicketState(10, time.Now(), 'C')
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID:  41,
		Model:      "gpt-6-astra",
		State:      oldState,
		Length:     292,
		CapturedAt: time.Now().Add(-30 * time.Minute),
		ExpiresAt:  time.Now().Add(-time.Minute),
	})
	account.Extra = map[string]any{openAICodexTicketExtraKey("gpt-6-astra"): &openAICodexTicket{
		Model:      "gpt-6-astra",
		State:      newState,
		Length:     292,
		CapturedAt: time.Now(),
		ExpiresAt:  time.Now().Add(time.Hour),
	},
	}
	got := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, got)
	require.Equal(t, newState, got.State)
	require.True(t, got.valid(time.Now(), openAICodexTicketPolicy(account, svc.openAICodexTicketConfig())))
}

func TestApplyOpenAICodexTicket_ExpiredNotInjected(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:         true,
		TargetLength:    292,
		TTLSeconds:      3600,
		FailClosed:      true,
		HarvestProxyURL: "socks5h://harvest",
	}, &httpUpstreamRecorder{err: io.EOF})
	account := ticketTestAccount(41)
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID:  41,
		Model:      "gpt-6-astra",
		State:      fakeCodexTicketState(292),
		Length:     292,
		CapturedAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt:  time.Now().Add(-time.Minute),
	})
	h := http.Header{}
	err := svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h)
	require.ErrorIs(t, err, ErrOpenAICodexTicketUnavailable)
	require.Empty(t, h.Get(openAICodexTurnStateHeader))
}

func TestApplyOpenAICodexTicket_OutOfBoundsLengthNotInjected(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:      true,
		TargetLength: 292,
		TTLSeconds:   3600,
		FailClosed:   true,
	}, nil)
	account := ticketTestAccount(41)
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID:  41,
		Model:      "gpt-6-astra",
		State:      fakeCodexTicketState(513),
		Length:     513,
		CapturedAt: time.Now(),
		ExpiresAt:  time.Now().Add(time.Hour),
	})
	h := http.Header{}
	err := svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h)
	require.ErrorIs(t, err, ErrOpenAICodexTicketUnavailable)
	require.Empty(t, h.Get(openAICodexTurnStateHeader))
}

func TestApplyOpenAICodexTicket_FailOpenSkipsInject(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:    true,
		FailClosed: false,
	}, nil)
	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, "client-state")
	err := svc.applyOpenAICodexTicket(context.Background(), ticketTestAccount(41), "gpt-6-astra", h)
	require.NoError(t, err)
	require.Equal(t, "client-state", h.Get(openAICodexTurnStateHeader))
	require.False(t, svc.openAICodexTicketBlocksAccount(ticketTestAccount(41), "gpt-6-astra"))
}

func TestApplyOpenAICodexTicket_LegacyGlobalDisabledDoesNotOverrideAccountOptIn(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: false, FailClosed: true}, nil)
	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, "client-state")
	err := svc.applyOpenAICodexTicket(context.Background(), ticketTestAccount(41), "gpt-6-astra", h)
	require.ErrorIs(t, err, ErrOpenAICodexTicketUnavailable)
	require.Equal(t, "client-state", h.Get(openAICodexTurnStateHeader))
}

func TestHarvestOpenAICodexTicket_RejectsOutOfBoundsAndUsesHarvestProxy(t *testing.T) {
	stateTooLong := fakeCodexTicketState(513)
	state292 := fakeCodexTicketState(292)
	headerTooLong := http.Header{}
	headerTooLong.Set(openAICodexTurnStateHeader, stateTooLong)
	header292 := http.Header{}
	header292.Set(openAICodexTurnStateHeader, state292)
	upstream := &httpUpstreamRecorder{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header:     headerTooLong,
				Body:       io.NopCloser(strings.NewReader(completedCodexTicketStream("gpt-6-astra"))),
			},
			{
				StatusCode: http.StatusOK,
				Header:     header292,
				Body:       io.NopCloser(strings.NewReader(completedCodexTicketStream("gpt-6-astra"))),
			},
		},
	}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:                      true,
		TargetLength:                 292,
		TTLSeconds:                   3600,
		HarvestProxyURL:              "socks5h://user:pass@harvest.example:31",
		HarvestAttemptTimeoutSeconds: 5,
		FailClosed:                   true,
	}, upstream)
	account := ticketTestAccount(41)

	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.Nil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	ticket := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, ticket)
	require.Equal(t, state292, ticket.State)
	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, "stale")
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, state292, h.Get(openAICodexTurnStateHeader))
	require.Equal(t, "socks5h://user:pass@harvest.example:31", upstream.lastProxyURL)
	require.Len(t, upstream.requests, 2)
	require.Empty(t, upstream.requests[0].Header.Get(openAICodexTurnStateHeader))
	require.Equal(t, openAICodexAstraMinVersion, upstream.requests[0].Header.Get("version"))
	require.Equal(t, HTTPUpstreamProfileOpenAIHarvest, HTTPUpstreamProfileFromContext(upstream.requests[0].Context()))
	require.True(t, upstream.requests[0].Close)
}

func TestHarvestOpenAICodexTicket_HTTP503DoesNotAbortHunt(t *testing.T) {
	state292 := fakeCodexTicketState(292)
	header503 := http.Header{}
	header292 := http.Header{}
	header292.Set(openAICodexTurnStateHeader, state292)
	responses := make([]*http.Response, 0, 3)
	responses = append(responses, &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     header503,
		Body:       io.NopCloser(strings.NewReader(`{"error":"overloaded"}`)),
	})
	responses = append(responses, &http.Response{
		StatusCode: http.StatusOK,
		Header:     header292,
		Body:       io.NopCloser(strings.NewReader(completedCodexTicketStream("gpt-6-astra"))),
	})
	upstream := &httpUpstreamRecorder{responses: responses}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:                      true,
		TargetLength:                 292,
		TTLSeconds:                   3600,
		HarvestProxyURL:              "socks5h://harvest.example:31",
		HarvestAttemptTimeoutSeconds: 5,
		FailClosed:                   true,
	}, upstream)
	account := ticketTestAccount(41)
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.Nil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	ticket := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, ticket)
	require.Equal(t, state292, ticket.State)
	require.Len(t, upstream.requests, 2)
}

func TestLookupOpenAICodexTicket_HydratesFromExtra(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 292, TTLSeconds: 3600}, nil)
	state := fakeCodexTicketState(292)
	account := ticketTestAccount(9)
	account.Extra = map[string]any{
		openAICodexTicketExtraKey("gpt-6-astra"): map[string]any{
			"state":       state,
			"length":      292,
			"model":       "gpt-6-astra",
			"captured_at": time.Now().Add(-time.Minute),
			"expires_at":  time.Now().Add(time.Hour),
		},
	}
	got := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, got)
	require.Equal(t, state, got.State)
	require.True(t, got.valid(time.Now(), openAICodexTicketPolicy(account, svc.openAICodexTicketConfig())))
}

func TestOpenAICodexTicketStatuses_ReportsRemainingTTL(t *testing.T) {
	account := ticketTestAccount(41)
	account.Extra = map[string]any{
		openAICodexTicketExtraKey("gpt-6-astra"): map[string]any{
			"state":       fakeCodexTicketState(292),
			"length":      292,
			"model":       "gpt-6-astra",
			"captured_at": time.Now().Add(-10 * time.Minute),
			"expires_at":  time.Now().Add(50 * time.Minute),
		},
	}
	now := time.Now()
	got := OpenAICodexTicketStatuses(account, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true}, now)
	require.Len(t, got, 3)
	require.Equal(t, "gpt-5.6-sol", got[0].Model)
	require.False(t, got[0].Ready)
	require.Equal(t, "gpt-5.6-terra", got[1].Model)
	require.False(t, got[1].Ready)
	require.Equal(t, "gpt-6-astra", got[2].Model)
	require.True(t, got[2].Ready)
	require.Greater(t, got[2].RemainingSeconds, int64(40*60))
	require.LessOrEqual(t, got[2].RemainingSeconds, int64(50*60))
}

func TestOpenAICodexTicketStatusesExplainAccountShapeAndExpiry(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	statusFor := func(account *Account, state string) OpenAICodexTicketStatus {
		account.Extra[openAICodexTicketExtraKey("gpt-6-astra")] = map[string]any{
			"state":       state,
			"length":      len(state),
			"model":       "gpt-6-astra",
			"captured_at": now,
		}
		statuses := OpenAICodexTicketStatuses(account, config.OpenAICodexTicketConfig{
			Models: []string{"gpt-6-astra"}, TTLSeconds: 3600, FailClosed: true,
		}, now)
		require.Len(t, statuses, 1)
		return statuses[0]
	}

	personal := ticketTestAccount(910)
	personal.Credentials["plan_type"] = "plus"
	personal312 := statusFor(personal, syntheticCodexTicketState(11, now, 11))
	require.Equal(t, "personal", personal312.AccountMode)
	require.Equal(t, 292, personal312.ExpectedLength)
	require.Equal(t, 312, personal312.Length)
	require.Equal(t, 11, personal312.Blocks)
	require.False(t, personal312.ShapeValid)
	require.True(t, personal312.Ready, "reference shape must not exclude a usable completed model result")
	require.NotNil(t, personal312.IssuedAt)
	require.Equal(t, now, *personal312.IssuedAt)

	team := ticketTestAccount(911)
	team.Credentials["plan_type"] = "team"
	team332 := statusFor(team, syntheticCodexTicketState(12, now, 12))
	require.Equal(t, "team", team332.AccountMode)
	require.Equal(t, 332, team332.ExpectedLength)
	require.Equal(t, 332, team332.Length)
	require.Equal(t, 12, team332.Blocks)
	require.True(t, team332.ShapeValid)
	require.True(t, team332.Ready)

	expired := ticketTestAccount(912)
	expired.Credentials["plan_type"] = "plus"
	expired292 := statusFor(expired, syntheticCodexTicketState(10, now.Add(-2*time.Hour), 10))
	require.Equal(t, 292, expired292.ExpectedLength)
	require.Equal(t, 292, expired292.Length)
	require.True(t, expired292.ShapeValid, "shape validity is independent from local TTL expiry")
	require.False(t, expired292.Ready)
	require.NotNil(t, expired292.IssuedAt)
	require.Equal(t, now.Add(-2*time.Hour), *expired292.IssuedAt)
}

func TestExtractOpenAICodexTicketModel(t *testing.T) {
	require.Equal(t, "gpt-6-astra", extractOpenAICodexTicketModel([]byte(`{"model":"gpt-6-astra"}`)))
	require.Empty(t, extractOpenAICodexTicketModel([]byte(`{}`)))
}

func TestChooseOpenAICodexTicketCandidatePrefersExactModelThenLatency(t *testing.T) {
	candidates := []openAICodexTicketCandidate{
		{openAICodexTicketProbeResult: openAICodexTicketProbeResult{ObservedModel: "gpt-5.6-luna", LatencyMs: 40}, ProxyID: 1},
		{openAICodexTicketProbeResult: openAICodexTicketProbeResult{ObservedModel: "gpt-6-astra", LatencyMs: 30}, ProxyID: 2},
		{openAICodexTicketProbeResult: openAICodexTicketProbeResult{ObservedModel: "gpt-6-astra", LatencyMs: 20}, ProxyID: 3},
	}
	selected := chooseOpenAICodexTicketCandidate(candidates, "gpt-6-astra")
	require.NotNil(t, selected)
	require.Equal(t, int64(3), selected.ProxyID)
	require.Equal(t, "gpt-6-astra", selected.ObservedModel)
}

func TestChooseOpenAICodexTicketCandidateFallsBackToFastestObservedModel(t *testing.T) {
	candidates := []openAICodexTicketCandidate{
		{openAICodexTicketProbeResult: openAICodexTicketProbeResult{ObservedModel: "gpt-5.6-luna", LatencyMs: 40}, ProxyID: 1},
		{openAICodexTicketProbeResult: openAICodexTicketProbeResult{ObservedModel: "gpt-5.6-luna", LatencyMs: 18}, ProxyID: 2},
	}
	selected := chooseOpenAICodexTicketCandidate(candidates, "gpt-6-astra")
	require.NotNil(t, selected)
	require.Equal(t, int64(2), selected.ProxyID)
	require.Equal(t, "gpt-5.6-luna", selected.ObservedModel)
}

type codexTicketMultiEgressUpstream struct {
	HTTPUpstream
	mu    sync.Mutex
	calls map[string]int
}

func (u *codexTicketMultiEgressUpstream) Do(_ *http.Request, proxyURL string, _ int64, _ int) (*http.Response, error) {
	delay := time.Millisecond
	model := "gpt-5.6-luna"
	switch {
	case strings.Contains(proxyURL, ":1082"):
		delay = 12 * time.Millisecond
		model = "gpt-6-astra"
	case strings.Contains(proxyURL, ":1083"):
		delay = 5 * time.Millisecond
		model = "gpt-6-astra"
	}
	time.Sleep(delay)
	u.mu.Lock()
	if u.calls == nil {
		u.calls = make(map[string]int)
	}
	u.calls[proxyURL]++
	u.mu.Unlock()
	header := http.Header{}
	header.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
	body := completedCodexTicketStream(model)
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (u *codexTicketMultiEgressUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	if profile == nil {
		return nil, errors.New("missing ticket TLS fingerprint")
	}
	return u.Do(req, proxyURL, accountID, concurrency)
}

func TestOpenAICodexTicketAccountOptInWorksWhenLegacyGlobalSwitchIsOff(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: false, TargetLength: 292, FailClosed: true, Models: []string{"gpt-6-astra"}}, nil)
	account := ticketTestAccount(41)
	state := fakeCodexTicketState(292)
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID: account.ID, Model: "gpt-6-astra", State: state, Length: len(state), CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	headers := http.Header{}
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", headers))
	require.Equal(t, state, headers.Get(openAICodexTurnStateHeader))
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
}

func TestRefreshOpenAICodexTicketProbesEveryEgressAndSelectsFastestExactModel(t *testing.T) {
	account := ticketTestAccount(41)
	account.MultiProxyConfigured = true
	for i, port := range []int{1081, 1082, 1083} {
		id := int64(i + 1)
		account.ProxyBindings = append(account.ProxyBindings, AccountProxyBinding{
			ProxyID:        id,
			MaxConcurrency: 1,
			Proxy:          &Proxy{ID: id, Name: fmt.Sprintf("exit-%d", id), Protocol: "socks5", Host: "127.0.0.1", Port: port, Status: StatusActive},
		})
	}
	upstream := &codexTicketMultiEgressUpstream{}
	repo := &codexTicketRefreshRepo{}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: false, TTLSeconds: 3600}, upstream)
	svc.accountRepo = repo

	statuses, err := svc.refreshOpenAICodexTicketForAccount(context.Background(), account, []string{"gpt-6-astra"}, true)
	require.NoError(t, err)
	require.Len(t, statuses, 3)
	status := statuses[2]
	require.Equal(t, "gpt-6-astra", status.Model)
	require.True(t, status.Ready)
	require.Equal(t, "gpt-6-astra", status.ObservedModel)
	require.Equal(t, int64(3), status.ProxyID)
	require.False(t, status.Fallback)
	require.Equal(t, 10, status.Blocks)
	require.True(t, status.Completed)
	require.Len(t, status.Probes, 3)
	require.Equal(t, 292, status.Probes[0].Length)
	require.Equal(t, 10, status.Probes[0].Blocks)
	require.True(t, status.Probes[0].Completed)
	upstream.mu.Lock()
	require.Len(t, upstream.calls, 3)
	for _, count := range upstream.calls {
		require.Equal(t, 1, count)
	}
	upstream.mu.Unlock()
}

// These stubs exercise the real continuous refresh path with both default models
// completing together. Run under -race to catch writes to the shared account maps.
type codexTicketRefreshRepo struct {
	AccountRepository
	accounts []Account
	mu       sync.Mutex
	updates  map[string]any
}

func (r *codexTicketRefreshRepo) ListByPlatform(context.Context, string) ([]Account, error) {
	return r.accounts, nil
}
func (r *codexTicketRefreshRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			account := r.accounts[i]
			return &account, nil
		}
	}
	return nil, ErrAccountNotFound
}
func (r *codexTicketRefreshRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updates == nil {
		r.updates = make(map[string]any)
	}
	for k, v := range updates {
		r.updates[k] = v
	}
	return nil
}

type codexTicketConcurrentUpstream struct {
	HTTPUpstream
	started atomic.Int64
	ready   chan struct{}
}

func (u *codexTicketConcurrentUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if u.started.Add(1) == 2 {
		close(u.ready)
	}
	select {
	case <-u.ready:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	model := extractOpenAICodexTicketModel(body)
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(completedCodexTicketStream(model)))}, nil
}

func (u *codexTicketConcurrentUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	if profile == nil {
		return nil, errors.New("missing ticket TLS fingerprint")
	}
	return u.Do(req, proxyURL, accountID, concurrency)
}

func TestRefreshOpenAICodexTickets_ConcurrentModelsPreserveAccountSnapshot(t *testing.T) {
	account := ticketTestAccount(41)
	account.Status = StatusActive
	account.Extra = map[string]any{"existing": true, OpenAICodexTicketEnabledExtraKey: true}
	repo := &codexTicketRefreshRepo{accounts: []Account{*account}}
	upstream := &codexTicketConcurrentUpstream{ready: make(chan struct{})}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "socks5h://proxy.example.com:1080"}, upstream)
	svc.accountRepo = repo
	svc.refreshOpenAICodexTickets(context.Background())
	require.Equal(t, int64(3), upstream.started.Load())
	require.Equal(t, map[string]any{"existing": true, OpenAICodexTicketEnabledExtraKey: true}, account.Extra)
	require.Len(t, repo.updates, 3)
	for _, model := range []string{openAICodexTicketDefaultSolModel, openAICodexTicketDefaultTerraModel, openAICodexTicketDefaultModel} {
		ticket := svc.lookupOpenAICodexTicket(account, model)
		require.NotNil(t, ticket)
		require.True(t, ticket.valid(time.Now(), openAICodexTicketPolicy(account, svc.openAICodexTicketConfig())))
	}
	// Valid tickets do not produce another probe on the next cycle.
	svc.refreshOpenAICodexTickets(context.Background())
	require.Equal(t, int64(3), upstream.started.Load())
}
func TestOpenAICodexTicketStatuses_RespectRuntimeConfiguration(t *testing.T) {
	account := ticketTestAccount(41)
	status := OpenAICodexTicketStatuses(account, config.OpenAICodexTicketConfig{}, time.Now())
	require.Len(t, status, 3)
	require.False(t, status[0].Blocked)
	cfg := config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"custom-model"}}
	status = OpenAICodexTicketStatuses(account, cfg, time.Now())
	require.Len(t, status, 1)
	require.Equal(t, "custom-model", status[0].Model)
	require.False(t, status[0].Blocked)
	cfg.FailClosed = true
	require.True(t, OpenAICodexTicketStatuses(account, cfg, time.Now())[0].Blocked)
}

func TestOpenAICodexTicketStatusesIncludesPersistedModelsWhenDisabled(t *testing.T) {
	account := ticketTestAccount(41)
	account.Extra = map[string]any{
		openAICodexTicketExtraKey("gpt-5.6-sol"): map[string]any{
			"state":       fakeCodexTicketState(292),
			"length":      292,
			"model":       "gpt-5.6-sol",
			"captured_at": time.Now().Add(-time.Minute),
			"expires_at":  time.Now().Add(time.Hour),
		},
	}
	status := OpenAICodexTicketStatuses(account, config.OpenAICodexTicketConfig{Models: []string{"gpt-6-astra"}}, time.Now())
	require.Len(t, status, 2)
	require.Equal(t, "gpt-5.6-sol", status[1].Model)
	require.True(t, status[1].Ready)
	require.Equal(t, 292, status[1].Length)
	require.False(t, status[1].Blocked)
}

func TestStoreOpenAICodexTicketPreservesExactTicketOverFallback(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 292, TTLSeconds: 3600}, nil)
	account := ticketTestAccount(777)
	now := time.Now()
	exactState := fakeCodexTicketState(292)
	fallbackState := syntheticCodexTicketState(10, now, 'C')
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID: 777, Model: "gpt-6-astra", RequestedModel: "gpt-6-astra", ObservedModel: "gpt-6-astra",
		State: exactState, Length: len(exactState), CapturedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), ProxyID: 1,
	})
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID: 777, Model: "gpt-6-astra", RequestedModel: "gpt-6-astra", ObservedModel: "gpt-5.6-luna",
		State: fallbackState, Length: len(fallbackState), CapturedAt: now, ExpiresAt: now.Add(time.Hour), ProxyID: 2,
	})

	got := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, got)
	require.Equal(t, exactState, got.State)
	require.EqualValues(t, 1, got.ProxyID)
	require.False(t, got.Fallback)
}

func TestLookupOpenAICodexTicketPreservesValidMemoryExactOverNewerPersistedFallback(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TTLSeconds: 3600}, nil)
	account := ticketTestAccount(780)
	now := time.Now()
	exactState := syntheticCodexTicketState(10, now, 'E')
	fallbackState := syntheticCodexTicketState(10, now, 'F')
	svc.openaiCodexTickets.Store(openAICodexTicketKey(account.ID, "gpt-6-astra"), &openAICodexTicket{
		AccountID: account.ID, Model: "gpt-6-astra", RequestedModel: "gpt-6-astra", ObservedModel: "gpt-6-astra",
		State: exactState, Length: len(exactState), Blocks: 10, CapturedAt: now.Add(-time.Minute), ExpiresAt: now.Add(50 * time.Minute),
	})
	account.Extra[openAICodexTicketExtraKey("gpt-6-astra")] = &openAICodexTicket{
		AccountID: account.ID, Model: "gpt-6-astra", RequestedModel: "gpt-6-astra", ObservedModel: "gpt-5.6-luna", Fallback: true,
		State: fallbackState, Length: len(fallbackState), Blocks: 10, CapturedAt: now, ExpiresAt: now.Add(50 * time.Minute),
	}

	got := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, got)
	require.Equal(t, exactState, got.State)
	require.False(t, got.Fallback)
}

func TestStoreOpenAICodexTicketReplacesFallbackWithExact(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 292, TTLSeconds: 3600}, nil)
	account := ticketTestAccount(778)
	now := time.Now()
	fallbackState := fakeCodexTicketState(292)
	exactState := syntheticCodexTicketState(10, now, 'D')
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID: 778, Model: "gpt-6-astra", RequestedModel: "gpt-6-astra", ObservedModel: "gpt-5.6-luna",
		State: fallbackState, Length: len(fallbackState), CapturedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), ProxyID: 2,
	})
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID: 778, Model: "gpt-6-astra", RequestedModel: "gpt-6-astra", ObservedModel: "gpt-6-astra",
		State: exactState, Length: len(exactState), CapturedAt: now, ExpiresAt: now.Add(time.Hour), ProxyID: 1,
	})

	got := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
	require.NotNil(t, got)
	require.Equal(t, exactState, got.State)
	require.EqualValues(t, 1, got.ProxyID)
	require.False(t, got.Fallback)
}

func TestParseLegacyCodexTicketInfersFallbackAndRetriesAreBounded(t *testing.T) {
	state := fakeCodexTicketState(292)
	raw := map[string]any{
		"model":          "gpt-6-astra",
		"observed_model": "gpt-5.6-luna",
		"state":          state,
		"length":         len(state),
		"captured_at":    time.Now().Add(-11 * time.Minute),
		"expires_at":     time.Now().Add(time.Hour),
	}
	ticket := parseOpenAICodexTicketFromAny(779, "gpt-6-astra", raw)
	require.NotNil(t, ticket)
	require.True(t, ticket.Fallback)
	require.False(t, ticket.needsRefresh(time.Now(), 10*time.Minute), "a valid fallback uses the same refresh schedule as an exact ticket")
	ticket.CapturedAt = time.Now().Add(-time.Minute)
	require.False(t, ticket.needsRefresh(time.Now(), 10*time.Minute))
}

func TestProbeOpenAICodexTicket_AcceptsSuccessfulEnvelopeAcrossReferenceShapes(t *testing.T) {
	tests := []struct {
		name     string
		plan     string
		blocks   int
		accepted bool
	}{
		{name: "personal-10", plan: "plus", blocks: 10, accepted: true},
		{name: "personal-11", plan: "plus", blocks: 11, accepted: true},
		{name: "personal-13", plan: "plus", blocks: 13, accepted: true},
		{name: "team-12", plan: "team", blocks: 12, accepted: true},
		{name: "team-10", plan: "team", blocks: 10, accepted: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := syntheticCodexTicketState(tc.blocks, time.Now(), byte(tc.blocks))
			h := http.Header{}
			h.Set(openAICodexTurnStateHeader, state)
			upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(completedCodexTicketStream("gpt-6-astra")))}}}
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example.com:8080"}, upstream)
			account := ticketTestAccount(41)
			account.Credentials["plan_type"] = tc.plan
			svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
			got := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
			if tc.accepted {
				require.NotNil(t, got)
				require.Equal(t, tc.blocks, got.Blocks)
				return
			}
			require.Nil(t, got)
		})
	}
}

func TestProbeOpenAICodexTicket_RejectsInvalidState(t *testing.T) {
	for _, state := range []string{
		fakeCodexTicketState(291),
		strings.Repeat("X", 292),
		fakeCodexTicketState(513),
		"gAAAAA" + strings.Repeat("B", 291) + "!",
		fakeCodexTicketState(289) + "===",
		fakeCodexTicketState(310) + "=B",
		"",
	} {
		h := http.Header{}
		h.Set(openAICodexTurnStateHeader, state)
		upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(completedCodexTicketStream("gpt-6-astra")))}}}
		svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, HarvestProxyURL: "http://proxy.example.com:8080"}, upstream)
		account := ticketTestAccount(41)
		svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
		require.Nil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
	}
}
func TestOpenAICodexTicket_RequiresActualLengthAndExpiry(t *testing.T) {
	policy := openAICodexTicketPolicy(ticketTestAccount(41), config.OpenAICodexTicketConfig{TTLSeconds: 3600})
	ticket := &openAICodexTicket{State: fakeCodexTicketState(312), Length: 312, ExpiresAt: time.Now().Add(time.Hour)}
	require.True(t, ticket.valid(time.Now(), policy), "11-block/312 state remains usable under the model-first selection policy")
	ticket.State = fakeCodexTicketState(292)
	ticket.Length = 291
	require.False(t, ticket.valid(time.Now(), policy))
	ticket.Length = 292
	require.True(t, ticket.valid(time.Now(), policy))
	ticket.ExpiresAt = time.Time{}
	require.True(t, ticket.valid(time.Now(), policy), "issued+local TTL remains authoritative when legacy expires_at is absent")
}

type boundedCodexTicketUpstream struct {
	HTTPUpstream
	active      atomic.Int64
	maxActive   atomic.Int64
	calls       atomic.Int64
	fourStarted chan struct{}
	release     chan struct{}
	once        sync.Once
}

func (u *boundedCodexTicketUpstream) Do(req *http.Request, proxyURL string, accountID int64, concurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, concurrency, &tlsfingerprint.Profile{Name: "test"})
}

func (u *boundedCodexTicketUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	active := u.active.Add(1)
	defer u.active.Add(-1)
	for {
		current := u.maxActive.Load()
		if active <= current || u.maxActive.CompareAndSwap(current, active) {
			break
		}
	}
	if u.calls.Add(1) == 4 && u.fourStarted != nil {
		u.once.Do(func() { close(u.fourStarted) })
	}
	select {
	case <-u.release:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(completedCodexTicketStream("gpt-6-astra")))}, nil
}

func codexTicketCaptureProxies(count int) []*Proxy {
	proxies := make([]*Proxy, 0, count)
	for i := 0; i < count; i++ {
		proxies = append(proxies, &Proxy{ID: int64(i + 1), Name: fmt.Sprintf("capture-%d", i+1), Protocol: "http", Host: "127.0.0.1", Port: 9000 + i, Status: StatusActive})
	}
	return proxies
}

func TestRefreshOpenAICodexTicketWithProxiesBoundsProbeConcurrencyAtFour(t *testing.T) {
	account := ticketTestAccount(901)
	account.Credentials["plan_type"] = "plus"
	upstream := &boundedCodexTicketUpstream{fourStarted: make(chan struct{}), release: make(chan struct{})}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TTLSeconds: 3600, HarvestAttemptTimeoutSeconds: 5}, upstream)
	svc.accountRepo = &codexTicketRefreshRepo{accounts: []Account{*account}}
	done := make(chan error, 1)
	go func() {
		_, err := svc.RefreshOpenAICodexTicketWithProxies(context.Background(), account.ID, "gpt-6-astra", codexTicketCaptureProxies(6))
		done <- err
	}()
	select {
	case <-upstream.fourStarted:
	case <-time.After(time.Second):
		t.Fatal("four bounded probes did not start")
	}
	require.Equal(t, int64(4), upstream.maxActive.Load())
	close(upstream.release)
	require.NoError(t, <-done)
	require.Equal(t, int64(6), upstream.calls.Load())
	require.LessOrEqual(t, upstream.maxActive.Load(), int64(4))
}

func TestRefreshOpenAICodexTicketWithProxiesCoalescesAccountModel(t *testing.T) {
	account := ticketTestAccount(902)
	upstream := &boundedCodexTicketUpstream{release: make(chan struct{})}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TTLSeconds: 3600, HarvestAttemptTimeoutSeconds: 5}, upstream)
	svc.accountRepo = &codexTicketRefreshRepo{accounts: []Account{*account}}
	proxy := codexTicketCaptureProxies(1)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := svc.RefreshOpenAICodexTicketWithProxies(context.Background(), account.ID, "gpt-6-astra", proxy)
			errs <- err
		}()
	}
	require.Eventually(t, func() bool { return upstream.calls.Load() == 1 }, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int64(1), upstream.calls.Load())
	close(upstream.release)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	require.Equal(t, int64(1), upstream.calls.Load())
}

func TestRefreshOpenAICodexTicketRejectsFailedOrErroredCompletedStream(t *testing.T) {
	tests := []struct {
		name   string
		stream string
	}{
		{
			name: "failed with error code then completed",
			stream: "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"synthetic_failure\"}}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-astra\"}}\n\n",
		},
		{
			name: "incomplete then completed",
			stream: "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"model\":\"gpt-6-astra\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-astra\"}}\n\n",
		},
		{
			name:   "completed event contains error code",
			stream: "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"error\":{\"code\":\"synthetic_error\"}}}\n\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
			upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(strings.NewReader(tc.stream))}}}
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TTLSeconds: 3600, HarvestProxyURL: "http://capture.example:8080"}, upstream)
			account := ticketTestAccount(903)
			_, err := svc.refreshOpenAICodexTicketForAccount(context.Background(), account, []string{"gpt-6-astra"}, true)
			require.Error(t, err)
			require.Nil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
		})
	}
}

// /responses/compact 的出站模型被 Forward 改写为 gateway.openai_compact_model
// （默认非空），门票门控必须按该出站模型判定。否则对门控模型发 compact 请求时，
// 所有无票账号都会被 fail_closed 误判为不可调度，而这些请求实际不需要票。
func TestOpenAICodexTicketGate_CompactRequestUsesForwardOutboundModel(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		OpenAICompactModel: "gpt-5.5",
		OpenAICodexTicket: config.OpenAICodexTicketConfig{
			Enabled:      true,
			TargetLength: 292,
			TTLSeconds:   3600,
			FailClosed:   true,
			Models:       []string{"gpt-6-astra"},
		},
	}}}
	account := ticketTestAccount(41) // 无票

	// 出站模型预测必须与 Forward 的解析链一致。
	require.Equal(t, "gpt-6-astra", svc.openAICodexTicketOutboundModel(account, "gpt-6-astra", false))
	require.Equal(t, "gpt-5.5", svc.openAICodexTicketOutboundModel(account, "gpt-6-astra", true))

	// 普通请求：出站仍是门控模型且无票 → fail_closed 必须拦号。
	require.True(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6-astra", false))

	// compact 请求：出站已被改写成非门控的 gpt-5.5 → 不得拦号。
	require.False(t, svc.isOpenAIAccountRequestRuntimeBlocked(account, "gpt-6-astra", true))

	// 回归锚点：按客户端原始模型判定（旧实现的口径）在 compact 下必然误拦。
	require.True(t, svc.openAICodexTicketBlocksAccount(account, canonicalOpenAIAccountSchedulingModel(account, "gpt-6-astra")))
}
