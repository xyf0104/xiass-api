package service

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type ProxyTrafficNode struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Protocol      string    `json:"protocol"`
	ProxyIDs      []int64   `json:"proxy_ids"`
	StartedAt     time.Time `json:"started_at"`
	UploadBytes   int64     `json:"upload_bytes"`
	DownloadBytes int64     `json:"download_bytes"`
	TotalBytes    int64     `json:"total_bytes"`
}

type ProxyTrafficReport struct {
	StartedAt     time.Time          `json:"started_at"`
	PeriodStart   time.Time          `json:"period_start"`
	PeriodEnd     time.Time          `json:"period_end"`
	LastSavedAt   *time.Time         `json:"last_saved_at"`
	PersistenceOK bool               `json:"persistence_ok"`
	Days          int                `json:"days"`
	Nodes         []ProxyTrafficNode `json:"nodes"`
}

func (a *httpProxySubscriptionAgent) Traffic(ctx context.Context, days int) (*ProxyTrafficReport, error) {
	// A stats read must never restart a busy proxy engine on timeout.
	a.mu.Lock()
	base, token := a.baseURL, a.token
	a.mu.Unlock()
	if base == "" {
		return nil, proxyAgentUnavailable()
	}
	var report ProxyTrafficReport
	if err := a.request(ctx, base, token, http.MethodGet, "/v1/traffic?days="+strconv.Itoa(days), nil, &report); err != nil {
		return nil, err
	}
	return &report, nil
}

// Traffic returns node totals. Aliased proxy rows sharing the same listener
// refer to the same counter; this is deliberately not account-attributed usage.
func (s *ProxySubscriptionService) Traffic(ctx context.Context, days int, proxies []Proxy) (*ProxyTrafficReport, error) {
	if days != 1 && days != 7 && days != 30 {
		return nil, infraerrors.BadRequest("INVALID_DAYS", "days must be 1, 7 or 30")
	}
	agent, ok := s.agent.(interface {
		Traffic(context.Context, int) (*ProxyTrafficReport, error)
	})
	if !ok {
		return nil, proxyAgentUnavailable()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c, _, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	report, err := agent.Traffic(ctx, days)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("PROXY_TRAFFIC_UNAVAILABLE", "Node traffic statistics are unavailable; proxy routing is unchanged")
	}
	proxyIDs := make(map[string][]int64)
	for _, p := range proxies {
		ip := net.ParseIP(p.Host)
		if ip == nil || !ip.IsLoopback() || (p.Protocol != "socks5" && p.Protocol != "socks5h") {
			continue
		}
		address := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
		proxyIDs[address] = append(proxyIDs[address], p.ID)
	}
	for i := range report.Nodes {
		n := &report.Nodes[i]
		n.ProxyIDs = append([]int64{}, proxyIDs[c.Addresses[n.ID]]...)
	}
	if report.Nodes == nil {
		report.Nodes = []ProxyTrafficNode{}
	}
	return report, nil
}
