package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRedeemReductionUsesLockedCurrentSubscription(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	stale := UserSubscription{ID: 7, UserID: 11, GroupID: 13, ExpiresAt: now.AddDate(0, 0, 10), Status: SubscriptionStatusActive, Notes: "stale"}
	current := stale
	current.ExpiresAt = now.AddDate(0, 0, 20)
	current.Notes = "renewed"
	repo := &lockingRenewalRepo{stale: stale, current: current}
	subscriptions := NewSubscriptionService(nil, repo, nil, nil, nil)
	subscriptions.now = func() time.Time { return now }
	svc := &RedeemService{subscriptionService: subscriptions}
	require.NoError(t, svc.reduceOrCancelSubscription(context.Background(), 11, 13, 1, "minus-one-day"))
	require.Equal(t, current.ExpiresAt.AddDate(0, 0, -1), repo.current.ExpiresAt)
	require.Contains(t, repo.current.Notes, "renewed")
	require.Equal(t, 1, repo.lockReads)
}

func TestRedeemReductionPreservesPartialDays(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		remaining time.Duration
		expired   bool
	}{
		{name: "partial day remains", remaining: 36 * time.Hour},
		{name: "exact exhaustion", remaining: 24 * time.Hour, expired: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub := UserSubscription{ID: 7, UserID: 11, GroupID: 13, ExpiresAt: now.Add(tc.remaining), Status: SubscriptionStatusActive, Notes: "original"}
			repo := &lockingRenewalRepo{stale: sub, current: sub}
			subscriptions := NewSubscriptionService(nil, repo, nil, nil, nil)
			subscriptions.now = func() time.Time { return now }
			svc := &RedeemService{subscriptionService: subscriptions}
			require.NoError(t, svc.reduceOrCancelSubscription(context.Background(), 11, 13, 1, "deduct"))
			wantExpiry := sub.ExpiresAt.AddDate(0, 0, -1)
			wantStatus := SubscriptionStatusActive
			if tc.expired {
				wantExpiry, wantStatus = now, SubscriptionStatusExpired
			}
			require.Equal(t, wantExpiry, repo.current.ExpiresAt)
			require.Equal(t, wantStatus, repo.current.Status)
		})
	}
}

type failingReductionLockRepo struct {
	*lockingRenewalRepo
	err error
}

func (r *failingReductionLockRepo) GetByIDForUpdate(context.Context, int64) (*UserSubscription, error) {
	return nil, r.err
}

func TestRedeemReductionLockFailureDoesNotWrite(t *testing.T) {
	sub := UserSubscription{ID: 7, UserID: 11, GroupID: 13, ExpiresAt: time.Now().AddDate(0, 0, 10), Status: SubscriptionStatusActive, Notes: "unchanged"}
	lockErr := errors.New("lock failed")
	repo := &failingReductionLockRepo{lockingRenewalRepo: &lockingRenewalRepo{stale: sub, current: sub}, err: lockErr}
	svc := &RedeemService{subscriptionService: NewSubscriptionService(nil, repo, nil, nil, nil)}
	require.ErrorIs(t, svc.reduceOrCancelSubscription(context.Background(), 11, 13, 1, "deduct"), lockErr)
	require.Equal(t, sub, repo.current)
}
