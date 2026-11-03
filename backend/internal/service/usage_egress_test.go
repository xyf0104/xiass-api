package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageEgressFreezesSelectedMultiProxyAndFinalAttempt(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	defaultID := int64(1)
	account := &Account{ID: 42, Platform: PlatformOpenAI, ProxyID: &defaultID,
		Proxy:        &Proxy{ID: 1, Name: "default", Protocol: "socks5", Host: "default.test", Port: 1080},
		RequestProxy: &Proxy{ID: 2, Name: "selected", Protocol: "socks5", Host: "2001:db8::2", Port: 1080, Username: "private-user", Password: "private-password"},
	}
	freezeOpenAIHTTPUpstreamProxy(c, account, account.requestProxyURL())
	first := UsageEgressContext(context.Background(), c, account)
	account.RequestProxy = &Proxy{ID: 3, Name: "successful", Protocol: "http", Host: "proxy.test", Port: 3128}
	freezeOpenAIHTTPUpstreamProxy(c, account, account.requestProxyURL())
	final := UsageEgressContext(context.Background(), c, account)
	account.RequestProxy.Name = "changed after request"

	firstRoute := UsageEgressFromContext(first)
	require.Equal(t, int64(2), firstRoute.ProxyID)
	require.Equal(t, "[2001:db8::2]:1080", firstRoute.ProxyAddress)
	require.Empty(t, firstRoute.IPAddress)
	finalRoute := UsageEgressFromContext(final)
	require.Equal(t, int64(3), finalRoute.ProxyID)
	require.Equal(t, "successful", finalRoute.ProxyName)
	raw, err := json.Marshal(firstRoute)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-user")
	require.NotContains(t, string(raw), "private-password")
	require.NotContains(t, string(raw), "CapturedAt")
	finalRoute.ProxyName = "modified copy"
	require.Equal(t, "successful", UsageEgressFromContext(final).ProxyName)
}

func TestUsageEgressDoesNotReuseOtherAccountOrUnknownWSRoute(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	var nilParent context.Context
	first := &Account{ID: 1, Platform: PlatformOpenAI, RequestProxy: &Proxy{ID: 4, Name: "first", Protocol: "http", Host: "proxy.test", Port: 3128}}
	freezeOpenAIHTTPUpstreamProxy(c, first, first.requestProxyURL())
	second := &Account{ID: 2, Platform: PlatformOpenAI}
	require.Equal(t, "direct", UsageEgressFromContext(UsageEgressContext(nilParent, c, second)).Route)
	freezeOpenAIWSUpstreamProxy(c, second, "")
	require.Nil(t, UsageEgressFromContext(UsageEgressContext(nilParent, c, second)))
	freezeOpenAIHTTPUpstreamProxy(c, second, "")
	require.Equal(t, "direct", UsageEgressFromContext(UsageEgressContext(nilParent, c, second)).Route)
	require.Nil(t, UsageEgressFromContext(context.Background()))
}

func TestUsageEgressCopiesScalarContextAndRejectsUnsafeAddress(t *testing.T) {
	account := &Account{ID: 9, RequestProxy: &Proxy{ID: 7, Name: "password=secret", Protocol: "http", Host: "user:password@host.test", Port: 3128}}
	var nilParent context.Context
	ctx := UsageEgressContext(nilParent, nil, account)
	copy := CopyUsageEgressContext(ctx, context.Background())
	account.RequestProxy.ID = 100
	route := UsageEgressFromContext(copy)
	require.Equal(t, int64(7), route.ProxyID)
	require.Empty(t, route.ProxyAddress)
	require.NotContains(t, route.ProxyName, "secret")
	require.Nil(t, newUsageEgress(account, "http://different.test:80", true))
}

func TestUsageEgressRecordsSelectedAccountProxy(t *testing.T) {
	proxyID := int64(85)
	account := &Account{ID: 42, ProxyID: &proxyID,
		RequestProxy: &Proxy{ID: proxyID, Name: "selected-account-proxy", Protocol: "socks5", Host: "127.0.0.1", Port: 19080, Username: "private-user", Password: "private"},
	}
	egress := newUsageEgress(account, account.requestProxyURL(), true)
	require.Equal(t, "proxy", egress.Route)
	require.Equal(t, proxyID, egress.ProxyID)
	require.Equal(t, "selected-account-proxy", egress.ProxyName)
	require.Equal(t, "127.0.0.1:19080", egress.ProxyAddress)
	raw, err := json.Marshal(egress)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "execution_node_id")

	account.RequestProxy = nil
	require.Equal(t, "direct", newUsageEgress(account, "", true).Route)
}

type usageEgressProbeCache struct {
	info  *ProxyLatencyInfo
	err   error
	calls int
}

func (c *usageEgressProbeCache) GetProxyLatencies(ctx context.Context, ids []int64) (map[int64]*ProxyLatencyInfo, error) {
	c.calls++
	return map[int64]*ProxyLatencyInfo{ids[0]: c.info}, c.err
}

func (c *usageEgressProbeCache) SetProxyLatency(context.Context, int64, *ProxyLatencyInfo) error {
	return nil
}

func TestUsageEgressEnrichmentUsesOnlyValidPriorProbe(t *testing.T) {
	now := time.Now()
	route := &UsageEgress{Route: "proxy", ProxyID: 7, ProxyUpdatedAt: now.Add(-time.Hour), CapturedAt: now}
	for _, tc := range []struct {
		name string
		info *ProxyLatencyInfo
		err  error
		want string
	}{
		{"ipv4", &ProxyLatencyInfo{Success: true, IPAddress: "203.0.113.10", UpdatedAt: now.Add(-time.Minute)}, nil, "203.0.113.10"},
		{"ipv6", &ProxyLatencyInfo{Success: true, IPAddress: "2001:db8::7", UpdatedAt: now.Add(-time.Minute)}, nil, "2001:db8::7"},
		{"failed probe", &ProxyLatencyInfo{IPAddress: "203.0.113.10", UpdatedAt: now.Add(-time.Minute)}, nil, ""},
		{"old configuration", &ProxyLatencyInfo{Success: true, IPAddress: "203.0.113.10", UpdatedAt: now.Add(-2 * time.Hour)}, nil, ""},
		{"later probe", &ProxyLatencyInfo{Success: true, IPAddress: "203.0.113.10", UpdatedAt: now.Add(time.Minute)}, nil, ""},
		{"no timestamp", &ProxyLatencyInfo{Success: true, IPAddress: "203.0.113.10"}, nil, ""},
		{"bad IP", &ProxyLatencyInfo{Success: true, IPAddress: "secret@host", UpdatedAt: now.Add(-time.Minute)}, nil, ""},
		{"cache failure", nil, errors.New("redis unavailable"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &usageEgressProbeCache{info: tc.info, err: tc.err}
			got := EnrichUsageEgress(context.Background(), route, cache)
			require.Equal(t, tc.want, got.IPAddress)
			require.Equal(t, int64(7), got.ProxyID)
			require.Empty(t, route.IPAddress)
			if tc.want != "" {
				require.Equal(t, tc.info.UpdatedAt, *got.IPObservedAt)
			}
		})
	}
	cache := &usageEgressProbeCache{}
	require.Nil(t, EnrichUsageEgress(context.Background(), nil, cache))
	require.Equal(t, "direct", EnrichUsageEgress(context.Background(), &UsageEgress{Route: "direct"}, cache).Route)
	require.Zero(t, cache.calls)
}
