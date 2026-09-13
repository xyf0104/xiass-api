package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResolveAntigravityWrappedModelProfile_Gemini37PublicTiers(t *testing.T) {
	tests := []struct {
		name       string
		requested  string
		mapped     string
		wantBudget *int
	}{
		{name: "high", requested: "gemini-3.7-flash-high", mapped: domain.AntigravityGemini37FlashTieredModel, wantBudget: antigravityIntPtr(-1)},
		{name: "medium", requested: "gemini-3.7-flash-medium", mapped: domain.AntigravityGemini37FlashTieredModel, wantBudget: antigravityIntPtr(4000)},
		{name: "low", requested: "gemini-3.7-flash-low", mapped: domain.AntigravityGemini37FlashTieredModel, wantBudget: antigravityIntPtr(1000)},
		{name: "base compatibility alias", requested: "gemini-3.7-flash", mapped: domain.AntigravityGemini37FlashTieredModel, wantBudget: antigravityIntPtr(4000)},
		{name: "raw tiered compatibility", requested: domain.AntigravityGemini37FlashTieredModel, mapped: domain.AntigravityGemini37FlashTieredModel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := resolveAntigravityWrappedModelProfile(tt.requested, tt.mapped)

			require.Equal(t, domain.AntigravityGemini37FlashTieredModel, profile.upstreamModel)
			if tt.wantBudget == nil {
				require.Nil(t, profile.thinkingBudget)
			} else {
				require.NotNil(t, profile.thinkingBudget)
				require.Equal(t, *tt.wantBudget, *profile.thinkingBudget)
			}
		})
	}
}

func TestResolveAntigravityWrappedModelProfile_Gemini38PublicTiers(t *testing.T) {
	tests := []struct {
		requested  string
		wantBudget *int
	}{
		{requested: "gemini-3.8-flash-high", wantBudget: antigravityIntPtr(-1)},
		{requested: "gemini-3.8-flash-medium", wantBudget: antigravityIntPtr(4000)},
		{requested: "gemini-3.8-flash-low", wantBudget: antigravityIntPtr(1000)},
		{requested: "gemini-3.8-flash", wantBudget: antigravityIntPtr(4000)},
		{requested: domain.AntigravityGemini38FlashTieredModel},
	}

	for _, tt := range tests {
		t.Run(tt.requested, func(t *testing.T) {
			profile := resolveAntigravityWrappedModelProfile(tt.requested, domain.AntigravityGemini38FlashTieredModel)
			require.Equal(t, domain.AntigravityGemini38FlashTieredModel, profile.upstreamModel)
			if tt.wantBudget == nil {
				require.Nil(t, profile.thinkingBudget)
				return
			}
			require.NotNil(t, profile.thinkingBudget)
			require.Equal(t, *tt.wantBudget, *profile.thinkingBudget)
		})
	}
}

func TestPublicAntigravityModelIDs_HidesGemini37InternalRoutes(t *testing.T) {
	models := publicAntigravityModelIDs([]string{
		"gemini-2.5-pro",
		"gemini-3.7-flash",
		domain.AntigravityGemini37FlashTieredModel,
		"gemini-3.7-flash-high",
	})

	require.Equal(t, []string{
		"gemini-2.5-pro",
		"gemini-3.7-flash-high",
		"gemini-3.7-flash-low",
		"gemini-3.7-flash-medium",
	}, models)
	require.NotContains(t, models, "gemini-3.7-flash")
	require.NotContains(t, models, domain.AntigravityGemini37FlashTieredModel)
}

func TestPublicAntigravityModelIDs_HidesGemini38InternalRoutes(t *testing.T) {
	models := publicAntigravityModelIDs([]string{
		"gemini-2.5-pro",
		"gemini-3.8-flash",
		domain.AntigravityGemini38FlashTieredModel,
		"gemini-3.8-flash-high",
	})

	require.Equal(t, []string{
		"gemini-2.5-pro",
		"gemini-3.8-flash-high",
		"gemini-3.8-flash-low",
		"gemini-3.8-flash-medium",
	}, models)
	require.NotContains(t, models, "gemini-3.8-flash")
	require.NotContains(t, models, domain.AntigravityGemini38FlashTieredModel)
}

