package service

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSafeUpstreamURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"strips query", "https://api.anthropic.com/v1/messages?beta=true", "https://api.anthropic.com/v1/messages"},
		{"strips fragment", "https://api.openai.com/v1/responses#frag", "https://api.openai.com/v1/responses"},
		{"strips both", "https://host/path?token=secret#x", "https://host/path"},
		{"strips userinfo", "https://user:password@host:8443/path?token=secret#x", "https://host:8443/path"},
		{"no query or fragment", "https://host/path", "https://host/path"},
		{"empty string", "", ""},
		{"whitespace only", "  ", ""},
		{"query before fragment", "https://h/p?a=1#f", "https://h/p"},
		{"rejects relative URL", "/v1/responses?token=secret", ""},
		{"rejects non HTTP URL", "file:///tmp/token", ""},
		{"rejects malformed URL", "https://host/%zz?token=secret", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, safeUpstreamURL(tt.input))
		})
	}
}

func TestOpsProxyNameSanitizationPreservesLabelsAndDropsConnectionMaterial(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "XIASS execution node label", raw: "api2-takeover-egress", want: "api2-takeover-egress"},
		{name: "friendly label", raw: "US egress", want: "US egress"},
		{name: "URL with userinfo", raw: "socks5://user:pass@10.0.0.1:1080", want: opsProxyNameUnnamed},
		{name: "userinfo without scheme", raw: "user:pass@proxy.example.com:1080", want: opsProxyNameUnnamed},
		{name: "IPv4", raw: "10.0.0.1", want: opsProxyNameUnnamed},
		{name: "embedded IPv4", raw: "edge 10.0.0.1", want: opsProxyNameUnnamed},
		{name: "IPv4 and port", raw: "10.0.0.1:1080", want: opsProxyNameUnnamed},
		{name: "IPv6 and port", raw: "[::1]:1080", want: opsProxyNameUnnamed},
		{name: "hostname and port", raw: "proxy.example.com:1080", want: opsProxyNameUnnamed},
		{name: "hostname", raw: "proxy.example.com", want: opsProxyNameUnnamed},
		{name: "hostname and path", raw: "proxy.example.com/edge", want: opsProxyNameUnnamed},
		{name: "password assignment", raw: "password=secret", want: opsProxyNameUnnamed},
		{name: "username field", raw: "username alice", want: opsProxyNameUnnamed},
		{name: "token assignment", raw: "token: secret", want: opsProxyNameUnnamed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, sanitizeManagedOpsProxyName(tt.raw))
		})
	}
}

func TestOpenAIProxySnapshotUsesXIASSRequestEgressAndIsImmutable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	durableProxyID := int64(11)
	account := &Account{
		ID:       42,
		Platform: PlatformOpenAI,
		ProxyID:  &durableProxyID,
		Proxy: &Proxy{
			ID: 11, Name: "api-owner-egress", Protocol: "socks5", Host: "10.0.0.11", Port: 1080,
			Username: "owner-user", Password: "owner-secret",
		},
	}
	proxyURL := account.requestProxyURL()
	freezeOpenAIHTTPUpstreamProxy(c, account, proxyURL)

	account.Proxy.Name = "edited-after-dispatch"
	account.Proxy.Host = "203.0.113.99"
	appendOpenAIOpsUpstreamError(c, OpsUpstreamErrorEvent{
		AccountID: 42,
		Kind:      "request_error",
		Message:   "upstream failed",
	})

	raw, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].ProxyID)
	require.Equal(t, int64(11), *events[0].ProxyID)
	require.Equal(t, "api-owner-egress", events[0].ProxyName)

	encoded := marshalOpsUpstreamErrors(events)
	require.NotNil(t, encoded)
	for _, sensitive := range []string{
		proxyURL, "10.0.0.11", "10.0.0.22", "203.0.113.99",
		"owner-user", "owner-secret", "api2-user", "api2-secret",
	} {
		require.NotContains(t, *encoded, sensitive)
	}
}

func TestOpenAIProxySnapshotNormalizesHTTPDirectAndWSUnknown(t *testing.T) {
	gin.SetMode(gin.TestMode)

	httpContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	freezeOpenAIHTTPUpstreamProxy(httpContext, &Account{Platform: PlatformOpenAI}, "")
	appendOpenAIOpsUpstreamError(httpContext, OpsUpstreamErrorEvent{Kind: "request_error", Message: "failed"})
	httpEvents, ok := getOpsUpstreamEventsForTest(httpContext)
	require.True(t, ok)
	require.Nil(t, httpEvents[0].ProxyID)
	require.Equal(t, opsProxyNameDirect, httpEvents[0].ProxyName)

	wsContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	freezeOpenAIWSUpstreamProxy(wsContext, &Account{Platform: PlatformOpenAI}, "")
	appendOpenAIOpsUpstreamError(wsContext, OpsUpstreamErrorEvent{Kind: "ws_error", Message: "failed"})
	wsEvents, ok := getOpsUpstreamEventsForTest(wsContext)
	require.True(t, ok)
	require.Nil(t, wsEvents[0].ProxyID)
	require.Equal(t, opsProxyNameUnknown, wsEvents[0].ProxyName)
}

