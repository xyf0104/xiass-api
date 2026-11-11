package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type apiKeyGroupRoutingCatalogStub struct {
	models map[int64][]string
	err    map[int64]error
}

func (s apiKeyGroupRoutingCatalogStub) ModelsForAPIKeyGroup(_ context.Context, group *service.Group) ([]string, error) {
	if group == nil {
		return nil, nil
	}
	if err := s.err[group.ID]; err != nil {
		return nil, err
	}
	return append([]string(nil), s.models[group.ID]...), nil
}

func newAPIKeyGroupRoutingContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	return c
}

func hydratedRoutingGroup(id int64, name string) *service.Group {
	return &service.Group{ID: id, Name: name, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true}
}

func TestSelectAPIKeyGroupForModelUsesExactModelCatalogMatch(t *testing.T) {
	chat := hydratedRoutingGroup(10, "chat")
	image := hydratedRoutingGroup(20, "image")
	primaryOverride := 7
	apiKey := &service.APIKey{
		Group:    chat,
		GroupID:  &chat.ID,
		Groups:   []*service.Group{chat, image},
		GroupIDs: []int64{chat.ID, image.ID},
		User:     &service.User{UserGroupRPMOverride: &primaryOverride},
	}
	c := newAPIKeyGroupRoutingContext()

	ok := selectAPIKeyGroupForModel(context.Background(), c, apiKey, "gpt-image-2", apiKeyGroupRoutingCatalogStub{
		models: map[int64][]string{chat.ID: {"gpt-5.6"}, image.ID: {"gpt-image-2"}},
	}, false)

	require.True(t, ok)
	require.Same(t, image, apiKey.Group)
	require.Equal(t, image.ID, *apiKey.GroupID)
	require.Nil(t, apiKey.User.UserGroupRPMOverride)
}

func TestSelectAPIKeyGroupForModelPrefersImageCapableGroupForExplicitImageIntent(t *testing.T) {
	chat := hydratedRoutingGroup(10, "chat")
	image := hydratedRoutingGroup(20, "image")
	image.AllowImageGeneration = true
	apiKey := &service.APIKey{Group: chat, GroupID: &chat.ID, Groups: []*service.Group{chat, image}, GroupIDs: []int64{chat.ID, image.ID}}
	c := newAPIKeyGroupRoutingContext()

	ok := selectAPIKeyGroupForModel(context.Background(), c, apiKey, "gpt-5.6", apiKeyGroupRoutingCatalogStub{
		models: map[int64][]string{chat.ID: {"gpt-5.6"}, image.ID: nil},
	}, true)

	require.True(t, ok)
	require.Same(t, chat, apiKey.Group)

	apiKey.Group = chat
	apiKey.GroupID = &chat.ID
	c = newAPIKeyGroupRoutingContext()
	ok = selectAPIKeyGroupForModel(context.Background(), c, apiKey, "gpt-image-2", apiKeyGroupRoutingCatalogStub{
		models: map[int64][]string{chat.ID: nil, image.ID: nil},
	}, true)
	require.True(t, ok)
	require.Same(t, image, apiKey.Group)
}

func TestSelectAPIKeyGroupForModelUnknownModelDoesNotFallbackToPrimaryAdapter(t *testing.T) {
	primary := hydratedRoutingGroup(10, "primary")
	secondary := hydratedRoutingGroup(20, "secondary")
	apiKey := &service.APIKey{Group: primary, GroupID: &primary.ID, Groups: []*service.Group{primary, secondary}, GroupIDs: []int64{primary.ID, secondary.ID}}
	c := newAPIKeyGroupRoutingContext()

	ok := selectAPIKeyGroupForModel(context.Background(), c, apiKey, "unknown-model", apiKeyGroupRoutingCatalogStub{
		models: map[int64][]string{primary.ID: {"gpt-5.6"}, secondary.ID: {"claude-opus"}},
	}, false)

	require.False(t, ok)
	require.Same(t, primary, apiKey.Group)
	require.Equal(t, primary.ID, *apiKey.GroupID)
}

