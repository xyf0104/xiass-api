package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMultiGroupAuthDefersPrimaryGroupEligibilityToSelection(t *testing.T) {
	for _, google := range []bool{false, true} {
		for _, primaryState := range []string{"disabled", "hidden", "subscription-without-entitlement", "zero-balance"} {
			t.Run(primaryState+map[bool]string{false: "/standard", true: "/google"}[google], func(t *testing.T) {
				primary := &service.Group{ID: 1, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true}
				target := &service.Group{ID: 2, Platform: service.PlatformGemini, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeSubscription}
				user := &service.User{ID: 1, Status: service.StatusActive, Balance: 10}
				switch primaryState {
				case "disabled":
					primary.Status = service.StatusDisabled
				case "hidden":
					user.RestrictPublicGroups = true
					user.AllowedGroups = []int64{2}
				case "subscription-without-entitlement":
					primary.SubscriptionType = service.SubscriptionTypeSubscription
				case "zero-balance":
					user.Balance = 0
				}
				key := &service.APIKey{ID: 127, Key: "test-multi-group", UserID: 1, User: user, Status: service.StatusActive, Group: primary, GroupID: &primary.ID, Groups: []*service.Group{primary, target}, GroupIDs: []int64{1, 2}}
				cfg := &config.Config{RunMode: config.RunModeStandard}
				apiKeys := newTestAPIKeyService(fakeAPIKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { copy := *key; return &copy, nil }})
				subscriptions := service.NewSubscriptionService(nil, fakeGoogleSubscriptionRepo{getActive: func(_ context.Context, userID, groupID int64) (*service.UserSubscription, error) {
					if groupID == 1 {
						return nil, service.ErrSubscriptionNotFound
					}
					return &service.UserSubscription{ID: 102, UserID: userID, GroupID: groupID, Status: service.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)}, nil
				}}, nil, nil, cfg)
				auth := apiKeyAuthWithSubscription(apiKeys, subscriptions, cfg)
				if google {
					auth = APIKeyAuthWithSubscriptionGoogle(apiKeys, subscriptions, cfg)
				}
				r := gin.New()
				r.POST("/v1beta/test", auth, func(c *gin.Context) {
					sub, ok := GetGroupSubscriptionFromContext(c, 2)
					require.True(t, ok)
					require.Equal(t, int64(2), sub.GroupID)
					_, ok = GetSubscriptionFromContext(c)
					require.False(t, ok, "primary subscription must not bind before model selection")
					c.Status(http.StatusNoContent)
				})
				req := httptest.NewRequest(http.MethodPost, "/v1beta/test", nil)
				req.Header.Set("Authorization", "Bearer test-multi-group")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
			})
		}
	}
}

