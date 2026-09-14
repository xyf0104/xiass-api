package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type groupAccountAllowlistAdminStub struct {
	service.AdminService
	group *service.Group
	users map[int64]*service.User
}

func (s *groupAccountAllowlistAdminStub) GetGroup(_ context.Context, _ int64) (*service.Group, error) {
	return s.group, nil
}

func (s *groupAccountAllowlistAdminStub) GetUser(_ context.Context, userID int64) (*service.User, error) {
	return s.users[userID], nil
}

type groupAccountAllowlistCandidateStub struct {
	service.AccountRepository
	accounts []service.Account
}

func (s *groupAccountAllowlistCandidateStub) ListSchedulableByGroupID(context.Context, int64) ([]service.Account, error) {
	return append([]service.Account(nil), s.accounts...), nil
}

func (s *groupAccountAllowlistCandidateStub) GetByIDs(_ context.Context, ids []int64) ([]*service.Account, error) {
	wanted := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	accounts := make([]*service.Account, 0, len(ids))
	for i := range s.accounts {
		if _, ok := wanted[s.accounts[i].ID]; !ok {
			continue
		}
		account := s.accounts[i]
		accounts = append(accounts, &account)
	}
	return accounts, nil
}

type groupAccountAllowlistRuntimeStub struct {
	snapshot *service.UserGroupAccountConcurrencySnapshot
}

type groupAccountAllowlistRepositoryStub struct {
	accountIDs []int64
	restricted bool
}

func (s *groupAccountAllowlistRepositoryStub) GetAllowedAccountIDs(context.Context, int64, int64) ([]int64, bool, error) {
	return append([]int64(nil), s.accountIDs...), s.restricted, nil
}

func (s *groupAccountAllowlistRepositoryStub) ReplaceAllowedAccountIDs(_ context.Context, _, _ int64, accountIDs []int64) error {
	s.accountIDs = append([]int64(nil), accountIDs...)
	s.restricted = true
	return nil
}

func (s *groupAccountAllowlistRepositoryStub) RestoreAllowedAccountIDs(context.Context, int64, int64) error {
	s.accountIDs = nil
	s.restricted = false
	return nil
}

func (s *groupAccountAllowlistRuntimeStub) GetUserGroupAccountConcurrencySnapshot(context.Context, int64) (*service.UserGroupAccountConcurrencySnapshot, error) {
	return s.snapshot, nil
}

