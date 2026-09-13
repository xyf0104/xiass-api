package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// UserGroupAccountRuntimeReader is the display-only runtime read boundary used
// by the admin allowlist page. It must not be used by scheduling decisions.
type UserGroupAccountRuntimeReader interface {
	GetUserGroupAccountConcurrencySnapshot(ctx context.Context, groupID int64) (*UserGroupAccountConcurrencySnapshot, error)
}

type adminUserGroupAccountRuntimeBatchReader interface {
	GetUsersByIDs(ctx context.Context, ids []int64) ([]User, error)
}

// AdminUserGroupAccountAllowlistService joins the existing allowlist domain
// service with the administrative existence checks and runtime read model.
type AdminUserGroupAccountAllowlistService struct {
	admin       AdminService
	allowlist   *UserGroupAccountAllowlistService
	candidates  UserGroupAccountAllowlistCandidateRepository
	runtimeRead UserGroupAccountRuntimeReader
}

type UserGroupAccountRuntimeAccount struct {
	AccountID          int64
	Name               string
	Platform           string
	Type               string
	Priority           int
	Concurrency        int
	CurrentConcurrency int
	// Available is true only for accounts that are currently schedulable in
	// this group. An account may remain in the read model with Available=false
	// while an already-started request is still holding a runtime lease.
	Available bool
}

type UserGroupAccountRuntimeUser struct {
	UserID             int64
	Username           string
	Email              string
	CurrentConcurrency int
	ActiveAccountIDs   []int64
}

type UserGroupAccountRuntime struct {
	SnapshotAt time.Time
	Accounts   []UserGroupAccountRuntimeAccount
	Users      []UserGroupAccountRuntimeUser
}

func NewAdminUserGroupAccountAllowlistService(
	admin AdminService,
	allowlist *UserGroupAccountAllowlistService,
	candidates UserGroupAccountAllowlistCandidateRepository,
	runtimeRead UserGroupAccountRuntimeReader,
) *AdminUserGroupAccountAllowlistService {
	return &AdminUserGroupAccountAllowlistService{
		admin:       admin,
		allowlist:   allowlist,
		candidates:  candidates,
		runtimeRead: runtimeRead,
	}
}

func (s *AdminUserGroupAccountAllowlistService) GetSelection(ctx context.Context, userID, groupID int64) (*UserGroupAccountAllowlistSelection, error) {
	if err := s.ensureScope(ctx, userID, groupID); err != nil {
		return nil, err
	}
	if s.allowlist == nil {
		return nil, allowlistUnavailable(nil)
	}
	return s.allowlist.GetAdminSelection(ctx, userID, groupID)
}

func (s *AdminUserGroupAccountAllowlistService) Replace(ctx context.Context, userID, groupID int64, accountIDs []int64) error {
	if err := s.ensureScope(ctx, userID, groupID); err != nil {
		return err
	}
	if s.allowlist == nil {
		return allowlistUnavailable(nil)
	}
	return s.allowlist.ReplaceAllowedAccountIDs(ctx, userID, groupID, accountIDs)
}

func (s *AdminUserGroupAccountAllowlistService) Restore(ctx context.Context, userID, groupID int64) error {
	if err := s.ensureScope(ctx, userID, groupID); err != nil {
		return err
	}
	if s.allowlist == nil {
		return allowlistUnavailable(nil)
	}
	return s.allowlist.RestoreAllowedAccountIDs(ctx, userID, groupID)
}

