//go:build unit

package service

import (
	"context"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIOnboardingTrustedCreatePersistsLoginWithAccount(t *testing.T) {
	repo := newSparkShadowRepoStub()
	svc := &adminServiceImpl{accountRepo: repo}
	credentials := map[string]any{
		"email": "owner@example.test", "access_token": "oauth-token",
		OpenAIOAuthReauthorizationEmailCredentialKey:      "owner@example.test",
		OpenAIOAuthReauthorizationPasswordCredentialKey:   "encrypted-password",
		OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "encrypted-totp",
	}
	schedulable := true
	a, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name: "saved account", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: credentials,
		Concurrency: 7, Priority: 9, Extra: map[string]any{"codex_fingerprint_mode": "off"},
		Schedulable: &schedulable, SkipDefaultGroupBind: true, AllowOpenAIReauthorizationCredentials: true,
	})
	require.NoError(t, err)
	require.Len(t, repo.accounts, 1)
	persisted, err := repo.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	for key, value := range credentials {
		require.Equal(t, value, persisted.Credentials[key])
	}
	require.Equal(t, "saved account", persisted.Name)
	require.Equal(t, 7, persisted.Concurrency)
	require.Equal(t, 9, persisted.Priority)
	require.True(t, persisted.Schedulable)
}

func TestOpenAIOnboardingCredentialsSurviveSameIdentityReauthorization(t *testing.T) {
	proxyID := int64(17)
	credentials := map[string]any{
		"email": "owner@example.test", "chatgpt_account_id": "same-account",
		"access_token": "old-token", "refresh_token": "old-refresh",
		OpenAIOAuthReauthorizationEmailCredentialKey:      "owner@example.test",
		OpenAIOAuthReauthorizationPasswordCredentialKey:   "encrypted-password",
		OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "encrypted-totp",
	}
	repo := &updateAccountCredsRepoStub{account: &Account{
		ID: 209, Name: "saved account", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusError, ErrorMessage: "401 unauthorized", Credentials: maps.Clone(credentials),
		Concurrency: 7, Priority: 9, ProxyID: &proxyID, GroupIDs: []int64{4, 5}, Schedulable: true,
		Extra: map[string]any{"codex_fingerprint_mode": "off", "custom_config": "preserved"},
	}}
	svc := &adminServiceImpl{accountRepo: repo}
	updated, err := svc.UpdateAccount(context.Background(), 209, &UpdateAccountInput{
		Credentials:               map[string]any{"email": "owner@example.test", "chatgpt_account_id": "same-account", "access_token": "new-token", "refresh_token": "new-refresh"},
		ResetOpenAIWeeklyEstimate: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, int64(209), updated.ID)
	for _, key := range []string{OpenAIOAuthReauthorizationEmailCredentialKey, OpenAIOAuthReauthorizationPasswordCredentialKey, OpenAIOAuthReauthorizationTOTPSecretCredentialKey} {
		require.Equal(t, credentials[key], updated.Credentials[key])
	}
	require.Equal(t, "new-token", updated.GetCredential("access_token"))
	require.Equal(t, "saved account", updated.Name)
	require.Equal(t, 7, updated.Concurrency)
	require.Equal(t, 9, updated.Priority)
	require.Equal(t, &proxyID, updated.ProxyID)
	require.Equal(t, []int64{4, 5}, updated.GroupIDs)
	require.True(t, updated.Schedulable)
	require.Equal(t, "preserved", updated.Extra["custom_config"])
}

func TestOpenAIOnboardingTOTPRequiresTrustedWritesAndPreservesOmitted(t *testing.T) {
	repo := &updateAccountCredsRepoStub{account: &Account{ID: 210, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "encrypted-original"}}}
	svc := &adminServiceImpl{accountRepo: repo}
	for _, incoming := range []any{"forged", nil} {
		_, err := svc.UpdateAccount(context.Background(), 210, &UpdateAccountInput{Credentials: map[string]any{OpenAIOAuthReauthorizationTOTPSecretCredentialKey: incoming, "access_token": "renewed"}})
		require.NoError(t, err)
		require.Equal(t, "encrypted-original", repo.account.GetCredential(OpenAIOAuthReauthorizationTOTPSecretCredentialKey))
	}
	_, err := svc.UpdateAccount(context.Background(), 210, &UpdateAccountInput{Credentials: map[string]any{OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "encrypted-new"}, AllowOpenAIReauthorizationCredentials: true})
	require.NoError(t, err)
	require.Equal(t, "encrypted-new", repo.account.GetCredential(OpenAIOAuthReauthorizationTOTPSecretCredentialKey))
	_, err = svc.UpdateAccount(context.Background(), 210, &UpdateAccountInput{Credentials: map[string]any{OpenAIOAuthReauthorizationPasswordCredentialKey: "encrypted-password"}, AllowOpenAIReauthorizationCredentials: true})
	require.NoError(t, err)
	require.Equal(t, "encrypted-new", repo.account.GetCredential(OpenAIOAuthReauthorizationTOTPSecretCredentialKey))
	_, err = svc.UpdateAccount(context.Background(), 210, &UpdateAccountInput{Credentials: map[string]any{OpenAIOAuthReauthorizationTOTPSecretCredentialKey: nil}, AllowOpenAIReauthorizationCredentials: true})
	require.NoError(t, err)
	require.Nil(t, repo.account.Credentials[OpenAIOAuthReauthorizationTOTPSecretCredentialKey])
}

