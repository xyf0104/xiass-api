//go:build unit

package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

// stubConcurrencyCacheForTest 用于并发服务单元测试的缓存桩
type stubConcurrencyCacheForTest struct {
	acquireResult        bool
	acquireErr           error
	releaseErr           error
	concurrency          int
	concurrencyErr       error
	waitAllowed          bool
	waitErr              error
	waitCount            int
	waitCountErr         error
	loadBatch            map[int64]*AccountLoadInfo
	loadBatchErr         error
	usersLoadBatch       map[int64]*UserLoadInfo
	usersLoadErr         error
	cleanupErr           error
	apiKeyTrackErr       error
	apiKeyReleaseErr     error
	apiKeyConcurrency    map[int64]int
	apiKeyConcurrencyErr error

	// 记录调用
	acquireAccountCalls      atomic.Int64
	releasedAccountIDs       []int64
	releasedRequestIDs       []string
	loadBatchCalls           atomic.Int64
	trackedAPIKeyIDs         []int64
	trackedAPIKeyRequestIDs  []string
	releasedAPIKeyIDs        []int64
	releasedAPIKeyRequestIDs []string
}

type ingressLeaseCacheForTest struct {
	stubConcurrencyCacheForTest
	acquireIngressResult bool
	acquireIngressErr    error
	acquireIngressFn     func(context.Context, int64, int, string) (bool, error)
	refreshIngressResult bool
	refreshIngressErr    error
	refreshIngressFn     func(context.Context, int64, string) (bool, error)
	releaseIngressErr    error
	releaseIngressFn     func(context.Context, int64, string) error
	acquireIngressCalls  int
	refreshIngressCalls  int
	releaseIngressCalls  int
}

type groupLeaseCacheForTest struct {
	stubConcurrencyCacheForTest
	trackedGroupIDs         []int64
	trackedGroupAccountIDs  []int64
	trackedGroupRequestIDs  []string
	releasedGroupIDs        []int64
	releasedGroupAccountIDs []int64
	releasedGroupRequestIDs []string
	groupCounts             map[int64]int
	groupAccountCounts      map[int64]map[int64]int
	groupSnapshotComplete   bool
	groupSnapshotAt         time.Time
	groupSnapshotErr        error
	groupTrackErr           error
	runtimeMu               sync.Mutex
	runtimeGroupLeases      map[int64]map[string]int64
}

type userGroupAccountLeaseIdentityForTest struct {
	userID    int64
	accountID int64
}

type userGroupAccountLeaseCacheForTest struct {
	groupLeaseCacheForTest
	trackedUserIDs            []int64
	trackedUserGroupIDs       []int64
	trackedUserAccountIDs     []int64
	trackedUserRequestIDs     []string
	releasedUserIDs           []int64
	releasedUserGroupIDs      []int64
	releasedUserAccountIDs    []int64
	releasedUserRequestIDs    []string
	userGroupAccountCounts    map[int64]map[int64]map[int64]int
	userGroupSnapshotComplete bool
	userGroupSnapshotAt       time.Time
	userGroupSnapshotErr      error
	userGroupTrackErr         error
	runtimeUserGroupLeases    map[int64]map[string]userGroupAccountLeaseIdentityForTest
}

type groupRequestLeaseCacheForTest struct {
	userGroupAccountLeaseCacheForTest
	groupRequestTrackErr      error
	groupRequestReleaseErr    error
	groupRequestSnapshotErr   error
	groupRequestSnapshotAt    time.Time
	runtimeGroupRequests      map[int64]map[string]int64
	trackedRequestGroupIDs    []int64
	trackedRequestUserIDs     []int64
	trackedRequestIDs         []string
	releasedRequestGroupIDs   []int64
	releasedRequestUserIDs    []int64
	releasedIngressRequestIDs []string
}

func (c *groupLeaseCacheForTest) TrackGroupSlot(_ context.Context, groupID, accountID int64, requestID string) error {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	c.trackedGroupIDs = append(c.trackedGroupIDs, groupID)
	c.trackedGroupAccountIDs = append(c.trackedGroupAccountIDs, accountID)
	c.trackedGroupRequestIDs = append(c.trackedGroupRequestIDs, requestID)
	if c.groupTrackErr != nil {
		return c.groupTrackErr
	}
	if c.runtimeGroupLeases != nil {
		if c.runtimeGroupLeases[groupID] == nil {
			c.runtimeGroupLeases[groupID] = make(map[string]int64)
		}
		c.runtimeGroupLeases[groupID][requestID] = accountID
	}
	return nil
}

func (c *groupLeaseCacheForTest) ReleaseGroupSlot(_ context.Context, groupID, accountID int64, requestID string) error {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	c.releasedGroupIDs = append(c.releasedGroupIDs, groupID)
	c.releasedGroupAccountIDs = append(c.releasedGroupAccountIDs, accountID)
	c.releasedGroupRequestIDs = append(c.releasedGroupRequestIDs, requestID)
	if leases := c.runtimeGroupLeases[groupID]; leases != nil {
		delete(leases, requestID)
		if len(leases) == 0 {
			delete(c.runtimeGroupLeases, groupID)
		}
	}
	return nil
}

func (c *groupLeaseCacheForTest) GetGroupConcurrencyBatch(_ context.Context, groupIDs []int64) (map[int64]int, error) {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	out := make(map[int64]int, len(groupIDs))
	for _, groupID := range groupIDs {
		if c.runtimeGroupLeases != nil {
			out[groupID] = len(c.runtimeGroupLeases[groupID])
		} else {
			out[groupID] = c.groupCounts[groupID]
		}
	}
	return out, nil
}

func (c *groupLeaseCacheForTest) GetGroupAccountConcurrency(_ context.Context, groupID int64) (map[int64]int, bool, time.Time, error) {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	if c.runtimeGroupLeases != nil {
		counts := make(map[int64]int)
		for _, accountID := range c.runtimeGroupLeases[groupID] {
			counts[accountID]++
		}
		return counts, c.groupSnapshotComplete, c.groupSnapshotAt, c.groupSnapshotErr
	}
	return c.groupAccountCounts[groupID], c.groupSnapshotComplete, c.groupSnapshotAt, c.groupSnapshotErr
}

