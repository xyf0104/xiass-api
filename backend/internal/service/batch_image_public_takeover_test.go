//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestBatchImageFixedOwnerPriorityAndScope(t *testing.T) {
	for _, mode := range []string{"offline", "healthy", "disabled", "unknown", "local_unavailable", "local_unsupported", "scope", "denied"} {
		t.Run(mode, func(t *testing.T) {
			remote := gatewayFixedOwnerAccount(9771, "api", PlatformGemini, 100)
			local := gatewayFixedOwnerAccount(9772, "api2", PlatformGemini, 1)
			if mode == "local_unavailable" {
				local.Schedulable = false
			}
			if mode == "local_unsupported" {
				local.Credentials["model_mapping"] = map[string]any{"unrelated-model": "unrelated-model"}
			}
			svc, _, _, gemini, vertex := newTestBatchImagePublicService(true)
			svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{*remote, *local}}
			svc.SettingService = gatewayFixedOwnerSettings(svc.Config, mode)
			policy := &publicBatchImageAccountPolicy{allowedIDs: map[int64]struct{}{remote.ID: {}, local.ID: {}}}
			if mode == "scope" {
				delete(policy.allowedIDs, local.ID)
			}
			if mode == "denied" {
				policy.err = ErrUserGroupAccountNotAllowed
			}
			svc.AccountPolicy = policy
			ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(999))
			ctx = context.WithValue(ctx, ctxkey.Group, &Group{ID: 999})
			provider, got, err := svc.selectProviderAndAccount(ctx, testBatchImageOwner(), "", "gemini-2.5-flash-image")
			if mode == "denied" {
				require.ErrorIs(t, err, ErrUserGroupAccountNotAllowed)
				require.Nil(t, got)
				return
			}
			if mode == "local_unavailable" || mode == "local_unsupported" || mode == "scope" {
				require.Error(t, err)
				require.Nil(t, got)
				require.Empty(t, gemini.submits)
				require.Empty(t, vertex.submits)
				return
			}
			require.NoError(t, err)
			want := remote.ID
			if mode == "offline" || mode == "disabled" || mode == "unknown" {
				want = local.ID
			}
			require.Equal(t, want, got.ID)
			require.Equal(t, BatchImageProviderGeminiAPI, provider.Name())
			proxyID := int64(83)
			if mode == "healthy" {
				proxyID = 84
			}
			require.Equal(t, proxyID, got.requestProxy().ID)
			require.Equal(t, int64(84), *remote.ProxyID)

			require.Empty(t, gemini.submits)
			require.Empty(t, vertex.submits)
			require.NotEmpty(t, policy.calls)
			for _, call := range policy.calls {
				require.Equal(t, testBatchImageOwner().UserID, call.userID)
				require.Zero(t, call.groupID)
				require.Zero(t, call.ambientGroupID)
			}
		})
	}
}

func TestBatchImageFixedOwnerHealthyWeightsAndProviderOrder(t *testing.T) {
	remote := gatewayFixedOwnerAccount(9781, "api", PlatformGemini, 1)
	local := gatewayFixedOwnerAccount(9782, "api2", PlatformGemini, 1)
	svc, _, _, _, _ := newTestBatchImagePublicService(true)
	svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{*remote, *local}}
	svc.SettingService = gatewayFixedOwnerSettings(svc.Config, "healthy")
	remoteCount := 0
	for i := 0; i < 4000; i++ {
		_, got, err := svc.selectProviderAndAccount(context.Background(), testBatchImageOwner(), "", "gemini-2.5-flash-image")
		require.NoError(t, err)
		if got.ID == remote.ID {
			remoteCount++
		}
	}
	require.InDelta(t, 0.9, float64(remoteCount)/4000, 0.03)

	// A healthy Vertex account must not move ahead of the existing Gemini API
	// provider preference. Its presence also must not bypass SupportsAccount.
	local.Type = AccountTypeServiceAccount
	svc.AccountRepo = &publicBatchImageAccountRepo{accounts: []Account{*remote, *local}}
	svc.SettingService = gatewayFixedOwnerSettings(svc.Config, "healthy")
	svc.ProviderRegistry = NewBatchImageProviderRegistry(&GeminiAPIBatchImageProvider{}, &publicBatchImageProvider{name: BatchImageProviderVertex})
	provider, got, err := svc.selectProviderAndAccount(context.Background(), testBatchImageOwner(), "", "gemini-2.5-flash-image")
	require.NoError(t, err)
	require.Equal(t, BatchImageProviderGeminiAPI, provider.Name())
	require.Equal(t, remote.ID, got.ID)
	require.Equal(t, int64(84), got.requestProxy().ID)
	svc.SettingService = gatewayFixedOwnerSettings(svc.Config, "offline")
	provider, got, err = svc.selectProviderAndAccount(context.Background(), testBatchImageOwner(), BatchImageProviderVertex, "gemini-2.5-flash-image")
	require.NoError(t, err)
	require.Equal(t, BatchImageProviderVertex, provider.Name())
	require.Equal(t, local.ID, got.ID)
}