func TestGroupHandlerGetUserAccountRuntimeReturnsFrontendContractWithoutCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminService := &groupAccountAllowlistAdminStub{
		group: &service.Group{ID: 9},
		users: map[int64]*service.User{
			41: {ID: 41, Username: "member", Email: "member@example.com"},
		},
	}
	candidates := &groupAccountAllowlistCandidateStub{accounts: []service.Account{
		{
			ID: 11, Name: "first", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
			Priority: 1, Concurrency: 3, Status: service.StatusActive, Schedulable: true,
			Credentials: map[string]any{"access_token": "must-not-appear"},
		},
	}}
	runtimeReader := &groupAccountAllowlistRuntimeStub{snapshot: &service.UserGroupAccountConcurrencySnapshot{
		SnapshotAt: time.Unix(1_700_000_000, 0),
		Counts: map[int64]map[int64]int{
			41: {11: 2},
		},
	}}
	allowlist := service.NewUserGroupAccountAllowlistService(nil, candidates)
	allowlistAdmin := service.NewAdminUserGroupAccountAllowlistService(adminService, allowlist, candidates, runtimeReader)
	handler := NewGroupHandler(adminService, nil, nil)
	handler.SetUserGroupAccountAllowlistService(allowlistAdmin)
	router := gin.New()
	router.GET("/admin/groups/:id/user-account-runtime", handler.GetUserAccountRuntime)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/groups/9/user-account-runtime", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "access_token")
	require.NotContains(t, recorder.Body.String(), "must-not-appear")

	var response struct {
		Data struct {
			Accounts []struct {
				ID                 int64 `json:"id"`
				CurrentConcurrency int   `json:"current_concurrency"`
				Available          bool  `json:"available"`
			} `json:"accounts"`
			Users []struct {
				ID                       int64         `json:"id"`
				CurrentConcurrency       int           `json:"current_concurrency"`
				ActiveAccountIDs         []int64       `json:"active_account_ids"`
				ActiveAccountConcurrency map[int64]int `json:"active_account_concurrency"`
			} `json:"users"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, int64(11), response.Data.Accounts[0].ID)
	require.Equal(t, 2, response.Data.Accounts[0].CurrentConcurrency)
	require.True(t, response.Data.Accounts[0].Available)
	require.Equal(t, int64(41), response.Data.Users[0].ID)
	require.Equal(t, 2, response.Data.Users[0].CurrentConcurrency)
	require.Equal(t, []int64{11}, response.Data.Users[0].ActiveAccountIDs)
	require.Equal(t, map[int64]int{11: 2}, response.Data.Users[0].ActiveAccountConcurrency)
}

func TestGroupHandlerGetUserAccountAllowlistMarksCandidateAvailability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminService := &groupAccountAllowlistAdminStub{
		group: &service.Group{ID: 9},
		users: map[int64]*service.User{41: {ID: 41}},
	}
	repository := &groupAccountAllowlistRepositoryStub{accountIDs: []int64{11, 13}, restricted: true}
	candidates := &groupAccountAllowlistCandidateStub{accounts: []service.Account{
		{ID: 11, Name: "selected", Status: service.StatusActive, Schedulable: true},
		{ID: 12, Name: "available", Status: service.StatusActive, Schedulable: true},
		{ID: 13, Name: "selected but unavailable", Status: service.StatusDisabled, Schedulable: true},
	}}
	allowlist := service.NewUserGroupAccountAllowlistService(repository, candidates)
	allowlistAdmin := service.NewAdminUserGroupAccountAllowlistService(adminService, allowlist, candidates, nil)
	handler := NewGroupHandler(adminService, nil, nil)
	handler.SetUserGroupAccountAllowlistService(allowlistAdmin)
	router := gin.New()
	router.GET("/admin/groups/:id/users/:user_id/account-allowlist", handler.GetUserAccountAllowlist)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/groups/9/users/41/account-allowlist", nil)
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)

	var response struct {
		Data struct {
			Restricted bool `json:"restricted"`
			Candidates []struct {
				ID        int64 `json:"id"`
				Allowed   bool  `json:"allowed"`
				Available bool  `json:"available"`
			} `json:"candidates"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Data.Restricted)
	require.Len(t, response.Data.Candidates, 3)
	require.Equal(t, int64(11), response.Data.Candidates[0].ID)
	require.True(t, response.Data.Candidates[0].Allowed)
	require.True(t, response.Data.Candidates[0].Available)
	require.False(t, response.Data.Candidates[1].Allowed)
	require.True(t, response.Data.Candidates[1].Available)
	require.Equal(t, int64(13), response.Data.Candidates[2].ID)
	require.True(t, response.Data.Candidates[2].Allowed)
	require.False(t, response.Data.Candidates[2].Available)
}

func TestGroupHandlerReplaceEmptyAccountAllowlistKeepsRestricted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminService := &groupAccountAllowlistAdminStub{
		group: &service.Group{ID: 9},
		users: map[int64]*service.User{41: {ID: 41}},
	}
	repository := &groupAccountAllowlistRepositoryStub{accountIDs: []int64{11}, restricted: true}
	candidates := &groupAccountAllowlistCandidateStub{}
	allowlist := service.NewUserGroupAccountAllowlistService(repository, candidates)
	allowlistAdmin := service.NewAdminUserGroupAccountAllowlistService(adminService, allowlist, candidates, nil)
	handler := NewGroupHandler(adminService, nil, nil)
	handler.SetUserGroupAccountAllowlistService(allowlistAdmin)
	router := gin.New()
	router.PUT("/admin/groups/:id/users/:user_id/account-allowlist", handler.ReplaceUserAccountAllowlist)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/admin/groups/9/users/41/account-allowlist",
		strings.NewReader(`{"account_ids":[]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, repository.restricted)
	require.Empty(t, repository.accountIDs)
	require.Contains(t, recorder.Body.String(), `"restricted":true`)
}