func (c *userGroupAccountLeaseCacheForTest) TrackUserGroupAccountSlot(_ context.Context, userID, groupID, accountID int64, requestID string) error {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	c.trackedUserIDs = append(c.trackedUserIDs, userID)
	c.trackedUserGroupIDs = append(c.trackedUserGroupIDs, groupID)
	c.trackedUserAccountIDs = append(c.trackedUserAccountIDs, accountID)
	c.trackedUserRequestIDs = append(c.trackedUserRequestIDs, requestID)
	if c.userGroupTrackErr != nil {
		return c.userGroupTrackErr
	}
	if c.runtimeUserGroupLeases != nil {
		if c.runtimeUserGroupLeases[groupID] == nil {
			c.runtimeUserGroupLeases[groupID] = make(map[string]userGroupAccountLeaseIdentityForTest)
		}
		c.runtimeUserGroupLeases[groupID][requestID] = userGroupAccountLeaseIdentityForTest{
			userID:    userID,
			accountID: accountID,
		}
	}
	return nil
}

func (c *userGroupAccountLeaseCacheForTest) ReleaseUserGroupAccountSlot(_ context.Context, userID, groupID, accountID int64, requestID string) error {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	c.releasedUserIDs = append(c.releasedUserIDs, userID)
	c.releasedUserGroupIDs = append(c.releasedUserGroupIDs, groupID)
	c.releasedUserAccountIDs = append(c.releasedUserAccountIDs, accountID)
	c.releasedUserRequestIDs = append(c.releasedUserRequestIDs, requestID)
	if leases := c.runtimeUserGroupLeases[groupID]; leases != nil {
		delete(leases, requestID)
		if len(leases) == 0 {
			delete(c.runtimeUserGroupLeases, groupID)
		}
	}
	return nil
}

func (c *userGroupAccountLeaseCacheForTest) GetUserGroupAccountConcurrency(_ context.Context, groupID int64) (map[int64]map[int64]int, bool, time.Time, error) {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	if c.runtimeUserGroupLeases != nil {
		counts := make(map[int64]map[int64]int)
		for _, lease := range c.runtimeUserGroupLeases[groupID] {
			if counts[lease.userID] == nil {
				counts[lease.userID] = make(map[int64]int)
			}
			counts[lease.userID][lease.accountID]++
		}
		return counts, c.userGroupSnapshotComplete, c.userGroupSnapshotAt, c.userGroupSnapshotErr
	}
	return c.userGroupAccountCounts[groupID], c.userGroupSnapshotComplete, c.userGroupSnapshotAt, c.userGroupSnapshotErr
}

func (c *groupRequestLeaseCacheForTest) TrackGroupRequestSlot(_ context.Context, groupID, userID int64, requestID string) error {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	c.trackedRequestGroupIDs = append(c.trackedRequestGroupIDs, groupID)
	c.trackedRequestUserIDs = append(c.trackedRequestUserIDs, userID)
	c.trackedRequestIDs = append(c.trackedRequestIDs, requestID)
	if c.groupRequestTrackErr != nil {
		return c.groupRequestTrackErr
	}
	if c.runtimeGroupRequests != nil {
		if c.runtimeGroupRequests[groupID] == nil {
			c.runtimeGroupRequests[groupID] = make(map[string]int64)
		}
		c.runtimeGroupRequests[groupID][requestID] = userID
	}
	return nil
}

func (c *groupRequestLeaseCacheForTest) ReleaseGroupRequestSlot(_ context.Context, groupID, userID int64, requestID string) error {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	c.releasedRequestGroupIDs = append(c.releasedRequestGroupIDs, groupID)
	c.releasedRequestUserIDs = append(c.releasedRequestUserIDs, userID)
	c.releasedIngressRequestIDs = append(c.releasedIngressRequestIDs, requestID)
	if c.groupRequestReleaseErr != nil {
		return c.groupRequestReleaseErr
	}
	if requests := c.runtimeGroupRequests[groupID]; requests != nil {
		delete(requests, requestID)
		if len(requests) == 0 {
			delete(c.runtimeGroupRequests, groupID)
		}
	}
	return nil
}

func (c *groupRequestLeaseCacheForTest) GetGroupRequestConcurrencyBatch(_ context.Context, groupIDs []int64) (map[int64]int, error) {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	if c.groupRequestSnapshotErr != nil {
		return nil, c.groupRequestSnapshotErr
	}
	counts := make(map[int64]int, len(groupIDs))
	for _, groupID := range groupIDs {
		counts[groupID] = len(c.runtimeGroupRequests[groupID])
	}
	return counts, nil
}

func (c *groupRequestLeaseCacheForTest) GetGroupUserRequestConcurrency(_ context.Context, groupID int64) (map[int64]int, time.Time, error) {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	if c.groupRequestSnapshotErr != nil {
		return nil, time.Time{}, c.groupRequestSnapshotErr
	}
	counts := make(map[int64]int)
	for _, userID := range c.runtimeGroupRequests[groupID] {
		counts[userID]++
	}
	return counts, c.groupRequestSnapshotAt, nil
}

func (c *ingressLeaseCacheForTest) AcquireOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, maxConnections int, leaseID string) (bool, error) {
	c.acquireIngressCalls++
	if c.acquireIngressFn != nil {
		return c.acquireIngressFn(ctx, apiKeyID, maxConnections, leaseID)
	}
	return c.acquireIngressResult, c.acquireIngressErr
}

func (c *ingressLeaseCacheForTest) RefreshOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, leaseID string) (bool, error) {
	c.refreshIngressCalls++
	if c.refreshIngressFn != nil {
		return c.refreshIngressFn(ctx, apiKeyID, leaseID)
	}
	return c.refreshIngressResult, c.refreshIngressErr
}

func (c *ingressLeaseCacheForTest) ReleaseOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, leaseID string) error {
	c.releaseIngressCalls++
	if c.releaseIngressFn != nil {
		return c.releaseIngressFn(ctx, apiKeyID, leaseID)
	}
	return c.releaseIngressErr
}

