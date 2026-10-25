//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUsageEgressReachesBothUsageWritersWithoutChangingBilling(t *testing.T) {
	account := &Account{ID: 37, Platform: PlatformOpenAI, RequestProxy: &Proxy{ID: 84, Name: "exit", Protocol: "socks5", Host: "127.0.0.1", Port: 19080}}
	ctx := UsageEgressContext(context.Background(), nil, account)
	for _, openAI := range []bool{true, false} {
		var logs []*UsageLog
		for _, requestCtx := range []context.Context{context.Background(), ctx} {
			repo := &openAIRecordUsageLogRepoStub{}
			userRepo := &openAIRecordUsageUserRepoStub{}
			subRepo := &openAIRecordUsageSubRepoStub{}
			if openAI {
				svc := newOpenAIRecordUsageServiceForTest(repo, userRepo, subRepo, nil)
				err := svc.RecordUsage(requestCtx, &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{RequestID: "egress-openai", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 20, OutputTokens: 10}, Duration: time.Second},
					APIKey: &APIKey{ID: 1}, User: &User{ID: 2}, Account: account,
				})
				require.NoError(t, err)
			} else {
				svc := newGatewayRecordUsageServiceForTest(repo, userRepo, subRepo)
				err := svc.RecordUsage(requestCtx, &RecordUsageInput{
					Result: &ForwardResult{RequestID: "egress-shared", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 20, OutputTokens: 10}, Duration: time.Second},
					APIKey: &APIKey{ID: 1}, User: &User{ID: 2}, Account: account,
				})
				require.NoError(t, err)
			}
			require.NotNil(t, repo.lastLog)
			logs = append(logs, repo.lastLog)
		}
		require.Nil(t, logs[0].Egress)
		require.Equal(t, int64(84), logs[1].Egress.ProxyID)
		require.Equal(t, logs[0].TotalCost, logs[1].TotalCost)
		require.Equal(t, logs[0].ActualCost, logs[1].ActualCost)
		require.Equal(t, logs[0].InputTokens, logs[1].InputTokens)
		require.Equal(t, logs[0].OutputTokens, logs[1].OutputTokens)
	}
}
