//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type updateAccountCredsRepoStub struct {
	mockAccountRepoForGemini
	account     *Account
	updateCalls int
}

func (r *updateAccountCredsRepoStub) GetByID(ctx context.Context, id int64) (*Account, error) {
	return r.account, nil
}

func (r *updateAccountCredsRepoStub) Update(ctx context.Context, account *Account) error {
	r.updateCalls++
	r.account = account
	return nil
}

func TestUpdateAccount_PreservesSensitiveCredsWhenIncomingOmits(t *testing.T) {
	accountID := int64(202)
	repo := &updateAccountCredsRepoStub{
		account: &Account{
			ID:       accountID,
			Platform: PlatformAnthropic,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Credentials: map[string]any{
				"refresh_token": "rt-existing",
				"access_token":  "at-existing",
				"id_token":      "id-existing",
				"base_url":      "https://old.example.com",
			},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	// 模拟前端编辑：仅修改 base_url，没有传 token（脱敏后前端 spread 拿不到敏感键）
	updated, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{
			"base_url": "https://new.example.com",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, updated)
	require.Equal(t, 1, repo.updateCalls)

	// 敏感键应保留
	require.Equal(t, "rt-existing", repo.account.Credentials["refresh_token"])
	require.Equal(t, "at-existing", repo.account.Credentials["access_token"])
	require.Equal(t, "id-existing", repo.account.Credentials["id_token"])
	// 非敏感键被替换
	require.Equal(t, "https://new.example.com", repo.account.Credentials["base_url"])
}

func TestUpdateAccount_ExplicitNewTokenOverwrites(t *testing.T) {
	accountID := int64(203)
	repo := &updateAccountCredsRepoStub{
		account: &Account{
			ID:       accountID,
			Platform: PlatformAnthropic,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Credentials: map[string]any{
				"refresh_token": "rt-old",
				"api_key":       "sk-old",
			},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	updated, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{
			"refresh_token": "rt-new",
			// api_key 没传 → 应保留旧值
		},
	})
	require.NoError(t, err)
	require.NotNil(t, updated)

	require.Equal(t, "rt-new", repo.account.Credentials["refresh_token"])
	require.Equal(t, "sk-old", repo.account.Credentials["api_key"])
}

func TestUpdateAccount_EmptyCredentialsSkipsUpdate(t *testing.T) {
	accountID := int64(204)
	repo := &updateAccountCredsRepoStub{
		account: &Account{
			ID:       accountID,
			Platform: PlatformAnthropic,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Credentials: map[string]any{
				"refresh_token": "rt-existing",
			},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	_, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{}, // len == 0 → 闸门跳过
		Name:        "renamed",
	})
	require.NoError(t, err)

	require.Equal(t, "rt-existing", repo.account.Credentials["refresh_token"], "空 credentials 不应触碰已有 token")
	require.Equal(t, "renamed", repo.account.Name)
}

func TestCreateAccount_RejectsManagedOpenAIReauthorizationCredentials(t *testing.T) {
	svc := &adminServiceImpl{}
	for _, key := range []string{
		OpenAITeamChildPasswordCredentialKey,
		OpenAIOAuthReauthorizationEmailCredentialKey,
		OpenAIOAuthReauthorizationPasswordCredentialKey,
		OpenAIOAuthReauthorizationTOTPSecretCredentialKey,
	} {
		t.Run(key, func(t *testing.T) {
			account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
				Platform:    PlatformOpenAI,
				Type:        AccountTypeOAuth,
				Credentials: map[string]any{key: "managed-value"},
			})

			require.Nil(t, account)
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
		})
	}
}

func TestUpdateAccount_ManagedOpenAIReauthorizationCredentialsRequireDedicatedCapability(t *testing.T) {
	accountID := int64(208)
	repo := &updateAccountCredsRepoStub{
		account: &Account{
			ID:       accountID,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Credentials: map[string]any{
				OpenAIOAuthReauthorizationEmailCredentialKey:    "old@example.test",
				OpenAIOAuthReauthorizationPasswordCredentialKey: "encrypted:old-password",
				"base_url": "https://old.example.test",
			},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	_, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{
			OpenAIOAuthReauthorizationEmailCredentialKey:    "generic@example.test",
			OpenAIOAuthReauthorizationPasswordCredentialKey: "encrypted:generic-password",
			"base_url": "https://generic.example.test",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "old@example.test", repo.account.Credentials[OpenAIOAuthReauthorizationEmailCredentialKey])
	require.Equal(t, "encrypted:old-password", repo.account.Credentials[OpenAIOAuthReauthorizationPasswordCredentialKey])
	require.Equal(t, "https://generic.example.test", repo.account.Credentials["base_url"])

	_, err = svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{
			OpenAIOAuthReauthorizationEmailCredentialKey:    "dedicated@example.test",
			OpenAIOAuthReauthorizationPasswordCredentialKey: "encrypted:dedicated-password",
		},
		AllowOpenAIReauthorizationCredentials: true,
	})
	require.NoError(t, err)
	require.Equal(t, "dedicated@example.test", repo.account.Credentials[OpenAIOAuthReauthorizationEmailCredentialKey])
	require.Equal(t, "encrypted:dedicated-password", repo.account.Credentials[OpenAIOAuthReauthorizationPasswordCredentialKey])

	_, err = svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{
			OpenAIOAuthReauthorizationEmailCredentialKey:    "passwordless@example.test",
			OpenAIOAuthReauthorizationPasswordCredentialKey: nil,
		},
		AllowOpenAIReauthorizationCredentials: true,
	})
	require.NoError(t, err)
	require.Equal(t, "passwordless@example.test", repo.account.Credentials[OpenAIOAuthReauthorizationEmailCredentialKey])
	_, passwordConfigured := repo.account.Credentials[OpenAIOAuthReauthorizationPasswordCredentialKey]
	require.True(t, passwordConfigured)
	require.Nil(t, repo.account.Credentials[OpenAIOAuthReauthorizationPasswordCredentialKey])
}

