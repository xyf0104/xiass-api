package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func ordinaryCredit(balance string) *OpenAICredits {
	return &OpenAICredits{HasCredits: true, Balance: &balance}
}

func TestOpenAICreditsHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, has, unlimited, balance string
		want                          *OpenAICredits
	}{
		{name: "missing"},
		{name: "partial", balance: "62500"},
		{name: "invalid flag", has: "yes", unlimited: "false", balance: "62500"},
		{name: "finite", has: "true", unlimited: "false", balance: "62500", want: ordinaryCredit("62500")},
		{name: "precision", has: "true", unlimited: "false", balance: "9007199254740993.125", want: ordinaryCredit("9007199254740993.125")},
		{name: "unlimited", has: "true", unlimited: "true", want: &OpenAICredits{HasCredits: true, Unlimited: true}},
		{name: "exhausted", has: "false", unlimited: "false", balance: "0", want: &OpenAICredits{Balance: strPtr("0")}},
		{name: "unknown finite", has: "true", unlimited: "false"},
		{name: "not a number", has: "true", unlimited: "false", balance: "NaN"},
		{name: "infinity", has: "true", unlimited: "false", balance: "Inf"},
		{name: "negative", has: "true", unlimited: "false", balance: "-3"},
		{name: "fraction syntax", has: "true", unlimited: "false", balance: "1/2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			h.Set("x-codex-credits-has-credits", tc.has)
			h.Set("x-codex-credits-unlimited", tc.unlimited)
			h.Set("x-codex-credits-balance", tc.balance)
			require.Equal(t, tc.want, ParseOpenAICreditsHeaders(h))
		})
	}
}

func TestOpenAICreditsOnlyOptedInOAuthCanBypassQuotaPause(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled any
		credits *OpenAICredits
		mutate  func(*Account)
		want    bool
	}{
		{name: "default off", credits: ordinaryCredit("62500")},
		{name: "explicit off", enabled: false, credits: ordinaryCredit("62500")},
		{name: "invalid flag", enabled: "true", credits: ordinaryCredit("62500")},
		{name: "unknown", enabled: true},
		{name: "positive", enabled: true, credits: ordinaryCredit("62500"), want: true},
		{name: "fractional", enabled: true, credits: ordinaryCredit("0.0001"), want: true},
		{name: "unlimited", enabled: true, credits: &OpenAICredits{Unlimited: true}, want: true},
		{name: "exhausted", enabled: true, credits: ordinaryCredit("0")},
		{name: "no credit", enabled: true, credits: &OpenAICredits{Balance: strPtr("62500")}},
		{name: "reset cards alone", enabled: true},
		{name: "apikey", enabled: true, credits: ordinaryCredit("62500"), mutate: func(a *Account) { a.Type = AccountTypeAPIKey }},
		{name: "other platform", enabled: true, credits: ordinaryCredit("62500"), mutate: func(a *Account) { a.Platform = PlatformAnthropic }},
		{name: "shadow", enabled: true, credits: ordinaryCredit("62500"), mutate: func(a *Account) { id := int64(2); a.ParentAccountID = &id; a.QuotaDimension = QuotaDimensionSpark }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
				openAICreditsEnabledExtraKey: tc.enabled,
				openaiQuotaCreditsKey:        &openAICreditsSnapshot{Credits: tc.credits, FetchedAt: time.Now().Unix()},
				openaiQuotaResetCreditsKey:   map[string]any{"available_count": 5},
				"codex_7d_used_percent":      100.0, "codex_usage_updated_at": time.Now().UTC().Format(time.RFC3339Nano),
				"auto_pause_7d_threshold": 0.99,
			}}
			if tc.mutate != nil {
				tc.mutate(a)
			}
			require.Equal(t, tc.want, accountHasUsableOpenAICredits(a))
			if a.Platform == PlatformOpenAI && a.Type == AccountTypeOAuth && !a.IsShadow() {
				ctx := withOpenAIQuotaAutoPauseSettings(context.Background(), OpsOpenAIAccountQuotaAutoPauseSettings{DefaultThreshold7d: .99})
				paused, _ := shouldAutoPauseOpenAIAccountByQuota(ctx, a)
				require.Equal(t, !tc.want, paused)
			}
		})
	}
}

func TestOpenAICreditsCacheIdentityAndUnknown(t *testing.T) {
	identity := quotaTestRequestIdentity()
	repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{identity.target.ID: identity.target}}
	svc := &OpenAIQuotaService{accountRepo: repo}
	usage := &OpenAIQuotaUsage{Credits: ordinaryCredit("62500"), FetchedAt: identity.observedAt.Unix(), requestIdentity: identity}
	require.NoError(t, svc.CacheCreditsSnapshot(context.Background(), identity.target.ID, usage))
	patch := repo.extraUpdates[identity.target.ID]
	require.Len(t, patch, 2)
	require.NotContains(t, patch, openaiQuotaResetCreditsKey)
	require.NotContains(t, patch, "codex_usage_updated_at")
	data, err := json.Marshal(patch[openaiQuotaCreditsKey])
	require.NoError(t, err)
	var cached openAICreditsSnapshot
	require.NoError(t, json.Unmarshal(data, &cached))
	require.Equal(t, ordinaryCredit("62500"), cached.Credits)
	require.Equal(t, identity.observedAt.Unix(), cached.FetchedAt)
	usage.Credits = nil
	require.NoError(t, svc.CacheCreditsSnapshot(context.Background(), identity.target.ID, usage))
	data, err = json.Marshal(repo.extraUpdates[identity.target.ID][openaiQuotaCreditsKey])
	require.NoError(t, err)
	require.Contains(t, string(data), `"credits":null`)
	usage.requestIdentity = nil
	require.Error(t, svc.CacheCreditsSnapshot(context.Background(), identity.target.ID, usage))
}

