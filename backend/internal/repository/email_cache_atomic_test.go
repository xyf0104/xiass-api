package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newMiniredisEmailCache(t *testing.T) (service.EmailCache, *miniredis.Miniredis) {
	t.Helper()
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	return NewEmailCache(redisClient), redisServer
}

func TestEmailCacheConcurrentWrongCodesCannotExceedAttemptCap(t *testing.T) {
	cache, _ := newMiniredisEmailCache(t)
	ctx := context.Background()
	const email = "user@example.com"

	serviceUnderTest := service.NewEmailService(nil, cache)
	require.NoError(t, cache.SetVerificationCode(ctx, email, &service.VerificationCodeData{
		Code:      "123456",
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(15 * time.Minute),
	}, 15*time.Minute))

	const workers = 50
	var invalidCount atomic.Int32
	var maxedCount atomic.Int32
	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			err := serviceUnderTest.VerifyCode(ctx, email, "000000")
			switch {
			case errors.Is(err, service.ErrInvalidVerifyCode):
				invalidCount.Add(1)
			case errors.Is(err, service.ErrVerifyCodeMaxAttempts):
				maxedCount.Add(1)
			default:
				t.Errorf("unexpected result: %v", err)
			}
		}()
	}
	waitGroup.Wait()

	require.LessOrEqual(t, int(invalidCount.Load()), 4)
	require.Equal(t, workers, int(invalidCount.Load()+maxedCount.Load()))
	require.ErrorIs(t, serviceUnderTest.VerifyCode(ctx, email, "123456"), service.ErrVerifyCodeMaxAttempts)

	data, err := cache.GetVerificationCode(ctx, email)
	require.NoError(t, err)
	require.GreaterOrEqual(t, data.Attempts, 5)
}

func TestEmailCacheAttemptsResetAndPreservePreUpgradeCount(t *testing.T) {
	cache, redisServer := newMiniredisEmailCache(t)
	ctx := context.Background()
	const email = "User@Example.com"

	require.NoError(t, cache.SetVerificationCode(ctx, email, &service.VerificationCodeData{
		Code:     "1",
		Attempts: 3,
	}, time.Minute))
	attempts, err := cache.IncrVerificationCodeAttempts(ctx, email)
	require.NoError(t, err)
	require.Equal(t, 4, attempts)
	require.Greater(t, redisServer.TTL(verifyCodeKey(email)+attemptsKeySuffix), time.Duration(0))

	require.NoError(t, cache.SetVerificationCode(ctx, email, &service.VerificationCodeData{Code: "2"}, time.Minute))
	data, err := cache.GetVerificationCode(ctx, email)
	require.NoError(t, err)
	require.Zero(t, data.Attempts)

	require.NoError(t, cache.DeleteVerificationCode(ctx, email))
	_, err = cache.IncrVerificationCodeAttempts(ctx, email)
	require.Error(t, err)
	require.False(t, redisServer.Exists(verifyCodeKey(email)+attemptsKeySuffix))
}

func TestEmailCachePasswordResetTokenHashedAndSingleUse(t *testing.T) {
	cache, redisServer := newMiniredisEmailCache(t)
	ctx := context.Background()
	const email = "reset@example.com"

	serviceUnderTest := service.NewEmailService(nil, cache)
	token, err := serviceUnderTest.GeneratePasswordResetToken()
	require.NoError(t, err)
	tokenHash := sha256.Sum256([]byte(token))
	require.NoError(t, cache.SetPasswordResetToken(ctx, email, &service.PasswordResetTokenData{
		Token:     hex.EncodeToString(tokenHash[:]),
		CreatedAt: time.Now(),
	}, 30*time.Minute))

	raw, err := redisServer.Get(passwordResetKey(email))
	require.NoError(t, err)
	require.False(t, strings.Contains(raw, token), "plaintext token must not be stored")
	require.NoError(t, serviceUnderTest.VerifyPasswordResetToken(ctx, email, token))
	require.ErrorIs(t, serviceUnderTest.VerifyPasswordResetToken(ctx, email, hex.EncodeToString(tokenHash[:])), service.ErrInvalidResetToken)
	require.ErrorIs(t, serviceUnderTest.ConsumePasswordResetToken(ctx, email, hex.EncodeToString(tokenHash[:])), service.ErrInvalidResetToken)
	require.ErrorIs(t, serviceUnderTest.ConsumePasswordResetToken(ctx, email, "wrong"), service.ErrInvalidResetToken)

	const workers = 30
	var successCount atomic.Int32
	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if serviceUnderTest.ConsumePasswordResetToken(ctx, email, token) == nil {
				successCount.Add(1)
			}
		}()
	}
	waitGroup.Wait()
	require.Equal(t, int32(1), successCount.Load())
	require.False(t, redisServer.Exists(passwordResetKey(email)))
}

func TestEmailCacheConsumePasswordResetTokenMismatchKeepsToken(t *testing.T) {
	cache, redisServer := newMiniredisEmailCache(t)
	ctx := context.Background()
	const email = "keep@example.com"
	require.NoError(t, cache.SetPasswordResetToken(ctx, email, &service.PasswordResetTokenData{Token: "abc"}, time.Minute))

	consumed, err := cache.ConsumePasswordResetToken(ctx, email, "xyz")
	require.NoError(t, err)
	require.False(t, consumed)
	require.True(t, redisServer.Exists(passwordResetKey(email)))

	consumed, err = cache.ConsumePasswordResetToken(ctx, email, "abc")
	require.NoError(t, err)
	require.True(t, consumed)
	consumed, err = cache.ConsumePasswordResetToken(ctx, email, "abc")
	require.NoError(t, err)
	require.False(t, consumed)
}