var _ ConcurrencyCache = (*stubConcurrencyCacheForTest)(nil)
var _ OpenAIWSIngressLeaseCache = (*ingressLeaseCacheForTest)(nil)
var _ GroupConcurrencyCache = (*groupLeaseCacheForTest)(nil)
var _ UserGroupAccountConcurrencyCache = (*userGroupAccountLeaseCacheForTest)(nil)
var _ GroupRequestConcurrencyCache = (*groupRequestLeaseCacheForTest)(nil)

func (c *stubConcurrencyCacheForTest) AcquireAccountSlot(_ context.Context, _ int64, _ int, _ string) (bool, error) {
	c.acquireAccountCalls.Add(1)
	return c.acquireResult, c.acquireErr
}
func (c *stubConcurrencyCacheForTest) ReleaseAccountSlot(_ context.Context, accountID int64, requestID string) error {
	c.releasedAccountIDs = append(c.releasedAccountIDs, accountID)
	c.releasedRequestIDs = append(c.releasedRequestIDs, requestID)
	return c.releaseErr
}
func (c *stubConcurrencyCacheForTest) GetAccountConcurrency(_ context.Context, _ int64) (int, error) {
	return c.concurrency, c.concurrencyErr
}
func (c *stubConcurrencyCacheForTest) GetAccountConcurrencyBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(accountIDs))
	for _, accountID := range accountIDs {
		if c.concurrencyErr != nil {
			return nil, c.concurrencyErr
		}
		result[accountID] = c.concurrency
	}
	return result, nil
}
func (c *stubConcurrencyCacheForTest) IncrementAccountWaitCount(_ context.Context, _ int64, _ int) (bool, error) {
	return c.waitAllowed, c.waitErr
}
func (c *stubConcurrencyCacheForTest) DecrementAccountWaitCount(_ context.Context, _ int64) error {
	return nil
}
func (c *stubConcurrencyCacheForTest) GetAccountWaitingCount(_ context.Context, _ int64) (int, error) {
	return c.waitCount, c.waitCountErr
}
func (c *stubConcurrencyCacheForTest) AcquireUserSlot(_ context.Context, _ int64, _ int, _ string) (bool, error) {
	return c.acquireResult, c.acquireErr
}
func (c *stubConcurrencyCacheForTest) ReleaseUserSlot(_ context.Context, _ int64, _ string) error {
	return c.releaseErr
}
func (c *stubConcurrencyCacheForTest) GetUserConcurrency(_ context.Context, _ int64) (int, error) {
	return c.concurrency, c.concurrencyErr
}
func (c *stubConcurrencyCacheForTest) TrackAPIKeySlot(_ context.Context, apiKeyID int64, requestID string) error {
	c.trackedAPIKeyIDs = append(c.trackedAPIKeyIDs, apiKeyID)
	c.trackedAPIKeyRequestIDs = append(c.trackedAPIKeyRequestIDs, requestID)
	return c.apiKeyTrackErr
}
func (c *stubConcurrencyCacheForTest) ReleaseAPIKeySlot(_ context.Context, apiKeyID int64, requestID string) error {
	c.releasedAPIKeyIDs = append(c.releasedAPIKeyIDs, apiKeyID)
	c.releasedAPIKeyRequestIDs = append(c.releasedAPIKeyRequestIDs, requestID)
	return c.apiKeyReleaseErr
}
func (c *stubConcurrencyCacheForTest) GetAPIKeyConcurrencyBatch(_ context.Context, apiKeyIDs []int64) (map[int64]int, error) {
	if c.apiKeyConcurrencyErr != nil {
		return nil, c.apiKeyConcurrencyErr
	}
	result := make(map[int64]int, len(apiKeyIDs))
	for _, apiKeyID := range apiKeyIDs {
		result[apiKeyID] = c.apiKeyConcurrency[apiKeyID]
	}
	return result, nil
}
func (c *stubConcurrencyCacheForTest) IncrementWaitCount(_ context.Context, _ int64, _ int) (bool, error) {
	return c.waitAllowed, c.waitErr
}
func (c *stubConcurrencyCacheForTest) DecrementWaitCount(_ context.Context, _ int64) error {
	return nil
}
func (c *stubConcurrencyCacheForTest) GetAccountsLoadBatch(_ context.Context, _ []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	c.loadBatchCalls.Add(1)
	return c.loadBatch, c.loadBatchErr
}
func (c *stubConcurrencyCacheForTest) GetUsersLoadBatch(_ context.Context, _ []UserWithConcurrency) (map[int64]*UserLoadInfo, error) {
	return c.usersLoadBatch, c.usersLoadErr
}
func (c *stubConcurrencyCacheForTest) CleanupExpiredAccountSlots(_ context.Context, _ int64) error {
	return c.cleanupErr
}

func (c *stubConcurrencyCacheForTest) CleanupExpiredAccountSlotKeys(_ context.Context) error {
	return c.cleanupErr
}

func (c *stubConcurrencyCacheForTest) CleanupStaleProcessSlots(_ context.Context, _ string) error {
	return c.cleanupErr
}

type trackingConcurrencyCache struct {
	stubConcurrencyCacheForTest
	cleanupPrefix string
}

func (c *trackingConcurrencyCache) CleanupStaleProcessSlots(_ context.Context, prefix string) error {
	c.cleanupPrefix = prefix
	return c.cleanupErr
}

func TestCleanupStaleProcessSlots_NilCache(t *testing.T) {
	svc := &ConcurrencyService{cache: nil}
	require.NoError(t, svc.CleanupStaleProcessSlots(context.Background()))
}

func TestCleanupStaleProcessSlots_DelegatesPrefix(t *testing.T) {
	cache := &trackingConcurrencyCache{}
	svc := NewConcurrencyService(cache)
	require.NoError(t, svc.CleanupStaleProcessSlots(context.Background()))
	require.Equal(t, RequestIDPrefix(), cache.cleanupPrefix)
}

func TestAcquireAccountSlot_Success(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{acquireResult: true}
	svc := NewConcurrencyService(cache)

	result, err := svc.AcquireAccountSlot(context.Background(), 1, 5)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.NotNil(t, result.ReleaseFunc)
}

