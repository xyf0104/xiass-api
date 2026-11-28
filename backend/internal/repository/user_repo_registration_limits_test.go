package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func registrationLimitTestUser(email string) *service.User {
	return &service.User{
		Email:        email,
		Username:     email,
		PasswordHash: "test-password-hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  1,
	}
}

func TestUserRepositoryRegistrationLimitsIPDaily(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := service.WithRegistrationLimits(context.Background(), service.RegistrationLimits{
		ClientIP:            "192.0.2.10",
		IPDailyLimitEnabled: true,
	})

	first := registrationLimitTestUser("first@example.com")
	require.NoError(t, repo.CreateWithEmailAliasGuard(ctx, first))
	stored, err := client.User.Get(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, "192.0.2.10", stored.RegistrationIP)

	second := registrationLimitTestUser("second@example.com")
	require.ErrorIs(t, repo.CreateWithEmailAliasGuard(ctx, second), service.ErrRegistrationIPDailyLimit)
	require.Zero(t, second.ID)

	otherIPCtx := service.WithRegistrationLimits(ctx, service.RegistrationLimits{
		ClientIP:            "192.0.2.11",
		IPDailyLimitEnabled: true,
	})
	require.NoError(t, repo.CreateWithEmailAliasGuard(otherIPCtx, second))
	stored, err = client.User.Get(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, "192.0.2.11", stored.RegistrationIP)
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

func TestUserRepositoryRegistrationLimitsIPDayBoundaries(t *testing.T) {
	start := timezone.Today()
	cases := []struct {
		name      string
		createdAt time.Time
		blocked   bool
	}{
		{"previous day", start.Add(-time.Microsecond), false},
		{"start of today", start, true},
		{"end of today", start.AddDate(0, 0, 1).Add(-time.Microsecond), true},
		{"start of tomorrow", start.AddDate(0, 0, 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, client := newUserEntRepo(t)
			ctx := service.WithRegistrationLimits(context.Background(), service.RegistrationLimits{
				ClientIP:            "192.0.2.20",
				IPDailyLimitEnabled: true,
			})
			_, err := client.User.Create().
				SetEmail("existing@example.com").
				SetPasswordHash("hash").
				SetRegistrationIP("192.0.2.20").
				SetCreatedAt(tc.createdAt).
				Save(ctx)
			require.NoError(t, err)

			err = repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("new@example.com"))
			if tc.blocked {
				require.ErrorIs(t, err, service.ErrRegistrationIPDailyLimit)
				return
			}
			require.NoError(t, err)
			// Once today's slot is used, another attempt must be rejected.
			require.ErrorIs(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("next@example.com")), service.ErrRegistrationIPDailyLimit)
		})
	}
}

func TestUserRepositoryRegistrationLimitsRequireIPWhenEnabled(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := service.WithRegistrationLimits(context.Background(), service.RegistrationLimits{
		IPDailyLimitEnabled: true,
	})
	candidate := registrationLimitTestUser("missing-ip@example.com")
	require.ErrorIs(t, repo.CreateWithEmailAliasGuard(ctx, candidate), service.ErrServiceUnavailable)
	require.Zero(t, candidate.ID)
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestUserRepositoryRegistrationLimitsCustomDomain(t *testing.T) {
	cases := []struct {
		name     string
		stored   string
		incoming string
		blocked  bool
	}{
		{"same root", "existing@example.com", "new@example.com", true},
		{"existing root incoming subdomain", "existing@example.com", "new@mail.example.com", true},
		{"existing subdomain incoming root", "existing@mail.example.com", "new@example.com", true},
		{"sibling subdomains", "existing@one.example.com", "new@two.example.com", true},
		{"multi label public suffix", "existing@mail.example.co.uk", "new@other.example.co.uk", true},
		{"different registrable domains", "existing@example.co.uk", "new@other.co.uk", false},
		{"legacy case and spacing", "  Existing@MAIL.Example.COM  ", "new@example.com", true},
		{"incoming case and spacing", "existing@example.com", "  New@MAIL.Example.COM  ", true},
		{"stored trailing dot", "existing@mail.example.com.", "new@example.com", true},
		{"incoming trailing dot", "existing@example.com", "new@mail.example.com.", true},
		{"stored multiple trailing dots", "existing@example.com...", "new@mail.example.com", true},
		{"IDNA stored unicode", "existing@b\u00fccher.example", "new@xn--bcher-kva.example", true},
		{"IDNA stored punycode", "existing@xn--bcher-kva.example", "new@b\u00fccher.example", true},
		{"IDNA unicode subdomain", "existing@mail.b\u00fccher.example", "new@other.xn--bcher-kva.example", true},
		{"IDNA case and trailing dot", " Existing@MAIL.B\u00fcCHER.Example. ", "new@xn--bcher-kva.example", true},
		{"domain suffix boundary", "existing@notexample.com", "new@example.com", false},
		{"different top level domain", "existing@example.net", "new@example.com", false},
		{"domain in local part", "existing.example.com@unrelated.net", "new@example.com", false},
		{"different private suffix tenants", "existing@one.github.io", "new@two.github.io", false},
		{"same private suffix tenant", "existing@mail.one.github.io", "new@one.github.io", true},
		{"public provider customer subdomains", "existing@one.gmail.com", "new@two.gmail.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, client := newUserEntRepo(t)
			// Existing accounts predate quota enforcement and have no quota metadata.
			require.NoError(t, repo.Create(context.Background(), registrationLimitTestUser(tc.stored)))
			ctx := service.WithRegistrationLimits(context.Background(), service.RegistrationLimits{
				EmailDomainQuotaEnabled: true,
			})
			candidate := registrationLimitTestUser(tc.incoming)
			err := repo.CreateWithEmailAliasGuard(ctx, candidate)
			if tc.blocked {
				require.ErrorIs(t, err, service.ErrRegistrationDomainLimit)
				require.Zero(t, candidate.ID)
				count, err := client.User.Query().Count(ctx)
				require.NoError(t, err)
				require.Equal(t, 1, count)
				return
			}
			require.NoError(t, err)
			require.Positive(t, candidate.ID)
		})
	}
}