func TestAntigravityGateway_Gemini38ResponsesUsesPublicIdentityAndTieredRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{antigravityCompatSuccessResponse()}}
	svc := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, upstream)
	body := []byte(`{"model":"gemini-3.8-flash-medium","input":"ok","stream":true}`)
	c := newAntigravityTierContext("/v1/responses", body)

	result, err := svc.ForwardAsResponses(context.Background(), c, newAntigravityTierAccount(), body, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gemini-3.8-flash-medium", result.Model)
	require.Equal(t, domain.AntigravityGemini38FlashTieredModel, result.UpstreamModel)
	require.Len(t, upstream.requestBodies, 1)
	written := upstream.requestBodies[0]
	require.Equal(t, domain.AntigravityGemini38FlashTieredModel, gjson.GetBytes(written, "model").String())
	require.Equal(t, int64(4000), gjson.GetBytes(written, "request.generationConfig.thinkingConfig.thinkingBudget").Int())
}

func TestApplyAntigravityWrappedModelProfile_Gemini37PreservesUnknownFields(t *testing.T) {
	profile := resolveAntigravityWrappedModelProfile(
		"gemini-3.7-flash-medium",
		domain.AntigravityGemini37FlashTieredModel,
	)
	body := []byte(`{
		"model":"gemini-3.7-flash-tiered",
		"project":"project-37",
		"unknownRoot":{"keep":true},
		"request":{
			"unknownRequest":"keep-me",
			"generationConfig":{"temperature":0.25}
		}
	}`)

	got, err := applyAntigravityWrappedModelProfile(body, profile)

	require.NoError(t, err)
	require.Equal(t, domain.AntigravityGemini37FlashTieredModel, gjson.GetBytes(got, "model").String())
	require.Equal(t, int64(4000), gjson.GetBytes(got, "request.generationConfig.thinkingConfig.thinkingBudget").Int())
	require.InDelta(t, 0.25, gjson.GetBytes(got, "request.generationConfig.temperature").Float(), 1e-12)
	require.True(t, gjson.GetBytes(got, "unknownRoot.keep").Bool())
	require.Equal(t, "keep-me", gjson.GetBytes(got, "request.unknownRequest").String())
}

func TestApplyAntigravityWrappedModelProfile_Gemini37OverridesPublicTierBudgetOnly(t *testing.T) {
	tests := []struct {
		model      string
		wantBudget int64
	}{
		{model: "gemini-3.7-flash-high", wantBudget: -1},
		{model: "gemini-3.7-flash-medium", wantBudget: 4000},
		{model: "gemini-3.7-flash-low", wantBudget: 1000},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			profile := resolveAntigravityWrappedModelProfile(tt.model, domain.AntigravityGemini37FlashTieredModel)
			body := []byte(`{
				"model":"gemini-3.7-flash-tiered",
				"request":{"generationConfig":{"thinkingConfig":{"thinkingBudget":2468,"includeThoughts":false,"unknown":"keep"}}}
			}`)

			got, err := applyAntigravityWrappedModelProfile(body, profile)

			require.NoError(t, err)
			require.Equal(t, tt.wantBudget, gjson.GetBytes(got, "request.generationConfig.thinkingConfig.thinkingBudget").Int())
			require.False(t, gjson.GetBytes(got, "request.generationConfig.thinkingConfig.includeThoughts").Bool())
			require.Equal(t, "keep", gjson.GetBytes(got, "request.generationConfig.thinkingConfig.unknown").String())
		})
	}
}

func TestApplyAntigravityWrappedModelProfile_RawTieredPreservesClientBudget(t *testing.T) {
	profile := resolveAntigravityWrappedModelProfile(
		domain.AntigravityGemini37FlashTieredModel,
		domain.AntigravityGemini37FlashTieredModel,
	)
	body := []byte(`{
		"model":"gemini-3.7-flash-tiered",
		"request":{"generationConfig":{"thinkingConfig":{"thinkingBudget":2468,"includeThoughts":false}}}
	}`)

	got, err := applyAntigravityWrappedModelProfile(body, profile)

	require.NoError(t, err)
	require.Equal(t, int64(2468), gjson.GetBytes(got, "request.generationConfig.thinkingConfig.thinkingBudget").Int())
	require.False(t, gjson.GetBytes(got, "request.generationConfig.thinkingConfig.includeThoughts").Bool())
}

