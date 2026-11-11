package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type followupResolverStub struct {
	groupID          int64
	err              error
	platform         string
	requestID        string
	userID, apiKeyID int64
	groups           []int64
	calls            int
}

func (s *followupResolverStub) ResolveGrokVideoRequestGroup(_ context.Context, requestID string, userID, apiKeyID int64, groups []int64) (int64, error) {
	s.calls++
	s.platform, s.requestID, s.userID, s.apiKeyID = service.PlatformGrok, requestID, userID, apiKeyID
	s.groups = append([]int64(nil), groups...)
	return s.groupID, s.err
}

func (s *followupResolverStub) ResolveLiveCallGroup(_ context.Context, requestID string, userID, apiKeyID int64) (int64, error) {
	s.calls++
	s.platform, s.requestID, s.userID, s.apiKeyID = service.PlatformOpenAI, requestID, userID, apiKeyID
	return s.groupID, s.err
}

func followupAPIKeyFixture(platform string) *service.APIKey {
	primary := &service.Group{ID: 10, Platform: service.PlatformAnthropic, Status: service.StatusActive, Hydrated: true}
	secondary := &service.Group{ID: 20, Platform: platform, Status: service.StatusActive, Hydrated: true, AllowLive: true, RateMultiplier: 1.7}
	rpm := 31
	return &service.APIKey{
		ID: 45, UserID: 7, Group: primary, GroupID: &primary.ID,
		Groups: []*service.Group{primary, secondary}, GroupIDs: []int64{10, 20},
		User: &service.User{ID: 7, UserGroupRPMOverride: &rpm},
	}
}

func serveFollowupRequest(t *testing.T, method, route string, key *service.APIKey, resolver apiKeyFollowupGroupResolver, simple bool, setup func(*gin.Context), next func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
		if key != nil {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID})
			middleware.SetGroupContext(c, key.Group)
		}
		if setup != nil {
			setup(c)
		}
		c.Next()
	})
	r.Handle(method, route, func(c *gin.Context) {
		if !restoreAPIKeyGroupForFollowup(c, key, resolver, simple) {
			return
		}
		c.Next()
	}, func(c *gin.Context) {
		if next != nil {
			next(c)
		}
		c.Status(http.StatusNoContent)
	})
	path := strings.ReplaceAll(strings.ReplaceAll(route, ":request_id", "task-test"), ":call_id", "call-test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestRestoreAPIKeyGroupForFollowupRoutesAndIsolation(t *testing.T) {
	for _, route := range []string{
		"/v1/videos/:request_id", "/v1/videos/:request_id/content", "/videos/:request_id", "/videos/:request_id/content",
		"/v1/live/:call_id", "/backend-api/codex/:call_id",
	} {
		for _, composite := range []bool{false, true} {
			t.Run(route+"/composite="+map[bool]string{false: "false", true: "true"}[composite], func(t *testing.T) {
				platform, requestID := service.PlatformGrok, "task-test"
				if strings.Contains(route, "call_id") {
					platform, requestID = service.PlatformOpenAI, "call-test"
				}
				key := followupAPIKeyFixture(platform)
				primary, originalUser, secondary := key.Group, key.User, key.Groups[1]
				if composite {
					secondary.Platform = service.PlatformComposite
				}
				resolver := &followupResolverStub{groupID: secondary.ID}
				w := serveFollowupRequest(t, http.MethodGet, route, key, resolver, false, func(c *gin.Context) {
					c.Set(string(middleware.ContextKeySubscription), &service.UserSubscription{ID: 11, UserID: 7, GroupID: primary.ID})
				}, func(c *gin.Context) {
					selected, ok := middleware.GetAPIKeyFromContext(c)
					require.True(t, ok)
					require.NotSame(t, key, selected)
					require.Same(t, secondary, selected.Group)
					require.Equal(t, secondary.ID, *selected.GroupID)
					require.Equal(t, 1.7, selected.Group.RateMultiplier)
					require.Same(t, secondary, c.Request.Context().Value(ctxkey.Group))
					require.Nil(t, selected.User.UserGroupRPMOverride)
					require.NotSame(t, originalUser, selected.User)
					require.Equal(t, []int64{10, 20}, selected.GroupIDs)
					sub, _ := middleware.GetSubscriptionFromContext(c)
					require.Nil(t, sub)
					require.Equal(t, platform, service.QuotaPlatform(c.Request.Context(), selected))
					if composite {
						resolved, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
						require.True(t, ok)
						require.Equal(t, platform, resolved)
					}
					marker, exists := c.Get(selectedAPIKeyGroupContextKey)
					require.True(t, exists)
					require.Equal(t, secondary.ID, marker)
				})
				require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
				require.Equal(t, 1, resolver.calls)
				require.Equal(t, platform, resolver.platform)
				require.Equal(t, requestID, resolver.requestID)
				require.Equal(t, key.UserID, resolver.userID)
				require.Equal(t, key.ID, resolver.apiKeyID)
				if platform == service.PlatformGrok {
					require.Equal(t, []int64{10, 20}, resolver.groups)
				}
				require.Same(t, primary, key.Group)
				require.Equal(t, primary.ID, *key.GroupID)
				require.Same(t, originalUser, key.User)
				require.Equal(t, 31, *key.User.UserGroupRPMOverride)
			})
		}
	}
}