func TestAcquireAccountSlot_TracksActualGroupAndAccount(t *testing.T) {
	cache := &groupLeaseCacheForTest{stubConcurrencyCacheForTest: stubConcurrencyCacheForTest{acquireResult: true}}
	svc := NewConcurrencyService(cache)
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 42})

	result, err := svc.AcquireAccountSlot(ctx, 99, 5)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.Equal(t, []int64{42}, cache.trackedGroupIDs)
	require.Equal(t, []int64{99}, cache.trackedGroupAccountIDs)
	require.Len(t, cache.trackedGroupRequestIDs, 1)

	result.ReleaseFunc()
	require.Equal(t, []int64{42}, cache.releasedGroupIDs)
	require.Equal(t, []int64{99}, cache.releasedGroupAccountIDs)
	require.Equal(t, cache.trackedGroupRequestIDs, cache.releasedGroupRequestIDs)
}

func TestAcquireAccountSlot_TracksActualUserGroupAndAccount(t *testing.T) {
	cache := &userGroupAccountLeaseCacheForTest{
		groupLeaseCacheForTest: groupLeaseCacheForTest{
			stubConcurrencyCacheForTest: stubConcurrencyCacheForTest{acquireResult: true},
		},
	}
	svc := NewConcurrencyService(cache)
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 42})
	ctx = context.WithValue(ctx, ctxkey.UserID, int64(18))

	result, err := svc.AcquireAccountSlot(ctx, 99, 5)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.Equal(t, []int64{18}, cache.trackedUserIDs)
	require.Equal(t, []int64{42}, cache.trackedUserGroupIDs)
	require.Equal(t, []int64{99}, cache.trackedUserAccountIDs)
	require.Len(t, cache.trackedUserRequestIDs, 1)

	result.ReleaseFunc()
	require.Equal(t, []int64{18}, cache.releasedUserIDs)
	require.Equal(t, []int64{42}, cache.releasedUserGroupIDs)
	require.Equal(t, []int64{99}, cache.releasedUserAccountIDs)
	require.Equal(t, cache.trackedUserRequestIDs, cache.releasedUserRequestIDs)
}

func TestGroupRequestConcurrencyMatchesAcceptedUserRequestsWhileAccountSlotsStaySeparate(t *testing.T) {
	cache := &groupRequestLeaseCacheForTest{
		userGroupAccountLeaseCacheForTest: userGroupAccountLeaseCacheForTest{
			groupLeaseCacheForTest: groupLeaseCacheForTest{
				stubConcurrencyCacheForTest: stubConcurrencyCacheForTest{acquireResult: true},
				groupSnapshotComplete:       true,
				runtimeGroupLeases:          make(map[int64]map[string]int64),
			},
			userGroupSnapshotComplete: true,
			runtimeUserGroupLeases:    make(map[int64]map[string]userGroupAccountLeaseIdentityForTest),
		},
		runtimeGroupRequests:   make(map[int64]map[string]int64),
		groupRequestSnapshotAt: time.Unix(1_700_000_001, 0).UTC(),
	}
	svc := NewConcurrencyService(cache)
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 42})
	ctx = context.WithValue(ctx, ctxkey.UserID, int64(18))

	requestRelease := svc.TrackAPIKeySlot(ctx, 88)
	accountResult, err := svc.AcquireAccountSlot(ctx, 99, 1)
	require.NoError(t, err)
	require.True(t, accountResult.Acquired)

	groupCounts, supported, err := svc.GetGroupConcurrencyBatch(ctx, []int64{42})
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, map[int64]int{42: 1}, groupCounts, "one request must not be counted again when it acquires an account")

	snapshot, err := svc.GetUserGroupAccountConcurrencySnapshot(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, map[int64]int{18: 1}, snapshot.UserCounts)
	require.Equal(t, map[int64]map[int64]int{18: {99: 1}}, snapshot.Counts)

	accountResult.ReleaseFunc()
	groupCounts, _, err = svc.GetGroupConcurrencyBatch(ctx, []int64{42})
	require.NoError(t, err)
	require.Equal(t, map[int64]int{42: 1}, groupCounts, "a request waiting or between account selections remains active at group ingress")

	snapshot, err = svc.GetUserGroupAccountConcurrencySnapshot(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, map[int64]int{18: 1}, snapshot.UserCounts)
	require.Empty(t, snapshot.Counts)

	requestRelease()
	requestRelease()
	groupCounts, _, err = svc.GetGroupConcurrencyBatch(ctx, []int64{42})
	require.NoError(t, err)
	require.Equal(t, map[int64]int{42: 0}, groupCounts)
	require.Equal(t, cache.trackedRequestIDs, cache.releasedIngressRequestIDs)
}

func TestLiveGroupRuntimeUsesTheSameGroupRequestAndAccountSnapshots(t *testing.T) {
	cache := &groupRequestLeaseCacheForTest{
		userGroupAccountLeaseCacheForTest: userGroupAccountLeaseCacheForTest{
			groupLeaseCacheForTest: groupLeaseCacheForTest{
				groupSnapshotComplete: true,
				runtimeGroupLeases:    make(map[int64]map[string]int64),
			},
			userGroupSnapshotComplete: true,
			runtimeUserGroupLeases:    make(map[int64]map[string]userGroupAccountLeaseIdentityForTest),
		},
		runtimeGroupRequests: make(map[int64]map[string]int64),
	}
	concurrencyService := NewConcurrencyService(cache)
	svc := &OpenAIGatewayService{concurrencyService: concurrencyService}

	svc.trackLiveGroupRuntime(context.Background(), 42, 18, 99, "live-lease")
	groupCounts, supported, err := concurrencyService.GetGroupConcurrencyBatch(context.Background(), []int64{42})
	require.NoError(t, err)
	require.True(t, supported)
	require.Equal(t, map[int64]int{42: 1}, groupCounts)
	snapshot, err := concurrencyService.GetUserGroupAccountConcurrencySnapshot(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, map[int64]int{18: 1}, snapshot.UserCounts)
	require.Equal(t, map[int64]map[int64]int{18: {99: 1}}, snapshot.Counts)

	svc.releaseLiveGroupRuntime(context.Background(), 42, 18, 99, "live-lease")
	groupCounts, _, err = concurrencyService.GetGroupConcurrencyBatch(context.Background(), []int64{42})
	require.NoError(t, err)
	require.Equal(t, map[int64]int{42: 0}, groupCounts)
	snapshot, err = concurrencyService.GetUserGroupAccountConcurrencySnapshot(context.Background(), 42)
	require.NoError(t, err)
	require.Empty(t, snapshot.UserCounts)
	require.Empty(t, snapshot.Counts)
}

