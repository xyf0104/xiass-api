package handler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type openAIWSProxySlotCache struct {
	*concurrencyCacheMock

	mu              sync.Mutex
	selectedProxyID int64
	proxyAcquired   bool
	proxyErr        error
	proxyCalls      [][]service.AccountProxySlotSpec
	proxyReleases   int32
}

func (c *openAIWSProxySlotCache) AcquireAccountProxySlot(
	_ context.Context,
	_ int64,
	slots []service.AccountProxySlotSpec,
	_ string,
) (int64, bool, error) {
	c.mu.Lock()
	c.proxyCalls = append(c.proxyCalls, append([]service.AccountProxySlotSpec(nil), slots...))
	c.mu.Unlock()
	return c.selectedProxyID, c.proxyAcquired, c.proxyErr
}

func (c *openAIWSProxySlotCache) ReleaseAccountProxySlot(context.Context, int64, int64, string) error {
	atomic.AddInt32(&c.proxyReleases, 1)
	return nil
}

func (c *openAIWSProxySlotCache) GetAccountProxyConcurrency(context.Context, int64, []int64) (map[int64]int, error) {
	return map[int64]int{}, nil
}

func (c *openAIWSProxySlotCache) GetAccountsProxyConcurrency(context.Context, map[int64][]int64) (map[int64]map[int64]int, error) {
	return map[int64]map[int64]int{}, nil
}

func (c *openAIWSProxySlotCache) calls() [][]service.AccountProxySlotSpec {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([][]service.AccountProxySlotSpec, len(c.proxyCalls))
	for i := range c.proxyCalls {
		result[i] = append([]service.AccountProxySlotSpec(nil), c.proxyCalls[i]...)
	}
	return result
}

func newOpenAIWSMultiProxyAccount() *service.Account {
	proxyOne := &service.Proxy{ID: 101, Status: service.StatusActive, Protocol: "socks5", Host: "127.0.0.1", Port: 1101}
	proxyTwo := &service.Proxy{ID: 102, Status: service.StatusActive, Protocol: "socks5", Host: "127.0.0.1", Port: 1102}
	return &service.Account{
		ID:                   77,
		Concurrency:          5,
		MultiProxyConfigured: true,
		ProxyBindings: []service.AccountProxyBinding{
			{ProxyID: proxyOne.ID, MaxConcurrency: 2, Proxy: proxyOne},
			{ProxyID: proxyTwo.ID, MaxConcurrency: 3, Proxy: proxyTwo},
		},
	}
}

func newOpenAIWSProxyTestHandler(cache *openAIWSProxySlotCache) *OpenAIGatewayHandler {
	return &OpenAIGatewayHandler{
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Second),
	}
}

func TestOpenAIWSAccountMaxConcurrencyKeepsAggregateCapacity(t *testing.T) {
	account := newOpenAIWSMultiProxyAccount()
	account.RequestProxy = account.ProxyBindings[1].Proxy
	account.RequestProxyMaxConcurrency = 3
	account.Concurrency = 3

	require.Equal(t, 5, openAIWSAccountMaxConcurrency(account, nil))
	require.Equal(t, 7, openAIWSAccountMaxConcurrency(account, &service.AccountWaitPlan{MaxConcurrency: 7}))
}

func TestBindOpenAIWSAccountProxyLeaseBindsWaitPlanFirstTurn(t *testing.T) {
	cache := &openAIWSProxySlotCache{
		concurrencyCacheMock: &concurrencyCacheMock{},
		selectedProxyID:      102,
		proxyAcquired:        true,
	}
	handler := newOpenAIWSProxyTestHandler(cache)
	account := newOpenAIWSMultiProxyAccount()
	var aggregateReleases int32

	bound, release, err := handler.bindOpenAIWSAccountProxyLease(context.Background(), account, func() {
		atomic.AddInt32(&aggregateReleases, 1)
	}, false)
	require.NoError(t, err)
	require.NotNil(t, bound)
	require.NotNil(t, bound.RequestProxy)
	require.EqualValues(t, 102, bound.RequestProxy.ID)
	require.Equal(t, 3, bound.Concurrency)
	require.Len(t, cache.calls(), 1)
	require.Equal(t, []service.AccountProxySlotSpec{
		{ProxyID: 101, MaxConcurrency: 2},
		{ProxyID: 102, MaxConcurrency: 3},
	}, cache.calls()[0])

	release()
	release()
	require.Equal(t, int32(1), atomic.LoadInt32(&aggregateReleases))
	require.Equal(t, int32(1), atomic.LoadInt32(&cache.proxyReleases))
}