func multiPlatformRoutingFixture() (*GatewayHandler, *service.APIKey) {
	platforms := []string{service.PlatformOpenAI, service.PlatformAnthropic, service.PlatformGrok, service.PlatformAntigravity, service.PlatformGemini}
	models := []string{"gpt-test", "claude-test", "grok-test", "gemini-3.7-flash-high", "gemini-native"}
	repo := &gatewayModelsAccountRepoStub{byGroup: make(map[int64][]service.Account)}
	key := &service.APIKey{ID: 127, User: &service.User{ID: 1}}
	for i, platform := range platforms {
		id := int64(i + 1)
		group := hydratedRoutingGroup(id, platform)
		group.Platform = platform
		group.RateMultiplier = float64(i+1) / 5
		group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{models[i]}}
		key.Groups = append(key.Groups, group)
		key.GroupIDs = append(key.GroupIDs, id)
		repo.byGroup[id] = []service.Account{{ID: id, Platform: platform, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{models[i]: models[i]}}}}
	}
	key.Group, key.GroupID = key.Groups[0], &key.Groups[0].ID
	h := newGatewayModelsHandlerForTest(repo)
	h.cfg = &config.Config{RunMode: config.RunModeSimple}
	return h, key
}

func TestAPIKeyGroupRoutingBeforePlatformDispatch(t *testing.T) {
	for _, endpoint := range []string{"/v1/messages", "/v1/messages/count_tokens", "/messages/count_tokens", "/v1/responses", "/responses", "/backend-api/codex/responses", "/v1/responses/compact", "/v1/responses/input_tokens", "/v1/chat/completions", "/chat/completions"} {
		for _, model := range []string{"gpt-test", "claude-test", "grok-test", "gemini-3.7-flash-high", "gemini-native"} {
			t.Run(endpoint+"/"+model, func(t *testing.T) {
				h, key := multiPlatformRoutingFixture()
				originalGroup := key.Group
				body := `{"model":"` + model + `","previous_response_id":"resp_keep","metadata":{"user_id":"session_keep"}}`
				r := gin.New()
				r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), key); c.Next() })
				r.Use(h.APIKeyGroupRouting(), middleware.GroupModelAllowlist())
				r.POST(endpoint, func(c *gin.Context) {
					selected, _ := middleware.GetAPIKeyFromContext(c)
					require.NotSame(t, key, selected)
					require.True(t, selected.Group.ModelAllowlist.Allows(model))
					require.Equal(t, selected.Group.ID, *selected.GroupID)
					require.Same(t, selected.Group, c.Request.Context().Value(ctxkey.Group))
					require.Equal(t, float64(selected.Group.ID)/5, selected.Group.RateMultiplier)
					got, err := io.ReadAll(c.Request.Body)
					require.NoError(t, err)
					require.Equal(t, body, string(got), "session/response bindings must survive selection")
					// Handlers must not select again after a composite alias rewrite.
					require.True(t, selectAPIKeyGroupForModel(c.Request.Context(), c, selected, "gpt-test", h.gatewayService, false))
					require.True(t, selected.Group.ModelAllowlist.Allows(model))
					c.Status(http.StatusNoContent)
				})
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(body)))
				require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
				require.Same(t, originalGroup, key.Group)
				require.Equal(t, []int64{1, 2, 3, 4, 5}, key.GroupIDs)
			})
		}
	}
}