func TestGetGroupAccountConcurrencySnapshot(t *testing.T) {
	snapshotAt := time.Unix(1_700_000_000, 0).UTC()
	t.Run("returns complete group scoped counts", func(t *testing.T) {
		cache := &groupLeaseCacheForTest{
			groupAccountCounts:    map[int64]map[int64]int{42: {7: 2, 8: 1}},
			groupSnapshotComplete: true,
			groupSnapshotAt:       snapshotAt,
		}
		snapshot, err := NewConcurrencyService(cache).GetGroupAccountConcurrencySnapshot(context.Background(), 42)
		require.NoError(t, err)
		require.Equal(t, map[int64]int{7: 2, 8: 1}, snapshot.Counts)
		require.Equal(t, snapshotAt, snapshot.SnapshotAt)
	})

	t.Run("legacy members fail closed", func(t *testing.T) {
		cache := &groupLeaseCacheForTest{
			groupAccountCounts:    map[int64]map[int64]int{42: {7: 1}},
			groupSnapshotComplete: false,
		}
		_, err := NewConcurrencyService(cache).GetGroupAccountConcurrencySnapshot(context.Background(), 42)
		require.ErrorIs(t, err, ErrGroupConcurrencySnapshotIncomplete)
	})

	t.Run("cache errors fail unavailable", func(t *testing.T) {
		cache := &groupLeaseCacheForTest{groupSnapshotErr: errors.New("redis down")}
		_, err := NewConcurrencyService(cache).GetGroupAccountConcurrencySnapshot(context.Background(), 42)
		require.ErrorIs(t, err, ErrGroupConcurrencySnapshotUnavailable)
	})
}

func TestGetUserGroupAccountConcurrencySnapshot(t *testing.T) {
	snapshotAt := time.Unix(1_700_000_000, 0).UTC()
	t.Run("returns complete user scoped counts", func(t *testing.T) {
		cache := &userGroupAccountLeaseCacheForTest{
			userGroupAccountCounts: map[int64]map[int64]map[int64]int{
				42: {
					18: {7: 2, 8: 1},
					19: {7: 1},
				},
			},
			userGroupSnapshotComplete: true,
			userGroupSnapshotAt:       snapshotAt,
		}
		snapshot, err := NewConcurrencyService(cache).GetUserGroupAccountConcurrencySnapshot(context.Background(), 42)
		require.NoError(t, err)
		require.Equal(t, map[int64]map[int64]int{18: {7: 2, 8: 1}, 19: {7: 1}}, snapshot.Counts)
		require.Equal(t, snapshotAt, snapshot.SnapshotAt)
	})

	t.Run("incomplete cache fails closed", func(t *testing.T) {
		cache := &userGroupAccountLeaseCacheForTest{userGroupSnapshotComplete: false}
		_, err := NewConcurrencyService(cache).GetUserGroupAccountConcurrencySnapshot(context.Background(), 42)
		require.ErrorIs(t, err, ErrUserGroupAccountConcurrencySnapshotIncomplete)
	})

	t.Run("cache errors fail unavailable", func(t *testing.T) {
		cache := &userGroupAccountLeaseCacheForTest{userGroupSnapshotErr: errors.New("redis down")}
		_, err := NewConcurrencyService(cache).GetUserGroupAccountConcurrencySnapshot(context.Background(), 42)
		require.ErrorIs(t, err, ErrUserGroupAccountConcurrencySnapshotUnavailable)
	})
}

func TestAcquireAccountSlot_Failure(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{acquireResult: false}
	svc := NewConcurrencyService(cache)

	result, err := svc.AcquireAccountSlot(context.Background(), 1, 5)
	require.NoError(t, err)
	require.False(t, result.Acquired)
	require.Nil(t, result.ReleaseFunc)
}

func TestAcquireAccountSlot_UnlimitedConcurrencyTracksRuntimeWithoutLimiting(t *testing.T) {
	for _, maxConcurrency := range []int{0, -1} {
		t.Run(strconv.Itoa(maxConcurrency), func(t *testing.T) {
			cache := &userGroupAccountLeaseCacheForTest{
				groupLeaseCacheForTest: groupLeaseCacheForTest{
					stubConcurrencyCacheForTest: stubConcurrencyCacheForTest{
						acquireErr: errors.New("account limiter must not be called"),
					},
					groupSnapshotComplete: true,
					runtimeGroupLeases:    make(map[int64]map[string]int64),
				},
				userGroupSnapshotComplete: true,
				runtimeUserGroupLeases:    make(map[int64]map[string]userGroupAccountLeaseIdentityForTest),
			}
			svc := NewConcurrencyService(cache)
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 42})
			ctx = context.WithValue(ctx, ctxkey.UserID, int64(18))

			result, err := svc.AcquireAccountSlot(ctx, 99, maxConcurrency)
			require.NoError(t, err)
			require.True(t, result.Acquired)
			require.NotNil(t, result.ReleaseFunc)
			require.Zero(t, cache.acquireAccountCalls.Load(), "unlimited accounts must bypass enforcement")

			groupCounts, supported, err := svc.GetGroupConcurrencyBatch(ctx, []int64{42})
			require.NoError(t, err)
			require.True(t, supported)
			require.Equal(t, map[int64]int{42: 1}, groupCounts)

			accountSnapshot, err := svc.GetGroupAccountConcurrencySnapshot(ctx, 42)
			require.NoError(t, err)
			require.Equal(t, map[int64]int{99: 1}, accountSnapshot.Counts)

			userSnapshot, err := svc.GetUserGroupAccountConcurrencySnapshot(ctx, 42)
			require.NoError(t, err)
			require.Equal(t, map[int64]map[int64]int{18: {99: 1}}, userSnapshot.Counts)

			result.ReleaseFunc()
			result.ReleaseFunc()

			groupCounts, supported, err = svc.GetGroupConcurrencyBatch(ctx, []int64{42})
			require.NoError(t, err)
			require.True(t, supported)
			require.Equal(t, map[int64]int{42: 0}, groupCounts)

			accountSnapshot, err = svc.GetGroupAccountConcurrencySnapshot(ctx, 42)
			require.NoError(t, err)
			require.Empty(t, accountSnapshot.Counts)

			userSnapshot, err = svc.GetUserGroupAccountConcurrencySnapshot(ctx, 42)
			require.NoError(t, err)
			require.Empty(t, userSnapshot.Counts)
			require.Empty(t, cache.runtimeGroupLeases)
			require.Empty(t, cache.runtimeUserGroupLeases)
			require.Empty(t, cache.releasedAccountIDs, "display leases must not create an enforced account slot")
			require.Len(t, cache.releasedGroupRequestIDs, 1)
			require.Len(t, cache.releasedUserRequestIDs, 1)
		})
	}
}

