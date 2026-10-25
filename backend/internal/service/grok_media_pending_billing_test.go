package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type grokVideoPendingBillingCache struct {
	GatewayCache
	payload []byte
}

func (c *grokVideoPendingBillingCache) SetGrokVideoPendingBilling(_ context.Context, _ string, payload []byte, _ time.Duration) error {
	c.payload = append([]byte(nil), payload...)
	return nil
}

func (c *grokVideoPendingBillingCache) GetGrokVideoPendingBilling(_ context.Context, _ string) ([]byte, error) {
	return append([]byte(nil), c.payload...), nil
}

func TestGrokVideoPendingBillingRoundTripsEgressCaptureTimestamps(t *testing.T) {
	cache := &grokVideoPendingBillingCache{}
	service := &OpenAIGatewayService{cache: cache}
	proxyUpdatedAt := time.Date(2026, 10, 24, 8, 0, 0, 123, time.UTC)
	capturedAt := proxyUpdatedAt.Add(2 * time.Minute)
	observedAt := proxyUpdatedAt.Add(30 * time.Second)
	pending := GrokVideoPendingBilling{
		Model: "grok-imagine-video",
		Egress: &UsageEgress{
			Route:          "proxy",
			ProxyID:        71,
			ProxyName:      "creation-exit",
			ProxyAddress:   "10.0.0.71:1080",
			IPAddress:      "198.51.100.71",
			IPObservedAt:   &observedAt,
			ProxyUpdatedAt: proxyUpdatedAt,
			CapturedAt:     capturedAt,
		},
	}

	require.NoError(t, service.StoreGrokVideoPendingBilling(context.Background(), "task", 10, 20, pending))
	loaded, err := service.LoadGrokVideoPendingBilling(context.Background(), "task", 10, 20)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Equal(t, pending.Egress, loaded.Egress)
	require.NotNil(t, loaded.EgressProxyUpdatedAt)
	require.Equal(t, proxyUpdatedAt, *loaded.EgressProxyUpdatedAt)
	require.NotNil(t, loaded.EgressCapturedAt)
	require.Equal(t, capturedAt, *loaded.EgressCapturedAt)
}

func TestGrokVideoPendingBillingLegacyPayloadKeepsEgressUnknown(t *testing.T) {
	cache := &grokVideoPendingBillingCache{payload: []byte(`{"model":"grok-imagine-video","created_at":"2026-10-24T08:00:00Z"}`)}
	service := &OpenAIGatewayService{cache: cache}

	loaded, err := service.LoadGrokVideoPendingBilling(context.Background(), "task", 10, 20)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Nil(t, loaded.Egress)
}