func TestAPIKeyGroupRoutingHonorsVisibilityAllowlistAndPlatform(t *testing.T) {
	for _, mode := range []string{"hidden", "disabled", "allowlist", "unknown", "forced-platform", "conflicting-models"} {
		t.Run(mode, func(t *testing.T) {
			h, key := multiPlatformRoutingFixture()
			body := `{"model":"claude-test"}`
			status := http.StatusNotFound
			switch mode {
			case "hidden":
				key.User.RestrictPublicGroups = true
				key.User.AllowedGroups = []int64{1}
			case "disabled":
				key.Groups[1].Status = service.StatusDisabled
			case "allowlist":
				key.Groups[1].ModelAllowlist.Models = []string{"different"}
			case "unknown":
				body = `{"model":"unknown-alias"}`
			case "conflicting-models":
				body = `{"model":"gpt-test","Model":"claude-test"}`
				status = http.StatusBadRequest
			}
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), key); c.Next() })
			if mode == "forced-platform" {
				r.Use(middleware.ForcePlatform(service.PlatformAntigravity))
			}
			r.POST("/v1/messages", h.APIKeyGroupRouting(), func(c *gin.Context) { t.Fatal("request must not reach an adapter") })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body)))
			require.Equal(t, status, w.Code, w.Body.String())
		})
	}
}

func TestAPIKeyGroupRoutingSelectedSubscriptionAndRPM(t *testing.T) {
	for _, kind := range []string{"selected-subscription", "missing-subscription", "standard"} {
		t.Run(kind, func(t *testing.T) {
			h, key := multiPlatformRoutingFixture()
			h.cfg.RunMode = "standard"
			primaryOverride := 77
			key.User.UserGroupRPMOverride = &primaryOverride
			key.Group.SubscriptionType = service.SubscriptionTypeSubscription
			primarySub := &service.UserSubscription{ID: 101, GroupID: 1}
			targetSub := &service.UserSubscription{ID: 102, GroupID: 2}
			if kind != "standard" {
				key.Groups[1].SubscriptionType = service.SubscriptionTypeSubscription
			}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeySubscription), primarySub)
				subs := map[int64]*service.UserSubscription{1: primarySub}
				if kind == "selected-subscription" {
					subs[2] = targetSub
				}
				c.Set(string(middleware.ContextKeyGroupSubscriptions), subs)
				c.Next()
			})
			r.POST("/v1/messages", h.APIKeyGroupRouting(), func(c *gin.Context) {
				require.NotEqual(t, "missing-subscription", kind)
				selected, _ := middleware.GetAPIKeyFromContext(c)
				require.Nil(t, selected.User.UserGroupRPMOverride)
				sub, _ := middleware.GetSubscriptionFromContext(c)
				if kind == "standard" {
					require.Nil(t, sub)
				} else {
					require.Same(t, targetSub, sub)
				}
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-test"}`)))
			if kind == "missing-subscription" {
				require.Equal(t, http.StatusForbidden, w.Code)
			} else {
				require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
			}
			require.Equal(t, &primaryOverride, key.User.UserGroupRPMOverride)
		})
	}
}

func TestAPIKeyGroupRoutingPreservesModelFreeAndMultipartBodies(t *testing.T) {
	var multipartBody bytes.Buffer
	form := multipart.NewWriter(&multipartBody)
	require.NoError(t, form.WriteField("model", "grok-test"))
	file, err := form.CreateFormFile("image", "fixture.png")
	require.NoError(t, err)
	_, err = file.Write([]byte("binary\"\x00\r\nimage\""))
	require.NoError(t, err)
	require.NoError(t, form.Close())
	for _, tc := range []struct {
		path, contentType, body string
		groupID                 int64
	}{
		{"/v1/images/edits", form.FormDataContentType(), multipartBody.String(), 3},
		{"/v1/responses/compact", "application/json", `{"input":[]}`, 1},
		{"/v1/audio/transcriptions", "audio/wav", "binary\x00audio", 1},
	} {
		t.Run(tc.path, func(t *testing.T) {
			h, key := multiPlatformRoutingFixture()
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), key); c.Next() })
			r.POST(tc.path, h.APIKeyGroupRouting(), func(c *gin.Context) {
				selected, _ := middleware.GetAPIKeyFromContext(c)
				require.Equal(t, tc.groupID, *selected.GroupID)
				got, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Equal(t, tc.body, string(got))
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		})
	}
}

