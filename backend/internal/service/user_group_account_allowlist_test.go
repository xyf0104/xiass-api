package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type userGroupAccountAllowlistRepositoryStub struct {
	accountIDs []int64
	restricted bool
	getErr     error
	replaceErr error
	restoreErr error
	getCalls   int

	replacedUserID     int64
	replacedGroupID    int64
	replacedAccountIDs []int64
	restoredUserID     int64
	restoredGroupID    int64
}

func (r *userGroupAccountAllowlistRepositoryStub) GetAllowedAccountIDs(context.Context, int64, int64) ([]int64, bool, error) {
	r.getCalls++
	return append([]int64(nil), r.accountIDs...), r.restricted, r.getErr
}

func (r *userGroupAccountAllowlistRepositoryStub) ReplaceAllowedAccountIDs(_ context.Context, userID, groupID int64, accountIDs []int64) error {
	r.replacedUserID = userID
	r.replacedGroupID = groupID
	r.replacedAccountIDs = append([]int64(nil), accountIDs...)
	if r.replaceErr == nil {
		r.accountIDs = append([]int64(nil), accountIDs...)
		r.restricted = true
	}
	return r.replaceErr
}

func (r *userGroupAccountAllowlistRepositoryStub) RestoreAllowedAccountIDs(_ context.Context, userID, groupID int64) error {
	r.restoredUserID = userID
	r.restoredGroupID = groupID
	if r.restoreErr == nil {
		r.accountIDs = nil
		r.restricted = false
	}
	return r.restoreErr
}

type userGroupAccountAllowlistCandidateRepositoryStub struct {
	accounts []Account
	err      error
}

func (r *userGroupAccountAllowlistCandidateRepositoryStub) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return append([]Account(nil), r.accounts...), r.err
}

func (r *userGroupAccountAllowlistCandidateRepositoryStub) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	if r.err != nil {
		return nil, r.err
	}
	wanted := int64Set(ids)
	accounts := make([]*Account, 0, len(ids))
	for i := range r.accounts {
		if wanted[r.accounts[i].ID] {
			account := r.accounts[i]
			accounts = append(accounts, &account)
		}
	}
	return accounts, nil
}

// Embedding the production interface keeps this narrow test double focused on
// the one candidate query used by Gemini compatibility scheduling.
type userGroupAccountAllowlistSchedulingRepositoryStub struct {
	AccountRepository
	accounts []Account
	err      error
}

func (r *userGroupAccountAllowlistSchedulingRepositoryStub) ListSchedulableByGroupIDAndPlatforms(_ context.Context, _ int64, _ []string) ([]Account, error) {
	return append([]Account(nil), r.accounts...), r.err
}

type userGroupAccountAllowlistAdminStub struct {
	AdminService
	group      *Group
	users      map[int64]*User
	batchCalls int
}

func (s *userGroupAccountAllowlistAdminStub) GetGroup(_ context.Context, _ int64) (*Group, error) {
	return s.group, nil
}

func (s *userGroupAccountAllowlistAdminStub) GetUser(_ context.Context, userID int64) (*User, error) {
	return s.users[userID], nil
}

func (s *userGroupAccountAllowlistAdminStub) GetUsersByIDs(_ context.Context, userIDs []int64) ([]User, error) {
	s.batchCalls++
	users := make([]User, 0, len(userIDs))
	for _, userID := range userIDs {
		if user := s.users[userID]; user != nil {
			users = append(users, *user)
		}
	}
	return users, nil
}

type userGroupAccountAllowlistRuntimeReaderStub struct {
	snapshot *UserGroupAccountConcurrencySnapshot
	err      error
}

func (s *userGroupAccountAllowlistRuntimeReaderStub) GetUserGroupAccountConcurrencySnapshot(context.Context, int64) (*UserGroupAccountConcurrencySnapshot, error) {
	return s.snapshot, s.err
}

func TestUserGroupAccountAllowlistServiceReplaceNormalizesAndValidatesCandidates(t *testing.T) {
	repo := &userGroupAccountAllowlistRepositoryStub{}
	candidates := &userGroupAccountAllowlistCandidateRepositoryStub{accounts: []Account{
		{ID: 12, Name: "first", Status: StatusActive, Schedulable: true},
		{ID: 7, Name: "second", Status: StatusActive, Schedulable: true},
	}}
	svc := NewUserGroupAccountAllowlistService(repo, candidates)

	err := svc.ReplaceAllowedAccountIDs(context.Background(), 41, 9, []int64{12, 7, 12})
	require.NoError(t, err)
	require.Equal(t, int64(41), repo.replacedUserID)
	require.Equal(t, int64(9), repo.replacedGroupID)
	require.Equal(t, []int64{7, 12}, repo.replacedAccountIDs)

	err = svc.ReplaceAllowedAccountIDs(context.Background(), 41, 9, []int64{99})
	require.ErrorIs(t, err, ErrUserGroupAccountAllowlistUnavailable)
	require.Equal(t, []int64{7, 12}, repo.replacedAccountIDs)
}