func TestStripOpenAIReauthorizationCredentials_RemovesAllManagedKeys(t *testing.T) {
	credentials := stripOpenAIReauthorizationCredentials(map[string]any{
		OpenAITeamChildPasswordCredentialKey:              "encrypted:team-password",
		OpenAIOAuthReauthorizationEmailCredentialKey:      "ordinary@example.test",
		OpenAIOAuthReauthorizationPasswordCredentialKey:   "encrypted:ordinary-password",
		OpenAIOAuthReauthorizationTOTPSecretCredentialKey: "encrypted:ordinary-totp",
		"base_url": "https://api.example.test",
	})

	require.NotContains(t, credentials, OpenAITeamChildPasswordCredentialKey)
	require.NotContains(t, credentials, OpenAIOAuthReauthorizationEmailCredentialKey)
	require.NotContains(t, credentials, OpenAIOAuthReauthorizationPasswordCredentialKey)
	require.NotContains(t, credentials, OpenAIOAuthReauthorizationTOTPSecretCredentialKey)
	require.Equal(t, "https://api.example.test", credentials["base_url"])
}

func TestUpdateAccount_OpenAIReauthorizationPreservesWeeklyEstimateForSameIdentity(t *testing.T) {
	accountID := int64(205)
	repo := &updateAccountCredsRepoStub{
		account: &Account{
			ID:       accountID,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Credentials: map[string]any{
				"access_token":       "access-old",
				"refresh_token":      "refresh-old",
				"chatgpt_account_id": "workspace-a",
			},
			Extra: map[string]any{
				openAIWeeklyEstimateBaselineKey: map[string]any{
					"version":             13,
					"percent_bucket":      10,
					"snapshot_cost":       260.0,
					"has_weekly_estimate": true,
					"estimate_usd":        2888.8888888888887,
					"reset_at":            "2099-08-25T05:17:50Z",
					"identity":            "workspace-a",
					"observed_at":         "2099-08-19T05:17:50Z",
				},
			},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	_, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{
			"access_token":       "access-new",
			"refresh_token":      "refresh-new",
			"chatgpt_account_id": "workspace-a",
		},
		ResetOpenAIWeeklyEstimate: true,
	})
	require.NoError(t, err)
	require.Contains(t, repo.account.Extra, openAIWeeklyEstimateBaselineKey)
}

func TestUpdateAccount_OpenAIReauthorizationClearsWeeklyEstimateForNewIdentity(t *testing.T) {
	accountID := int64(207)
	repo := &updateAccountCredsRepoStub{
		account: &Account{
			ID:       accountID,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Credentials: map[string]any{
				"access_token":       "access-old",
				"refresh_token":      "refresh-old",
				"chatgpt_account_id": "workspace-a",
			},
			Extra: map[string]any{
				openAIWeeklyEstimateBaselineKey: map[string]any{
					"version":             13,
					"percent_bucket":      10,
					"snapshot_cost":       260.0,
					"has_weekly_estimate": true,
					"estimate_usd":        2888.8888888888887,
					"reset_at":            "2099-08-25T05:17:50Z",
					"identity":            "workspace-a",
					"observed_at":         "2099-08-19T05:17:50Z",
				},
			},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	_, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{
			"access_token":       "access-new",
			"refresh_token":      "refresh-new",
			"chatgpt_account_id": "workspace-b",
		},
		ResetOpenAIWeeklyEstimate: true,
	})
	require.NoError(t, err)
	require.NotContains(t, repo.account.Extra, openAIWeeklyEstimateBaselineKey)
}

func TestUpdateAccount_OpenAITokenRefreshPreservesWeeklyEstimateBaseline(t *testing.T) {
	accountID := int64(206)
	repo := &updateAccountCredsRepoStub{
		account: &Account{
			ID:       accountID,
			Platform: PlatformOpenAI,
			Type:     AccountTypeOAuth,
			Status:   StatusActive,
			Credentials: map[string]any{
				"access_token":       "access-old",
				"refresh_token":      "refresh-old",
				"chatgpt_account_id": "workspace-a",
			},
			Extra: map[string]any{
				openAIWeeklyEstimateBaselineKey: map[string]any{
					"version":             13,
					"percent_bucket":      10,
					"snapshot_cost":       260.0,
					"has_weekly_estimate": true,
					"estimate_usd":        2888.8888888888887,
					"reset_at":            "2099-08-25T05:17:50Z",
					"identity":            "workspace-a",
					"observed_at":         "2099-08-19T05:17:50Z",
				},
			},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	_, err := svc.UpdateAccount(context.Background(), accountID, &UpdateAccountInput{
		Credentials: map[string]any{
			"access_token":       "access-refreshed",
			"refresh_token":      "refresh-rotated",
			"chatgpt_account_id": "workspace-a",
		},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"version":             13,
		"percent_bucket":      10,
		"snapshot_cost":       260.0,
		"has_weekly_estimate": true,
		"estimate_usd":        2888.8888888888887,
		"reset_at":            "2099-08-25T05:17:50Z",
		"identity":            "workspace-a",
		"observed_at":         "2099-08-19T05:17:50Z",
	}, repo.account.Extra[openAIWeeklyEstimateBaselineKey])
}
