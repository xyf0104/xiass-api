package repository

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type lifecycleRoundTripFunc func(*http.Request) (*http.Response, error)

func (f lifecycleRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type contextBlockedResponseBody struct {
	ctx         context.Context
	readStarted chan struct{}
	startOnce   sync.Once
	closed      chan struct{}
	closeOnce   sync.Once
}

func (b *contextBlockedResponseBody) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.readStarted) })
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func (b *contextBlockedResponseBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestDoUpstreamRequestCancelsAttemptBeforeClosingActiveBody(t *testing.T) {
	readStarted := make(chan struct{})
	bodyClosed := make(chan struct{})
	client := &http.Client{Transport: lifecycleRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: &contextBlockedResponseBody{
				ctx:         req.Context(),
				readStarted: readStarted,
				closed:      bodyClosed,
			},
			Request: req,
		}, nil
	})}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://upstream.example/v1/responses", nil)
	require.NoError(t, err)

	resp, err := doUpstreamRequest(client, req)
	require.NoError(t, err)

	readDone := make(chan error, 1)
	go func() {
		_, readErr := resp.Body.Read(make([]byte, 1))
		readDone <- readErr
	}()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("response body read did not start")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- resp.Body.Close() }()

	select {
	case closeErr := <-closeDone:
		require.NoError(t, closeErr)
	case <-time.After(time.Second):
		t.Fatal("response body Close blocked behind an active read")
	}
	require.ErrorIs(t, <-readDone, context.Canceled)
	select {
	case <-bodyClosed:
	default:
		t.Fatal("underlying response body was not closed")
	}
	require.ErrorIs(t, func() error {
		_, readErr := resp.Body.Read(make([]byte, 1))
		return readErr
	}(), http.ErrBodyReadAfterClose)
	require.NoError(t, resp.Body.Close(), "Close must remain idempotent")
}

var _ io.ReadCloser = (*contextBlockedResponseBody)(nil)
