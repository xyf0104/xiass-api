package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseMessageTimeAcceptsProviderDateFormats(t *testing.T) {
	expected := time.Date(2026, time.September, 16, 7, 6, 30, 0, time.UTC)

	require.True(t, expected.Equal(parseMessageTime("2026-09-16T07:06:30Z")))
	require.True(t, expected.Equal(parseMessageTime("Wed, 16 Sep 2026 07:06:30 +0000")))
	require.True(t, expected.Equal(parseMessageTime("2026-09-16 07:06:30")))
	require.True(t, expected.Equal(parseMessageTime("1789542390")))
	require.True(t, expected.Equal(parseMessageTime("1789542390000")))
}

func TestMessagePredatesKeepsMessagesWithUnknownTimestamp(t *testing.T) {
	cutoff := time.Date(2026, time.September, 16, 7, 0, 0, 0, time.UTC)

	require.False(t, messagePredates("", cutoff))
	require.False(t, messagePredates("provider-specific-date", cutoff))
	require.False(t, messagePredates("2026-09-16T07:01:00Z", cutoff))
	require.True(t, messagePredates("2026-09-16T06:59:59Z", cutoff))
}