func (s *AdminUserGroupAccountAllowlistService) GetRuntime(ctx context.Context, groupID int64) (*UserGroupAccountRuntime, error) {
	if s == nil || s.admin == nil || s.candidates == nil || s.runtimeRead == nil {
		return nil, allowlistUnavailable(nil)
	}
	if groupID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_GROUP_ID", "group id must be positive")
	}
	if _, err := s.admin.GetGroup(ctx, groupID); err != nil {
		return nil, err
	}

	snapshot, err := s.runtimeRead.GetUserGroupAccountConcurrencySnapshot(ctx, groupID)
	if err != nil {
		return nil, allowlistUnavailable(err)
	}
	accounts, err := s.candidates.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return nil, err
	}

	accountConcurrency := make(map[int64]int)
	userConcurrency := snapshot.UserCounts
	if userConcurrency == nil {
		userConcurrency = make(map[int64]int, len(snapshot.Counts))
	}
	for _, counts := range snapshot.Counts {
		for accountID, count := range counts {
			if accountID > 0 && count > 0 {
				accountConcurrency[accountID] += count
			}
		}
	}
	if snapshot.UserCounts == nil {
		for userID, counts := range snapshot.Counts {
			for _, count := range counts {
				userConcurrency[userID] += count
			}
		}
	}

	runtime := &UserGroupAccountRuntime{
		SnapshotAt: snapshot.SnapshotAt,
		Accounts:   make([]UserGroupAccountRuntimeAccount, 0, len(accounts)+len(accountConcurrency)),
		Users:      make([]UserGroupAccountRuntimeUser, 0, len(userConcurrency)),
	}
	availableAccountIDs := make(map[int64]struct{}, len(accounts))
	for _, account := range accounts {
		if !account.IsSchedulable() {
			continue
		}
		availableAccountIDs[account.ID] = struct{}{}
		runtime.Accounts = append(runtime.Accounts, UserGroupAccountRuntimeAccount{
			AccountID: account.ID, Name: account.Name, Platform: account.Platform, Type: account.Type,
			Priority: account.Priority, Concurrency: account.Concurrency, CurrentConcurrency: accountConcurrency[account.ID],
			Available: true,
		})
	}

	// A request can outlive a scheduling-state transition or a group-membership
	// edit. Preserve those active account identities in this display-only
	// snapshot instead of showing the user as having concurrency but "no active
	// account". They remain unavailable in the editor and cannot be newly
	// selected for scheduling.
	missingActiveAccountIDs := make([]int64, 0)
	for accountID := range accountConcurrency {
		if _, available := availableAccountIDs[accountID]; !available {
			missingActiveAccountIDs = append(missingActiveAccountIDs, accountID)
		}
	}
	sort.Slice(missingActiveAccountIDs, func(i, j int) bool {
		return missingActiveAccountIDs[i] < missingActiveAccountIDs[j]
	})
	if len(missingActiveAccountIDs) > 0 {
		storedAccounts, err := s.candidates.GetByIDs(ctx, missingActiveAccountIDs)
		if err != nil {
			return nil, allowlistUnavailable(fmt.Errorf("load active runtime accounts: %w", err))
		}
		storedByID := make(map[int64]*Account, len(storedAccounts))
		for _, account := range storedAccounts {
			if account != nil {
				storedByID[account.ID] = account
			}
		}
		for _, accountID := range missingActiveAccountIDs {
			account := storedByID[accountID]
			runtimeAccount := UserGroupAccountRuntimeAccount{
				AccountID: accountID, Name: fmt.Sprintf("#%d", accountID),
				CurrentConcurrency: accountConcurrency[accountID], Available: false,
			}
			if account != nil {
				runtimeAccount.Name = account.Name
				runtimeAccount.Platform = account.Platform
				runtimeAccount.Type = account.Type
				runtimeAccount.Priority = account.Priority
				runtimeAccount.Concurrency = account.Concurrency
			}
			runtime.Accounts = append(runtime.Accounts, runtimeAccount)
		}
	}
	sort.Slice(runtime.Accounts, func(i, j int) bool { return runtime.Accounts[i].AccountID < runtime.Accounts[j].AccountID })

	userIDs := make([]int64, 0, len(userConcurrency))
	for userID := range userConcurrency {
		if userID > 0 {
			userIDs = append(userIDs, userID)
		}
	}
	sort.Slice(userIDs, func(i, j int) bool { return userIDs[i] < userIDs[j] })
	usersByID := make(map[int64]User, len(userIDs))
	if batch, ok := s.admin.(adminUserGroupAccountRuntimeBatchReader); ok {
		users, err := batch.GetUsersByIDs(ctx, userIDs)
		if err != nil {
			return nil, allowlistUnavailable(fmt.Errorf("load active runtime users: %w", err))
		}
		for _, user := range users {
			usersByID[user.ID] = user
		}
	}
	for _, userID := range userIDs {
		user, ok := usersByID[userID]
		if !ok {
			if _, hasBatch := s.admin.(adminUserGroupAccountRuntimeBatchReader); hasBatch {
				continue
			}
			loaded, err := s.admin.GetUser(ctx, userID)
			if errors.Is(err, ErrUserNotFound) {
				// Redis leases outlive a soft-deleted user briefly; a stale lease must
				// not make the entire runtime page unavailable.
				continue
			}
			if err != nil {
				return nil, allowlistUnavailable(fmt.Errorf("load active runtime user %d: %w", userID, err))
			}
			if loaded == nil {
				continue
			}
			user = *loaded
		}
		activeAccountIDs := make([]int64, 0, len(snapshot.Counts[userID]))
		for accountID, count := range snapshot.Counts[userID] {
			if accountID > 0 && count > 0 {
				activeAccountIDs = append(activeAccountIDs, accountID)
			}
		}
		sort.Slice(activeAccountIDs, func(i, j int) bool { return activeAccountIDs[i] < activeAccountIDs[j] })
		runtime.Users = append(runtime.Users, UserGroupAccountRuntimeUser{
			UserID: user.ID, Username: user.Username, Email: user.Email,
			CurrentConcurrency: userConcurrency[userID], ActiveAccountIDs: activeAccountIDs,
		})
	}
	return runtime, nil
}

func (s *AdminUserGroupAccountAllowlistService) ensureScope(ctx context.Context, userID, groupID int64) error {
	if s == nil || s.admin == nil {
		return allowlistUnavailable(nil)
	}
	if userID <= 0 || groupID <= 0 {
		return infraerrors.BadRequest("INVALID_ALLOWLIST_SCOPE", "user id and group id must be positive")
	}
	if _, err := s.admin.GetGroup(ctx, groupID); err != nil {
		return err
	}
	if _, err := s.admin.GetUser(ctx, userID); err != nil {
		return err
	}
	return nil
}

func allowlistUnavailable(cause error) error {
	err := infraerrors.New(http.StatusServiceUnavailable, "USER_GROUP_ACCOUNT_RUNTIME_UNAVAILABLE", "user-group account runtime is unavailable")
	if cause != nil {
		return err.WithCause(cause)
	}
	return err
}
