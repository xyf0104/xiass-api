//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func gatewayFixedOwnerAccount(id int64, owner, platform string, priority int) *Account {
	account := executionNodeTestAccount(id, owner, priority)
	account.Platform, account.Type = platform, AccountTypeAPIKey
	account.Status, account.Schedulable, account.Concurrency = StatusActive, true, 1
	account.Credentials = map[string]any{"api_key": "selection-test-only"}
	return account
}

func gatewayFixedOwnerSettings(cfg *config.Config, mode string) *SettingService {
	cfg.Gateway.ExecutionNode = config.GatewayExecutionNodeConfig{
		Enabled: true, ID: "api2", DefaultProxyID: 83, LegacyUnassignedNodeID: "api", LegacyUnassignedProxyID: 84,
	}
	settings := ExecutionNodeRoutingSettings{
		Available: true, Enabled: true,
		Weights: map[string]float64{"api": 9, "api2": 1}, ProxyIDs: map[string]int64{"api": 84, "api2": 83},
		Healthy: map[string]bool{"api": false, "api2": true},
	}
	if mode == "healthy" {
		settings.Healthy["api"] = true
	}
	if mode == "unknown" {
		delete(settings.Healthy, "api")
	}
	svc := NewSettingService(&executionNodeSettingRepo{}, cfg)
	svc.executionNodeRoutingCache.Store(&cachedExecutionNodeRoutingSettings{settings: settings, expiresAt: time.Now().Add(time.Hour).UnixNano()})
	return svc
}

func TestGatewayFixedOwnerPriorityAndSlots(t *testing.T) {
	for _, mode := range []string{"batch", "no_batch", "routed"} {
		for _, scenario := range []string{"idle", "local_full", "all_full", "local_unavailable", "load_error", "equal_priority_full"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				remote := gatewayFixedOwnerAccount(9711, "api", PlatformAnthropic, 0)
				local := gatewayFixedOwnerAccount(9712, "api2", PlatformAnthropic, 10)
				accounts := []*Account{remote, local}
				attempts := []int64{}
				cache := schedulerTestConcurrencyCache{acquiredIDs: &attempts, acquireResults: map[int64]bool{remote.ID: true, local.ID: true}}
				want, acquired := local.ID, true
				switch scenario {
				case "local_full", "equal_priority_full":
					cache.acquireResults[local.ID] = false
					acquired = false
					if scenario == "equal_priority_full" {
						local.Priority = remote.Priority
					}
				case "all_full":
					cache.acquireResults[local.ID], cache.acquireResults[remote.ID] = false, false
					acquired = false
				case "local_unavailable":
					local.Schedulable = false
				case "load_error":
					cache.loadBatchErr = errors.New("test load snapshot unavailable")
				}
				var groupID *int64
				var groups GroupRepository
				if mode == "routed" {
					id := int64(9741)
					groupID = &id
					for _, a := range accounts {
						a.GroupIDs = []int64{id}
					}
					groups = &mockGroupRepoForGateway{groups: map[int64]*Group{id: {
						ID: id, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true, ModelRoutingEnabled: true,
						ModelRouting: map[string][]int64{"claude-3-5-sonnet-20241022": {remote.ID, local.ID}},
					}}}
				}
				svc := newGatewayExecutionNodeStickyTestService(t, accounts, &mockGatewayCacheForPlatform{}, groups)
				svc.cfg.Gateway.Scheduling.LoadBatchEnabled = mode != "no_batch"
				svc.settingService = gatewayFixedOwnerSettings(svc.cfg, "offline")
				svc.concurrencyService = NewConcurrencyService(cache)
				result, err := svc.SelectAccountWithLoadAwareness(context.Background(), groupID, "new-session", "claude-3-5-sonnet-20241022", nil, "", 0)
				if scenario == "local_unavailable" {
					require.Error(t, err)
					require.Nil(t, result)
					require.NotContains(t, attempts, remote.ID)
					return
				}
				require.NoError(t, err)
				require.Equal(t, want, result.Account.ID)
				require.Equal(t, acquired, result.Acquired)
				if result.ReleaseFunc != nil {
					result.ReleaseFunc()
				}
				require.Equal(t, local.ID, attempts[0])
				require.NotContains(t, attempts, remote.ID, "offline owners must never acquire a slot")
				require.Equal(t, int64(83), result.Account.requestProxy().ID)
				require.Equal(t, int64(84), *remote.ProxyID)

				if mode == "no_batch" {
					require.Equal(t, want, svc.cache.(*mockGatewayCacheForPlatform).sessionBindings["new-session"])
				}
			})
		}
	}
}