func TestAPIKeyGroupRoutingWebSocketDefersSelectionToFirstFrame(t *testing.T) {
	h, key := multiPlatformRoutingFixture()
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), key); c.Next() })
	r.GET("/v1/responses", h.APIKeyGroupRouting(), func(c *gin.Context) {
		selected, _ := middleware.GetAPIKeyFromContext(c)
		require.Equal(t, int64(1), *selected.GroupID)
		require.True(t, selectAPIKeyGroupForModel(c.Request.Context(), c, selected, "grok-test", h.gatewayService, false))
		require.Equal(t, int64(3), *selected.GroupID)
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/responses?model=claude-test", nil))
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestAPIKeyGroupRoutingGeminiNativeURL(t *testing.T) {
	for _, model := range []string{"gemini-native", "gemini-3.7-flash-high"} {
		t.Run(model, func(t *testing.T) {
			h, key := multiPlatformRoutingFixture()
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), key); c.Next() })
			r.POST("/v1beta/models/*modelAction", h.APIKeyGroupRouting(), func(c *gin.Context) {
				selected, _ := middleware.GetAPIKeyFromContext(c)
				require.True(t, geminiNativeAPIKeyPlatformAllowed(c, selected))
				require.True(t, selected.Group.ModelAllowlist.Allows(model))
				body, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Equal(t, `{"contents":[]}`, string(body))
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1beta/models/"+model+":generateContent", strings.NewReader(`{"contents":[]}`)))
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		})
	}
}

func TestAPIKeyGroupRoutingGeminiNativeSkipsIncompatibleSameName(t *testing.T) {
	for _, targetPlatform := range []string{service.PlatformGemini, service.PlatformAntigravity} {
		for _, compatible := range []bool{true, false} {
			for _, method := range []string{http.MethodPost, http.MethodGet} {
				t.Run(targetPlatform+"/"+method+map[bool]string{true: "/available", false: "/unavailable"}[compatible], func(t *testing.T) {
					model := "gemini-3.7-flash-high"
					primary := hydratedRoutingGroup(1, "openai mapping")
					target := hydratedRoutingGroup(2, targetPlatform)
					target.Platform = targetPlatform
					key := &service.APIKey{Group: primary, GroupID: &primary.ID, Groups: []*service.Group{primary, target}}
					accounts := map[int64][]service.Account{1: {{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{model: "upstream-alias"}}}}}
					if compatible {
						accounts[2] = []service.Account{{ID: 2, Platform: targetPlatform, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{model: model}}}}
					}
					h := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{byGroup: accounts})
					r := gin.New()
					r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), key); c.Next() })
					path, url := "/v1beta/models/:model", "/v1beta/models/"+model
					if method == http.MethodPost {
						path, url = "/v1beta/models/*modelAction", url+":generateContent"
					}
					r.Handle(method, path, h.APIKeyGroupRouting(), func(c *gin.Context) {
						require.True(t, compatible)
						selected, _ := middleware.GetAPIKeyFromContext(c)
						require.Equal(t, target.ID, *selected.GroupID)
						require.True(t, geminiNativeAPIKeyPlatformAllowed(c, selected))
						c.Status(http.StatusNoContent)
					})
					w := httptest.NewRecorder()
					r.ServeHTTP(w, httptest.NewRequest(method, url, strings.NewReader(`{"contents":[]}`)))
					expected := http.StatusNotFound
					if compatible {
						expected = http.StatusNoContent
					}
					require.Equal(t, expected, w.Code, w.Body.String())
				})
			}
		}
	}
}

func TestAPIKeyGroupRoutingCatalogUnionAndVisibility(t *testing.T) {
	for _, restrict := range []bool{false, true} {
		h, key := multiPlatformRoutingFixture()
		key.User.RestrictPublicGroups = restrict
		key.User.AllowedGroups = []int64{2}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		c.Set(string(middleware.ContextKeyAPIKey), key)
		h.Models(c)
		var response gatewayModelsResponseForTest
		require.Equal(t, http.StatusOK, recorder.Code)
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		ids := make([]string, 0, len(response.Data))
		for _, model := range response.Data {
			ids = append(ids, model.ID)
		}
		if restrict {
			require.Equal(t, []string{"claude-test"}, ids)
		} else {
			require.ElementsMatch(t, []string{"gpt-test", "claude-test", "grok-test", "gemini-3.7-flash-high", "gemini-native"}, ids)
		}
	}
}

