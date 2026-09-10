//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeminiFixedOwnerPriorityAndBoundaries(t *testing.T) {
	for _, endpoint := range []string{"messages", "ai_studio"} {
		for _, mode := range []string{"offline", "healthy", "disabled", "unknown", "local_unavailable", "scope", "denied", "sticky", "credential_rank", "vertex"} {
			if endpoint == "ai_studio" && mode == "sticky" {
				continue
			}
			if endpoint == "messages" && (mode == "credential_rank" || mode == "vertex") {
				continue
			}
			t.Run(endpoint+"/"+mode, func(t *testing.T) {
				remote := gatewayFixedOwnerAccount(9751, "api", PlatformGemini, 0)
				local := gatewayFixedOwnerAccount(9752, "api2", PlatformGemini, 10)
				if mode == "local_unavailable" {
					local.Schedulable = false
				}
				if mode == "credential_rank" {
					local.Type = AccountTypeOAuth
				}
				if mode == "vertex" {
					local.Type = AccountTypeServiceAccount
				}
				gateway := newGatewayExecutionNodeStickyTestService(t, []*Account{remote, local}, &mockGatewayCacheForPlatform{}, nil)
				cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{}}
				if mode == "sticky" {
					cache.sessionBindings["gemini:bound"] = remote.ID
				}
				svc := &GeminiMessagesCompatService{cfg: gateway.cfg, accountRepo: gateway.accountRepo, cache: cache}
				svc.settingService = gatewayFixedOwnerSettings(svc.cfg, mode)
				if mode == "scope" || mode == "denied" {
					policy := &publicBatchImageAccountPolicy{allowedIDs: map[int64]struct{}{remote.ID: {}}}
					if mode == "denied" {
						policy.err = ErrUserGroupAccountNotAllowed
					}
					svc.SetAccountCandidateAccessPolicy(policy)
				}
				var got *Account
				var err error
				if endpoint == "messages" {
					got, err = svc.SelectAccountForModelWithExclusions(context.Background(), nil, "bound", "gemini-2.5-flash", nil)
				} else {
					got, err = svc.SelectAccountForAIStudioEndpoints(context.Background(), nil)
				}
				if mode == "denied" {
					require.ErrorIs(t, err, ErrUserGroupAccountNotAllowed)
					require.Nil(t, got)
					return
				}
				if mode == "local_unavailable" || mode == "scope" || mode == "vertex" {
					require.Error(t, err)
					require.Nil(t, got)
					return
				}
				require.NoError(t, err)
				want := remote.ID
				if mode != "healthy" {
					want = local.ID
				}
				require.Equal(t, want, got.ID)
				proxyID := int64(83)
				if mode == "healthy" {
					proxyID = 84
				}
				require.Equal(t, proxyID, got.requestProxy().ID)
				require.Equal(t, int64(84), *remote.ProxyID)

			})
		}
	}
}

func TestGeminiFixedOwnerHealthyWeightsAndExclusion(t *testing.T) {
	remote := gatewayFixedOwnerAccount(9761, "api", PlatformGemini, 1)
	local := gatewayFixedOwnerAccount(9762, "api2", PlatformGemini, 1)
	gateway := newGatewayExecutionNodeStickyTestService(t, []*Account{remote, local}, &mockGatewayCacheForPlatform{}, nil)
	svc := &GeminiMessagesCompatService{cfg: gateway.cfg, accountRepo: gateway.accountRepo, cache: &schedulerTestGatewayCache{}}
	svc.settingService = gatewayFixedOwnerSettings(svc.cfg, "healthy")
	policy := resolveExecutionNodeRoutingPolicy(context.Background(), svc.cfg, svc.settingService)

	remoteCount := 0
	for i := 0; i < 2000; i++ {
		anchor := fmt.Sprintf("healthy-session-%d", i)
		expected := firstExecutionNodeCandidateGroup([]*Account{remote, local}, func(a *Account) *Account { return a }, policy, anchor)
		got, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, anchor, "gemini-2.5-flash", nil)
		require.NoError(t, err)
		require.Equal(t, expected[0].ID, got.ID)
		if got.ID == remote.ID {
			remoteCount++
		}
	}
	require.InDelta(t, 0.9, float64(remoteCount)/2000, 0.03)
	svc.settingService = gatewayFixedOwnerSettings(svc.cfg, "healthy")
	got, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, "", "gemini-2.5-flash", map[int64]struct{}{local.ID: {}})
	require.NoError(t, err)
	require.Equal(t, remote.ID, got.ID)
	require.Equal(t, int64(84), got.requestProxy().ID)
}