func TestApplyAntigravityWrappedModelProfile_DoesNotUndoWebSearchFallback(t *testing.T) {
	profile := resolveAntigravityWrappedModelProfile(
		"gemini-3.7-flash-high",
		domain.AntigravityGemini37FlashTieredModel,
	)
	body := []byte(`{"model":"gemini-2.5-flash","request":{"generationConfig":{"temperature":0.4}}}`)

	got, err := applyAntigravityWrappedModelProfile(body, profile)

	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestApplyAntigravityWrappedModelProfile_Sonnet46ThinkingUsesBaseModel(t *testing.T) {
	profile := resolveAntigravityWrappedModelProfile("claude-sonnet-4-6-thinking", "claude-sonnet-4-6")

	t.Run("adds missing thinking config", func(t *testing.T) {
		got, err := applyAntigravityWrappedModelProfile(
			[]byte(`{"model":"claude-sonnet-4-6","request":{"generationConfig":{"temperature":0.2}}}`),
			profile,
		)

		require.NoError(t, err)
		require.Equal(t, "claude-sonnet-4-6", gjson.GetBytes(got, "model").String())
		require.Equal(t, int64(-1), gjson.GetBytes(got, "request.generationConfig.thinkingConfig.thinkingBudget").Int())
		require.True(t, gjson.GetBytes(got, "request.generationConfig.thinkingConfig.includeThoughts").Bool())
	})

	t.Run("preserves client budget", func(t *testing.T) {
		got, err := applyAntigravityWrappedModelProfile(
			[]byte(`{"model":"claude-sonnet-4-6","request":{"generationConfig":{"thinkingConfig":{"thinkingBudget":8192}}}}`),
			profile,
		)

		require.NoError(t, err)
		require.Equal(t, int64(8192), gjson.GetBytes(got, "request.generationConfig.thinkingConfig.thinkingBudget").Int())
		require.True(t, gjson.GetBytes(got, "request.generationConfig.thinkingConfig.includeThoughts").Bool())
	})
}

func TestApplyAntigravityWrappedModelProfile_RejectsInvalidWrappedJSON(t *testing.T) {
	profile := resolveAntigravityWrappedModelProfile(
		"gemini-3.7-flash-low",
		domain.AntigravityGemini37FlashTieredModel,
	)

	got, err := applyAntigravityWrappedModelProfile([]byte(`{"model":`), profile)

	require.Error(t, err)
	require.Nil(t, got)
}

func TestAntigravityGateway_Gemini37FinalWrappedBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Claude messages uses high tier", func(t *testing.T) {
		upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{antigravityCompatSuccessResponse()}}
		svc := newAntigravityTierGatewayService(upstream)
		body := []byte(`{"model":"gemini-3.7-flash-high","messages":[{"role":"user","content":"ok"}],"max_tokens":8,"stream":true}`)
		c := newAntigravityTierContext("/v1/messages", body)

		result, err := svc.Forward(context.Background(), c, newAntigravityTierAccount(), body, false)

		require.NoError(t, err)
		assertAntigravity37Forward(t, upstream, result, "gemini-3.7-flash-high", -1)
	})

	t.Run("Gemini native uses medium tier", func(t *testing.T) {
		upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{antigravityCompatSuccessResponse()}}
		svc := newAntigravityTierGatewayService(upstream)
		body := []byte(`{"contents":[{"role":"user","parts":[{"text":"ok"}]}]}`)
		c := newAntigravityTierContext("/v1beta/models/gemini-3.7-flash-medium:streamGenerateContent", body)

		result, err := svc.ForwardGemini(
			context.Background(),
			c,
			newAntigravityTierAccount(),
			"gemini-3.7-flash-medium",
			"streamGenerateContent",
			true,
			body,
			false,
		)

		require.NoError(t, err)
		assertAntigravity37Forward(t, upstream, result, "gemini-3.7-flash-medium", 4000)
	})

	t.Run("Chat Completions uses low tier", func(t *testing.T) {
		upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{antigravityCompatSuccessResponse()}}
		svc := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, upstream)
		body := []byte(`{"model":"gemini-3.7-flash-low","messages":[{"role":"user","content":"ok"}],"stream":true}`)
		c := newAntigravityTierContext("/v1/chat/completions", body)

		result, err := svc.ForwardAsChatCompletions(context.Background(), c, newAntigravityTierAccount(), body, nil)

		require.NoError(t, err)
		assertAntigravity37Forward(t, upstream, result, "gemini-3.7-flash-low", 1000)
	})

	t.Run("Responses uses high tier", func(t *testing.T) {
		upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{antigravityCompatSuccessResponse()}}
		svc := newAntigravityCompatService(config.GatewayConfig{MaxLineSize: defaultMaxLineSize}, upstream)
		body := []byte(`{"model":"gemini-3.7-flash-high","input":"ok","stream":true}`)
		c := newAntigravityTierContext("/v1/responses", body)

		result, err := svc.ForwardAsResponses(context.Background(), c, newAntigravityTierAccount(), body, nil)

		require.NoError(t, err)
		assertAntigravity37Forward(t, upstream, result, "gemini-3.7-flash-high", -1)
	})

	t.Run("admin connection test uses low tier", func(t *testing.T) {
		upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{antigravityCompatSuccessResponse()}}
		svc := newAntigravityTierGatewayService(upstream)

		result, err := svc.TestConnection(context.Background(), newAntigravityTierAccount(), "gemini-3.7-flash-low")

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, domain.AntigravityGemini37FlashTieredModel, result.MappedModel)
		assertAntigravity37WrappedBody(t, upstream, 1000)
	})
}

