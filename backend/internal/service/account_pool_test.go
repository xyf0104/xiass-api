//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type accountPoolRepoStub struct {
	AccountPoolRepository
	pool      AccountPool
	accounts  []*Account
	shadows   []*Account
	updates   []AccountBulkUpdate
	ids       []int64
	proxyErr  error
	bulkErr   error
	shadowErr error
	commitErr error
	committed bool
	deleted   bool
}

func (r *accountPoolRepoStub) WithAccountPoolTransaction(ctx context.Context, fn func(context.Context, AccountPoolRepository) error) error {
	if err := fn(ctx, r); err != nil {
		return err
	}
	if r.commitErr != nil {
		return r.commitErr
	}
	r.committed = true
	return nil
}
func (r *accountPoolRepoStub) GetAccountPool(context.Context, int64) (*AccountPool, error) {
	p := r.pool
	return &p, nil
}
func (r *accountPoolRepoStub) CreateAccountPool(_ context.Context, name string, proxyID *int64) (*AccountPool, error) {
	r.pool = AccountPool{ID: 8, Name: name, ProxyID: proxyID, AccountIDs: []int64{}}
	return &r.pool, nil
}
func (r *accountPoolRepoStub) UpdateAccountPool(_ context.Context, _ int64, name string, proxyID *int64) error {
	r.pool.Name, r.pool.ProxyID = name, proxyID
	return nil
}
func (r *accountPoolRepoStub) DeleteAccountPool(context.Context, int64) error {
	r.deleted = true
	return nil
}
func (r *accountPoolRepoStub) LockAccountPoolAccounts(context.Context, []int64) ([]*Account, error) {
	return r.accounts, nil
}
func (r *accountPoolRepoStub) GetByIDs(context.Context, []int64) ([]*Account, error) {
	return r.accounts, nil
}
func (r *accountPoolRepoStub) ValidateAccountPoolProxy(context.Context, int64) error {
	return r.proxyErr
}
func (r *accountPoolRepoStub) ListShadowsByParent(context.Context, int64) ([]*Account, error) {
	return r.shadows, r.shadowErr
}
func (r *accountPoolRepoStub) BulkUpdate(_ context.Context, ids []int64, input AccountBulkUpdate) (int64, error) {
	r.ids = append([]int64{}, ids...)
	r.updates = append(r.updates, input)
	return int64(len(ids)), r.bulkErr
}

func TestAccountPoolAssignMixesPlansWithoutChangingScheduling(t *testing.T) {
	p := int64(42)
	r := &accountPoolRepoStub{pool: AccountPool{ID: 8, Name: "mixed", ProxyID: &p}}
	for i, plan := range []string{"pro", "plus", "team"} {
		r.accounts = append(r.accounts, &Account{ID: int64(i + 1), Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Credentials: map[string]any{"plan_type": plan}, Extra: map[string]any{"codex_fingerprint_mode": "device", "unrelated": "preserve"}})
	}
	s := &adminServiceImpl{accountRepo: r}
	pool, err := s.AssignAccountPool(context.Background(), 8, []int64{3, 1, 2, 1}, false)
	require.NoError(t, err)
	require.NotNil(t, pool)
	require.True(t, r.committed)
	require.Equal(t, []int64{1, 2, 3}, r.ids)
	require.Len(t, r.updates, 1)
	u := r.updates[0]
	require.Equal(t, &p, u.ProxyID)
	require.Equal(t, "8", u.Extra[AccountPoolExtraKey])
	require.Equal(t, "42", u.Extra[AccountExecutionProxyExtraKey])
	require.Nil(t, u.Concurrency)
	require.Nil(t, u.Priority)
	require.Nil(t, u.RateMultiplier)
	require.Nil(t, u.LoadFactor)
	require.Nil(t, u.Schedulable)
	require.Empty(t, u.Credentials)
	require.NotContains(t, u.Extra, "codex_fingerprint_mode")
	require.False(t, u.EnsureCodexFingerprintSeed)
	require.Equal(t, "preserve", r.accounts[0].Extra["unrelated"])
}

func TestAccountPoolRejectsInvalidTargetsBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		ids                           []int64
		missing, shadow, invalidProxy bool
	}{
		{name: "empty"}, {name: "negative", ids: []int64{-1}},
		{name: "missing", ids: []int64{1, 2}, missing: true},
		{name: "shadow", ids: []int64{1}, shadow: true},
		{name: "proxy", ids: []int64{1}, invalidProxy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, parent := int64(42), int64(10)
			a := &Account{ID: 1}
			if tc.shadow {
				a.ParentAccountID = &parent
			}
			r := &accountPoolRepoStub{pool: AccountPool{ID: 8, ProxyID: &p}, accounts: []*Account{a}}
			if tc.invalidProxy {
				r.proxyErr = errors.New("disabled proxy")
			}
			_, err := (&adminServiceImpl{accountRepo: r}).AssignAccountPool(context.Background(), 8, tc.ids, false)
			require.Error(t, err)
			require.Empty(t, r.updates)
			require.False(t, r.committed)
		})
	}
}

