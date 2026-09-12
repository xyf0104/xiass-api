package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// AccountPoolExtraKey is administrative organization only, never a scheduling group.
const AccountPoolExtraKey = "xiass_account_pool"

var (
	ErrAccountPoolNotFound    = infraerrors.NotFound("ACCOUNT_POOL_NOT_FOUND", "account pool not found")
	ErrAccountPoolNameTaken   = infraerrors.Conflict("ACCOUNT_POOL_NAME_TAKEN", "account pool name already exists")
	ErrAccountPoolUnavailable = infraerrors.ServiceUnavailable("ACCOUNT_POOL_UNAVAILABLE", "account pool storage is unavailable")
)

type AccountPool struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	ProxyID      *int64    `json:"proxy_id"`
	AccountIDs   []int64   `json:"account_ids"`
	AccountCount int       `json:"account_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AccountPoolService is an optional admin capability to avoid expanding the shared
// account service interface used by unrelated OAuth and gateway consumers.
type AccountPoolService interface {
	ListAccountPools(context.Context) ([]AccountPool, error)
	GetAccountPool(context.Context, int64) (*AccountPool, error)
	CreateAccountPool(context.Context, string, *int64) (*AccountPool, error)
	RenameAccountPool(context.Context, int64, string) (*AccountPool, error)
	DeleteAccountPool(context.Context, int64) error
	AssignAccountPool(context.Context, int64, []int64, bool) (*AccountPool, error)
	SetAccountPoolProxy(context.Context, int64, *int64) (*AccountPool, error)
}

// OAuth assignment must compare the browser's egress while holding the same
// transaction lock as pool proxy changes, including an explicitly direct egress.
type AccountPoolOAuthAssigner interface {
	AssignOAuthAccountPool(context.Context, int64, int64, *int64) (*AccountPool, error)
}

// The transaction callback must receive a transaction-bound account repository:
// existing bulk update and shadow propagation must read their own writes.
type AccountPoolRepository interface {
	AccountRepository
	WithAccountPoolTransaction(context.Context, func(context.Context, AccountPoolRepository) error) error
	ListAccountPools(context.Context) ([]AccountPool, error)
	GetAccountPool(context.Context, int64) (*AccountPool, error)
	CreateAccountPool(context.Context, string, *int64) (*AccountPool, error)
	UpdateAccountPool(context.Context, int64, string, *int64) error
	DeleteAccountPool(context.Context, int64) error
	LockAccountPoolAccounts(context.Context, []int64) ([]*Account, error)
	ValidateAccountPoolProxy(context.Context, int64) error
}

var _ AccountPoolService = (*adminServiceImpl)(nil)

func (s *adminServiceImpl) accountPoolRepository() (AccountPoolRepository, error) {
	r, ok := s.accountRepo.(AccountPoolRepository)
	if !ok {
		return nil, ErrAccountPoolUnavailable
	}
	return r, nil
}

func (s *adminServiceImpl) ListAccountPools(ctx context.Context) ([]AccountPool, error) {
	r, err := s.accountPoolRepository()
	if err != nil {
		return nil, err
	}
	return r.ListAccountPools(ctx)
}

func (s *adminServiceImpl) GetAccountPool(ctx context.Context, id int64) (*AccountPool, error) {
	if id <= 0 {
		return nil, ErrAccountPoolNotFound
	}
	r, err := s.accountPoolRepository()
	if err != nil {
		return nil, err
	}
	return r.GetAccountPool(ctx, id)
}

func normalizeAccountPoolName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || name == "" || utf8.RuneCountInString(name) > 100 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", infraerrors.BadRequest("INVALID_ACCOUNT_POOL_NAME", "pool name must contain 1 to 100 characters without control characters")
	}
	return name, nil
}

func (s *adminServiceImpl) withAccountPoolTransaction(ctx context.Context, fn func(context.Context, *adminServiceImpl, AccountPoolRepository) error) error {
	r, err := s.accountPoolRepository()
	if err != nil {
		return err
	}
	return r.WithAccountPoolTransaction(ctx, func(ctx context.Context, txRepo AccountPoolRepository) error {
		txService := *s
		txService.accountRepo = txRepo
		return fn(ctx, &txService, txRepo)
	})
}

func (s *adminServiceImpl) validateAccountPoolProxy(ctx context.Context, r AccountPoolRepository, id *int64) error {
	if id == nil {
		if s.executionNodeRoutingActive(ctx) {
			return infraerrors.BadRequest("EXECUTION_NODE_PROXY_REQUIRED", "accounts must keep a private egress proxy while multi-node routing is enabled")
		}
		return nil
	}
	if *id <= 0 {
		return infraerrors.BadRequest("INVALID_ACCOUNT_POOL_PROXY", "proxy_id must be positive or null for direct access")
	}
	return r.ValidateAccountPoolProxy(ctx, *id)
}

func (s *adminServiceImpl) CreateAccountPool(ctx context.Context, name string, proxyID *int64) (*AccountPool, error) {
	name, err := normalizeAccountPoolName(name)
	if err != nil {
		return nil, err
	}
	var pool *AccountPool
	err = s.withAccountPoolTransaction(ctx, func(ctx context.Context, txs *adminServiceImpl, r AccountPoolRepository) error {
		if err := txs.validateAccountPoolProxy(ctx, r, proxyID); err != nil {
			return err
		}
		var err error
		pool, err = r.CreateAccountPool(ctx, name, proxyID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func (s *adminServiceImpl) RenameAccountPool(ctx context.Context, id int64, name string) (*AccountPool, error) {
	name, err := normalizeAccountPoolName(name)
	if err != nil {
		return nil, err
	}
	return s.mutateAccountPool(ctx, id, func(ctx context.Context, txs *adminServiceImpl, r AccountPoolRepository, pool *AccountPool) error {
		if _, err := txs.lockAccountPoolMembers(ctx, r, pool.AccountIDs); err != nil {
			return err
		}
		return r.UpdateAccountPool(ctx, pool.ID, name, pool.ProxyID)
	})
}

func (s *adminServiceImpl) mutateAccountPool(ctx context.Context, id int64, fn func(context.Context, *adminServiceImpl, AccountPoolRepository, *AccountPool) error) (*AccountPool, error) {
	if id <= 0 {
		return nil, ErrAccountPoolNotFound
	}
	var pool *AccountPool
	err := s.withAccountPoolTransaction(ctx, func(ctx context.Context, txs *adminServiceImpl, r AccountPoolRepository) error {
		current, err := r.GetAccountPool(ctx, id)
		if err != nil {
			return err
		}
		if err := fn(ctx, txs, r, current); err != nil {
			return err
		}
		pool, err = r.GetAccountPool(ctx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func (s *adminServiceImpl) lockAccountPoolMembers(ctx context.Context, r AccountPoolRepository, ids []int64) ([]*Account, error) {
	accounts, err := r.LockAccountPoolAccounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		a, found := findAccountByID(accounts, id)
		if !found {
			return nil, ErrAccountNotFound
		}
		if err := s.ensureAccountManagementAccess(ctx, a); err != nil {
			return nil, err
		}
	}
	return accounts, nil
}

func normalizeAccountPoolIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 || len(ids) > 5000 {
		return nil, infraerrors.BadRequest("INVALID_ACCOUNT_POOL_ACCOUNTS", "provide between 1 and 5000 account IDs")
	}
	seen := make(map[int64]bool, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, infraerrors.BadRequest("INVALID_ACCOUNT_POOL_ACCOUNTS", "account IDs must be positive")
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func (s *adminServiceImpl) AssignAccountPool(ctx context.Context, id int64, ids []int64, remove bool) (*AccountPool, error) {
	return s.assignAccountPool(ctx, id, ids, remove, nil)
}

func (s *adminServiceImpl) AssignOAuthAccountPool(ctx context.Context, id, accountID int64, expectedProxy *int64) (*AccountPool, error) {
	return s.assignAccountPool(ctx, id, []int64{accountID}, false, &expectedProxy)
}

func (s *adminServiceImpl) assignAccountPool(ctx context.Context, id int64, ids []int64, remove bool, expectedProxy **int64) (*AccountPool, error) {
	ids, err := normalizeAccountPoolIDs(ids)
	if err != nil {
		return nil, err
	}
	return s.mutateAccountPool(ctx, id, func(ctx context.Context, txs *adminServiceImpl, r AccountPoolRepository, pool *AccountPool) error {
		accounts, err := txs.lockAccountPoolMembers(ctx, r, ids)
		if err != nil {
			return err
		}
		if expectedProxy != nil {
			matches := func(actual *int64) bool {
				return actual == nil && *expectedProxy == nil || actual != nil && *expectedProxy != nil && *actual == **expectedProxy
			}
			if !matches(pool.ProxyID) {
				return infraerrors.Conflict("ACCOUNT_POOL_PROXY_CHANGED", "pool proxy changed; restart OAuth")
			}
			for _, account := range accounts {
				if !account.IsOpenAIOAuth() || !matches(account.ProxyID) {
					return infraerrors.Conflict("ACCOUNT_POOL_PROXY_CHANGED", "OAuth account egress changed; review account configuration")
				}
			}
		}
		if remove {
			members := make(map[int64]bool, len(pool.AccountIDs))
			for _, member := range pool.AccountIDs {
				members[member] = true
			}
			for _, accountID := range ids {
				if !members[accountID] {
					return infraerrors.Conflict("ACCOUNT_POOL_MEMBERSHIP_CHANGED", "selected account is no longer in this pool")
				}
			}
			if err := txs.updateAccountPoolMembers(ctx, ids, nil, nil); err != nil {
				return err
			}
		} else {
			if err := txs.validateAccountPoolProxy(ctx, r, pool.ProxyID); err != nil {
				return err
			}
			if err := txs.validateAccountPoolProxyTargets(ctx, accounts); err != nil {
				return err
			}
			proxyID := int64(0)
			if pool.ProxyID != nil {
				proxyID = *pool.ProxyID
			}
			if err := txs.updateAccountPoolMembers(ctx, ids, strconv.FormatInt(pool.ID, 10), &proxyID); err != nil {
				return err
			}
		}
		return r.UpdateAccountPool(ctx, pool.ID, pool.Name, pool.ProxyID)
	})
}

func (s *adminServiceImpl) validateAccountPoolProxyTargets(ctx context.Context, accounts []*Account) error {
	for _, account := range accounts {
		if account.IsCredentialShadow() {
			return infraerrors.BadRequest("SPARK_SHADOW_PROXY_INHERITED", "shadow accounts inherit their parent's proxy; add the parent to the pool instead")
		}
		shadows, err := s.accountRepo.ListShadowsByParent(ctx, account.ID)
		if err != nil {
			return err
		}
		for _, shadow := range shadows {
			if err := s.ensureAccountManagementAccess(ctx, shadow); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *adminServiceImpl) updateAccountPoolMembers(ctx context.Context, ids []int64, membership any, proxyID *int64) error {
	if len(ids) == 0 {
		return nil
	}
	result, err := s.BulkUpdateAccounts(ctx, &BulkUpdateAccountsInput{
		AccountIDs: ids, ProxyID: proxyID, Extra: map[string]any{AccountPoolExtraKey: membership},
	})
	if err != nil {
		return err
	}
	if result == nil || result.Failed != 0 || result.Success != len(ids) {
		return fmt.Errorf("account pool mutation did not update every locked account")
	}
	return nil
}

func (s *adminServiceImpl) SetAccountPoolProxy(ctx context.Context, id int64, proxyID *int64) (*AccountPool, error) {
	return s.mutateAccountPool(ctx, id, func(ctx context.Context, txs *adminServiceImpl, r AccountPoolRepository, pool *AccountPool) error {
		if err := txs.validateAccountPoolProxy(ctx, r, proxyID); err != nil {
			return err
		}
		accounts, err := txs.lockAccountPoolMembers(ctx, r, pool.AccountIDs)
		if err != nil {
			return err
		}
		if err := txs.validateAccountPoolProxyTargets(ctx, accounts); err != nil {
			return err
		}
		value := int64(0)
		if proxyID != nil {
			value = *proxyID
		}
		if err := txs.updateAccountPoolMembers(ctx, pool.AccountIDs, strconv.FormatInt(pool.ID, 10), &value); err != nil {
			return err
		}
		return r.UpdateAccountPool(ctx, pool.ID, pool.Name, proxyID)
	})
}

func (s *adminServiceImpl) DeleteAccountPool(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrAccountPoolNotFound
	}
	return s.withAccountPoolTransaction(ctx, func(ctx context.Context, txs *adminServiceImpl, r AccountPoolRepository) error {
		pool, err := r.GetAccountPool(ctx, id)
		if err != nil {
			return err
		}
		if _, err := txs.lockAccountPoolMembers(ctx, r, pool.AccountIDs); err != nil {
			return err
		}
		if err := txs.updateAccountPoolMembers(ctx, pool.AccountIDs, nil, nil); err != nil {
			return err
		}
		return r.DeleteAccountPool(ctx, id)
	})
}