func TestUserRepositoryRegistrationLimitsPublicEmailExemptions(t *testing.T) {
	for _, domain := range []string{
		"gmail.com", "googlemail.com", "qq.com", "vip.qq.com", "foxmail.com",
		"163.com", "vip.163.com", "outlook.com", "hotmail.co.uk", "icloud.com",
		"yahoo.co.jp", "proton.me", "fastmail.com", "mail.ru", "naver.com",
		"GMAIL.COM.",
	} {
		t.Run(domain, func(t *testing.T) {
			repo, _ := newUserEntRepo(t)
			ctx := service.WithRegistrationLimits(context.Background(), service.RegistrationLimits{
				EmailDomainQuotaEnabled: true,
			})
			require.NoError(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("first@"+domain)))
			require.NoError(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("second@"+domain)))
		})
	}
}

func TestUserRepositoryRegistrationLimitsPublicEmailStillEnforcesIP(t *testing.T) {
	repo, _ := newUserEntRepo(t)
	ctx := service.WithRegistrationLimits(context.Background(), service.RegistrationLimits{
		ClientIP:                "192.0.2.30",
		IPDailyLimitEnabled:     true,
		EmailDomainQuotaEnabled: true,
	})
	require.NoError(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("first@gmail.com")))
	require.ErrorIs(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("second@gmail.com")), service.ErrRegistrationIPDailyLimit)
}

func TestUserRepositoryRegistrationLimitsDisabled(t *testing.T) {
	cases := []struct {
		name   string
		limits service.RegistrationLimits
	}{
		{"no context", service.RegistrationLimits{}},
		{"disabled with IP", service.RegistrationLimits{ClientIP: "192.0.2.40"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, _ := newUserEntRepo(t)
			ctx := context.Background()
			if tc.name != "no context" {
				ctx = service.WithRegistrationLimits(ctx, tc.limits)
			}
			require.NoError(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("first@example.com")))
			require.NoError(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("second@mail.example.com")))
		})
	}

	t.Run("domain quota independent of IP limit", func(t *testing.T) {
		repo, _ := newUserEntRepo(t)
		ctx := service.WithRegistrationLimits(context.Background(), service.RegistrationLimits{
			ClientIP:                "192.0.2.41",
			EmailDomainQuotaEnabled: true,
		})
		require.NoError(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("first@example.com")))
		require.NoError(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("second@other.com")))
		require.ErrorIs(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("third@mail.example.com")), service.ErrRegistrationDomainLimit)
	})

	t.Run("unguarded creation", func(t *testing.T) {
		repo, _ := newUserEntRepo(t)
		ctx := service.WithRegistrationLimits(context.Background(), service.RegistrationLimits{
			ClientIP:                "192.0.2.42",
			IPDailyLimitEnabled:     true,
			EmailDomainQuotaEnabled: true,
		})
		require.NoError(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("first@example.com")))
		require.NoError(t, repo.Create(ctx, registrationLimitTestUser("admin-created@example.com")))
	})
}

func TestUserRepositoryRegistrationLimitsRollbackReleasesQuota(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := service.WithRegistrationLimits(context.Background(), service.RegistrationLimits{
		ClientIP:                "192.0.2.50",
		IPDailyLimitEnabled:     true,
		EmailDomainQuotaEnabled: true,
	})
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	txCtx := dbent.NewTxContext(ctx, tx)
	require.NoError(t, repo.CreateWithEmailAliasGuard(txCtx, registrationLimitTestUser("rolled-back@example.com")))
	require.NoError(t, tx.Rollback())

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	count, err = client.AuthIdentity.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	require.NoError(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("retry@mail.example.com")))
}

func TestUserRepositoryRegistrationLimitsFailedCreateReleasesQuota(t *testing.T) {
	repo, client := newUserEntRepo(t)
	ctx := service.WithRegistrationLimits(context.Background(), service.RegistrationLimits{
		ClientIP:                "192.0.2.60",
		IPDailyLimitEnabled:     true,
		EmailDomainQuotaEnabled: true,
	})
	injectedErr := errors.New("email identity creation failed")
	failIdentity := true
	client.AuthIdentity.Use(func(next dbent.Mutator) dbent.Mutator {
		return dbent.MutateFunc(func(ctx context.Context, mutation dbent.Mutation) (dbent.Value, error) {
			if failIdentity {
				return nil, injectedErr
			}
			return next.Mutate(ctx, mutation)
		})
	})

	candidate := registrationLimitTestUser("failed@example.com")
	require.ErrorIs(t, repo.CreateWithEmailAliasGuard(ctx, candidate), injectedErr)
	require.Zero(t, candidate.ID)
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
	failIdentity = false
	require.NoError(t, repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser("retry@mail.example.com")))
}