func TestOpenAICreditsRuntimeDoesNotStripEnableSetting(t *testing.T) {
	clean := stripOpenAIQuotaRuntimeExtra(map[string]any{
		openAICreditsEnabledExtraKey: true,
		openaiQuotaCreditsKey:        map[string]any{"credits": ordinaryCredit("62500")},
		openaiQuotaCreditsTimeKey:    time.Now().UTC().Format(time.RFC3339Nano),
	})
	require.Equal(t, map[string]any{openAICreditsEnabledExtraKey: true}, clean)
}

func TestOpenAICreditsStreamPreservesChunkedDelivery(t *testing.T) {
	event := `{"type":"codex.rate_limits","credits":{"has_credits":true,"unlimited":false,"balance":"62499.75"}}`
	input := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\r\n\r\n" + "data: " + event + "\r\n\r\ndata: [DONE]\n\n"
	for _, size := range []int{1, 7, 128, 4096} {
		t.Run(string(rune(size)), func(t *testing.T) {
			var observations []*OpenAICredits
			tap := &openAICreditsStream{ReadCloser: io.NopCloser(strings.NewReader(input)), observe: func(data []byte) {
				if credits := parseOpenAICreditsEvent(data); credits != nil {
					observations = append(observations, credits)
				}
			}}
			var output strings.Builder
			buf := make([]byte, size)
			for {
				n, err := tap.Read(buf)
				_, writeErr := output.Write(buf[:n])
				require.NoError(t, writeErr)
				if err != nil {
					require.ErrorIs(t, err, io.EOF)
					break
				}
			}
			require.NoError(t, tap.Close())
			require.Equal(t, input, output.String())
			require.Equal(t, []*OpenAICredits{ordinaryCredit("62499.75")}, observations)
		})
	}
}

func TestOpenAICreditsStreamBoundsOversizedEventsAndRejectsText(t *testing.T) {
	event := `{"type":"codex.rate_limits","credits":{"has_credits":true,"unlimited":false,"balance":"62490"}}`
	input := "data: " + strings.Repeat("x", 70*1024) + "\n\ndata: " + event + "\n\n"
	var got []*OpenAICredits
	tap := &openAICreditsStream{ReadCloser: io.NopCloser(strings.NewReader(input)), observe: func(data []byte) {
		if c := parseOpenAICreditsEvent(data); c != nil {
			got = append(got, c)
		}
	}}
	_, err := io.CopyBuffer(io.Discard, tap, make([]byte, 31))
	require.NoError(t, err)
	require.LessOrEqual(t, cap(tap.line), 128*1024)
	require.Equal(t, []*OpenAICredits{ordinaryCredit("62490")}, got)
	require.Nil(t, parseOpenAICreditsEvent([]byte(`{"type":"response.output_text.delta","credits":{"has_credits":true,"unlimited":true},"delta":"codex.rate_limits"}`)))
	require.Nil(t, parseOpenAICreditsEvent([]byte(`{"type":"codex.rate_limits","credits":{"balance":"62500"}}`)))
}

type ordinaryCreditAsyncRepo struct {
	AccountRepository
	updates  chan map[string]any
	expected chan *Account
}

func (r *ordinaryCreditAsyncRepo) UpdateOpenAIQuotaSnapshotIfIdentityMatches(_ context.Context, target, _ *Account, _ time.Time, updates map[string]any) (bool, error) {
	r.expected <- target
	r.updates <- updates
	return true, nil
}

func TestOpenAICreditsTransportCapturesOriginalRequestAndBothBalances(t *testing.T) {
	repo := &ordinaryCreditAsyncRepo{updates: make(chan map[string]any, 4), expected: make(chan *Account, 4)}
	account := quotaTestRequestIdentity().target
	svc := &OpenAIGatewayService{accountRepo: repo, httpUpstream: &codexTicketFuncUpstream{do: func(_ *http.Request) (*http.Response, error) {
		account.Credentials = map[string]any{"access_token": "synthetic-new-token"}
		h := http.Header{}
		h.Set("Content-Type", "text/event-stream")
		h.Set("x-codex-credits-has-credits", "true")
		h.Set("x-codex-credits-unlimited", "false")
		h.Set("x-codex-credits-balance", "62500")
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"codex.rate_limits\",\"credits\":{\"has_credits\":true,\"unlimited\":false,\"balance\":\"62499\"}}\n\n"))}, nil
	}}}
	request, err := http.NewRequest(http.MethodPost, "https://example.invalid/responses", nil)
	require.NoError(t, err)
	response, err := svc.doOpenAIUpstream(request, "", account)
	require.NoError(t, err)
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	balances := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		select {
		case expected := <-repo.expected:
			require.Equal(t, "synthetic-old-access", expected.GetOpenAIAccessToken())
		case <-time.After(time.Second):
			t.Fatal("credit snapshot was not dispatched")
		}
		select {
		case updates := <-repo.updates:
			snapshot, ok := updates[openaiQuotaCreditsKey].(*openAICreditsSnapshot)
			require.True(t, ok)
			require.NotNil(t, snapshot)
			balances = append(balances, *snapshot.Credits.Balance)
			require.NotContains(t, updates, openaiQuotaResetCreditsKey)
		case <-time.After(time.Second):
			t.Fatal("credit snapshot was not persisted")
		}
	}
	require.ElementsMatch(t, []string{"62500", "62499"}, balances)
}