func TestBindOpenAIWSAccountProxyLeaseReacquiresFixedExit(t *testing.T) {
	cache := &openAIWSProxySlotCache{
		concurrencyCacheMock: &concurrencyCacheMock{},
		selectedProxyID:      102,
		proxyAcquired:        true,
	}
	handler := newOpenAIWSProxyTestHandler(cache)
	account := newOpenAIWSMultiProxyAccount()
	account.RequestProxy = account.ProxyBindings[1].Proxy
	account.RequestProxyMaxConcurrency = 3
	account.Concurrency = 3
	var aggregateReleases int32

	bound, release, err := handler.bindOpenAIWSAccountProxyLease(context.Background(), account, func() {
		atomic.AddInt32(&aggregateReleases, 1)
	}, true)
	require.NoError(t, err)
	require.NotNil(t, bound.RequestProxy)
	require.EqualValues(t, 102, bound.RequestProxy.ID)
	require.Equal(t, [][]service.AccountProxySlotSpec{{{ProxyID: 102, MaxConcurrency: 3}}}, cache.calls())

	release()
	require.Equal(t, int32(1), atomic.LoadInt32(&aggregateReleases))
	require.Equal(t, int32(1), atomic.LoadInt32(&cache.proxyReleases))
}

func TestBindOpenAIWSAccountProxyLeaseFailureReleasesAggregateSlot(t *testing.T) {
	cache := &openAIWSProxySlotCache{
		concurrencyCacheMock: &concurrencyCacheMock{},
		selectedProxyID:      102,
		proxyAcquired:        false,
	}
	handler := newOpenAIWSProxyTestHandler(cache)
	account := newOpenAIWSMultiProxyAccount()
	account.RequestProxy = account.ProxyBindings[1].Proxy
	account.RequestProxyMaxConcurrency = 3
	account.Concurrency = 3
	var aggregateReleases int32

	bound, release, err := handler.bindOpenAIWSAccountProxyLease(context.Background(), account, func() {
		atomic.AddInt32(&aggregateReleases, 1)
	}, true)
	require.Error(t, err)
	require.Nil(t, bound)
	require.Nil(t, release)
	require.Equal(t, int32(1), atomic.LoadInt32(&aggregateReleases))
	require.Zero(t, atomic.LoadInt32(&cache.proxyReleases))
}

func TestBindOpenAIWSAccountProxyLeaseRejectsMissingFixedExit(t *testing.T) {
	cache := &openAIWSProxySlotCache{
		concurrencyCacheMock: &concurrencyCacheMock{},
		selectedProxyID:      102,
		proxyAcquired:        true,
	}
	handler := newOpenAIWSProxyTestHandler(cache)
	account := newOpenAIWSMultiProxyAccount()
	var aggregateReleases int32

	bound, release, err := handler.bindOpenAIWSAccountProxyLease(context.Background(), account, func() {
		atomic.AddInt32(&aggregateReleases, 1)
	}, true)
	require.Error(t, err)
	require.Nil(t, bound)
	require.Nil(t, release)
	require.Equal(t, int32(1), atomic.LoadInt32(&aggregateReleases))
	require.Empty(t, cache.calls())
	require.Zero(t, atomic.LoadInt32(&cache.proxyReleases))
}

func TestBindOpenAIWSAccountProxyLeaseDoesNotDoubleAcquireInitialBoundExit(t *testing.T) {
	cache := &openAIWSProxySlotCache{
		concurrencyCacheMock: &concurrencyCacheMock{},
		selectedProxyID:      102,
		proxyAcquired:        true,
	}
	handler := newOpenAIWSProxyTestHandler(cache)
	account := newOpenAIWSMultiProxyAccount()
	account.RequestProxy = account.ProxyBindings[1].Proxy
	account.RequestProxyMaxConcurrency = 3
	account.Concurrency = 3
	var aggregateReleases int32

	bound, release, err := handler.bindOpenAIWSAccountProxyLease(context.Background(), account, func() {
		atomic.AddInt32(&aggregateReleases, 1)
	}, false)
	require.NoError(t, err)
	require.Same(t, account, bound)
	require.Empty(t, cache.calls())

	release()
	require.Equal(t, int32(1), atomic.LoadInt32(&aggregateReleases))
	require.Zero(t, atomic.LoadInt32(&cache.proxyReleases))
}
