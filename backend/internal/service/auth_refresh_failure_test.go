//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type refreshFailureCache struct {
	RefreshTokenCache
	data          RefreshTokenData
	storeErr      error
	userErr       error
	familyErr     error
	expireConsume bool
	consumed      bool
	calls         []string
}

func (*refreshFailureCache) RequiresRefreshTokenIssuanceAdmission() bool { return true }

func (s *refreshFailureCache) GetRefreshToken(context.Context, string) (*RefreshTokenData, error) {
	s.calls = append(s.calls, "get")
	if s.consumed {
		return nil, ErrRefreshTokenNotFound
	}
	data := s.data
	return &data, nil
}

func (s *refreshFailureCache) ConsumeRefreshToken(context.Context, string) (*RefreshTokenData, error) {
	s.calls = append(s.calls, "consume")
	if s.consumed {
		return nil, ErrRefreshTokenNotFound
	}
	s.consumed = true
	data := s.data
	if s.expireConsume {
		data.FamilyExpiresAt = time.Now().Add(-time.Minute)
	}
	return &data, nil
}

func (s *refreshFailureCache) StoreRefreshToken(context.Context, string, *RefreshTokenData, time.Duration) error {
	s.calls = append(s.calls, "store")
	return s.storeErr
}

func (s *refreshFailureCache) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	s.calls = append(s.calls, "user_index")
	return s.userErr
}

func (s *refreshFailureCache) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	s.calls = append(s.calls, "family_index")
	return s.familyErr
}

type refreshFailurePreparer struct {
	*refreshFailureCache
	err error
}

func (s *refreshFailurePreparer) PrepareRefreshTokenIssuance(context.Context, int64) (*RefreshTokenIssuance, error) {
	s.calls = append(s.calls, "prepare")
	return nil, s.err
}

type refreshFailureUserRepo struct {
	UserRepository
	user  *User
	cache *refreshFailureCache
}

func (r *refreshFailureUserRepo) GetByID(context.Context, int64) (*User, error) {
	r.cache.calls = append(r.cache.calls, "user")
	user := *r.user
	return &user, nil
}

func TestAuthRefreshAfterConsumeErrorMapping(t *testing.T) {
	sensitiveErr := errors.New("synthetic-sensitive-storage-detail")
	authorityErr := fmt.Errorf("refresh authority acknowledgment lost: %w", sensitiveErr)
	expiryErr := fmt.Errorf("issuance deadline: %w", ErrRefreshTokenExpired)
	for _, tc := range []struct {
		name          string
		storeErr      error
		userErr       error
		familyErr     error
		prepareErr    error
		expireConsume bool
		wantCalls     []string
		wantError     error
		wantCause     error
	}{
		{name: "storage failure", storeErr: sensitiveErr, wantError: ErrServiceUnavailable, wantCause: sensitiveErr, wantCalls: []string{"get", "user", "consume", "store"}},
		{name: "authority acknowledgment failure", storeErr: authorityErr, wantError: ErrServiceUnavailable, wantCause: authorityErr, wantCalls: []string{"get", "user", "consume", "store"}},
		{name: "user membership failure", userErr: sensitiveErr, wantError: ErrServiceUnavailable, wantCause: sensitiveErr, wantCalls: []string{"get", "user", "consume", "store", "user_index"}},
		{name: "family membership failure", familyErr: sensitiveErr, wantError: ErrServiceUnavailable, wantCause: sensitiveErr, wantCalls: []string{"get", "user", "consume", "store", "user_index", "family_index"}},
		{name: "persistent issuance admission failure", prepareErr: authorityErr, wantError: ErrServiceUnavailable, wantCause: authorityErr, wantCalls: []string{"get", "user", "consume", "prepare"}},
		{name: "consumed family expired", expireConsume: true, wantError: ErrRefreshTokenExpired, wantCalls: []string{"get", "user", "consume"}},
		{name: "wrapped issuance expiry", prepareErr: expiryErr, wantError: ErrRefreshTokenExpired, wantCause: expiryErr, wantCalls: []string{"get", "user", "consume", "prepare"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := &User{ID: 17, Email: "refresh-failure@example.invalid", Role: RoleUser, Status: StatusActive, TokenVersion: 7, TokenVersionResolved: true}
			now := time.Now()
			cache := &refreshFailureCache{
				data:     RefreshTokenData{UserID: user.ID, TokenVersion: user.TokenVersion, FamilyID: "synthetic-family", CreatedAt: now, ExpiresAt: now.Add(time.Hour), FamilyExpiresAt: now.Add(time.Hour)},
				storeErr: tc.storeErr, userErr: tc.userErr, familyErr: tc.familyErr, expireConsume: tc.expireConsume,
			}
			var store RefreshTokenCache = cache
			if tc.prepareErr != nil {
				store = &refreshFailurePreparer{refreshFailureCache: cache, err: tc.prepareErr}
			}
			svc := &AuthService{
				refreshTokenCache: store,
				userRepo:          &refreshFailureUserRepo{user: user, cache: cache},
				cfg:               &config.Config{JWT: config.JWTConfig{Secret: "synthetic-refresh-failure-signing-key", ExpireHour: 168}},
			}

			pair, err := svc.RefreshTokenPair(context.Background(), "rt_synthetic-original-token")
			require.Nil(t, pair, "no partially generated credentials may escape after consume")
			require.ErrorIs(t, err, tc.wantError)
			if tc.wantCause != nil {
				require.ErrorIs(t, err, tc.wantCause, "retain the internal cause for diagnostics")
			}
			require.True(t, cache.consumed)
			require.Equal(t, tc.wantCalls, cache.calls, "fail at the intended post-consume stage without retries")

			statusCode, status := infraerrors.ToHTTP(err)
			if errors.Is(tc.wantError, ErrServiceUnavailable) {
				require.Equal(t, http.StatusServiceUnavailable, statusCode)
				require.Equal(t, "SERVICE_UNAVAILABLE", status.Reason)
				require.Equal(t, "service temporarily unavailable", status.Message)
			} else {
				require.Equal(t, http.StatusUnauthorized, statusCode)
				require.Equal(t, "REFRESH_TOKEN_EXPIRED", status.Reason)
				require.Equal(t, "refresh token has expired", status.Message)
			}
			require.Empty(t, status.Metadata)

			// A failed issuance must not silently restore the consumed token or
			// mint a replacement on replay. This is not a cross-store rollback test.
			pair, err = svc.RefreshTokenPair(context.Background(), "rt_synthetic-original-token")
			require.Nil(t, pair)
			require.ErrorIs(t, err, ErrRefreshTokenInvalid)
			require.Equal(t, append(append([]string{}, tc.wantCalls...), "get"), cache.calls)
		})
	}
}
