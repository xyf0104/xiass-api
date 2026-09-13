//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIImagesResponsesDriverAndImage25Models(t *testing.T) {
	for _, override := range []string{"", "  ", " gpt-5.6-sol "} {
		t.Run(fmt.Sprintf("override=%q", override), func(t *testing.T) {
			t.Setenv("XIASS_OPENAI_IMAGES_MAIN_MODEL", override)
			driver := strings.TrimSpace(override)
			if driver == "" {
				driver = "gpt-5.6-luna"
			}

			for _, model := range []string{
				"gpt-image-2",
				"gpt-image-2.5-flare",
				"gpt-image-2.5-sunburst",
				"gpt-image-2.5-flare-2026-09-08",
			} {
				parsed := &OpenAIImagesRequest{
					Endpoint: openAIImagesGenerationsEndpoint,
					Model:    model,
					Prompt:   "draw a red cup",
					Quality:  "xhigh",
					Size:     "1536x864",
					N:        1,
				}
				body, err := buildOpenAIImagesResponsesRequest(parsed, model)
				require.NoError(t, err)
				require.Equal(t, driver, gjson.GetBytes(body, "model").String())
				require.Equal(t, model, gjson.GetBytes(body, "tools.0.model").String())

				req := map[string]any{"model": model, "input": "draw a red cup"}
				require.True(t, normalizeOpenAIResponsesImageOnlyModel(req))
				require.Equal(t, driver, req["model"])
				require.Equal(t, model, req["tools"].([]any)[0].(map[string]any)["model"])
			}
		})
	}
}

func TestOpenAIImagesRejectedDriverDoesNotCoolImageModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("XIASS_OPENAI_IMAGES_MAIN_MODEL", "gpt-5.4-mini")

	for _, rejected := range []string{"gpt-5.4-mini", "gpt-image-2.5-flare"} {
		t.Run(rejected, func(t *testing.T) {
			repo := &modelNotFoundAccountRepoStub{}
			svc := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, openAIImagesGenerationsEndpoint, nil)
			body := fmt.Sprintf(`{"error":{"message":"The '%s' model is not supported when using Codex with a ChatGPT account.","type":"invalid_request_error"}}`, rejected)
			resp := &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}

			_, err := svc.handleOpenAIImagesErrorResponse(
				WithOpenAIImagesEndpoint(context.Background()),
				resp,
				c,
				openAICodexPlanGatedOAuthAccount(),
				"gpt-image-2.5-flare",
			)

			require.Error(t, err)
			if rejected == "gpt-5.4-mini" {
				require.Empty(t, repo.modelRateLimitCalls)
				require.Zero(t, repo.tempCalls)
				return
			}
			require.Len(t, repo.modelRateLimitCalls, 1)
		})
	}
}

func TestGPTImage25PricingDoesNotUseLegacyImageRates(t *testing.T) {
	for _, model := range []string{
		"gpt-image-2.5-flare",
		"gpt-image-2.5-sunburst",
		"gpt-image-2.5-flare-2026-09-08",
		"gpt-image-2.5-sunburst-2026-09-08",
	} {
		svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
			"gpt-image-2": {InputCostPerToken: 2.5e-6, OutputCostPerImageToken: 15e-6},
		}}
		pricing := svc.GetModelPricing(model)
		require.NotNil(t, pricing)
		require.Equal(t, 5e-6, pricing.InputCostPerToken)
		require.Equal(t, 8e-6, pricing.InputCostPerImageToken)
		require.Equal(t, 30e-6, pricing.OutputCostPerImageToken)
		require.Equal(t, 1.25e-6, pricing.CacheReadInputTokenCost)
		require.Zero(t, pricing.OutputCostPerToken)
	}
}

func TestGPTImage25UsagePreservesImageInputTokens(t *testing.T) {
	svc := &OpenAIGatewayService{}
	var usage OpenAIUsage
	svc.parseOpenAIImagesSSEUsageBytes([]byte(`{"type":"response.completed","response":{"tool_usage":{"image_gen":{"input_tokens":1550,"input_tokens_details":{"image_tokens":1521,"text_tokens":29},"output_tokens":515,"output_tokens_details":{"image_tokens":515}}}}}`), &usage)
	require.Equal(t, 1550, usage.InputTokens)
	require.Equal(t, 1521, usage.ImageInputTokens)
	require.Equal(t, 515, usage.ImageOutputTokens)
}
