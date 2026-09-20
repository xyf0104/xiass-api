package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestRequestProxyForLoggingPrefersRequestScopedProxy(t *testing.T) {
	durable := &service.Proxy{ID: 92, Name: "durable"}
	selected := &service.Proxy{ID: 85, Name: "selected"}
	account := &service.Account{ProxyID: &durable.ID, Proxy: durable, RequestProxy: selected}

	encoder := zapcore.NewMapObjectEncoder()
	for _, field := range appendRequestProxyLogFields(nil, account) {
		field.AddTo(encoder)
	}

	require.EqualValues(t, int64(85), encoder.Fields["proxy_id"])
	require.Equal(t, "selected", encoder.Fields["proxy_name"])
}

func TestRequestProxyForLoggingFallsBackToDurableProxy(t *testing.T) {
	durable := &service.Proxy{ID: 92, Name: "durable"}
	account := &service.Account{ProxyID: &durable.ID, Proxy: durable}

	encoder := zapcore.NewMapObjectEncoder()
	for _, field := range appendRequestProxyLogFields(nil, account) {
		field.AddTo(encoder)
	}

	require.EqualValues(t, int64(92), encoder.Fields["proxy_id"])
	require.Equal(t, "durable", encoder.Fields["proxy_name"])
}

func TestRequestProxyForLoggingDoesNotInventProxy(t *testing.T) {
	require.Empty(t, appendRequestProxyLogFields(nil, &service.Account{}))
}
