//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type registrationLimitsUserRepo struct {
	*userRepoStub
	limits RegistrationLimits
}

func (r *registrationLimitsUserRepo) CreateWithEmailAliasGuard(ctx context.Context, user *User) error {
	r.limits = RegistrationLimitsFromContext(ctx)
	return r.userRepoStub.CreateWithEmailAliasGuard(ctx, user)
}

func TestAuthRegistrationLimitsAllSignupPaths(t *testing.T) {
	paths := map[string]func(*AuthService, context.Context) error{
		"email": func(s *AuthService, ctx context.Context) error {
			_, _, err := s.Register(ctx, "new@example.com", "Password123!")
			return err
		},
		"oauth": func(s *AuthService, ctx context.Context) error {
			_, _, err := s.LoginOrRegisterOAuth(ctx, "new@example.com", "new")
			return err
		},
		"oauth token pair": func(s *AuthService, ctx context.Context) error {
			_, _, err := s.LoginOrRegisterOAuthWithTokenPair(ctx, "new@example.com", "new", "", "", "linuxdo")
			return err
		},
		"verified oauth email": func(s *AuthService, ctx context.Context) error {
			_, _, err := s.RegisterVerifiedOAuthEmailAccount(ctx, "new@example.com", "Password123!", "", "github")
			return err
		},
		"oauth email code": func(s *AuthService, ctx context.Context) error {
			_, _, err := s.RegisterOAuthEmailAccount(ctx, "new@example.com", "Password123!", "123456", "", "github")
			return err
		},
		"automatic email oauth": func(s *AuthService, ctx context.Context) error {
			_, err := s.createEmailOAuthUser(ctx, "new@example.com", "new", "github", "", "")
			return err
		},
	}
	for name, signup := range paths {
		for _, limitErr := range []error{ErrRegistrationIPDailyLimit, ErrRegistrationDomainLimit} {
			t.Run(name+"/"+limitErr.Error(), func(t *testing.T) {
				repo := &registrationLimitsUserRepo{userRepoStub: &userRepoStub{createErr: limitErr}}
				svc := newAuthService(repo.userRepoStub, map[string]string{
					SettingKeyRegistrationEnabled:                 "true",
					SettingKeyRegistrationIPDailyLimitEnabled:     "true",
					SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
				}, &emailCacheStub{data: &VerificationCodeData{Code: "123456"}}, nil)
				svc.userRepo = repo
				svc.refreshTokenCache = &refreshTokenCacheStub{}
				ctx := WithRegistrationLimits(context.Background(), RegistrationLimits{ClientIP: "::ffff:192.0.2.10"})
				require.ErrorIs(t, signup(svc, ctx), limitErr)
				require.Equal(t, RegistrationLimits{
					ClientIP: "192.0.2.10", IPDailyLimitEnabled: true, EmailDomainQuotaEnabled: true,
				}, repo.limits)
				require.Equal(t, 1, repo.guardedCreates)
				require.Empty(t, repo.created)
			})
		}
	}
}

func TestAuthRegistrationContext(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ip      string
		enabled bool
		wantIP  string
		wantErr error
	}{
		{"IPv4", "192.0.2.1", true, "192.0.2.1", nil},
		{"IPv6 prefix", "2001:db8:1:2:abcd::1", true, "2001:db8:1:2::", nil},
		{"missing enabled", "", true, "", ErrServiceUnavailable},
		{"invalid enabled", "invalid", true, "", ErrServiceUnavailable},
		{"missing disabled", "", false, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := map[string]string{}
			if tc.enabled {
				settings[SettingKeyRegistrationIPDailyLimitEnabled] = "true"
			}
			svc := newAuthService(&userRepoStub{}, settings, nil, nil)
			ctx, err := svc.registrationContext(WithRegistrationLimits(context.Background(), RegistrationLimits{ClientIP: tc.ip}))
			require.ErrorIs(t, err, tc.wantErr)
			if err == nil {
				require.Equal(t, tc.wantIP, RegistrationLimitsFromContext(ctx).ClientIP)
			}
		})
	}
	t.Run("settings unavailable", func(t *testing.T) {
		svc := newAuthService(&userRepoStub{}, map[string]string{}, nil, nil)
		svc.settingService.settingRepo.(*settingRepoStub).err = errors.New("database unavailable")
		_, err := svc.registrationContext(context.Background())
		require.ErrorIs(t, err, ErrServiceUnavailable)
	})
}

func TestAuthRegistrationLimitsExistingOAuthLogin(t *testing.T) {
	user := &User{ID: 12, Email: "existing@example.com", Status: StatusActive}
	repo := &userRepoStub{user: user}
	svc := newAuthService(repo, map[string]string{
		SettingKeyRegistrationIPDailyLimitEnabled:     "true",
		SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
	}, nil, nil)
	token, got, err := svc.LoginOrRegisterOAuth(context.Background(), user.Email, "existing")
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.Equal(t, user.ID, got.ID)
	require.Zero(t, repo.guardedCreates)
}

func TestRegistrationEmailDomainInvalid(t *testing.T) {
	for _, email := range []string{"missing-at", "user@@example.com", "user@bad_domain.com", "user@.example.com", "user@localhost", "user@bad\u200b_domain.com"} {
		t.Run(email, func(t *testing.T) {
			_, err := RegistrationEmailDomain(email)
			require.Error(t, err)
		})
	}
}
