//go:build unit

package service

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type ticketAwareProxySlotCacheForTest struct {
	stubConcurrencyCacheForTest

	mu       sync.Mutex
	calls    [][]AccountProxySlotSpec
	acquired []bool
	results  []int64
}

func (c *ticketAwareProxySlotCacheForTest) AcquireAccountProxySlot(_ context.Context, _ int64, slots []AccountProxySlotSpec, _ string) (int64, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, append([]AccountProxySlotSpec(nil), slots...))
	index := len(c.calls) - 1
	if index >= len(c.acquired) || !c.acquired[index] {
		return 0, false, nil
	}
	return c.results[index], true, nil
}

func (c *ticketAwareProxySlotCacheForTest) ReleaseAccountProxySlot(context.Context, int64, int64, string) error {
	return nil
}

func (c *ticketAwareProxySlotCacheForTest) GetAccountProxyConcurrency(context.Context, int64, []int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}

func (c *ticketAwareProxySlotCacheForTest) GetAccountsProxyConcurrency(context.Context, map[int64][]int64) (map[int64]map[int64]int, error) {
	return map[int64]map[int64]int{}, nil
}

func TestAcquireAccountProxySlotUsesAllBusinessExitsDespiteTicketProbeObservations(t *testing.T) {
	now := time.Now()
	state := fakeCodexTicketState(292)
	account := ticketTestAccount(555)
	account.MultiProxyConfigured = true
	account.Extra = map[string]any{
		OpenAICodexTicketEnabledExtraKey: true,
		openAICodexTicketExtraKey("gpt-6-astra"): map[string]any{
			"account_id":  555,
			"model":       "gpt-6-astra",
			"state":       state,
			"length":      len(state),
			"captured_at": now,
			"expires_at":  now.Add(time.Hour),
			"probes": []map[string]any{
				{"proxy_id": 1, "valid": true, "observed_model": "gpt-5.6-luna"},
				{"proxy_id": 2, "valid": true, "observed_model": "gpt-6-astra"},
				{"proxy_id": 3, "valid": true, "observed_model": "gpt-5.6-luna"},
			},
		},
	}
	for id := int64(1); id <= 3; id++ {
		account.ProxyBindings = append(account.ProxyBindings, AccountProxyBinding{
			ProxyID: id, MaxConcurrency: 2,
			Proxy: &Proxy{ID: id, Name: "exit", Status: StatusActive},
		})
	}

	cache := &ticketAwareProxySlotCacheForTest{
		acquired: []bool{true},
		results:  []int64{2},
	}
	ctx := context.WithValue(context.Background(), ctxkey.Model, "gpt-6-astra")
	selected, release, acquired, err := NewConcurrencyService(cache).AcquireAccountProxySlot(ctx, account)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, release)
	require.NotNil(t, selected)
	require.NotNil(t, selected.RequestProxy)
	require.EqualValues(t, 2, selected.RequestProxy.ID)

	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Len(t, cache.calls, 1)
	require.Equal(t, []AccountProxySlotSpec{
		{ProxyID: 1, MaxConcurrency: 2},
		{ProxyID: 2, MaxConcurrency: 2},
		{ProxyID: 3, MaxConcurrency: 2},
	}, cache.calls[0])
}