func TestUserGroupAccountAllowlistServiceReplaceEmptyKeepsRestrictedScope(t *testing.T) {
	repo := &userGroupAccountAllowlistRepositoryStub{}
	svc := NewUserGroupAccountAllowlistService(repo, nil)

	err := svc.ReplaceAllowedAccountIDs(context.Background(), 41, 9, []int64{})
	require.NoError(t, err)
	require.Empty(t, repo.replacedAccountIDs)
	require.Zero(t, repo.restoredUserID)
}

func TestUserGroupAccountAllowlistServiceRestoreIsExplicit(t *testing.T) {
	repo := &userGroupAccountAllowlistRepositoryStub{}
	svc := NewUserGroupAccountAllowlistService(repo, nil)

	err := svc.RestoreAllowedAccountIDs(context.Background(), 41, 9)
	require.NoError(t, err)
	require.Equal(t, int64(41), repo.restoredUserID)
	require.Equal(t, int64(9), repo.restoredGroupID)
}

func TestUserGroupAccountAllowlistServiceRejectsInvalidIDsBeforeCandidateLookup(t *testing.T) {
	repo := &userGroupAccountAllowlistRepositoryStub{}
	candidates := &userGroupAccountAllowlistCandidateRepositoryStub{err: errors.New("should not query candidates")}
	svc := NewUserGroupAccountAllowlistService(repo, candidates)

	err := svc.ReplaceAllowedAccountIDs(context.Background(), 41, 9, []int64{0})
	require.ErrorIs(t, err, ErrUserGroupAccountAllowlistInvalidID)
	require.Empty(t, repo.replacedAccountIDs)
}

func TestUserGroupAccountAllowlistServiceGetAdminSelectionKeepsStoredIDsAndFiltersCandidates(t *testing.T) {
	repo := &userGroupAccountAllowlistRepositoryStub{
		accountIDs: []int64{5, 2, 5},
		restricted: true,
	}
	candidates := &userGroupAccountAllowlistCandidateRepositoryStub{accounts: []Account{
		{ID: 2, Name: "allowed", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Priority: 1, Concurrency: 3, Status: StatusActive, Schedulable: true},
		{ID: 4, Name: "temporary", Status: StatusActive, Schedulable: true, RateLimitResetAt: allowlistTimePtr(time.Now().Add(time.Hour))},
		{ID: 5, Name: "no longer active", Status: StatusDisabled, Schedulable: true},
	}}
	svc := NewUserGroupAccountAllowlistService(repo, candidates)

	selection, err := svc.GetAdminSelection(context.Background(), 41, 9)
	require.NoError(t, err)
	require.True(t, selection.Restricted)
	require.Equal(t, []int64{2, 5}, selection.AllowedAccountIDs)
	require.Len(t, selection.Candidates, 2)
	require.Equal(t, int64(2), selection.Candidates[0].AccountID)
	require.True(t, selection.Candidates[0].Allowed)
	require.True(t, selection.Candidates[0].Available)
	require.Equal(t, int64(5), selection.Candidates[1].AccountID)
	require.True(t, selection.Candidates[1].Allowed)
	require.False(t, selection.Candidates[1].Available)
}

func TestUserGroupAccountAllowlistServiceMayRetainButNotAddUnavailableAccounts(t *testing.T) {
	repo := &userGroupAccountAllowlistRepositoryStub{
		accountIDs: []int64{5},
		restricted: true,
	}
	candidates := &userGroupAccountAllowlistCandidateRepositoryStub{accounts: []Account{
		{ID: 2, Status: StatusActive, Schedulable: true},
		{ID: 5, Status: StatusDisabled, Schedulable: true},
	}}
	svc := NewUserGroupAccountAllowlistService(repo, candidates)

	err := svc.ReplaceAllowedAccountIDs(context.Background(), 41, 9, []int64{2, 5})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 5}, repo.replacedAccountIDs)

	err = svc.ReplaceAllowedAccountIDs(context.Background(), 41, 9, []int64{7})
	require.ErrorIs(t, err, ErrUserGroupAccountAllowlistUnavailable)
	require.Equal(t, []int64{2, 5}, repo.replacedAccountIDs)
}

