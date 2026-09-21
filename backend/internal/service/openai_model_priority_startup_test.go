//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestProvideSettingServiceLoadsModelPriorityBeforeFirstRequest(t *testing.T) {
	repo := newStubSettingRepo()
	repo.values[SettingKeyOpenAIModelPrioritySettings] = `{"enabled":true,"rules":[{"model_pattern":"gpt-test","account_ids":[2,9],"account_order":[9,2]}]}`
	svc, err := ProvideSettingService(repo, nil, nil, nil, nil, nil, &config.Config{})
	require.NoError(t, err)
	require.NotNil(t, svc.openAIModelPriorityCache.Load())
	preference := svc.resolveOpenAIModelPriorityPreference(context.Background(), "gpt-test")
	require.Equal(t, 0, preference.accountRanks[9])
	require.Equal(t, 1, preference.accountRanks[2])
	require.Contains(t, preference.accountIDs, int64(9))
	require.NotContains(t, preference.accountIDs, int64(3))
}

func TestProvideSettingServiceDoesNotIgnoreInvalidModelPriority(t *testing.T) {
	repo := newStubSettingRepo()
	repo.values[SettingKeyOpenAIModelPrioritySettings] = `{"enabled":true,"rules":[{"model_pattern":"gpt-test","account_ids":[2,9],"account_order":[9]}]}`
	svc, err := ProvideSettingService(repo, nil, nil, nil, nil, nil, &config.Config{})
	require.ErrorContains(t, err, "initialize OpenAI model priority settings")
	require.Nil(t, svc)
}

func TestProvideSettingServiceAllowsFreshInstallWithoutRules(t *testing.T) {
	svc, err := ProvideSettingService(newStubSettingRepo(), nil, nil, nil, nil, nil, &config.Config{})
	require.NoError(t, err)
	require.Empty(t, svc.ResolveOpenAIModelPriorityAccountIDs(context.Background(), "gpt-test"))
}