func TestRestoreAPIKeyGroupForFollowupAuthorization(t *testing.T) {
	for _, mode := range []string{"hidden", "exclusive-revoked", "disabled", "deleted", "unbound", "wrong-platform", "forced-other", "missing-owner", "cache-error", "zero-owner", "wrong-auth-user", "wrong-key-user", "too-many-groups"} {
		t.Run(mode, func(t *testing.T) {
			key := followupAPIKeyFixture(service.PlatformGrok)
			resolver := &followupResolverStub{groupID: 20}
			wantStatus, wantCalls := http.StatusNotFound, 1
			setup := func(*gin.Context) {}
			switch mode {
			case "hidden":
				key.User.RestrictPublicGroups = true
				key.User.AllowedGroups = []int64{10}
			case "exclusive-revoked":
				key.Groups[1].IsExclusive = true
			case "disabled":
				key.Groups[1].Status = service.StatusDisabled
			case "deleted":
				key.Groups[1].Status = "deleted"
			case "unbound":
				resolver.groupID = 999
			case "wrong-platform":
				key.Groups[1].Platform = service.PlatformOpenAI
			case "forced-other":
				setup = func(c *gin.Context) { c.Set(string(middleware.ContextKeyForcePlatform), service.PlatformAntigravity) }
				wantCalls = 0
			case "missing-owner":
				resolver.err = service.ErrAPIKeyFollowupNotFound
			case "cache-error":
				resolver.err = errors.New("private cache address with secret")
				wantStatus = http.StatusServiceUnavailable
			case "zero-owner":
				resolver.groupID = 0
			case "wrong-auth-user":
				setup = func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 8}) }
				wantStatus, wantCalls = http.StatusUnauthorized, 0
			case "wrong-key-user":
				key.User.ID = 8
				wantStatus, wantCalls = http.StatusUnauthorized, 0
			case "too-many-groups":
				for len(key.Groups) <= service.MaxAPIKeyGroups {
					key.Groups = append(key.Groups, key.Group)
				}
				wantCalls = 0
			}
			primary := key.Group
			w := serveFollowupRequest(t, http.MethodGet, "/v1/videos/:request_id", key, resolver, false, setup, func(*gin.Context) { t.Fatal("rejected request reached handler") })
			require.Equal(t, wantStatus, w.Code, w.Body.String())
			require.Equal(t, wantCalls, resolver.calls)
			require.NotContains(t, w.Body.String(), "secret")
			require.NotContains(t, w.Body.String(), "999")
			require.Same(t, primary, key.Group)
		})
	}
}