func TestAcquireAccountSlot_UnlimitedConcurrencyRollsBackPartialTracking(t *testing.T) {
	tests := []struct {
		name              string
		groupTrackErr     error
		userGroupTrackErr error
	}{
		{name: "group lease survives user tracking failure", userGroupTrackErr: errors.New("user tracker unavailable")},
		{name: "user lease survives group tracking failure", groupTrackErr: errors.New("group tracker unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &userGroupAccountLeaseCacheForTest{
				groupLeaseCacheForTest: groupLeaseCacheForTest{
					groupSnapshotComplete: true,
					groupTrackErr:         tt.groupTrackErr,
					runtimeGroupLeases:    make(map[int64]map[string]int64),
				},
				userGroupSnapshotComplete: true,
				userGroupTrackErr:         tt.userGroupTrackErr,
				runtimeUserGroupLeases:    make(map[int64]map[string]userGroupAccountLeaseIdentityForTest),
			}
			svc := NewConcurrencyService(cache)
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 42})
			ctx = context.WithValue(ctx, ctxkey.UserID, int64(18))

			result, err := svc.AcquireAccountSlot(ctx, 99, 0)
			require.NoError(t, err)
			require.True(t, result.Acquired)
			result.ReleaseFunc()

			require.Empty(t, cache.runtimeGroupLeases)
			require.Empty(t, cache.runtimeUserGroupLeases)
			require.Empty(t, cache.releasedAccountIDs)
		})
	}
}

func TestAcquireAccountSlot_CacheError(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{acquireErr: errors.New("redis down")}
	svc := NewConcurrencyService(cache)

	result, err := svc.AcquireAccountSlot(context.Background(), 1, 5)
	require.Error(t, err)
	require.Nil(t, result)
}

func TestAcquireAccountSlot_ReleaseDecrements(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{acquireResult: true}
	svc := NewConcurrencyService(cache)

	result, err := svc.AcquireAccountSlot(context.Background(), 42, 5)
	require.NoError(t, err)
	require.True(t, result.Acquired)

	// 调用 ReleaseFunc 应释放槽位
	result.ReleaseFunc()

	require.Len(t, cache.releasedAccountIDs, 1)
	require.Equal(t, int64(42), cache.releasedAccountIDs[0])
	require.Len(t, cache.releasedRequestIDs, 1)
	require.NotEmpty(t, cache.releasedRequestIDs[0], "requestID 不应为空")
}

func TestAcquireAccountSlotTracksAndReleasesActualGroup(t *testing.T) {
	cache := &groupLeaseCacheForTest{stubConcurrencyCacheForTest: stubConcurrencyCacheForTest{acquireResult: true}}
	svc := NewConcurrencyService(cache)
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 77})

	result, err := svc.AcquireAccountSlot(ctx, 42, 5)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.Equal(t, []int64{77}, cache.trackedGroupIDs)
	require.Len(t, cache.trackedGroupRequestIDs, 1)

	result.ReleaseFunc()
	require.Equal(t, []int64{77}, cache.releasedGroupIDs)
	require.Equal(t, cache.trackedGroupRequestIDs, cache.releasedGroupRequestIDs)
	require.Equal(t, []int64{42}, cache.releasedAccountIDs)
}

func TestAcquireUserSlot_IndependentFromAccount(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{acquireResult: true}
	svc := NewConcurrencyService(cache)

	// 用户槽位获取应独立于账户槽位
	result, err := svc.AcquireUserSlot(context.Background(), 100, 3)
	require.NoError(t, err)
	require.True(t, result.Acquired)
	require.NotNil(t, result.ReleaseFunc)
}

func TestAcquireUserSlot_UnlimitedConcurrency(t *testing.T) {
	svc := NewConcurrencyService(&stubConcurrencyCacheForTest{})

	result, err := svc.AcquireUserSlot(context.Background(), 1, 0)
	require.NoError(t, err)
	require.True(t, result.Acquired)
}

func TestTrackAPIKeySlot_ReleaseDecrements(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{}
	svc := NewConcurrencyService(cache)

	release := svc.TrackAPIKeySlot(context.Background(), 88)
	require.NotNil(t, release)
	require.Equal(t, []int64{88}, cache.trackedAPIKeyIDs)
	require.Len(t, cache.trackedAPIKeyRequestIDs, 1)
	require.NotEmpty(t, cache.trackedAPIKeyRequestIDs[0])

	release()

	require.Equal(t, []int64{88}, cache.releasedAPIKeyIDs)
	require.Equal(t, cache.trackedAPIKeyRequestIDs, cache.releasedAPIKeyRequestIDs)
}

func TestTrackAPIKeySlot_FailOpen(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{apiKeyTrackErr: errors.New("redis down")}
	svc := NewConcurrencyService(cache)

	release := svc.TrackAPIKeySlot(context.Background(), 88)
	require.NotNil(t, release)
	require.Equal(t, []int64{88}, cache.trackedAPIKeyIDs)

	require.NotPanics(t, release)
	require.Empty(t, cache.releasedAPIKeyIDs)
}