func TestAPIKeyGroupRoutingWebSocketRejectsNonCompatibleFirstFrame(t *testing.T) {
	for _, model := range []string{"claude-test", "gemini-3.7-flash-high", "unknown-model"} {
		t.Run(model, func(t *testing.T) {
			_, key := multiPlatformRoutingFixture()
			originalGroup := key.Group
			originalGroupID := *key.GroupID
			repo := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{}}
			for _, group := range key.Groups {
				name := group.ModelAllowlist.Models[0]
				repo.byGroup[group.ID] = []service.Account{{ID: group.ID, Platform: group.Platform, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{name: name}}}}
			}
			h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
			h.cfg = &config.Config{RunMode: config.RunModeSimple}
			h.gatewayService = service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, h.cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1, Concurrency: 1})
				c.Next()
			})
			r.GET("/v1/responses", h.ResponsesWebSocket)
			server := httptest.NewServer(r)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", nil)
			require.NoError(t, err)
			defer conn.CloseNow()
			require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"`+model+`","input":[]}`)))
			_, _, err = conn.Read(ctx)
			require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(err))
			require.Same(t, originalGroup, key.Group, "first-frame selection must not mutate the auth key")
			require.Equal(t, originalGroupID, *key.GroupID)
			if model == "unknown-model" {
				require.Contains(t, err.Error(), "not available")
			} else {
				require.Contains(t, err.Error(), "only supports OpenAI-compatible")
			}
		})
	}
}

func TestAPIKeyGroupRoutingExplicitPassthroughKeepsUnknownOpenAIModel(t *testing.T) {
	for _, tc := range []struct {
		model   string
		enabled bool
		status  int
	}{
		{"gpt-future-not-in-catalog", true, http.StatusNoContent},
		{"gpt-future-not-in-catalog", false, http.StatusNotFound},
		{"claude-future-not-in-catalog", true, http.StatusNotFound},
		{"gemini-future-not-in-catalog", true, http.StatusNotFound},
		{"unknown-alias", true, http.StatusNotFound},
	} {
		t.Run(tc.model+"/"+http.StatusText(tc.status), func(t *testing.T) {
			primary := hydratedRoutingGroup(1, "claude")
			primary.Platform = service.PlatformAnthropic
			target := hydratedRoutingGroup(2, "openai")
			key := &service.APIKey{Group: primary, GroupID: &primary.ID, Groups: []*service.Group{primary, target}}
			h := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{2: {{ID: 2, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Extra: map[string]any{"openai_passthrough": tc.enabled}}}}})
			body := `{"model":"` + tc.model + `","input":"keep"}`
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyAPIKey), key); c.Next() })
			r.POST("/v1/responses", h.APIKeyGroupRouting(), func(c *gin.Context) {
				selected, _ := middleware.GetAPIKeyFromContext(c)
				require.Equal(t, int64(2), *selected.GroupID)
				got, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Equal(t, body, string(got), "passthrough must not replace unknown models")
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
			require.Equal(t, tc.status, w.Code, w.Body.String())
		})
	}
}

func TestUnionAPIKeyGroupModelsDeduplicatesCaseInsensitiveIDs(t *testing.T) {
	first := hydratedRoutingGroup(10, "first")
	second := hydratedRoutingGroup(20, "second")
	models, groups, err := unionAPIKeyGroupModels(context.Background(), apiKeyGroupRoutingCatalogStub{
		models: map[int64][]string{first.ID: {"gpt-5.6", "GPT-Shared"}, second.ID: {"gpt-shared", "gpt-image-2"}},
	}, []*service.Group{first, second}, "", nil)

	require.NoError(t, err)
	require.Len(t, groups, 2)
	require.Equal(t, []string{"gpt-5.6", "GPT-Shared", "gpt-image-2"}, models)
}