func TestRestoreAPIKeyGroupForFollowupSubscriptions(t *testing.T) {
	for _, mode := range []string{"active", "missing", "primary-only", "expired", "suspended", "wrong-user", "wrong-group", "deleted", "simple"} {
		t.Run(mode, func(t *testing.T) {
			key := followupAPIKeyFixture(service.PlatformOpenAI)
			key.Groups[1].SubscriptionType = service.SubscriptionTypeSubscription
			sub := &service.UserSubscription{ID: 71, UserID: 7, GroupID: 20, Status: service.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)}
			subs := map[int64]*service.UserSubscription{20: sub}
			status := http.StatusForbidden
			switch mode {
			case "active":
				status = http.StatusNoContent
			case "missing", "primary-only", "simple":
				subs = nil
				if mode == "simple" {
					status = http.StatusNoContent
				}
			case "expired":
				sub.ExpiresAt = time.Now().Add(-time.Minute)
			case "suspended":
				sub.Status = service.SubscriptionStatusSuspended
			case "wrong-user":
				sub.UserID++
			case "wrong-group":
				sub.GroupID++
			case "deleted":
				now := time.Now()
				sub.DeletedAt = &now
			}
			before := *sub
			w := serveFollowupRequest(t, http.MethodGet, "/v1/live/:call_id", key, &followupResolverStub{groupID: 20}, mode == "simple", func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyGroupSubscriptions), subs)
				if mode == "primary-only" {
					c.Set(string(middleware.ContextKeySubscription), &service.UserSubscription{ID: 72, UserID: 7, GroupID: 10, Status: service.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)})
				}
			}, func(c *gin.Context) {
				require.Equal(t, http.StatusNoContent, status)
				selectedSub, _ := middleware.GetSubscriptionFromContext(c)
				if mode == "active" {
					require.Same(t, sub, selectedSub)
				} else {
					require.Nil(t, selectedSub)
				}
			})
			require.Equal(t, status, w.Code, w.Body.String())
			require.Equal(t, before, *sub)
		})
	}
}

func TestRestoreAPIKeyGroupForFollowupForcePlatform(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformGrok} {
		for _, composite := range []bool{false, true} {
			key := followupAPIKeyFixture(platform)
			if composite {
				key.Groups[1].Platform = service.PlatformComposite
			}
			route := "/v1/videos/:request_id"
			if platform == service.PlatformOpenAI {
				route = "/backend-api/codex/:call_id"
			}
			w := serveFollowupRequest(t, http.MethodGet, route, key, &followupResolverStub{groupID: 20}, false, func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyForcePlatform), platform)
				c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ForcePlatform, platform))
			}, func(c *gin.Context) {
				forced, ok := middleware.GetForcePlatformFromContext(c)
				require.True(t, ok)
				require.Equal(t, platform, forced)
				require.Equal(t, platform, c.Request.Context().Value(ctxkey.ForcePlatform))
			})
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		}
	}
}

func TestRestoreAPIKeyGroupForFollowupLeavesOtherRequestsAlone(t *testing.T) {
	for _, route := range []string{"/v1/models", "/v1/responses", "/backend-api/codex/models", "/unrelated/:request_id"} {
		key := followupAPIKeyFixture(service.PlatformGrok)
		w := serveFollowupRequest(t, http.MethodGet, route, key, nil, false, nil, func(c *gin.Context) {
			selected, _ := middleware.GetAPIKeyFromContext(c)
			require.Same(t, key, selected)
		})
		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	}
	key := followupAPIKeyFixture(service.PlatformGrok)
	w := serveFollowupRequest(t, http.MethodPost, "/v1/videos/:request_id", key, nil, false, nil, nil)
	require.Equal(t, http.StatusNoContent, w.Code)
	key.Groups = key.Groups[:1]
	w = serveFollowupRequest(t, http.MethodGet, "/v1/videos/:request_id", key, nil, false, nil, nil)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestRestoreAPIKeyGroupForFollowupMissingService(t *testing.T) {
	for _, resolver := range []apiKeyFollowupGroupResolver{nil, (*service.OpenAIGatewayService)(nil)} {
		key := followupAPIKeyFixture(service.PlatformGrok)
		w := serveFollowupRequest(t, http.MethodGet, "/v1/videos/:request_id", key, resolver, false, nil, func(*gin.Context) { t.Fatal("missing service reached handler") })
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
	}
	// Check the production wrapper also fails closed with an unwired service.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &GatewayHandler{cfg: &config.Config{RunMode: config.RunModeSimple}}
	key := followupAPIKeyFixture(service.PlatformOpenAI)
	r.GET("/v1/live/:call_id", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.UserID})
		require.False(t, h.restoreAPIKeyGroupForFollowup(c, key))
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/live/call-test", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}