func TestGetAPIKeyConcurrencyBatch_Fallbacks(t *testing.T) {
	t.Run("nil cache returns zeroes", func(t *testing.T) {
		svc := &ConcurrencyService{cache: nil}

		counts, err := svc.GetAPIKeyConcurrencyBatch(context.Background(), []int64{1, 2})
		require.NoError(t, err)
		require.Equal(t, map[int64]int{1: 0, 2: 0}, counts)
	})

	t.Run("redis error returns zeroes", func(t *testing.T) {
		cache := &stubConcurrencyCacheForTest{apiKeyConcurrencyErr: errors.New("redis down")}
		svc := NewConcurrencyService(cache)

		counts, err := svc.GetAPIKeyConcurrencyBatch(context.Background(), []int64{1, 2})
		require.NoError(t, err)
		require.Equal(t, map[int64]int{1: 0, 2: 0}, counts)
	})

	t.Run("success returns counts", func(t *testing.T) {
		cache := &stubConcurrencyCacheForTest{apiKeyConcurrency: map[int64]int{1: 3, 2: 0}}
		svc := NewConcurrencyService(cache)

		counts, err := svc.GetAPIKeyConcurrencyBatch(context.Background(), []int64{1, 2})
		require.NoError(t, err)
		require.Equal(t, map[int64]int{1: 3, 2: 0}, counts)
	})
}

func TestAcquireOpenAIWSIngressLease(t *testing.T) {
	t.Run("zero value release is safe", func(t *testing.T) {
		var lease OpenAIWSIngressLease
		require.NotPanics(t, lease.Release)
	})

	t.Run("disabled", func(t *testing.T) {
		cache := &ingressLeaseCacheForTest{}
		lease, acquired, err := NewConcurrencyService(cache).AcquireOpenAIWSIngressLease(nil, 1, 0)
		require.NoError(t, err)
		require.True(t, acquired)
		require.Nil(t, lease)
		require.Zero(t, cache.acquireIngressCalls)
	})

	t.Run("unsupported cache fails closed", func(t *testing.T) {
		lease, acquired, err := NewConcurrencyService(&stubConcurrencyCacheForTest{}).AcquireOpenAIWSIngressLease(context.Background(), 1, 1)
		require.Error(t, err)
		require.False(t, acquired)
		require.Nil(t, lease)
	})

	t.Run("capacity rejected", func(t *testing.T) {
		cache := &ingressLeaseCacheForTest{acquireIngressResult: false}
		lease, acquired, err := NewConcurrencyService(cache).AcquireOpenAIWSIngressLease(context.Background(), 1, 1)
		require.NoError(t, err)
		require.False(t, acquired)
		require.Nil(t, lease)
	})

	t.Run("release returns capacity", func(t *testing.T) {
		cache := &ingressLeaseCacheForTest{acquireIngressResult: true, refreshIngressResult: true}
		lease, acquired, err := NewConcurrencyService(cache).AcquireOpenAIWSIngressLease(nil, 1, 1)
		require.NoError(t, err)
		require.True(t, acquired)
		require.NotNil(t, lease)
		lease.Release()
		lease.Release()
		require.Equal(t, 1, cache.releaseIngressCalls)
	})
}

func TestOpenAIWSIngressLeaseRefreshLoss(t *testing.T) {
	t.Run("missing lease is lost immediately", func(t *testing.T) {
		cache := &ingressLeaseCacheForTest{refreshIngressResult: false}
		lease := &OpenAIWSIngressLease{cache: cache, apiKeyID: 1, leaseID: "missing"}
		_, lost := lease.refresh(time.Now())
		require.True(t, lost)
		require.Equal(t, 1, cache.refreshIngressCalls)
	})

	t.Run("persistent redis errors lose lease after ttl", func(t *testing.T) {
		cache := &ingressLeaseCacheForTest{refreshIngressErr: errors.New("redis unavailable")}
		lease := &OpenAIWSIngressLease{cache: cache, apiKeyID: 1, leaseID: "unconfirmed"}
		_, lost := lease.refresh(time.Now().Add(-openAIWSIngressLeaseTTL))
		require.True(t, lost)
		require.Equal(t, 1, cache.refreshIngressCalls)
	})
}

func TestOpenAIWSIngressLeaseReleaseWaitsForInFlightRefresh(t *testing.T) {
	refreshStarted := make(chan struct{})
	allowRefresh := make(chan struct{})
	cache := &ingressLeaseCacheForTest{
		refreshIngressFn: func(context.Context, int64, string) (bool, error) {
			close(refreshStarted)
			<-allowRefresh
			return true, nil
		},
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	lease := &OpenAIWSIngressLease{
		ctx:         ctx,
		cancel:      cancel,
		cache:       cache,
		apiKeyID:    1,
		leaseID:     "in-flight-refresh",
		stopCh:      make(chan struct{}),
		refreshDone: make(chan struct{}),
	}
	go func() {
		defer close(lease.refreshDone)
		_, _ = lease.refresh(time.Now())
	}()
	<-refreshStarted

	released := make(chan struct{})
	go func() {
		lease.Release()
		close(released)
	}()

	select {
	case <-released:
		t.Fatal("release returned before the in-flight refresh completed")
	case <-time.After(20 * time.Millisecond):
	}
	require.Zero(t, cache.releaseIngressCalls)

	close(allowRefresh)
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("release did not complete after the refresh returned")
	}
	require.Equal(t, 1, cache.releaseIngressCalls)
}

func TestGenerateRequestID_UsesStablePrefixAndMonotonicCounter(t *testing.T) {
	id1 := generateRequestID()
	id2 := generateRequestID()
	require.NotEmpty(t, id1)
	require.NotEmpty(t, id2)

	p1 := strings.Split(id1, "-")
	p2 := strings.Split(id2, "-")
	require.Len(t, p1, 2)
	require.Len(t, p2, 2)
	require.Equal(t, p1[0], p2[0], "同一进程前缀应保持一致")

	n1, err := strconv.ParseUint(p1[1], 36, 64)
	require.NoError(t, err)
	n2, err := strconv.ParseUint(p2[1], 36, 64)
	require.NoError(t, err)
	require.Equal(t, n1+1, n2, "计数器应单调递增")
}