func TestUserGroupAccountAllowlistPolicyPreservesOriginalSchedulingWithoutRestriction(t *testing.T) {
	groupID := int64(9)
	accounts := []Account{{ID: 1}, {ID: 2}}
	repo := &userGroupAccountAllowlistRepositoryStub{accountIDs: []int64{2}, restricted: true}
	policy := NewUserGroupAccountAllowlistPolicy(repo)

	withoutUser, err := policy.FilterCandidates(context.Background(), &groupID, accounts)
	require.NoError(t, err)
	require.Equal(t, accounts, withoutUser)

	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(41))
	repo.restricted = false
	withoutRows, err := policy.FilterCandidates(ctx, &groupID, accounts)
	require.NoError(t, err)
	require.Equal(t, accounts, withoutRows)
	require.NoError(t, policy.RequireCandidate(ctx, &groupID, 1))
}

func TestUserGroupAccountAllowlistPolicyFiltersExplicitCandidatesAndFailsClosed(t *testing.T) {
	groupID := int64(9)
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(41))
	accounts := []Account{{ID: 1}, {ID: 2}, {ID: 3}}
	repo := &userGroupAccountAllowlistRepositoryStub{accountIDs: []int64{2}, restricted: true}
	policy := NewUserGroupAccountAllowlistPolicy(repo)

	filtered, err := policy.FilterCandidates(ctx, &groupID, accounts)
	require.NoError(t, err)
	require.Equal(t, []Account{{ID: 2}}, filtered)
	require.NoError(t, policy.RequireCandidate(ctx, &groupID, 2))
	require.ErrorIs(t, policy.RequireCandidate(ctx, &groupID, 1), ErrUserGroupAccountNotAllowed)

	repo.getErr = errors.New("allowlist store unavailable")
	filtered, err = filterAccountCandidatesWithPolicy(ctx, policy, &groupID, accounts)
	require.Error(t, err)
	require.Equal(t, accounts, filtered, "callers must see the error and fail closed instead of widening the pool")
}

func TestUserGroupAccountAllowlistPolicyRestrictedEmptyScopeDeniesEveryCandidate(t *testing.T) {
	groupID := int64(9)
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(41))
	policy := NewUserGroupAccountAllowlistPolicy(&userGroupAccountAllowlistRepositoryStub{restricted: true})

	filtered, err := policy.FilterCandidates(ctx, &groupID, []Account{{ID: 1}, {ID: 2}})
	require.NoError(t, err)
	require.Empty(t, filtered)
	require.ErrorIs(t, policy.RequireCandidate(ctx, &groupID, 1), ErrUserGroupAccountNotAllowed)
}

func TestUserGroupAccountAllowlistPolicySameClientRequestIDObservesReplaceAndRestoreImmediately(t *testing.T) {
	groupID := int64(9)
	repo := &userGroupAccountAllowlistRepositoryStub{accountIDs: []int64{2}, restricted: true}
	policy := NewUserGroupAccountAllowlistPolicy(repo)
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(41))
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "websocket-connection-1")

	filtered, err := policy.FilterCandidates(ctx, &groupID, []Account{{ID: 1}, {ID: 2}})
	require.NoError(t, err)
	require.Equal(t, []Account{{ID: 2}}, filtered)

	require.NoError(t, repo.ReplaceAllowedAccountIDs(ctx, 41, groupID, []int64{1}))
	filtered, err = policy.FilterCandidates(ctx, &groupID, []Account{{ID: 1}, {ID: 2}})
	require.NoError(t, err)
	require.Equal(t, []Account{{ID: 1}}, filtered)
	require.ErrorIs(t, policy.RequireCandidate(ctx, &groupID, 2), ErrUserGroupAccountNotAllowed)

	require.NoError(t, repo.RestoreAllowedAccountIDs(ctx, 41, groupID))
	filtered, err = policy.FilterCandidates(ctx, &groupID, []Account{{ID: 1}, {ID: 2}})
	require.NoError(t, err)
	require.Equal(t, []Account{{ID: 1}, {ID: 2}}, filtered)
	require.Equal(t, 4, repo.getCalls, "every scheduling decision must observe repository invalidation")
}

