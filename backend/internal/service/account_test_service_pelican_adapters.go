package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
)

type pelicanContextKey struct{}

func pelicanAccountSupported(a *Account) bool {
	switch a.Platform {
	case PlatformOpenAI, PlatformGrok:
		return a.Type == AccountTypeOAuth || a.Type == AccountTypeAPIKey
	case PlatformAnthropic:
		return a.IsOAuth() || a.Type == AccountTypeAPIKey || a.IsBedrock() || a.Type == AccountTypeServiceAccount
	case PlatformGemini:
		return a.Type == AccountTypeOAuth || a.Type == AccountTypeAPIKey || a.Type == AccountTypeServiceAccount
	case PlatformAntigravity:
		return a.Type == AccountTypeOAuth || a.Type == AccountTypeAPIKey || a.Type == AccountTypeUpstream
	default:
		return (a.IsCNProvider() || a.IsOpenCodeGo()) && a.Type == AccountTypeAPIKey
	}
}

// Unknown/custom text aliases remain usable; vision input is not an exclusion.
func pelicanNonTextModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:]
	}
	if isImageGenerationModel(model) || isOpenAIImageGenerationModel(model) || xai.IsGrokImagineModel(model) {
		return true
	}
	if model == "chatgpt-image-latest" || model == "multimodalembedding" {
		return true
	}
	for _, prefix := range []string{"dall-e-", "imagen-", "veo-", "sora-", "grok-imagine-", "grok-2-image", "whisper-", "tts-", "embedding-", "textembedding-", "multimodalembedding@", "rerank-", "gemini-2.5-flash-native-audio", "gemini-2.5-flash-preview-native-audio"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	for _, marker := range []string{"-embedding", "-rerank", "-moderation", "-transcribe", "-tts"} {
		if strings.Contains(model, marker) {
			return true
		}
	}
	return false
}

// Preserve the original OpenAI default, while allowing mixed-platform batches
// to omit a global model. Explicit per-account models always take precedence.
func PelicanBenchmarkDefaultModel(a *Account) string {
	model := benchmark.DefaultModel
	switch a.Platform {
	case PlatformAnthropic, PlatformAntigravity:
		model = claude.DefaultTestModel
	case PlatformGemini:
		model = geminicli.DefaultTestModel
	case PlatformGrok:
		model = grokDefaultResponsesModel
	case PlatformDeepseek:
		model = "deepseek-flash"
	case PlatformKimi:
		model = "kimi-k2.5"
	case PlatformZhipu, PlatformOpenCodeGo:
		model = DefaultOpenCodeGoTestModel
	case PlatformMiniMax:
		model = "MiniMax-M2.5"
	}
	if a.IsModelSupported(model) && !pelicanNonTextModel(a.GetMappedModel(model)) {
		return model
	}
	var candidates []string
	for id, mapped := range a.GetModelMapping() {
		if a.Platform == PlatformAntigravity && isAntigravityGeminiFlashInternalModel(id) {
			continue
		}
		if benchmark.ValidModel(id) && !pelicanNonTextModel(id) && !pelicanNonTextModel(mapped) && a.IsModelSupported(id) {
			candidates = append(candidates, id)
		}
	}
	sort.Strings(candidates)
	if len(candidates) > 0 {
		return candidates[0]
	}
	return model
}

func (p *openAIPelicanProbe) addSecrets(value any) {
	switch v := value.(type) {
	case string:
		p.secrets = append(p.secrets, v)
	case map[string]any:
		for _, item := range v {
			p.addSecrets(item)
		}
	case map[string]string:
		for _, item := range v {
			p.addSecrets(item)
		}
	case []any:
		for _, item := range v {
			p.addSecrets(item)
		}
	}
}

func pelicanBeforeSend(c *gin.Context, model string) error {
	if probe := pelicanProbe(c); probe != nil {
		if err := c.Request.Context().Err(); err != nil {
			return err
		}
		if !benchmark.ValidModel(model) || pelicanNonTextModel(model) {
			probe.errorCode = "model_not_supported"
			return errors.New(probe.errorCode)
		}
		if probe.beforeSend != nil {
			return probe.beforeSend(model)
		}
	}
	return nil
}