func TestMultiGroupAuthMaintainsOnlySelectedSubscription(t *testing.T) {
	for _, google := range []bool{false, true} {
		for _, mode := range []string{"daily", "weekly", "monthly", "activation", "maintenance-failure", "reread-limit", "daily-limit", "weekly-limit", "monthly-limit", "expired", "suspended"} {
			t.Run(mode+map[bool]string{false: "/standard", true: "/google"}[google], func(t *testing.T) {
				now := time.Now()
				old := now.Add(-40 * 24 * time.Hour)
				limit := 10.0
				primary := &service.Group{ID: 1, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeSubscription, DailyLimitUSD: &limit}
				target := &service.Group{ID: 2, Platform: service.PlatformGemini, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeSubscription, DailyLimitUSD: &limit, WeeklyLimitUSD: &limit, MonthlyLimitUSD: &limit}
				primarySub := &service.UserSubscription{ID: 101, UserID: 1, GroupID: 1, Status: service.SubscriptionStatusSuspended, ExpiresAt: now.Add(time.Hour), DailyWindowStart: &old, DailyUsageUSD: 99}
				loaded := &service.UserSubscription{ID: 102, UserID: 1, GroupID: 2, Status: service.SubscriptionStatusActive, StartsAt: old, ExpiresAt: now.Add(40 * 24 * time.Hour), DailyWindowStart: &now, WeeklyWindowStart: &now, MonthlyWindowStart: &now}
				expectedStatus := http.StatusNoContent
				switch mode {
				case "daily", "maintenance-failure", "reread-limit":
					loaded.DailyWindowStart, loaded.DailyUsageUSD = &old, 99
				case "weekly":
					loaded.WeeklyWindowStart, loaded.WeeklyUsageUSD = &old, 99
				case "monthly":
					loaded.MonthlyWindowStart, loaded.MonthlyUsageUSD = &old, 99
				case "activation":
					loaded.DailyWindowStart, loaded.WeeklyWindowStart, loaded.MonthlyWindowStart = nil, nil, nil
				case "daily-limit":
					loaded.DailyUsageUSD = 99
					expectedStatus = http.StatusTooManyRequests
				case "weekly-limit":
					loaded.WeeklyUsageUSD = 99
					expectedStatus = http.StatusTooManyRequests
				case "monthly-limit":
					loaded.MonthlyUsageUSD = 99
					expectedStatus = http.StatusTooManyRequests
				case "expired":
					loaded.ExpiresAt = now.Add(-time.Hour)
					expectedStatus = http.StatusForbidden
				case "suspended":
					loaded.Status = service.SubscriptionStatusSuspended
					expectedStatus = http.StatusForbidden
				}
				switch mode {
				case "maintenance-failure":
					expectedStatus = http.StatusInternalServerError
				case "reread-limit":
					expectedStatus = http.StatusTooManyRequests
				}
				before := *loaded
				persisted := *loaded
				var maintained []string
				reads := 0
				reset := func(window string, id int64, start time.Time) error {
					require.Equal(t, loaded.ID, id, "the primary subscription must never be maintained")
					maintained = append(maintained, window)
					if mode == "maintenance-failure" {
						return errors.New("maintenance failed")
					}
					switch window {
					case "daily":
						persisted.DailyWindowStart, persisted.DailyUsageUSD = &start, 0
					case "weekly":
						persisted.WeeklyWindowStart, persisted.WeeklyUsageUSD = &start, 0
					case "monthly":
						persisted.MonthlyWindowStart, persisted.MonthlyUsageUSD = &start, 0
					}
					return nil
				}
				repo := fakeGoogleSubscriptionRepo{
					getActive: func(_ context.Context, _, groupID int64) (*service.UserSubscription, error) {
						if groupID == 1 {
							return primarySub, nil
						}
						return loaded, nil
					},
					getByID: func(_ context.Context, id int64) (*service.UserSubscription, error) {
						require.Equal(t, loaded.ID, id)
						reads++
						if mode == "reread-limit" {
							persisted.DailyUsageUSD = 99
						}
						return &persisted, nil
					},
					resetDaily:   func(_ context.Context, id int64, start time.Time) error { return reset("daily", id, start) },
					resetWeekly:  func(_ context.Context, id int64, start time.Time) error { return reset("weekly", id, start) },
					resetMonthly: func(_ context.Context, id int64, start time.Time) error { return reset("monthly", id, start) },
					activateWindow: func(_ context.Context, id int64, dailyStart, periodicStart time.Time) error {
						require.Equal(t, loaded.ID, id)
						maintained = append(maintained, "activation")
						persisted.DailyWindowStart, persisted.WeeklyWindowStart, persisted.MonthlyWindowStart = &dailyStart, &periodicStart, &periodicStart
						return nil
					},
				}
				key := &service.APIKey{ID: 127, UserID: 1, User: &service.User{ID: 1, Status: service.StatusActive}, Status: service.StatusActive, Group: primary, GroupID: &primary.ID, Groups: []*service.Group{primary, target}, GroupIDs: []int64{1, 2}}
				cfg := &config.Config{RunMode: config.RunModeStandard}
				apiKeys := newTestAPIKeyService(fakeAPIKeyRepo{getByKey: func(context.Context, string) (*service.APIKey, error) { copy := *key; return &copy, nil }})
				subscriptions := service.NewSubscriptionService(nil, repo, nil, nil, cfg)
				auth := apiKeyAuthWithSubscription(apiKeys, subscriptions, cfg)
				if google {
					auth = APIKeyAuthWithSubscriptionGoogle(apiKeys, subscriptions, cfg)
				}
				r := gin.New()
				r.POST("/v1beta/test", auth, func(c *gin.Context) {
					require.Empty(t, maintained, "auth must defer maintenance until a group is selected")
					selected, _ := GetAPIKeyFromContext(c)
					selected.Group, selected.GroupID = target, &target.ID
					status, message := ValidateSelectedAPIKeyGroupSubscription(c, selected)
					if status != 0 {
						c.String(status, "%s", message)
						return
					}
					sub, ok := GetSubscriptionFromContext(c)
					require.True(t, ok)
					require.Equal(t, persisted, *sub)
					byGroup, ok := GetGroupSubscriptionFromContext(c, target.ID)
					require.True(t, ok)
					require.Same(t, sub, byGroup, "billing must receive the maintained snapshot")
					c.Status(http.StatusNoContent)
				})
				req := httptest.NewRequest(http.MethodPost, "/v1beta/test", nil)
				req.Header.Set("Authorization", "Bearer test-multi-group")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, expectedStatus, w.Code, w.Body.String())
				require.Equal(t, before, *loaded, "auth snapshot must stay unchanged")
				require.Equal(t, 99.0, primarySub.DailyUsageUSD)
				if expectedStatus == http.StatusNoContent || mode == "reread-limit" {
					require.Len(t, maintained, 1)
					require.Equal(t, 1, reads)
				} else {
					require.Zero(t, reads)
				}
			})
		}
	}
}
