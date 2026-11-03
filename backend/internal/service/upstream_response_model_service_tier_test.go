package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamResponseModelObserverCapturesServiceTier(t *testing.T) {
	observer := &upstreamResponseModelObserver{}
	observer.ObserveOpenAI(
		[]byte(`{"type":"response.completed","response":{"model":"gpt-5.6-sol","service_tier":"default"}}`),
		"response.completed",
	)
	require.Equal(t, "default", observer.ServiceTier())

	observer = &upstreamResponseModelObserver{}
	observer.ObserveAnthropic(
		[]byte(`{"message":{"model":"claude-fable-5-1","usage":{"speed":"fast"}}}`),
	)
	require.Equal(t, "fast", observer.ServiceTier())
}

func TestUpstreamResponseModelObserverRejectsConflictingNonTerminalTier(t *testing.T) {
	observer := &upstreamResponseModelObserver{}
	observer.ObserveServiceTier("standard", false)
	observer.ObserveServiceTier("fast", false)
	require.Empty(t, observer.ServiceTier())
}

func TestUpstreamResponseModelObserverPreservesUltrafast(t *testing.T) {
	for _, eventType := range []string{"", "response.completed", "response.done"} {
		t.Run(eventType, func(t *testing.T) {
			observer := &upstreamResponseModelObserver{}
			observer.ObserveOpenAI([]byte(`{"model":"gpt-5.5","service_tier":" UltraFast "}`), eventType)
			require.Equal(t, "ultrafast", observer.ServiceTier())
		})
	}
	observer := &upstreamResponseModelObserver{}
	observer.ObserveOpenAI([]byte(`{"model":"gpt-5.5","service_tier":"priority"}`), "")
	observer.ObserveOpenAI([]byte(`{"type":"response.completed","response":{"model":"gpt-5.5","service_tier":"ultrafast"}}`), "response.completed")
	observer.ObserveOpenAI([]byte(`{"model":"gpt-5.5","service_tier":"default"}`), "")
	require.Equal(t, "ultrafast", observer.ServiceTier(), "terminal declaration wins")

	for _, payload := range []string{
		`{"service_tier":"ultrafast"}`,
		`{"model":"gpt-5.5","service_tier":"unknown"}`,
		`{"model":"gpt-5.5","service_tier":true}`,
	} {
		observer := &upstreamResponseModelObserver{}
		observer.ObserveOpenAI([]byte(payload), "response.completed")
		require.Empty(t, observer.ServiceTier())
	}
}
