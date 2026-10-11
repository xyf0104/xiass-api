//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type resetTokenCacheStub struct {
	emailCacheStub
	stored        *PasswordResetTokenData
	consumedValue string
}

func (s *resetTokenCacheStub) GetPasswordResetToken(context.Context, string) (*PasswordResetTokenData, error) {
	return s.stored, nil
}

func (s *resetTokenCacheStub) ConsumePasswordResetToken(_ context.Context, _ string, expectedTokenValue string) (bool, error) {
	s.consumedValue = expectedTokenValue
	if s.stored == nil || s.stored.Token != expectedTokenValue {
		return false, nil
	}
	s.stored = nil
	return true, nil
}

func TestConsumePasswordResetTokenComparesHashNotPlaintext(t *testing.T) {
	const token = "deadbeef"
	hash := hashPasswordResetToken(token)
	require.NotEqual(t, token, hash)

	cache := &resetTokenCacheStub{stored: &PasswordResetTokenData{Token: hash}}
	serviceUnderTest := NewEmailService(nil, cache)

	require.NoError(t, serviceUnderTest.ConsumePasswordResetToken(context.Background(), "a@b.c", token))
	require.Equal(t, hash, cache.consumedValue)
	require.ErrorIs(t, serviceUnderTest.ConsumePasswordResetToken(context.Background(), "a@b.c", token), ErrInvalidResetToken)
}

func TestPasswordResetStoredHashCannotBePresentedAsToken(t *testing.T) {
	const token = "deadbeef"
	hash := hashPasswordResetToken(token)
	cache := &resetTokenCacheStub{stored: &PasswordResetTokenData{Token: hash}}
	serviceUnderTest := NewEmailService(nil, cache)

	require.ErrorIs(t, serviceUnderTest.VerifyPasswordResetToken(context.Background(), "a@b.c", hash), ErrInvalidResetToken)
	require.ErrorIs(t, serviceUnderTest.ConsumePasswordResetToken(context.Background(), "a@b.c", hash), ErrInvalidResetToken)
	require.Empty(t, cache.consumedValue)
	require.NotNil(t, cache.stored)
}
