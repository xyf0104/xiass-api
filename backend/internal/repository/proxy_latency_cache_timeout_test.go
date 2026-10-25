package repository

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestProxyLatencyCacheHonorsShortLookupDeadlineWithStalledRedis(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()
	client := redis.NewClient(&redis.Options{
		Addr: listener.Addr().String(), MaxRetries: -1,
		ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
	})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = NewProxyLatencyCache(client).GetProxyLatencies(ctx, []int64{84})
	require.Error(t, err)
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, 3*time.Second, client.Options().ReadTimeout)
}