func TestAntigravityGateway_Gemini37WebSearchReportsActualFallbackModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{antigravityCompatSuccessResponse()}}
	svc := newAntigravityTierGatewayService(upstream)
	body := []byte(`{
		"model":"gemini-3.7-flash-high",
		"messages":[{"role":"user","content":"search the web"}],
		"tools":[{"type":"web_search_20250305","name":"web_search"}],
		"max_tokens":8,
		"stream":true
	}`)
	c := newAntigravityTierContext("/v1/messages", body)

	result, err := svc.Forward(context.Background(), c, newAntigravityTierAccount(), body, false)

	require.NoError(t, err)
	require.Equal(t, "gemini-3.7-flash-high", result.Model)
	require.Equal(t, "gemini-2.5-flash", result.UpstreamModel)
	require.Len(t, upstream.requestBodies, 1)
	require.Equal(t, "gemini-2.5-flash", antigravityWrappedRequestModel(upstream.requestBodies[0]))
	require.False(t, gjson.GetBytes(upstream.requestBodies[0], "request.generationConfig.thinkingConfig.thinkingBudget").Exists())
}

func newAntigravityTierGatewayService(upstream HTTPUpstream) *AntigravityGatewayService {
	return &AntigravityGatewayService{
		settingService: NewSettingService(
			&antigravitySettingRepoStub{},
			&config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		),
		tokenProvider: &AntigravityTokenProvider{},
		httpUpstream:  upstream,
	}
}

func newAntigravityTierAccount() *Account {
	return &Account{
		ID:          3700,
		Name:        "gemini-37-tier-test",
		Platform:    PlatformAntigravity,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "tier-token",
			"project_id":   "tier-project",
		},
	}
}

func newAntigravityTierContext(path string, body []byte) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	return c
}

func assertAntigravity37Forward(
	t *testing.T,
	upstream *queuedHTTPUpstreamStub,
	result *ForwardResult,
	wantPublicModel string,
	wantBudget int64,
) {
	t.Helper()
	require.NotNil(t, result)
	require.Equal(t, wantPublicModel, result.Model)
	require.Equal(t, domain.AntigravityGemini37FlashTieredModel, result.UpstreamModel)
	assertAntigravity37WrappedBody(t, upstream, wantBudget)
}

func assertAntigravity37WrappedBody(t *testing.T, upstream *queuedHTTPUpstreamStub, wantBudget int64) {
	t.Helper()
	require.Len(t, upstream.requestBodies, 1)
	written := upstream.requestBodies[0]
	require.Equal(t, domain.AntigravityGemini37FlashTieredModel, gjson.GetBytes(written, "model").String())
	require.Equal(t, wantBudget, gjson.GetBytes(written, "request.generationConfig.thinkingConfig.thinkingBudget").Int())
}