func TestGatewayFixedOwnerHealthyWeightsUnchanged(t *testing.T) {
	for _, batch := range []bool{true, false} {
		t.Run(fmt.Sprint(batch), func(t *testing.T) {
			remote := gatewayFixedOwnerAccount(9731, "api", PlatformAnthropic, 1)
			local := gatewayFixedOwnerAccount(9732, "api2", PlatformAnthropic, 1)
			svc := newGatewayExecutionNodeStickyTestService(t, []*Account{remote, local}, &mockGatewayCacheForPlatform{}, nil)
			svc.cfg.Gateway.Scheduling.LoadBatchEnabled = batch
			svc.settingService = gatewayFixedOwnerSettings(svc.cfg, "healthy")
			svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{})
			policy := resolveExecutionNodeRoutingPolicy(context.Background(), svc.cfg, svc.settingService)

			remoteCount := 0
			for i := 0; i < 2000; i++ {
				anchor := fmt.Sprintf("healthy-session-%d", i)
				expected := firstExecutionNodeCandidateGroup([]*Account{remote, local}, func(a *Account) *Account { return a }, policy, anchor)
				got, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, anchor, "claude-3-5-sonnet-20241022", nil, "", 0)
				require.NoError(t, err)
				require.Equal(t, expected[0].ID, got.Account.ID, "healthy placement must match the weighted owner policy")
				got.ReleaseFunc()
				if got.Account.ID == remote.ID {
					remoteCount++
				}
			}
			require.InDelta(t, 0.9, float64(remoteCount)/2000, 0.03)
		})
	}
}

type gatewayFixedOwnerRevokingPolicy struct{ *publicBatchImageAccountPolicy }

func (p gatewayFixedOwnerRevokingPolicy) FilterCandidates(ctx context.Context, groupID *int64, accounts []Account) ([]Account, error) {
	filtered, err := p.publicBatchImageAccountPolicy.FilterCandidates(ctx, groupID, accounts)
	p.allowedIDs = nil
	return filtered, err
}

func TestGatewayFixedOwnerWaitRejectsRevokedAccess(t *testing.T) {
	local := gatewayFixedOwnerAccount(9733, "api2", PlatformAnthropic, 10)
	svc := newGatewayExecutionNodeStickyTestService(t, []*Account{local}, &mockGatewayCacheForPlatform{}, nil)
	svc.cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc.settingService = gatewayFixedOwnerSettings(svc.cfg, "offline")
	svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{local.ID: false}})
	svc.SetAccountCandidateAccessPolicy(gatewayFixedOwnerRevokingPolicy{&publicBatchImageAccountPolicy{allowedIDs: map[int64]struct{}{local.ID: {}}}})
	got, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, "", "claude-3-5-sonnet-20241022", nil, "", 0)
	require.ErrorIs(t, err, ErrUserGroupAccountNotAllowed)
	require.Nil(t, got)
}

func TestGatewayFixedOwnerGatesStickyAndScope(t *testing.T) {
	for _, batch := range []bool{true, false} {
		for _, mode := range []string{"healthy", "disabled", "unknown", "sticky", "scope", "denied"} {
			t.Run(mode+map[bool]string{true: "/batch", false: "/no_batch"}[batch], func(t *testing.T) {
				remote := gatewayFixedOwnerAccount(9721, "api", PlatformAnthropic, 0)
				local := gatewayFixedOwnerAccount(9722, "api2", PlatformAnthropic, 10)
				cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{}}
				if mode == "sticky" {
					cache.sessionBindings["bound"] = remote.ID
				}
				svc := newGatewayExecutionNodeStickyTestService(t, []*Account{remote, local}, cache, nil)
				svc.cfg.Gateway.Scheduling.LoadBatchEnabled = batch
				svc.settingService = gatewayFixedOwnerSettings(svc.cfg, mode)
				svc.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{})
				if mode == "scope" || mode == "denied" {
					policy := &publicBatchImageAccountPolicy{allowedIDs: map[int64]struct{}{remote.ID: {}}}
					if mode == "denied" {
						policy.err = ErrUserGroupAccountNotAllowed
					}
					svc.SetAccountCandidateAccessPolicy(policy)
				}
				ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(19))
				result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "bound", "claude-3-5-sonnet-20241022", nil, "", 19)
				if mode == "denied" || mode == "scope" {
					require.Error(t, err)
					require.Nil(t, result)
					return
				}
				require.NoError(t, err)
				want := remote.ID
				if mode != "healthy" {
					want = local.ID
				}
				require.Equal(t, want, result.Account.ID)
				if mode == "healthy" {
					require.Equal(t, int64(84), result.Account.requestProxy().ID)
				}
				if result.ReleaseFunc != nil {
					result.ReleaseFunc()
				}
			})
		}
	}
}
