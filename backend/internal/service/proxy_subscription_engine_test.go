package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This opt-in test crosses the real process, parser, SOCKS listener, upstream
// proxy, and HTTP target; it never calls external providers or live accounts.
func TestProxySubscriptionRealEngineRestartAndRefresh(t *testing.T) {
	binary := os.Getenv("XIASS_PROXY_AGENT_INTEGRATION_BINARY")
	if binary == "" {
		t.Skip("set XIASS_PROXY_AGENT_INTEGRATION_BINARY to the locally built agent")
	}
	t.Setenv("XIASS_PROXY_AGENT_BINARY", binary)
	t.Setenv("XIASS_PROXY_AGENT_URL", "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "subscription-engine-ok")
	}))
	defer target.Close()
	first, firstCalls := subscriptionTunnelFixture(t, target.Listener.Addr().String())
	second, secondCalls := subscriptionTunnelFixture(t, target.Listener.Addr().String())
	var downloads atomic.Int32
	subscription := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		if r.URL.Path == "/first" {
			_, _ = io.WriteString(w, first+"#first-exit")
		} else {
			_, _ = io.WriteString(w, base64.StdEncoding.EncodeToString([]byte(second+"#second-exit")))
		}
	}))
	defer subscription.Close()

	svc, store, _, _ := subscriptionFixture()
	agent := newHTTPProxySubscriptionAgent()
	svc.agent = agent
	defer svc.Stop()
	sources := []ProxySubscriptionSource{
		{ID: "first", Name: "First", URL: subscription.URL + "/first"},
		{ID: "second", Name: "Second", URL: subscription.URL + "/second"},
	}
	preview, err := svc.Preview(ctx, sources)
	require.NoError(t, err)
	require.Len(t, preview.Nodes, 2)
	selected := []string{preview.Nodes[0].ID, preview.Nodes[1].ID}
	_, err = svc.Apply(ctx, sources, selected, preview.PreviewID)
	require.NoError(t, err)
	require.EqualValues(t, 2, downloads.Load(), "apply must use the exact preview snapshot")
	require.Len(t, store.proxies.rows, 2)
	before := maps.Clone(store.proxies.rows)
	assertTraffic := func() {
		t.Helper()
		for _, p := range store.proxies.rows {
			proxyURL, parseErr := url.Parse(p.URL())
			require.NoError(t, parseErr)
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			resp, requestErr := client.Get(target.URL)
			require.NoError(t, requestErr)
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			transport.CloseIdleConnections()
			require.NoError(t, readErr)
			require.Equal(t, "subscription-engine-ok", string(body))
		}
	}
	assertTraffic()
	require.EqualValues(t, 1, firstCalls.Load())
	require.EqualValues(t, 1, secondCalls.Load())

	// A crashed child gets a new bearer token, but the saved snapshot must work.
	agent.mu.Lock()
	child, childDone := agent.cmd, agent.done
	agent.mu.Unlock()
	require.NotNil(t, child)
	require.NoError(t, child.Process.Kill())
	<-childDone
	subscription.Close()
	require.NoError(t, svc.reconcile(ctx))
	assertTraffic()
	require.EqualValues(t, 2, downloads.Load())
	require.Equal(t, before, store.proxies.rows)
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		stopSignal := syscall.Signal(19) // Linux SIGSTOP; Darwin uses 17.
		if runtime.GOOS == "darwin" {
			stopSignal = syscall.Signal(17)
		}
		agent.mu.Lock()
		stalled := agent.cmd
		agent.mu.Unlock()
		require.NoError(t, stalled.Process.Signal(stopSignal))
		probeCtx, probeCancel := context.WithTimeout(ctx, 200*time.Millisecond)
		require.Error(t, agent.Health(probeCtx))
		probeCancel()
		require.NoError(t, svc.reconcile(ctx))
		assertTraffic()
		require.Equal(t, before, store.proxies.rows)
	}

	// Full service restart also restores without contacting the subscription.
	svc.Stop()
	restarted := NewProxySubscriptionService(store, &subscriptionTestCipher{}, nil)
	defer restarted.Stop()
	require.NoError(t, restarted.reconcile(ctx))
	assertTraffic()
	require.Equal(t, before, store.proxies.rows)
	_, err = restarted.Refresh(ctx)
	require.Error(t, err)
	assertTraffic()

	// Clearing sources must tolerate retained addresses of removed nodes.
	empty, err := restarted.Preview(ctx, nil)
	require.NoError(t, err)
	_, err = restarted.Apply(ctx, nil, nil, empty.PreviewID)
	require.Error(t, err, "imported proxies must be deleted through proxy management first")
	assertTraffic()
	for id := range store.proxies.rows {
		delete(store.proxies.rows, id)
	}
	_, err = restarted.Apply(ctx, nil, nil, empty.PreviewID)
	require.NoError(t, err)
	for _, p := range before {
		conn, dialErr := net.DialTimeout("tcp", net.JoinHostPort(p.Host, fmt.Sprint(p.Port)), time.Second)
		if conn != nil {
			_ = conn.Close()
		}
		require.Error(t, dialErr, "removed listener must be closed")
	}
}

func subscriptionTunnelFixture(t *testing.T, target string) (string, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != target {
			http.Error(w, "unexpected destination", http.StatusForbidden)
			return
		}
		upstream, err := net.DialTimeout("tcp", target, time.Second)
		if err != nil {
			http.Error(w, "target unavailable", http.StatusBadGateway)
			return
		}
		downstream, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		calls.Add(1)
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = buffered.Flush()
		go func() {
			_, _ = io.Copy(upstream, buffered)
			_ = upstream.Close()
		}()
		go func() {
			_, _ = io.Copy(downstream, upstream)
			_ = downstream.Close()
		}()
	}))
	t.Cleanup(server.Close)
	return server.URL, &calls
}