func TestAccountPoolDetachAndDeleteKeepCurrentProxy(t *testing.T) {
	for _, removePool := range []bool{false, true} {
		r := &accountPoolRepoStub{pool: AccountPool{ID: 8, AccountIDs: []int64{1}}, accounts: []*Account{{ID: 1}}}
		s := &adminServiceImpl{accountRepo: r}
		if removePool {
			require.NoError(t, s.DeleteAccountPool(context.Background(), 8))
		} else {
			_, err := s.AssignAccountPool(context.Background(), 8, []int64{1}, true)
			require.NoError(t, err)
		}
		require.True(t, r.committed)
		require.Len(t, r.updates, 1)
		require.Nil(t, r.updates[0].ProxyID)
		require.Equal(t, map[string]any{AccountPoolExtraKey: nil}, r.updates[0].Extra)
	}
}

func TestAccountPoolDetachRejectsStaleMembership(t *testing.T) {
	r := &accountPoolRepoStub{pool: AccountPool{ID: 8, AccountIDs: []int64{2}}, accounts: []*Account{{ID: 1}}}
	_, err := (&adminServiceImpl{accountRepo: r}).AssignAccountPool(context.Background(), 8, []int64{1}, true)
	require.Equal(t, "ACCOUNT_POOL_MEMBERSHIP_CHANGED", infraerrors.Reason(err))
	require.Empty(t, r.updates)
}

func TestAccountPoolHonorsRemoteAccountOwnership(t *testing.T) {
	p := int64(42)
	r := &accountPoolRepoStub{pool: AccountPool{ID: 8, ProxyID: &p}, accounts: []*Account{{ID: 1, Extra: map[string]any{AccountExecutionNodeExtraKey: "api"}}}}
	s := &adminServiceImpl{accountRepo: r, settingService: executionNodeAdminAccessService("api2", false, "true")}
	_, err := s.AssignAccountPool(context.Background(), 8, []int64{1}, false)
	require.Equal(t, "ACCOUNT_REMOTE_NODE_READ_ONLY", infraerrors.Reason(err))
	require.Empty(t, r.updates)
}

func TestAccountPoolNoSuccessOnWriteOrCommitFailure(t *testing.T) {
	for _, commitFailure := range []bool{false, true} {
		r := &accountPoolRepoStub{pool: AccountPool{ID: 8, AccountIDs: []int64{1}}, accounts: []*Account{{ID: 1}}}
		failure := errors.New("database failure")
		if commitFailure {
			r.commitErr = failure
		} else {
			r.bulkErr = failure
		}
		pool, err := (&adminServiceImpl{accountRepo: r}).SetAccountPoolProxy(context.Background(), 8, nil)
		require.ErrorIs(t, err, failure)
		require.Nil(t, pool)
		require.False(t, r.committed)
	}
}

func TestAccountPoolNameValidationAndEmptyPoolPersistence(t *testing.T) {
	for _, name := range []string{"", "   ", strings.Repeat("x", 101), "bad\nname", string([]byte{255})} {
		_, err := normalizeAccountPoolName(name)
		require.Error(t, err)
	}
	r := &accountPoolRepoStub{}
	p, err := (&adminServiceImpl{accountRepo: r}).CreateAccountPool(context.Background(), "  Pro Plus Team  ", nil)
	require.NoError(t, err)
	require.Equal(t, "Pro Plus Team", p.Name)
	require.Empty(t, p.AccountIDs)
	require.True(t, r.committed)
	_, err = (&adminServiceImpl{accountRepo: r}).CreateAccountPool(context.Background(), "pool", new(int64))
	require.Equal(t, "INVALID_ACCOUNT_POOL_PROXY", infraerrors.Reason(err))
}

func TestAccountPoolDirectProxyRespectsExecutionNodeRouting(t *testing.T) {
	r := &accountPoolRepoStub{pool: AccountPool{ID: 8}}
	s := &adminServiceImpl{accountRepo: r, settingService: executionNodeAdminAccessService("api2", false, "true")}
	s.settingService.executionNodeRoutingCache.Store(&cachedExecutionNodeRoutingSettings{
		settings:  ExecutionNodeRoutingSettings{Available: true, Enabled: true},
		expiresAt: time.Now().Add(time.Minute).UnixNano(),
	})
	_, err := s.SetAccountPoolProxy(context.Background(), 8, nil)
	require.Equal(t, "EXECUTION_NODE_PROXY_REQUIRED", infraerrors.Reason(err))
	require.Empty(t, r.updates)
	require.False(t, r.committed)
}

func TestAccountPoolRenameAndDeleteHonorAllMemberOwnership(t *testing.T) {
	for _, remove := range []bool{false, true} {
		r := &accountPoolRepoStub{pool: AccountPool{ID: 8, Name: "old", AccountIDs: []int64{1}},
			accounts: []*Account{{ID: 1, Extra: map[string]any{AccountExecutionNodeExtraKey: "api"}}}}
		s := &adminServiceImpl{accountRepo: r, settingService: executionNodeAdminAccessService("api2", false, "true")}
		var err error
		if remove {
			err = s.DeleteAccountPool(context.Background(), 8)
		} else {
			_, err = s.RenameAccountPool(context.Background(), 8, "new")
		}
		require.Equal(t, "ACCOUNT_REMOTE_NODE_READ_ONLY", infraerrors.Reason(err))
		require.False(t, r.deleted)
		require.Equal(t, "old", r.pool.Name)
		require.Empty(t, r.updates)
	}
}
