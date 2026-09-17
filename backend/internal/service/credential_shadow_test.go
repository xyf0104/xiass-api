package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubCredRepo 是最小化 AccountRepository stub，仅实现 GetByID，供 credential_shadow_test 使用。
// 嵌入接口满足完整方法集；未实现的方法若被调用会 panic，从而快速暴露误调用。
type stubCredRepo struct {
	AccountRepository
	parent *Account
}

func (s *stubCredRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	return s.parent, nil
}

func newStubCredRepo(parent *Account) AccountRepository {
	return &stubCredRepo{parent: parent}
}

func TestResolveCredentialAccount(t *testing.T) {
	ctx := context.Background()
	pid := int64(100)

	// 普通账号（非影子）→ 返回自身
	parent := &Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive}
	repo := newStubCredRepo(parent)
	got, err := resolveCredentialAccount(ctx, repo, parent)
	require.NoError(t, err)
	require.Equal(t, int64(100), got.ID)

	// 影子账号 + 合法 OpenAI OAuth 母账号 → 返回母账号
	shadow := &Account{ID: 200, ParentAccountID: &pid, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	got, err = resolveCredentialAccount(ctx, repo, shadow)
	require.NoError(t, err)
	require.Equal(t, int64(100), got.ID)

	// 影子账号 + 母账号非 OpenAI OAuth（API Key 类型）→ 返回 error
	badRepo := newStubCredRepo(&Account{ID: 100, Platform: PlatformOpenAI, Type: AccountTypeAPIKey})
	_, err = resolveCredentialAccount(ctx, badRepo, shadow)
	require.Error(t, err)
}

func TestResolveCredentialAccountMergesOpenAICopyCredentialsWithoutReplacingExecutionConfig(t *testing.T) {
	ctx := context.Background()
	proxyID := int64(84)
	source := &Account{
		ID:       100,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "source-access",
			"refresh_token":      "source-refresh",
			"chatgpt_account_id": "source-chatgpt",
			"plan_type":          "pro",
			"model_mapping":      map[string]any{"source": "ignored"},
		},
	}
	copyAccount := &Account{
		ID:       200,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		ProxyID:  &proxyID,
		Credentials: map[string]any{
			"access_token":  "stale-copy-access",
			"refresh_token": "stale-copy-refresh",
			"model_mapping": map[string]any{"gpt-6": "gpt-6-astra"},
		},
		Extra: map[string]any{
			OpenAIOAuthCredentialSourceIDExtraKey: "100",
			codexFingerprintSeedExtraKey:          "copy-seed",
		},
	}

	resolved, err := resolveCredentialAccount(ctx, newStubCredRepo(source), copyAccount)

	require.NoError(t, err)
	require.Equal(t, copyAccount.ID, resolved.ID)
	require.Equal(t, proxyID, *resolved.ProxyID)
	require.Equal(t, "copy-seed", resolved.Extra[codexFingerprintSeedExtraKey])
	require.Equal(t, "source-access", resolved.Credentials["access_token"])
	require.Equal(t, "source-refresh", resolved.Credentials["refresh_token"])
	require.Equal(t, "source-chatgpt", resolved.Credentials["chatgpt_account_id"])
	require.Equal(t, map[string]any{"gpt-6": "gpt-6-astra"}, resolved.Credentials["model_mapping"])
	require.Equal(t, "stale-copy-access", copyAccount.Credentials["access_token"])
}

func TestResolveOpenAIOAuthCredentialSourceRejectsChains(t *testing.T) {
	copySource := &Account{
		ID:       100,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{OpenAIOAuthCredentialSourceIDExtraKey: "50"},
	}
	copyAccount := &Account{
		ID:       200,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{OpenAIOAuthCredentialSourceIDExtraKey: "100"},
	}

	_, err := resolveOpenAIOAuthCredentialSourceAccount(context.Background(), newStubCredRepo(copySource), copyAccount)

	require.ErrorContains(t, err, "itself a credential copy")
}

func TestStripOpenAIOAuthSourceCredentialUpdatesKeepsCopyLocalConfiguration(t *testing.T) {
	filtered := stripOpenAIOAuthSourceCredentialUpdates(map[string]any{
		"access_token":              "forged-access",
		"refresh_token":             "forged-refresh",
		"plan_type":                 "free",
		"model_mapping":             map[string]any{"gpt-6": "gpt-6-astra"},
		"intercept_warmup_requests": true,
	})

	require.NotContains(t, filtered, "access_token")
	require.NotContains(t, filtered, "refresh_token")
	require.NotContains(t, filtered, "plan_type")
	require.Equal(t, map[string]any{"gpt-6": "gpt-6-astra"}, filtered["model_mapping"])
	require.Equal(t, true, filtered["intercept_warmup_requests"])

	preserved := preserveOpenAIOAuthSourceCredentialSnapshot(map[string]any{
		"access_token": "snapshot-access",
		"plan_type":    "pro",
	}, map[string]any{
		"access_token":  "forged-access",
		"plan_type":     "free",
		"model_mapping": map[string]any{"gpt-6": "gpt-6-astra"},
	})
	require.Equal(t, "snapshot-access", preserved["access_token"])
	require.Equal(t, "pro", preserved["plan_type"])
	require.Equal(t, map[string]any{"gpt-6": "gpt-6-astra"}, preserved["model_mapping"])
}
