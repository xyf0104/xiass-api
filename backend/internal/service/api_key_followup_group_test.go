package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type followupBindingKey struct {
	groupID int64
	key     string
}

// Embedded methods panic if a resolver tries to write or enumerate state.
type followupGroupCache struct {
	GatewayCache
	LiveCallStore
	bindings   map[followupBindingKey]int64
	readErrs   map[int64]error
	reads      []followupBindingKey
	liveRecord *LiveCallRecord
	liveErr    error
	liveReads  []string
}

func (c *followupGroupCache) GetSessionAccountID(_ context.Context, groupID int64, key string) (int64, error) {
	binding := followupBindingKey{groupID, key}
	c.reads = append(c.reads, binding)
	if err := c.readErrs[groupID]; err != nil {
		return 0, err
	}
	accountID, ok := c.bindings[binding]
	if !ok {
		return 0, ErrStickySessionNotFound
	}
	return accountID, nil
}

func (c *followupGroupCache) GetLiveCall(_ context.Context, callHash string) (*LiveCallRecord, error) {
	c.liveReads = append(c.liveReads, callHash)
	return c.liveRecord, c.liveErr
}

func TestResolveGrokVideoRequestGroupOwnerIsolation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		userID, keyID int64
		groups        []int64
		wantGroup     int64
	}{
		{"secondary", 7, 45, []int64{10, 20}, 20},
		{"different-user", 8, 45, []int64{10, 20}, 0},
		{"different-key", 7, 46, []int64{10, 20}, 0},
		{"not-bound", 7, 45, []int64{10, 30}, 0},
		{"duplicate-group", 7, 45, []int64{20, 20}, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &followupGroupCache{bindings: map[followupBindingKey]int64{
				{20, "openai:" + GrokMediaVideoRequestSessionHash("task", 7, 45)}:  101,
				{30, "openai:" + GrokMediaVideoRequestSessionHash("task", 99, 99)}: 102,
			}}
			svc := &OpenAIGatewayService{cache: cache}
			groupID, err := svc.ResolveGrokVideoRequestGroup(context.Background(), "task", tc.userID, tc.keyID, tc.groups)
			if tc.wantGroup == 0 {
				require.ErrorIs(t, err, ErrAPIKeyFollowupNotFound)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.wantGroup, groupID)
			require.Len(t, cache.reads, len(NormalizeAPIKeyGroupIDs(tc.groups)))
			for _, read := range cache.reads {
				require.Contains(t, tc.groups, read.groupID)
				require.Equal(t, "openai:"+GrokMediaVideoRequestSessionHash("task", tc.userID, tc.keyID), read.key)
			}
			require.Len(t, cache.bindings, 2)
		})
	}
}

func TestResolveGrokVideoRequestGroupFailsClosed(t *testing.T) {
	cacheErr := errors.New("cache unavailable")
	for _, tc := range []struct {
		name      string
		bindings  map[int64]int64
		readErrs  map[int64]error
		wantError error
	}{
		{"missing", nil, nil, ErrAPIKeyFollowupNotFound},
		{"zero-binding", map[int64]int64{10: 0}, nil, ErrAPIKeyFollowupNotFound},
		{"ambiguous", map[int64]int64{10: 101, 20: 102}, nil, ErrAPIKeyFollowupNotFound},
		{"ambiguous-same-account", map[int64]int64{10: 101, 20: 101}, nil, ErrAPIKeyFollowupNotFound},
		{"cache-error", nil, map[int64]error{10: cacheErr}, cacheErr},
		{"error-after-match", map[int64]int64{10: 101}, map[int64]error{20: cacheErr}, cacheErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &followupGroupCache{bindings: make(map[followupBindingKey]int64), readErrs: tc.readErrs}
			for groupID, accountID := range tc.bindings {
				cache.bindings[followupBindingKey{groupID, "openai:" + GrokMediaVideoRequestSessionHash("task", 7, 45)}] = accountID
			}
			svc := &OpenAIGatewayService{cache: cache}
			groupID, err := svc.ResolveGrokVideoRequestGroup(context.Background(), "task", 7, 45, []int64{10, 20})
			require.ErrorIs(t, err, tc.wantError)
			require.Zero(t, groupID)
		})
	}
}

