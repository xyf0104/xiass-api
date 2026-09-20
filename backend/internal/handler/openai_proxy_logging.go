package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

// requestProxyForLogging mirrors the request-time routing precedence without
// exposing credentials: a multi-proxy request must be attributed to its
// selected RequestProxy, not to the account's durable default proxy.
func requestProxyForLogging(account *service.Account) *service.Proxy {
	if account == nil {
		return nil
	}
	if account.RequestProxy != nil {
		return account.RequestProxy
	}
	if account.ProxyID == nil {
		return nil
	}
	return account.Proxy
}

func appendRequestProxyLogFields(fields []zap.Field, account *service.Account) []zap.Field {
	proxy := requestProxyForLogging(account)
	if proxy == nil {
		return fields
	}
	return append(fields,
		zap.Int64("proxy_id", proxy.ID),
		zap.String("proxy_name", proxy.Name),
		zap.String("proxy_host", proxy.Host),
		zap.Int("proxy_port", proxy.Port),
	)
}
