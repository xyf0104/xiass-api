package service

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type usageEgressContextKey struct{}

const usageEgressSnapshotKey = "xiass_usage_egress"

type usageEgressSnapshot struct {
	accountID int64
	egress    *UsageEgress
}

func cloneUsageEgress(egress *UsageEgress) *UsageEgress {
	if egress == nil {
		return nil
	}
	copy := *egress
	if egress.IPObservedAt != nil {
		observed := *egress.IPObservedAt
		copy.IPObservedAt = &observed
	}
	return &copy
}

func newUsageEgress(account *Account, proxyURL string, directWhenEmpty bool) *UsageEgress {
	if account == nil {
		return nil
	}
	if strings.TrimSpace(proxyURL) == "" {
		if !directWhenEmpty {
			return nil
		}
		return &UsageEgress{Route: "direct", CapturedAt: time.Now()}
	}
	proxy := account.requestProxy()
	if proxy == nil || proxy.ID <= 0 || proxy.URL() != proxyURL {
		return nil
	}
	host := strings.TrimSpace(proxy.Host)
	address := ""
	if host != "" && !strings.ContainsAny(host, "/\\?#@ \t\r\n") && proxy.Port > 0 && proxy.Port <= 65535 {
		address = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(proxy.Port))
	}
	nodeID := ""
	name := sanitizeManagedOpsProxyName(proxy.Name)
	if strings.HasPrefix(proxy.Name, executionNodeBuiltinProxyNamePrefix) {
		candidate := strings.TrimPrefix(proxy.Name, executionNodeBuiltinProxyNamePrefix)
		if validExecutionNodeID(candidate) && proxy.Protocol == "socks5" && host == "127.0.0.1" && proxy.Port == 19080 && proxy.Username == candidate {
			nodeID = candidate
			name = executionNodeBuiltinProxyNamePrefix + candidate
		}
	}
	if name == opsProxyNameUnnamed {
		name = ""
	}
	return &UsageEgress{
		Route:           "proxy",
		ProxyID:         proxy.ID,
		ProxyName:       name,
		ProxyAddress:    address,
		ExecutionNodeID: nodeID,
		ProxyUpdatedAt:  proxy.UpdatedAt,
		CapturedAt:      time.Now(),
	}
}

func freezeUsageEgress(c *gin.Context, account *Account, proxyURL string, directWhenEmpty bool) {
	if c == nil || account == nil {
		return
	}
	c.Set(usageEgressSnapshotKey, usageEgressSnapshot{
		accountID: account.ID,
		egress:    newUsageEgress(account, proxyURL, directWhenEmpty),
	})
}

// UsageEgressContext must run before dispatch to the usage worker. It copies
// only the final attempt's scalar route, never the Gin context or an Account.
func UsageEgressContext(parent context.Context, c *gin.Context, account *Account) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	var egress *UsageEgress
	if account != nil {
		if c != nil {
			if value, ok := c.Get(usageEgressSnapshotKey); ok {
				if snapshot, ok := value.(usageEgressSnapshot); ok && snapshot.accountID == account.ID {
					return context.WithValue(parent, usageEgressContextKey{}, cloneUsageEgress(snapshot.egress))
				}
			}
		}
		// Shared HTTP gateways use the selected request account. For a WS
		// request with no captured route, an environment proxy may be in use.
		direct := c == nil || c.Request == nil || !strings.EqualFold(c.GetHeader("Upgrade"), "websocket")
		egress = newUsageEgress(account, account.requestProxyURL(), direct)
	}
	return context.WithValue(parent, usageEgressContextKey{}, egress)
}

func UsageEgressFromContext(ctx context.Context) *UsageEgress {
	if ctx == nil {
		return nil
	}
	egress, _ := ctx.Value(usageEgressContextKey{}).(*UsageEgress)
	return cloneUsageEgress(egress)
}

// WithUsageEgressContext installs an already captured scalar snapshot. A nil
// snapshot is meaningful: delayed legacy work must remain unattributed instead
// of inheriting the route of the request that happened to finalize it.
func WithUsageEgressContext(parent context.Context, egress *UsageEgress) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithValue(parent, usageEgressContextKey{}, cloneUsageEgress(egress))
}

func CopyUsageEgressContext(parent, target context.Context) context.Context {
	if target == nil {
		target = context.Background()
	}
	return context.WithValue(target, usageEgressContextKey{}, UsageEgressFromContext(parent))
}

// EnrichUsageEgress runs only in the usage writer, after inference. A cached
// probe is evidence of the last observed public IP, not a per-request IP test.
func EnrichUsageEgress(ctx context.Context, egress *UsageEgress, cache ProxyLatencyCache) *UsageEgress {
	result := cloneUsageEgress(egress)
	if result == nil || result.Route != "proxy" || result.ProxyID <= 0 || cache == nil || result.IPAddress != "" {
		return result
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	observations, err := cache.GetProxyLatencies(lookupCtx, []int64{result.ProxyID})
	if err != nil {
		return result
	}
	info := observations[result.ProxyID]
	if info == nil || !info.Success || info.UpdatedAt.IsZero() ||
		(!result.ProxyUpdatedAt.IsZero() && info.UpdatedAt.Before(result.ProxyUpdatedAt)) ||
		(!result.CapturedAt.IsZero() && info.UpdatedAt.After(result.CapturedAt)) {
		return result
	}
	if publicIP := net.ParseIP(strings.TrimSpace(info.IPAddress)); publicIP != nil {
		result.IPAddress = publicIP.String()
		observedAt := info.UpdatedAt
		result.IPObservedAt = &observedAt
	}
	return result
}
