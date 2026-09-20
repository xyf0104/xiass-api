package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type httpProxySubscriptionAgent struct {
	mu       sync.Mutex
	baseURL  string
	token    string
	client   *http.Client
	cmd      *exec.Cmd
	done     chan struct{}
	external bool
	closed   bool
}

func newHTTPProxySubscriptionAgent() *httpProxySubscriptionAgent {
	return &httpProxySubscriptionAgent{client: &http.Client{
		Timeout:       3 * time.Minute,
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, ResponseHeaderTimeout: 3 * time.Minute},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("proxy agent redirect rejected") },
	}}
}

func proxyAgentUnavailable() error {
	return infraerrors.ServiceUnavailable("PROXY_AGENT_UNAVAILABLE", "XIASS subscription engine is unavailable; install the matching xiass-proxy-agent binary")
}

func (a *httpProxySubscriptionAgent) ensure(ctx context.Context) (string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", "", proxyAgentUnavailable()
	}
	if a.external {
		return a.baseURL, a.token, nil
	}
	if a.cmd != nil {
		select {
		case <-a.done:
			a.cmd = nil
		default:
			return a.baseURL, a.token, nil
		}
	}
	if raw := strings.TrimSpace(os.Getenv("XIASS_PROXY_AGENT_URL")); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
			return "", "", proxyAgentUnavailable()
		}
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return "", "", proxyAgentUnavailable()
		}
		data, err := os.ReadFile(os.Getenv("XIASS_PROXY_AGENT_TOKEN_FILE"))
		if err != nil || strings.TrimSpace(string(data)) == "" {
			return "", "", proxyAgentUnavailable()
		}
		a.baseURL, a.token, a.external = strings.TrimRight(raw, "/"), strings.TrimSpace(string(data)), true
		return a.baseURL, a.token, nil
	}
	path := strings.TrimSpace(os.Getenv("XIASS_PROXY_AGENT_BINARY"))
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", "", proxyAgentUnavailable()
		}
		name := "xiass-proxy-agent"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path = filepath.Join(filepath.Dir(exe), name)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", proxyAgentUnavailable()
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", proxyAgentUnavailable()
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", proxyAgentUnavailable()
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	a.baseURL, a.token = "http://"+addr, hex.EncodeToString(secret)
	// The executable is the installed sibling or operator-controlled process
	// environment, never a subscription URL or an HTTP request parameter.
	cmd := exec.Command(path, "-listen", addr) // #nosec G702 -- trusted operator executable path; no shell invocation.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "XIASS_PROXY_AGENT_TOKEN=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "XIASS_PROXY_AGENT_TOKEN="+a.token)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Start(); err != nil {
		return "", "", proxyAgentUnavailable()
	}
	a.cmd, a.done = cmd, make(chan struct{})
	done := a.done
	go func() { _ = cmd.Wait(); close(done) }()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-done:
			a.cmd = nil
			return "", "", proxyAgentUnavailable()
		case <-deadline.C:
			_ = cmd.Process.Kill()
			return "", "", proxyAgentUnavailable()
		case <-ticker.C:
			probe, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
			err := a.request(probe, a.baseURL, a.token, http.MethodGet, "/health", nil, nil)
			cancel()
			if err == nil {
				return a.baseURL, a.token, nil
			}
		}
	}
}

func (a *httpProxySubscriptionAgent) request(ctx context.Context, baseURL, token, method, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return proxyAgentUnavailable()
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return proxyAgentUnavailable()
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (40<<20)+1))
	if err != nil || len(data) > 40<<20 {
		return errors.New("invalid proxy agent response")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var message struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &message) == nil && message.Error != "" {
			return infraerrors.BadRequest("PROXY_SUBSCRIPTION_FAILED", message.Error)
		}
		return proxyAgentUnavailable()
	}
	if out != nil && json.Unmarshal(data, out) != nil {
		return errors.New("invalid proxy agent response")
	}
	return nil
}

func (a *httpProxySubscriptionAgent) do(ctx context.Context, method, path string, payload, out any) error {
	base, token, err := a.ensure(ctx)
	if err != nil {
		return err
	}
	err = a.request(ctx, base, token, method, path, payload, out)
	if err != nil {
		a.restartUnresponsiveChild(base, token)
	}
	return err
}

func (a *httpProxySubscriptionAgent) restartUnresponsiveChild(base, token string) {
	probe, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if a.request(probe, base, token, http.MethodGet, "/health", nil, nil) == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.external || a.cmd == nil || a.baseURL != base || a.token != token {
		return
	}
	_ = a.cmd.Process.Kill()
	<-a.done
	a.cmd = nil
}

func (a *httpProxySubscriptionAgent) Health(ctx context.Context) error {
	return a.do(ctx, http.MethodGet, "/health", nil, nil)
}
func (a *httpProxySubscriptionAgent) Preview(ctx context.Context, sources []ProxySubscriptionSource) (*proxySubscriptionAgentResult, error) {
	var result proxySubscriptionAgentResult
	err := a.do(ctx, http.MethodPost, "/v1/subscriptions/preview", map[string]any{"sources": sources}, &result)
	return &result, err
}
func (a *httpProxySubscriptionAgent) Sync(ctx context.Context, req proxySubscriptionAgentSync) (*proxySubscriptionAgentResult, error) {
	var result proxySubscriptionAgentResult
	err := a.do(ctx, http.MethodPost, "/v1/subscriptions/sync", req, &result)
	return &result, err
}

func (a *httpProxySubscriptionAgent) Close() {
	a.mu.Lock()
	a.closed = true
	cmd, done := a.cmd, a.done
	a.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	a.client.CloseIdleConnections()
}
