package repository

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// networkLifecycleClient replaces only the pooled client's transport so these
// tests exercise the public HTTP-upstream entry points against local servers.
func networkLifecycleClient(t *testing.T, srv *httptest.Server, withTLS bool) (*httpUpstreamService, func(*http.Request) (*http.Response, error)) {
	t.Helper()
	s, ok := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.True(t, ok)

	profile := &tlsfingerprint.Profile{Name: "network-lifecycle-test"}
	var entry *upstreamClientEntry
	var err error
	if withTLS {
		entry, err = s.getClientEntryWithTLS("", 1, 1, profile, service.HTTPUpstreamProfileDefault, false, true)
	} else {
		entry, err = s.acquireClientWithProfile("", 1, 1, service.HTTPUpstreamProfileDefault)
		if err == nil {
			atomic.AddInt64(&entry.inFlight, -1)
		}
	}
	require.NoError(t, err)

	baseTransport, ok := srv.Client().Transport.(*http.Transport)
	require.True(t, ok)
	transport := baseTransport.Clone()
	transport.MaxConnsPerHost = 1
	transport.MaxIdleConnsPerHost = 1
	transport.IdleConnTimeout = 10 * time.Second
	entry.client = &http.Client{Transport: transport}
	t.Cleanup(transport.CloseIdleConnections)

	return s, func(req *http.Request) (*http.Response, error) {
		if withTLS {
			return s.DoWithTLS(req, "", 1, 1, profile)
		}
		return s.Do(req, "", 1, 1)
	}
}

func requireNetworkLifecycleIdle(t *testing.T, s *httpUpstreamService) {
	t.Helper()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.clients {
		require.Zero(t, atomic.LoadInt64(&entry.inFlight))
	}
}

type networkLifecycleNotifyReadCloser struct {
	io.ReadCloser
	started chan<- struct{}
	once    sync.Once
}

func (r *networkLifecycleNotifyReadCloser) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.ReadCloser.Read(p)
}

func TestHTTPUpstreamNetworkConcurrentEarlyClosePreservesPool(t *testing.T) {
	for _, withTLS := range []bool{false, true} {
		t.Run(fmt.Sprintf("tls=%v", withTLS), func(t *testing.T) {
			finishFirst := make(chan struct{})
			firstFinished := make(chan struct{})
			var requests atomic.Int32
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: begin\n\n")
				_ = http.NewResponseController(w).Flush()
				if requests.Add(1) == 1 {
					defer close(firstFinished)
					select {
					case <-finishFirst:
					case <-r.Context().Done():
						return
					}
				}
				_, _ = io.WriteString(w, "data: complete\n\n")
			}))
			if withTLS {
				srv.StartTLS()
			} else {
				srv.Start()
			}
			defer srv.Close()
			defer srv.CloseClientConnections()

			s, do := networkLifecycleClient(t, srv, withTLS)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
			require.NoError(t, err)
			resp, err := do(req)
			require.NoError(t, err)

			prefix := make([]byte, len("data: begin\n\n"))
			_, err = io.ReadFull(resp.Body, prefix)
			require.NoError(t, err)
			readerDone := make(chan struct{})
			readStarted := make(chan struct{})
			resp.Body = &networkLifecycleNotifyReadCloser{ReadCloser: resp.Body, started: readStarted}
			go func() {
				_, _ = io.Copy(io.Discard, resp.Body)
				close(readerDone)
			}()
			<-readStarted

			closed := make(chan error, 1)
			go func() { closed <- resp.Body.Close() }()
			select {
			case err := <-closed:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("Close blocked behind the active reader")
			}

			close(finishFirst)
			<-firstFinished
			for range 8 {
				resp, err := do(req)
				require.NoError(t, err)
				done := make(chan error, 1)
				go func() {
					_, copyErr := io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					done <- copyErr
				}()
				select {
				case err := <-done:
					require.NoError(t, err)
				case <-time.After(5 * time.Second):
					t.Fatal("response EOF waits for a subsequent request")
				}
			}
			select {
			case <-readerDone:
			case <-time.After(5 * time.Second):
				t.Fatal("early-closed reader leaked")
			}
			require.NoError(t, req.Context().Err(), "closing an attempt must not cancel its caller or retries")
			requireNetworkLifecycleIdle(t, s)
		})
	}
}

