//go:build unit

package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type authRefreshHTTPStore struct {
	*authCallerHTTPGuardedStore
	data          service.RefreshTokenData
	expireConsume bool
	consumed      bool
}

func (s *authRefreshHTTPStore) GetRefreshToken(context.Context, string) (*service.RefreshTokenData, error) {
	s.calls = append(s.calls, "get")
	if s.consumed {
		return nil, service.ErrRefreshTokenNotFound
	}
	data := s.data
	return &data, nil
}

func (s *authRefreshHTTPStore) ConsumeRefreshToken(context.Context, string) (*service.RefreshTokenData, error) {
	s.calls = append(s.calls, "consume")
	if s.consumed {
		return nil, service.ErrRefreshTokenNotFound
	}
	s.consumed = true
	data := s.data
	if s.expireConsume {
		data.FamilyExpiresAt = time.Now().Add(-time.Minute)
	}
	return &data, nil
}

type authRefreshHTTPPreparer struct {
	*authRefreshHTTPStore
	err error
}

func (s *authRefreshHTTPPreparer) PrepareRefreshTokenIssuance(context.Context, int64) (*service.RefreshTokenIssuance, error) {
	s.calls = append(s.calls, "prepare")
	return nil, s.err
}

func TestAuthRefreshHTTPAfterConsumeErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	storageErr := errors.New(authCallerSensitiveDetail)
	authorityErr := fmt.Errorf("authority acknowledgment lost: %w", storageErr)
	sensitiveErr := infraerrors.ServiceUnavailable("SENSITIVE_BACKEND_REASON", authCallerSensitiveDetail).
		WithMetadata(map[string]string{"internal": authCallerSensitiveDetail})
	for _, tc := range []struct {
		name          string
		storeErr      error
		userErr       error
		familyErr     error
		prepareErr    error
		expireConsume bool
		wantStatus    int
		wantCalls     []string
	}{
		{name: "storage failure", storeErr: storageErr, wantStatus: 503, wantCalls: []string{"get", "consume", "store"}},
		{name: "authority acknowledgment failure", storeErr: authorityErr, wantStatus: 503, wantCalls: []string{"get", "consume", "store"}},
		{name: "user membership failure", userErr: sensitiveErr, wantStatus: 503, wantCalls: []string{"get", "consume", "store", "user_index"}},
		{name: "family membership failure", familyErr: sensitiveErr, wantStatus: 503, wantCalls: []string{"get", "consume", "store", "user_index", "family_index"}},
		{name: "persistent issuance admission failure", prepareErr: sensitiveErr, wantStatus: 503, wantCalls: []string{"get", "consume", "prepare"}},
		{name: "consumed family expired", expireConsume: true, wantStatus: 401, wantCalls: []string{"get", "consume"}},
		{name: "wrapped issuance expiry", prepareErr: fmt.Errorf("issuance deadline: %w", service.ErrRefreshTokenExpired), wantStatus: 401, wantCalls: []string{"get", "consume", "prepare"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := &service.User{ID: 17, Email: "refresh-failure@example.invalid", Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 7, TokenVersionResolved: true}
			now := time.Now()
			cache := &authRefreshHTTPStore{
				authCallerHTTPGuardedStore: &authCallerHTTPGuardedStore{authCallerHTTPStore: &authCallerHTTPStore{storeErr: tc.storeErr, userErr: tc.userErr, familyErr: tc.familyErr}},
				data:                       service.RefreshTokenData{UserID: user.ID, TokenVersion: user.TokenVersion, FamilyID: "synthetic-family", CreatedAt: now, ExpiresAt: now.Add(time.Hour), FamilyExpiresAt: now.Add(time.Hour)},
				expireConsume:              tc.expireConsume,
			}
			var store service.RefreshTokenCache = cache
			if tc.prepareErr != nil {
				store = &authRefreshHTTPPreparer{authRefreshHTTPStore: cache, err: tc.prepareErr}
			}
			h := &AuthHandler{authService: newAuthCallerHTTPService(store, &authCallerHTTPUserRepo{user: user})}
			router := gin.New()
			router.POST("/api/v1/auth/refresh", h.RefreshToken)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":"rt_synthetic-original-token"}`))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)

			require.True(t, cache.consumed, "the test must reach the post-consume failure path")
			require.Equal(t, tc.wantCalls, cache.calls)
			require.Equal(t, tc.wantStatus, recorder.Code)
			if tc.wantStatus == http.StatusServiceUnavailable {
				require.JSONEq(t, `{"code":503,"reason":"SERVICE_UNAVAILABLE","message":"service temporarily unavailable"}`, recorder.Body.String())
			} else {
				require.JSONEq(t, `{"code":401,"reason":"REFRESH_TOKEN_EXPIRED","message":"refresh token has expired"}`, recorder.Body.String())
			}
			for _, forbidden := range []string{"access_token", "refresh_token", "rt_synthetic-original-token", authCallerSensitiveDetail, "SENSITIVE_BACKEND_REASON", "authority acknowledgment", "issuance deadline"} {
				require.NotContains(t, recorder.Body.String(), forbidden)
			}
			require.Empty(t, recorder.Header().Values("Set-Cookie"), "failure must not issue credentials in cookies either")
		})
	}
}