func TestUserGroupAccountAllowlistPolicyClearsOrdinaryStickyBinding(t *testing.T) {
	groupID := int64(9)
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(41))
	cache := &stubGatewayCache{sessionBindings: map[string]int64{"session": 1}}
	policy := NewUserGroupAccountAllowlistPolicy(&userGroupAccountAllowlistRepositoryStub{
		accountIDs: []int64{2}, restricted: true,
	})
	svc := &GatewayService{cache: cache, candidatePolicy: policy}

	accountID, err := svc.getAllowedGatewayStickySessionAccountID(ctx, &groupID, "session")
	require.ErrorIs(t, err, ErrStickySessionNotFound)
	require.Zero(t, accountID)
	require.Equal(t, 1, cache.deletedSessions["session"])
	require.NotContains(t, cache.sessionBindings, "session")
}

func TestOpenAIAccountAllowlistFiltersCandidatesAndClearsOrdinaryStickyBinding(t *testing.T) {
	groupID := int64(9)
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(41))
	policy := NewUserGroupAccountAllowlistPolicy(&userGroupAccountAllowlistRepositoryStub{
		accountIDs: []int64{2}, restricted: true,
	})
	cache := &stubGatewayCache{sessionBindings: map[string]int64{"openai:session": 1}}
	svc := &OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []Account{
			{ID: 1, Platform: PlatformOpenAI},
			{ID: 2, Platform: PlatformOpenAI},
		}},
		cache:           cache,
		candidatePolicy: policy,
	}

	accounts, err := svc.listSchedulableAccounts(ctx, &groupID, PlatformOpenAI)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, []int64{accounts[0].ID})

	accountID, err := svc.getStickySessionAccountID(ctx, &groupID, "session")
	require.ErrorIs(t, err, ErrStickySessionNotFound)
	require.Zero(t, accountID)
	require.Equal(t, 1, cache.deletedSessions["openai:session"])
}

func TestGeminiAccountAllowlistFiltersSnapshotFallbackCandidates(t *testing.T) {
	groupID := int64(9)
	ctx := context.WithValue(context.Background(), ctxkey.UserID, int64(41))
	policy := NewUserGroupAccountAllowlistPolicy(&userGroupAccountAllowlistRepositoryStub{
		accountIDs: []int64{2}, restricted: true,
	})
	svc := &GeminiMessagesCompatService{
		accountRepo: &userGroupAccountAllowlistSchedulingRepositoryStub{accounts: []Account{
			{ID: 1, Platform: PlatformGemini},
			{ID: 2, Platform: PlatformGemini},
		}},
		candidatePolicy: policy,
	}

	accounts, err := svc.listSchedulableAccountsOnce(ctx, &groupID, PlatformGemini, false)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, int64(2), accounts[0].ID)
}

func TestAdminUserGroupAccountAllowlistRuntimeReportsActualUsersAccountsAndConcurrency(t *testing.T) {
	admin := &userGroupAccountAllowlistAdminStub{
		group: &Group{ID: 9},
		users: map[int64]*User{
			41: {ID: 41, Username: "member", Email: "member@example.com"},
		},
	}
	candidates := &userGroupAccountAllowlistCandidateRepositoryStub{accounts: []Account{
		{ID: 11, Name: "first", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Priority: 1, Concurrency: 3, Status: StatusActive, Schedulable: true},
		{ID: 12, Name: "second", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Priority: 2, Concurrency: 2, Status: StatusActive, Schedulable: true},
	}}
	runtimeReader := &userGroupAccountAllowlistRuntimeReaderStub{snapshot: &UserGroupAccountConcurrencySnapshot{
		SnapshotAt: time.Unix(1_700_000_000, 0),
		Counts: map[int64]map[int64]int{
			41: {12: 1, 11: 2},
		},
	}}
	svc := NewAdminUserGroupAccountAllowlistService(admin, nil, candidates, runtimeReader)

	runtime, err := svc.GetRuntime(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, runtime.Accounts, 2)
	require.Equal(t, int64(11), runtime.Accounts[0].AccountID)
	require.Equal(t, 2, runtime.Accounts[0].CurrentConcurrency)
	require.Equal(t, 1, runtime.Accounts[1].CurrentConcurrency)
	require.Len(t, runtime.Users, 1)
	require.Equal(t, 3, runtime.Users[0].CurrentConcurrency)
	require.Equal(t, []int64{11, 12}, runtime.Users[0].ActiveAccountIDs)
	require.Equal(t, map[int64]int{11: 2, 12: 1}, runtime.Users[0].ActiveAccountConcurrency)
	require.Equal(t, 1, admin.batchCalls)
}