func TestAcquireAccountProxySlotDoesNotRetryWithTicketFilteredSubset(t *testing.T) {
	now := time.Now()
	state := fakeCodexTicketState(292)
	account := ticketTestAccount(557)
	account.MultiProxyConfigured = true
	account.Extra = map[string]any{
		OpenAICodexTicketEnabledExtraKey: true,
		openAICodexTicketExtraKey("gpt-6-astra"): map[string]any{
			"account_id":  557,
			"model":       "gpt-6-astra",
			"state":       state,
			"length":      len(state),
			"captured_at": now,
			"expires_at":  now.Add(time.Hour),
			"probes": []map[string]any{
				{"proxy_id": 1, "valid": true, "observed_model": "gpt-5.6-luna"},
				{"proxy_id": 2, "valid": true, "observed_model": "gpt-6-astra"},
				{"proxy_id": 3, "valid": true, "observed_model": "gpt-5.6-luna"},
			},
		},
	}
	for id := int64(1); id <= 3; id++ {
		account.ProxyBindings = append(account.ProxyBindings, AccountProxyBinding{
			ProxyID: id, MaxConcurrency: 2,
			Proxy: &Proxy{ID: id, Name: "exit", Status: StatusActive},
		})
	}

	cache := &ticketAwareProxySlotCacheForTest{acquired: []bool{false}}
	ctx := context.WithValue(context.Background(), ctxkey.Model, "gpt-6-astra")
	selected, release, acquired, err := NewConcurrencyService(cache).AcquireAccountProxySlot(ctx, account)
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, selected)
	require.Nil(t, release)

	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Len(t, cache.calls, 1)
	require.Equal(t, []AccountProxySlotSpec{
		{ProxyID: 1, MaxConcurrency: 2},
		{ProxyID: 2, MaxConcurrency: 2},
		{ProxyID: 3, MaxConcurrency: 2},
	}, cache.calls[0])
}

func TestAcquireAccountProxySlotUsesAllExitsWhenNoExactModelWasObserved(t *testing.T) {
	now := time.Now()
	state := fakeCodexTicketState(292)
	account := ticketTestAccount(558)
	account.MultiProxyConfigured = true
	account.Extra = map[string]any{
		OpenAICodexTicketEnabledExtraKey: true,
		openAICodexTicketExtraKey("gpt-6-astra"): map[string]any{
			"account_id":  558,
			"model":       "gpt-6-astra",
			"state":       state,
			"length":      len(state),
			"captured_at": now,
			"expires_at":  now.Add(time.Hour),
			"probes": []map[string]any{
				{"proxy_id": 1, "valid": true, "observed_model": "gpt-5.6-luna"},
				{"proxy_id": 2, "valid": true, "observed_model": "gpt-5.6-luna"},
			},
		},
	}
	for id := int64(1); id <= 2; id++ {
		account.ProxyBindings = append(account.ProxyBindings, AccountProxyBinding{
			ProxyID: id, MaxConcurrency: 2,
			Proxy: &Proxy{ID: id, Name: "exit", Status: StatusActive},
		})
	}

	cache := &ticketAwareProxySlotCacheForTest{acquired: []bool{true}, results: []int64{1}}
	ctx := context.WithValue(context.Background(), ctxkey.Model, "gpt-6-astra")
	selected, release, acquired, err := NewConcurrencyService(cache).AcquireAccountProxySlot(ctx, account)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, selected)
	require.NotNil(t, release)
	require.EqualValues(t, 1, selected.RequestProxy.ID)

	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Len(t, cache.calls, 1)
	require.Equal(t, []AccountProxySlotSpec{
		{ProxyID: 1, MaxConcurrency: 2},
		{ProxyID: 2, MaxConcurrency: 2},
	}, cache.calls[0])
}

func TestOpenAICodexTicketIsStillSharedAcrossRequestProxies(t *testing.T) {
	account := ticketTestAccount(556)
	state := fakeCodexTicketState(292)
	account.Extra = map[string]any{
		OpenAICodexTicketEnabledExtraKey: true,
		openAICodexTicketExtraKey("gpt-6-astra"): map[string]any{
			"account_id":  556,
			"model":       "gpt-6-astra",
			"state":       state,
			"length":      len(state),
			"captured_at": time.Now(),
			"expires_at":  time.Now().Add(time.Hour),
		},
	}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 292, TTLSeconds: 3600}, nil)
	for _, proxyID := range []int64{1, 2, 3} {
		headers := make(http.Header)
		account.RequestProxy = &Proxy{ID: proxyID, Status: StatusActive}
		require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", headers))
		require.Equal(t, state, headers.Get(openAICodexTurnStateHeader))
	}
}