func TestHTTPUpstreamNetworkCompletedBodyPreservesHTTP1KeepAlive(t *testing.T) {
	for _, encoding := range []string{"", "gzip"} {
		t.Run("encoding="+encoding, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if encoding == "gzip" {
					w.Header().Set("Content-Encoding", encoding)
					zw := gzip.NewWriter(w)
					_, _ = io.WriteString(zw, "complete response")
					_ = zw.Close()
					return
				}
				_, _ = io.WriteString(w, "complete response")
			}))
			defer srv.Close()
			defer srv.CloseClientConnections()

			s, do := networkLifecycleClient(t, srv, false)
			for i := 0; i < 20; i++ {
				var reused bool
				ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
					GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
				})
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
				require.NoError(t, err)
				req.Header.Set("Accept-Encoding", encoding)
				resp, err := do(req)
				require.NoError(t, err)
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Equal(t, "complete response", string(body))

				var wg sync.WaitGroup
				for range 4 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_ = resp.Body.Close()
					}()
				}
				wg.Wait()
				if i > 0 {
					require.True(t, reused, "fully consumed responses must keep their connection")
				}
			}
			requireNetworkLifecycleIdle(t, s)
		})
	}
}

func TestHTTPUpstreamNetworkCompressedEarlyCloseStopsReader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		zw := gzip.NewWriter(w)
		_, _ = io.WriteString(zw, "data: begin\n\n")
		_ = zw.Flush()
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
		_ = zw.Close()
	}))
	defer srv.Close()
	defer srv.CloseClientConnections()

	s, do := networkLifecycleClient(t, srv, false)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := do(req)
	require.NoError(t, err)

	prefix := make([]byte, len("data: begin\n\n"))
	_, err = io.ReadFull(resp.Body, prefix)
	require.NoError(t, err)
	done := make(chan error, 1)
	readStarted := make(chan struct{})
	resp.Body = &networkLifecycleNotifyReadCloser{ReadCloser: resp.Body, started: readStarted}
	go func() {
		_, copyErr := io.Copy(io.Discard, resp.Body)
		done <- copyErr
	}()
	<-readStarted

	closed := make(chan error, 1)
	go func() { closed <- resp.Body.Close() }()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("compressed body Close blocked")
	}
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("compressed body reader leaked")
	}
	requireNetworkLifecycleIdle(t, s)
}

func TestHTTPUpstreamNetworkHTTP2CloseKeepsOtherStreamReusable(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/held" {
			w.WriteHeader(http.StatusOK)
			_ = http.NewResponseController(w).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "complete response")
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	defer srv.CloseClientConnections()

	s, do := networkLifecycleClient(t, srv, false)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/held", nil)
	require.NoError(t, err)
	held, err := do(req)
	require.NoError(t, err)
	require.Equal(t, 2, held.ProtoMajor)

	for i := 0; i < 2; i++ {
		var reused bool
		ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		require.NoError(t, err)
		resp, err := do(req)
		require.NoError(t, err)
		if i == 0 {
			require.NoError(t, held.Body.Close())
		}
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "complete response", string(body))
		require.NoError(t, resp.Body.Close())
		require.True(t, reused, "closing one HTTP/2 stream must retain the shared connection")
	}
	requireNetworkLifecycleIdle(t, s)
}

func TestHTTPUpstreamNetworkDetachedRequestDrainsAfterClientDisconnect(t *testing.T) {
	disconnectCtx, disconnect := context.WithCancel(t.Context())
	defer disconnect()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "begin ")
		_ = http.NewResponseController(w).Flush()
		<-disconnectCtx.Done()
		_, _ = io.WriteString(w, "usage and completion")
	}))
	defer srv.Close()
	defer srv.CloseClientConnections()

	s, do := networkLifecycleClient(t, srv, false)
	req, err := http.NewRequestWithContext(context.WithoutCancel(disconnectCtx), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := do(req)
	require.NoError(t, err)
	disconnect()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "begin usage and completion", string(body))
	require.NoError(t, resp.Body.Close())
	requireNetworkLifecycleIdle(t, s)
}