func TestAdminUserGroupAccountAllowlistRuntimeKeepsIngressRequestsAndOccupiedAccountsDistinct(t *testing.T) {
	admin := &userGroupAccountAllowlistAdminStub{
		group: &Group{ID: 9},
		users: map[int64]*User{
			41: {ID: 41, Username: "member", Email: "member@example.com"},
		},
	}
	candidates := &userGroupAccountAllowlistCandidateRepositoryStub{accounts: []Account{
		{ID: 11, Name: "single-slot", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Priority: 1, Concurrency: 1, Status: StatusActive, Schedulable: true},
	}}
	runtimeReader := &userGroupAccountAllowlistRuntimeReaderStub{snapshot: &UserGroupAccountConcurrencySnapshot{
		SnapshotAt: time.Unix(1_700_000_000, 0),
		Counts: map[int64]map[int64]int{
			41: {11: 1},
		},
		UserCounts: map[int64]int{41: 4},
	}}
	svc := NewAdminUserGroupAccountAllowlistService(admin, nil, candidates, runtimeReader)

	runtime, err := svc.GetRuntime(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, runtime.Accounts, 1)
	require.Equal(t, 1, runtime.Accounts[0].CurrentConcurrency)
	require.Len(t, runtime.Users, 1)
	require.Equal(t, 4, runtime.Users[0].CurrentConcurrency)
	require.Equal(t, []int64{11}, runtime.Users[0].ActiveAccountIDs)
	require.Equal(t, map[int64]int{11: 1}, runtime.Users[0].ActiveAccountConcurrency)
}

func TestAdminUserGroupAccountAllowlistRuntimeKeepsActiveAccountThatBecameUnavailable(t *testing.T) {
	admin := &userGroupAccountAllowlistAdminStub{
		group: &Group{ID: 9},
		users: map[int64]*User{
			41: {ID: 41, Username: "member", Email: "member@example.com"},
		},
	}
	candidates := &userGroupAccountAllowlistCandidateRepositoryStub{accounts: []Account{
		{ID: 11, Name: "normal", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Priority: 1, Concurrency: 3, Status: StatusActive, Schedulable: true},
		{ID: 12, Name: "disabled while running", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Priority: 2, Concurrency: 2, Status: StatusDisabled, Schedulable: false},
	}}
	runtimeReader := &userGroupAccountAllowlistRuntimeReaderStub{snapshot: &UserGroupAccountConcurrencySnapshot{
		SnapshotAt: time.Unix(1_700_000_000, 0),
		Counts: map[int64]map[int64]int{
			41: {12: 1},
		},
	}}
	svc := NewAdminUserGroupAccountAllowlistService(admin, nil, candidates, runtimeReader)

	runtime, err := svc.GetRuntime(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, runtime.Accounts, 2)
	require.Equal(t, int64(11), runtime.Accounts[0].AccountID)
	require.True(t, runtime.Accounts[0].Available)
	require.Equal(t, int64(12), runtime.Accounts[1].AccountID)
	require.Equal(t, "disabled while running", runtime.Accounts[1].Name)
	require.False(t, runtime.Accounts[1].Available)
	require.Equal(t, 1, runtime.Accounts[1].CurrentConcurrency)
	require.Equal(t, []int64{12}, runtime.Users[0].ActiveAccountIDs)
	require.Equal(t, 1, runtime.Users[0].CurrentConcurrency)
}

func TestAdminUserGroupAccountAllowlistRuntimeIgnoresStaleDeletedUserLease(t *testing.T) {
	admin := &userGroupAccountAllowlistAdminStub{
		group: &Group{ID: 9},
		users: map[int64]*User{41: {ID: 41, Username: "member", Email: "member@example.com"}},
	}
	runtimeReader := &userGroupAccountAllowlistRuntimeReaderStub{snapshot: &UserGroupAccountConcurrencySnapshot{
		SnapshotAt: time.Unix(1_700_000_000, 0),
		Counts: map[int64]map[int64]int{
			41: {11: 1},
			42: {12: 1},
		},
	}}
	svc := NewAdminUserGroupAccountAllowlistService(
		admin,
		nil,
		&userGroupAccountAllowlistCandidateRepositoryStub{},
		runtimeReader,
	)

	runtime, err := svc.GetRuntime(context.Background(), 9)
	require.NoError(t, err)
	require.Len(t, runtime.Users, 1)
	require.Equal(t, int64(41), runtime.Users[0].UserID)
	require.Equal(t, 1, admin.batchCalls)
}

func allowlistTimePtr(value time.Time) *time.Time { return &value }
