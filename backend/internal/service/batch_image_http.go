package service

import (
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
)

// batchImageAccountProxyURL resolves the durable egress attached to an account.
// A missing proxy is valid for legacy single-node accounts, but a dangling or
// inactive proxy ID must never silently turn into a direct request.
func batchImageAccountProxyURL(account *Account) (string, error) {
	if account == nil {
		return "", nil
	}
	proxy := account.requestProxy()
	if proxy == nil {
		if account.ProxyID != nil || account.MultiProxyConfigured {
			return "", ErrBatchImageProviderEgressUnavailable
		}
		return "", nil
	}
	if proxy.ID <= 0 || !proxy.IsActive() || proxy.IsExpired(time.Now()) {
		return "", ErrBatchImageProviderEgressUnavailable
	}
	return proxy.URL(), nil
}

func newBatchImageHTTPClient(proxyURL string) (*http.Client, error) {
	return httpclient.GetClient(httpclient.Options{
		ProxyURL:              strings.TrimSpace(proxyURL),
		ResponseHeaderTimeout: 60 * time.Second,
	})
}