// OAuth probes use the stored snapshot, never refreshing/backfilling credentials.
// Service-account exchanges are ephemeral and use the selected account proxy.
func pelicanAccessToken(ctx context.Context, a *Account, load func() (string, error)) (string, error) {
	probe, _ := ctx.Value(pelicanContextKey{}).(*openAIPelicanProbe)
	if probe == nil {
		return load()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	token := a.GetCredential("access_token")
	if a.Type == AccountTypeServiceAccount {
		var err error
		token, err = getVertexServiceAccountAccessToken(ctx, nil, a)
		if err != nil {
			return "", err
		}
	}
	if token == "" {
		return "", errors.New("access token unavailable")
	}
	probe.secrets = append(probe.secrets, token)
	return token, nil
}

func pelicanClaudePayload(c *gin.Context, payload map[string]any) {
	if pelicanProbe(c) != nil {
		payload["messages"] = pelicanMessages(c.Request.Context(), false)
		// Messages requires an explicit limit; the connectivity probe's 256/1024
		// tokens cannot hold an animation. Truncation is still a failed benchmark.
		payload["max_tokens"] = 8192
	}
}

func pelicanGeminiContents(ctx context.Context) []map[string]any {
	var contents []map[string]any
	for _, message := range pelicanMessages(ctx, false) {
		role := message["role"]
		if role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{"role": role, "parts": []map[string]any{{"text": message["content"]}}})
	}
	return contents
}

func pelicanGeminiPayload(c *gin.Context, payload []byte) []byte {
	if pelicanProbe(c) == nil {
		return payload
	}
	var body map[string]any
	if json.Unmarshal(payload, &body) != nil {
		return payload
	}
	body["contents"] = pelicanGeminiContents(c.Request.Context())
	result, _ := json.Marshal(body)
	return result
}

// Reuse the connection-test builders and its read-only endpoint negotiation.
// The gateway's TestConnectionWithPrompt refreshes tokens and buffers output,
// so Pelican supplies snapshot auth and consumes the bounded stream itself.
func (s *AccountTestService) testPelicanAntigravityConnection(c *gin.Context, a *Account, model string) error {
	gateway := s.antigravityGatewayService
	if gateway == nil {
		return s.pelicanError(c, "upstream_unavailable")
	}
	ctx := c.Request.Context()
	token := a.GetCredential("access_token")
	if a.Type == AccountTypeUpstream {
		token = a.GetCredential("api_key")
	}
	if token == "" {
		return s.pelicanError(c, "credentials_unavailable")
	}
	project, err := resolveAntigravityProjectID(a)
	if err != nil {
		return s.pelicanError(c, "project_unavailable")
	}
	mapped := gateway.getMappedModel(a, model)
	if mapped == "" {
		return s.pelicanError(c, "model_not_supported")
	}
	profile := resolveAntigravityWrappedModelProfile(model, mapped)
	if profile.upstreamModel != "" {
		mapped = profile.upstreamModel
	}
	var body []byte
	if strings.HasPrefix(model, "gemini-") {
		body, err = gateway.buildGeminiTestRequestWithPrompt(project, mapped, benchmark.Prompt)
	} else {
		body, err = gateway.buildClaudeTestRequestWithPrompt(project, mapped, benchmark.Prompt)
	}
	if err != nil {
		return s.pelicanError(c, "invalid_request")
	}
	var wrapped map[string]any
	if json.Unmarshal(body, &wrapped) != nil {
		return s.pelicanError(c, "invalid_request")
	}
	request, ok := wrapped["request"].(map[string]any)
	if !ok {
		return s.pelicanError(c, "invalid_request")
	}
	request["contents"] = pelicanGeminiContents(ctx)
	generation, _ := request["generationConfig"].(map[string]any)
	if generation == nil {
		generation = map[string]any{}
		request["generationConfig"] = generation
	}
	generation["maxOutputTokens"] = 8192
	body, _ = json.Marshal(wrapped)
	body, err = applyAntigravityWrappedModelProfile(body, profile)
	if err != nil {
		return s.pelicanError(c, "invalid_request")
	}
	if err := pelicanBeforeSend(c, antigravityWrappedRequestModel(body)); err != nil {
		return err
	}
	result, err := gateway.antigravityRetryLoop(antigravityRetryLoopParams{
		ctx: ctx, account: a, proxyURL: a.requestProxyURL(), accessToken: token,
		action: "streamGenerateContent", body: body, httpUpstream: gateway.httpUpstream,
		requestedModel: model, readOnlyProbe: true,
	})
	if err != nil {
		return s.pelicanError(c, "upstream_network_error")
	}
	if result == nil || result.resp == nil {
		return s.pelicanError(c, "upstream_incomplete")
	}
	resp := result.resp
	defer resp.Body.Close()
	boundPelicanResponse(c, resp)
	if resp.StatusCode != http.StatusOK {
		return s.pelicanHTTPError(c, resp)
	}
	return s.processPelicanProtocolStream(c, resp.Body, "gemini")
}