func TestResolveGrokVideoRequestGroupBoundedAndCancelled(t *testing.T) {
	tooMany := make([]int64, MaxAPIKeyGroups+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	for _, groups := range [][]int64{nil, {0}, {-1}, {10, 0}, tooMany} {
		cache := &followupGroupCache{}
		svc := &OpenAIGatewayService{cache: cache}
		groupID, err := svc.ResolveGrokVideoRequestGroup(context.Background(), "task", 7, 45, groups)
		require.ErrorIs(t, err, ErrAPIKeyFollowupNotFound)
		require.Zero(t, groupID)
		require.Empty(t, cache.reads)
	}
	cache := &followupGroupCache{}
	svc := &OpenAIGatewayService{cache: cache}
	for _, owner := range [][2]int64{{0, 45}, {7, 0}, {-1, 45}} {
		_, err := svc.ResolveGrokVideoRequestGroup(context.Background(), "task", owner[0], owner[1], []int64{10})
		require.ErrorIs(t, err, ErrAPIKeyFollowupNotFound)
	}
	_, err := svc.ResolveGrokVideoRequestGroup(context.Background(), " ", 7, 45, []int64{10})
	require.ErrorIs(t, err, ErrAPIKeyFollowupNotFound)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.ResolveGrokVideoRequestGroup(ctx, "task", 7, 45, []int64{10})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, cache.reads)
}

func TestResolveLiveCallGroupOwnerIsolation(t *testing.T) {
	cacheErr := errors.New("cache unavailable")
	for _, tc := range []struct {
		name   string
		mutate func(*LiveCallRecord)
		err    error
	}{
		{"valid", nil, nil},
		{"different-user", func(r *LiveCallRecord) { r.UserID++ }, nil},
		{"different-key", func(r *LiveCallRecord) { r.APIKeyID++ }, nil},
		{"different-call", func(r *LiveCallRecord) { r.CallID = "another-call" }, nil},
		{"ungrouped", func(r *LiveCallRecord) { r.GroupID = 0 }, nil},
		{"expired", func(r *LiveCallRecord) { r.ExpiresAt = time.Now().Add(-time.Minute) }, nil},
		{"closed", func(r *LiveCallRecord) { r.Controller = LiveControllerClosed }, nil},
		{"missing", nil, ErrLiveCallNotFound},
		{"cache-error", nil, cacheErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := &LiveCallRecord{CallID: "call-test", UserID: 7, APIKeyID: 45, GroupID: 20, ExpiresAt: time.Now().Add(time.Hour)}
			if tc.mutate != nil {
				tc.mutate(record)
			}
			before := *record
			cache := &followupGroupCache{liveRecord: record, liveErr: tc.err}
			svc := &OpenAIGatewayService{cache: cache}
			groupID, err := svc.ResolveLiveCallGroup(context.Background(), "call-test", 7, 45)
			if tc.name == "valid" {
				require.NoError(t, err)
				require.Equal(t, int64(20), groupID)
			} else {
				wantErr := ErrAPIKeyFollowupNotFound
				if tc.name == "cache-error" {
					wantErr = cacheErr
				}
				require.ErrorIs(t, err, wantErr)
				require.Zero(t, groupID)
			}
			require.Equal(t, []string{hashLiveCallID("call-test")}, cache.liveReads)
			require.Equal(t, before, *record)
			require.Empty(t, cache.reads)
		})
	}
}

func TestResolveLiveCallGroupMissingInvalidAndCancelled(t *testing.T) {
	cache := &followupGroupCache{}
	svc := &OpenAIGatewayService{cache: cache}
	_, err := svc.ResolveLiveCallGroup(context.Background(), "call-test", 7, 45)
	require.ErrorIs(t, err, ErrAPIKeyFollowupNotFound)
	cache.liveReads = nil
	for _, owner := range [][2]int64{{0, 45}, {7, 0}, {-1, 45}} {
		_, err = svc.ResolveLiveCallGroup(context.Background(), "call-test", owner[0], owner[1])
		require.ErrorIs(t, err, ErrAPIKeyFollowupNotFound)
	}
	_, err = svc.ResolveLiveCallGroup(context.Background(), " ", 7, 45)
	require.ErrorIs(t, err, ErrAPIKeyFollowupNotFound)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = svc.ResolveLiveCallGroup(ctx, "call-test", 7, 45)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, cache.liveReads)
	var unavailable *OpenAIGatewayService
	_, err = unavailable.ResolveLiveCallGroup(context.Background(), "call-test", 7, 45)
	require.ErrorIs(t, err, ErrLiveUnavailable)
	_, err = unavailable.ResolveGrokVideoRequestGroup(context.Background(), "task", 7, 45, []int64{10})
	require.Error(t, err)
}