func TestGetAccountsLoadBatch_ReturnsCorrectData(t *testing.T) {
	expected := map[int64]*AccountLoadInfo{
		1: {AccountID: 1, CurrentConcurrency: 3, WaitingCount: 0, LoadRate: 60},
		2: {AccountID: 2, CurrentConcurrency: 5, WaitingCount: 2, LoadRate: 100},
	}
	cache := &stubConcurrencyCacheForTest{loadBatch: expected}
	svc := NewConcurrencyService(cache)

	accounts := []AccountWithConcurrency{
		{ID: 1, MaxConcurrency: 5},
		{ID: 2, MaxConcurrency: 5},
	}
	result, err := svc.GetAccountsLoadBatch(context.Background(), accounts)
	require.NoError(t, err)
	require.Equal(t, expected, result)
}

func TestGetAccountsLoadBatch_NilCache(t *testing.T) {
	svc := &ConcurrencyService{cache: nil}

	result, err := svc.GetAccountsLoadBatch(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, result)
}

func TestGetAccountsLoadBatch_UsesShortTTLCache(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{
		loadBatch: map[int64]*AccountLoadInfo{
			1: {AccountID: 1, CurrentConcurrency: 1, LoadRate: 20},
		},
	}
	svc := NewConcurrencyService(cache)
	svc.SetAccountLoadBatchCacheTTL(time.Second)

	accounts := []AccountWithConcurrency{{ID: 1, MaxConcurrency: 5}}
	first, err := svc.GetAccountsLoadBatch(context.Background(), accounts)
	require.NoError(t, err)
	require.Equal(t, 1, first[int64(1)].CurrentConcurrency)

	cache.loadBatch[1] = &AccountLoadInfo{AccountID: 1, CurrentConcurrency: 4, LoadRate: 80}
	second, err := svc.GetAccountsLoadBatch(context.Background(), accounts)
	require.NoError(t, err)
	require.Equal(t, 1, second[int64(1)].CurrentConcurrency)
	require.Equal(t, int64(1), cache.loadBatchCalls.Load())
}

func TestGetAccountsLoadBatchFresh_BypassesShortTTLCache(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{
		loadBatch: map[int64]*AccountLoadInfo{
			1: {AccountID: 1, CurrentConcurrency: 1, LoadRate: 20},
		},
	}
	svc := NewConcurrencyService(cache)
	svc.SetAccountLoadBatchCacheTTL(time.Second)

	accounts := []AccountWithConcurrency{{ID: 1, MaxConcurrency: 5}}
	_, err := svc.GetAccountsLoadBatch(context.Background(), accounts)
	require.NoError(t, err)

	cache.loadBatch[1] = &AccountLoadInfo{AccountID: 1, CurrentConcurrency: 4, LoadRate: 80}
	fresh, err := svc.GetAccountsLoadBatchFresh(context.Background(), accounts)
	require.NoError(t, err)
	require.Equal(t, 4, fresh[int64(1)].CurrentConcurrency)
	require.Equal(t, int64(2), cache.loadBatchCalls.Load())
}

func TestIncrementWaitCount_Success(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{waitAllowed: true}
	svc := NewConcurrencyService(cache)

	allowed, err := svc.IncrementWaitCount(context.Background(), 1, 25)
	require.NoError(t, err)
	require.True(t, allowed)
}

func TestIncrementWaitCount_QueueFull(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{waitAllowed: false}
	svc := NewConcurrencyService(cache)

	allowed, err := svc.IncrementWaitCount(context.Background(), 1, 25)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestIncrementWaitCount_FailOpen(t *testing.T) {
	// Redis 错误时应 fail-open（允许请求通过）
	cache := &stubConcurrencyCacheForTest{waitErr: errors.New("redis timeout")}
	svc := NewConcurrencyService(cache)

	allowed, err := svc.IncrementWaitCount(context.Background(), 1, 25)
	require.NoError(t, err, "Redis 错误不应传播")
	require.True(t, allowed, "Redis 错误时应 fail-open")
}

func TestIncrementWaitCount_NilCache(t *testing.T) {
	svc := &ConcurrencyService{cache: nil}

	allowed, err := svc.IncrementWaitCount(context.Background(), 1, 25)
	require.NoError(t, err)
	require.True(t, allowed, "nil cache 应 fail-open")
}

func TestCalculateMaxWait(t *testing.T) {
	tests := []struct {
		concurrency int
		expected    int
	}{
		{5, 25},  // 5 + 20
		{1, 21},  // 1 + 20
		{0, 21},  // min(1) + 20
		{-1, 21}, // min(1) + 20
		{10, 30}, // 10 + 20
	}
	for _, tt := range tests {
		result := CalculateMaxWait(tt.concurrency)
		require.Equal(t, tt.expected, result, "CalculateMaxWait(%d)", tt.concurrency)
	}
}

func TestGetAccountWaitingCount(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{waitCount: 5}
	svc := NewConcurrencyService(cache)

	count, err := svc.GetAccountWaitingCount(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 5, count)
}

func TestGetAccountWaitingCount_NilCache(t *testing.T) {
	svc := &ConcurrencyService{cache: nil}

	count, err := svc.GetAccountWaitingCount(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestGetAccountConcurrencyBatch(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{concurrency: 3}
	svc := NewConcurrencyService(cache)

	result, err := svc.GetAccountConcurrencyBatch(context.Background(), []int64{1, 2, 3})
	require.NoError(t, err)
	require.Len(t, result, 3)
	for _, id := range []int64{1, 2, 3} {
		require.Equal(t, 3, result[id])
	}
}

func TestIncrementAccountWaitCount_FailOpen(t *testing.T) {
	cache := &stubConcurrencyCacheForTest{waitErr: errors.New("redis error")}
	svc := NewConcurrencyService(cache)

	allowed, err := svc.IncrementAccountWaitCount(context.Background(), 1, 10)
	require.NoError(t, err, "Redis 错误不应传播")
	require.True(t, allowed, "Redis 错误时应 fail-open")
}

func TestIncrementAccountWaitCount_NilCache(t *testing.T) {
	svc := &ConcurrencyService{cache: nil}

	allowed, err := svc.IncrementAccountWaitCount(context.Background(), 1, 10)
	require.NoError(t, err)
	require.True(t, allowed)
}
