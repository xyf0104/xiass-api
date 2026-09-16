package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type emailTestTransport func(*http.Request) (*http.Response, error)

func (f emailTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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

func TestEligibleEmailCodeMessageRejectsPreviousOrUnknownMessages(t *testing.T) {
	requestedAt := time.Date(2026, time.September, 16, 16, 45, 0, 0, time.UTC)
	now := requestedAt.Add(20 * time.Second)

	require.False(t, eligibleEmailCodeMessage(emailCodeMessage{Date: "2026-09-16T16:44:09Z"}, requestedAt, now))
	require.False(t, eligibleEmailCodeMessage(emailCodeMessage{Date: "provider-specific-date"}, requestedAt, now))
	require.True(t, eligibleEmailCodeMessage(emailCodeMessage{Date: "2026-09-16T16:45:15Z"}, requestedAt, now))
}

func TestEligibleEmailCodeMessageRejectsOlderThanOneMinute(t *testing.T) {
	requestedAt := time.Date(2026, time.September, 16, 16, 45, 0, 0, time.UTC)
	now := requestedAt.Add(2 * time.Minute)

	require.False(t, eligibleEmailCodeMessage(emailCodeMessage{Date: "2026-09-16T16:45:15Z"}, requestedAt, now))
	require.True(t, eligibleEmailCodeMessage(emailCodeMessage{Date: "2026-09-16T16:46:20Z"}, requestedAt, now))
}

func TestLatestEmailCodeCandidateUsesOnlyNewestEligibleMessage(t *testing.T) {
	requestedAt := time.Date(2026, time.September, 16, 16, 45, 0, 0, time.UTC)
	now := requestedAt.Add(30 * time.Second)
	candidate, ok := latestEmailCodeCandidate([]emailCodeMessage{
		{ID: "older", Date: "2026-09-16T16:45:10Z", Subject: "OpenAI code 111111"},
		{ID: "newer", Date: "2026-09-16T16:45:25Z", Subject: "OpenAI code 222222"},
	}, nil, requestedAt, now)
	require.True(t, ok)
	require.Equal(t, "newer", candidate.ID)
}

func TestLatestEmailCodeCandidateSkipsBaselineAndUnknownDates(t *testing.T) {
	requestedAt := time.Date(2026, time.September, 16, 16, 45, 0, 0, time.UTC)
	now := requestedAt.Add(30 * time.Second)
	candidate, ok := latestEmailCodeCandidate([]emailCodeMessage{
		{ID: "baseline", Date: "2026-09-16T16:45:25Z"},
		{ID: "unknown", Date: "provider date"},
		{ID: "fresh", Date: "2026-09-16T16:45:20Z"},
	}, map[string]bool{"baseline": true}, requestedAt, now)
	require.True(t, ok)
	require.Equal(t, "fresh", candidate.ID)
}

func TestWaitForCodeWaitsTenSecondsAndRetriesOnlyLatestMessageDetail(t *testing.T) {
	requestedAt := time.Now()
	olderDate := requestedAt.Add(time.Second).Format(time.RFC3339Nano)
	newerDate := requestedAt.Add(2 * time.Second).Format(time.RFC3339Nano)
	lists, details := 0, 0
	session := &emailCodeSession{
		gateway: "/m/test", proof: "test-proof", proofUntil: requestedAt.Add(time.Hour), baseline: map[string]bool{},
		client: &http.Client{Transport: emailTestTransport(func(r *http.Request) (*http.Response, error) {
			status, body := http.StatusOK, ""
			switch r.URL.Path {
			case "/m/test/items":
				lists++
				require.GreaterOrEqual(t, time.Since(requestedAt), 10*time.Second)
				body = `{"ok":true,"data":{"messages":[{"id":"older","date":"` + olderDate + `","subject":"OpenAI verification code 111111"},{"id":"newer","date":"` + newerDate + `","subject":"OpenAI verification"}]}}`
			case "/m/test/items/newer":
				details++
				if details == 1 {
					status, body = http.StatusBadGateway, `{}`
				} else {
					body = `{"ok":true,"data":{"message":{"id":"newer","from":"OpenAI","text":"Your verification code is 222222"}}}`
				}
			default:
				t.Errorf("unexpected mailbox request: %s", r.URL.Path)
			}
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code, err := session.waitForCode(ctx, requestedAt)
	require.NoError(t, err)
	require.Equal(t, "222222", code)
	require.Equal(t, 2, lists)
	require.Equal(t, 2, details)
}