func TestOpenAIProxySnapshotKeepsFixedProxyIDButRedactsSensitiveName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	account := &Account{
		ID:       42,
		Platform: PlatformOpenAI,
		ProxyID:  func() *int64 { id := int64(22); return &id }(),
		Proxy: &Proxy{
			ID:       22,
			Name:     "socks5://api2-user:api2-secret@10.0.0.22:2080",
			Protocol: "socks5",
			Host:     "10.0.0.22",
			Port:     2080,
			Username: "api2-user",
			Password: "api2-secret",
		},
	}

	freezeOpenAIHTTPUpstreamProxy(c, account, account.requestProxyURL())
	appendOpenAIOpsUpstreamError(c, OpsUpstreamErrorEvent{Kind: "request_error", Message: "failed"})
	events, ok := getOpsUpstreamEventsForTest(c)
	require.True(t, ok)
	require.NotNil(t, events[0].ProxyID)
	require.Equal(t, int64(22), *events[0].ProxyID)
	require.Equal(t, opsProxyNameUnnamed, events[0].ProxyName)
	encoded := marshalOpsUpstreamErrors(events)
	require.NotNil(t, encoded)
	for _, sensitive := range []string{"10.0.0.22", "api2-user", "api2-secret", "socks5://"} {
		require.NotContains(t, *encoded, sensitive)
	}
}

func TestAppendOpsUpstreamErrorMaintainsBoundedTailAndCumulativeDropCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	total := opsUpstreamErrorsMaxEvents + 40
	for i := 0; i < total; i++ {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			AtUnixMs:           int64(i + 1),
			ProxyName:          opsProxyNameDirect,
			UpstreamStatusCode: 502,
			Message:            "attempt",
		})
	}

	events, ok := getOpsUpstreamEventsForTest(c)
	require.True(t, ok)
	require.Len(t, events, opsUpstreamErrorsMaxEvents)
	require.Equal(t, opsUpstreamErrorsMaxEvents, cap(events))
	require.Equal(t, 40, events[0].DroppedEarlierAttempts)
	require.Equal(t, int64(41), events[0].AtUnixMs)
	require.Equal(t, int64(total), events[len(events)-1].AtUnixMs)

	entry := &OpsInsertErrorLogInput{UpstreamErrors: events}
	require.NoError(t, SanitizeOpsUpstreamErrorsForQueue(entry))
	require.Nil(t, entry.UpstreamErrors)
	require.NotNil(t, entry.UpstreamErrorsJSON)
	sanitized, err := ParseOpsUpstreamErrors(*entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, sanitized, opsUpstreamErrorsMaxEvents)
	require.Equal(t, 40, sanitized[0].DroppedEarlierAttempts)
	require.Equal(t, int64(total), sanitized[len(sanitized)-1].AtUnixMs)
}

func TestNormalizeOpsUpstreamErrorsJSONPreservesLegacyFields(t *testing.T) {
	raw := `[{"at_unix_ms":1,"account_id":42,"xiass_execution_node_id":"api2","legacy_field":"keep","kind":"http_error"},{"at_unix_ms":2,"proxy_id":9,"proxy_name":"edge-9","kind":"failover"}]`

	normalized, err := normalizeOpsUpstreamErrorsJSON(raw)
	require.NoError(t, err)
	require.Contains(t, normalized, `"xiass_execution_node_id":"api2"`)
	require.Contains(t, normalized, `"legacy_field":"keep"`)
	require.True(t, strings.Index(normalized, `"account_id":42`) < strings.Index(normalized, `"legacy_field":"keep"`))

	events, err := ParseOpsUpstreamErrors(normalized)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Nil(t, events[0].ProxyID)
	require.Equal(t, opsProxyNameUnknown, events[0].ProxyName)
	require.NotNil(t, events[1].ProxyID)
	require.Equal(t, int64(9), *events[1].ProxyID)
	require.Equal(t, "edge-9", events[1].ProxyName)
}

func TestOpsServiceGetErrorLogByIDNormalizesLegacyProxyAttribution(t *testing.T) {
	repo := &opsRepoMock{
		GetErrorLogByIDFn: func(context.Context, int64) (*OpsErrorLogDetail, error) {
			return &OpsErrorLogDetail{UpstreamErrors: `[{"account_id":42,"kind":"http_error"}]`}, nil
		},
	}
	svc := NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	detail, err := svc.GetErrorLogByID(context.Background(), 1)
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.Contains(t, detail.UpstreamErrors, `"proxy_id":null`)
	require.Contains(t, detail.UpstreamErrors, `"proxy_name":"unknown"`)
}

func getOpsUpstreamEventsForTest(c *gin.Context) ([]*OpsUpstreamErrorEvent, bool) {
	raw, ok := c.Get(OpsUpstreamErrorsKey)
	if !ok {
		return nil, false
	}
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	return events, ok && len(events) > 0
}
