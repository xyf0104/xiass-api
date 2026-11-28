//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/user"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUserRepositoryRegistrationLimitsConcurrentPostgres(t *testing.T) {
	cases := []struct {
		name   string
		limits service.RegistrationLimits
		want   error
	}{
		{
			name: "same IP different domains",
			limits: service.RegistrationLimits{
				ClientIP:            "192.0.2.100",
				IPDailyLimitEnabled: true,
			},
			want: service.ErrRegistrationIPDailyLimit,
		},
		{
			name: "same domain different IPs",
			limits: service.RegistrationLimits{
				EmailDomainQuotaEnabled: true,
			},
			want: service.ErrRegistrationDomainLimit,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := testEntClient(t)
			repo := NewUserRepository(client, integrationDB)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			const attempts = 8
			token := uuid.NewString()
			emails := make([]string, attempts)
			for i := range emails {
				if tc.limits.IPDailyLimitEnabled {
					emails[i] = fmt.Sprintf("user@registration-%s-%d.example", token, i)
				} else {
					emails[i] = fmt.Sprintf("user%d@team%d.registration-%s.example", i, i, token)
				}
			}
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanupCancel()
				_, err := client.User.Delete().Where(user.EmailIn(emails...)).Exec(cleanupCtx)
				require.NoError(t, err)
			})

			start := make(chan struct{})
			results := make(chan error, attempts)
			for i, email := range emails {
				limits := tc.limits
				if !limits.IPDailyLimitEnabled {
					limits.ClientIP = fmt.Sprintf("192.0.2.%d", 110+i)
				}
				go func() {
					<-start
					attemptCtx := service.WithRegistrationLimits(ctx, limits)
					results <- repo.CreateWithEmailAliasGuard(attemptCtx, registrationLimitTestUser(email))
				}()
			}
			close(start)

			successes, rejected := 0, 0
			for range attempts {
				err := <-results
				switch {
				case err == nil:
					successes++
				case errors.Is(err, tc.want):
					rejected++
				default:
					t.Errorf("unexpected registration result: %v", err)
				}
			}
			require.Equal(t, 1, successes)
			require.Equal(t, attempts-1, rejected)
			count, err := client.User.Query().Where(user.EmailIn(emails...)).Count(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, count)
		})
	}
}

func TestUserRepositoryRegistrationLimitsWaitForOuterTransactionPostgres(t *testing.T) {
	for _, limit := range []string{"IP", "domain"} {
		for _, commit := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/commit=%t", limit, commit), func(t *testing.T) {
				client := testEntClient(t)
				repo := NewUserRepository(client, integrationDB)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				token := uuid.NewString()
				domain := "registration-" + token + ".example"
				firstEmail := "first@" + domain
				secondEmail := "second@mail." + domain
				limits := service.RegistrationLimits{EmailDomainQuotaEnabled: true}
				lockKey := "registration-domain:" + domain
				limitErr := service.ErrRegistrationDomainLimit
				if limit == "IP" {
					limits = service.RegistrationLimits{ClientIP: "192.0.2.101", IPDailyLimitEnabled: true}
					secondEmail = "second@other-" + domain
					lockKey = "registration-ip:" + limits.ClientIP
					limitErr = service.ErrRegistrationIPDailyLimit
				}
				t.Cleanup(func() {
					cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cleanupCancel()
					_, err := client.User.Delete().Where(user.EmailIn(firstEmail, secondEmail)).Exec(cleanupCtx)
					require.NoError(t, err)
				})
				ctx = service.WithRegistrationLimits(ctx, limits)
				tx, err := client.Tx(ctx)
				require.NoError(t, err)
				defer func() { _ = tx.Rollback() }()
				require.NoError(t, repo.CreateWithEmailAliasGuard(dbent.NewTxContext(ctx, tx), registrationLimitTestUser(firstEmail)))

				result := make(chan error, 1)
				go func() {
					result <- repo.CreateWithEmailAliasGuard(ctx, registrationLimitTestUser(secondEmail))
				}()

				// The process-local lock has been released. The transaction-scoped
				// PostgreSQL lock must still fence the uncommitted registration.
				hash := uint64(advisoryLockHash(lockKey))
				require.Eventually(t, func() bool {
					var waiting bool
					err := integrationDB.QueryRowContext(ctx, `
						SELECT EXISTS (
							SELECT 1 FROM pg_locks
							WHERE locktype = 'advisory' AND NOT granted
							AND classid = $1::oid AND objid = $2::oid AND objsubid = 1
						)`, int64(uint32(hash>>32)), int64(uint32(hash))).Scan(&waiting)
					return err == nil && waiting
				}, 5*time.Second, 10*time.Millisecond, "competing registration must wait for the open transaction")

				if commit {
					require.NoError(t, tx.Commit())
					require.ErrorIs(t, <-result, limitErr)
				} else {
					require.NoError(t, tx.Rollback())
					require.NoError(t, <-result)
				}
				count, err := client.User.Query().Where(user.EmailIn(firstEmail, secondEmail)).Count(ctx)
				require.NoError(t, err)
				require.Equal(t, 1, count)
			})
		}
	}
}