func TestOpenAIOnboardingDedicatedSavePreservesCurrentCredentialSettings(t *testing.T) {
	existing := map[string]any{"email": "owner@example.test", "access_token": "current-token", "base_url": "https://example.test", "model_mapping": map[string]any{"public": "upstream"}, OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "encrypted-original"}
	repo := &updateAccountCredsRepoStub{account: &Account{ID: 211, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: maps.Clone(existing)}}
	svc := &adminServiceImpl{accountRepo: repo}
	updated, err := svc.UpdateAccount(context.Background(), 211, &UpdateAccountInput{
		Credentials: map[string]any{OpenAIOAuthReauthorizationEmailCredentialKey: "owner@example.test", OpenAIOAuthReauthorizationPasswordCredentialKey: "encrypted-password"}, AllowOpenAIReauthorizationCredentials: true,
	})
	require.NoError(t, err)
	for key, value := range existing {
		require.Equal(t, value, updated.Credentials[key], key)
	}
}

func TestDuplicateAccountStripsAllOpenAIOnboardingLoginCredentials(t *testing.T) {
	repo := newDuplicateAccountRepoStub()
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
	// OAuth duplication is already disallowed; also strip legacy login material
	// on any duplicable source so changing its type cannot bypass isolation.
	source := &Account{Name: "source", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "existing-api-key", OpenAITeamChildPasswordCredentialKey: "team-password-ciphertext",
		OpenAIOAuthReauthorizationEmailCredentialKey: "owner@example.test", OpenAIOAuthReauthorizationPasswordCredentialKey: "password-ciphertext", OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "totp-ciphertext",
	}}
	require.NoError(t, repo.Create(context.Background(), source))
	duplicate, err := svc.DuplicateAccount(context.Background(), source.ID, "admin:1", "")
	require.NoError(t, err)
	require.Equal(t, map[string]any{"api_key": "existing-api-key"}, duplicate.Credentials)
	require.Equal(t, "totp-ciphertext", source.GetCredential(OpenAIOAuthReauthorizationTOTPSecretCredentialKey))
}

func TestOpenAIOnboardingSecretsAreSensitiveInAuditBodies(t *testing.T) {
	for _, key := range []string{OpenAIOAuthReauthorizationEmailCredentialKey, OpenAIOAuthReauthorizationPasswordCredentialKey, OpenAIOAuthReauthorizationTOTPSecretCredentialKey} {
		require.True(t, IsSensitiveCredentialKey(key))
		require.True(t, isAuditSensitiveBodyKey(key))
	}
	require.True(t, isAuditSensitiveBodyKey("totp_secret"))
	require.True(t, isAuditSensitiveBodyKey("password"))
}
