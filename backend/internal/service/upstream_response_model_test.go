package service

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUpstreamResponseModelObserverTerminalWinsAndRecordsConflict(t *testing.T) {
	observer := &upstreamResponseModelObserver{}

	observer.ObserveOpenAI([]byte(`{"type":"response.created","response":{"model":"gpt-5.5"}}`), "response.created")
	observer.ObserveOpenAI([]byte(`{"type":"response.completed","response":{"model":"gpt-5.4"}}`), "response.completed")

	require.Equal(t, "gpt-5.4", observer.Model())
	require.True(t, observer.Conflict())
}

func TestUpstreamResponseModelObserverSupportsAnthropicAndGeminiShapes(t *testing.T) {
	t.Run("anthropic", func(t *testing.T) {
		observer := &upstreamResponseModelObserver{}
		observer.ObserveAnthropic([]byte(`{"type":"message_start","message":{"model":"claude-sonnet-4-20250514"}}`))
		require.Equal(t, "claude-sonnet-4-20250514", observer.Model())
	})

	t.Run("gemini outer and nested", func(t *testing.T) {
		observer := &upstreamResponseModelObserver{}
		observer.ObserveGemini([]byte(`{"response":{"modelVersion":"gemini-2.5-pro"}}`))
		observer.ObserveGemini([]byte(`{"modelVersion":"gemini-2.5-pro-latest"}`))
		require.Equal(t, "gemini-2.5-pro-latest", observer.Model())
		require.True(t, observer.Conflict())
	})
}

func TestUpstreamResponseModelObservationAttemptReset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	first := beginUpstreamResponseModelObservation(c)
	first.Observe("failed-attempt-model", false)
	second := beginUpstreamResponseModelObservation(c)
	second.Observe("successful-attempt-model", false)

	require.Equal(t, "successful-attempt-model", observedUpstreamResponseModel(c))
	require.False(t, observedUpstreamResponseModelConflict(c))
}

func TestUpstreamModelMismatchThreeStateAndCaseInsensitiveComparison(t *testing.T) {
	require.Nil(t, upstreamModelMismatch("gpt-5.5", ""))

	matched := upstreamModelMismatch("gpt-5.5", "GPT-5.5")
	require.NotNil(t, matched)
	require.False(t, *matched)

	mismatched := upstreamModelMismatch("gpt-5.5", "gpt-5.4")
	require.NotNil(t, mismatched)
	require.True(t, *mismatched)
}

func TestObserveOpenAISSEBodyIgnoresMalformedPayload(t *testing.T) {
	observer := &upstreamResponseModelObserver{}
	observeOpenAISSEBody(observer, "data: not-json\n\ndata: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-5.4\"}}\n\n")

	require.Equal(t, "gpt-5.4", observer.Model())
	require.False(t, observer.Conflict())
}

func TestExtractExplicitOpenAIResponseModelRecognizedPaths(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "response metadata id", payload: `{"response":{"metadata":{"model_id":"gpt-6-astra"}}}`, want: "gpt-6-astra"},
		{name: "response slug", payload: `{"response":{"model_slug":"gpt-6-luna"}}`, want: "gpt-6-luna"},
		{name: "top level name", payload: `{"model_name":"gpt-6-sol"}`, want: "gpt-6-sol"},
		{name: "later untyped output item metadata", payload: `{"response":{"output":[{"type":"reasoning"},{"metadata":{"model_name":"gpt-6-luna"}}]}}`, want: "gpt-6-luna"},
		{name: "assistant message output item", payload: `{"output":[{"type":"message","role":"assistant","model_slug":"gpt-6-astra"}]}`, want: "gpt-6-astra"},
		{name: "non-object before assistant message", payload: `{"output":["ignored",{"type":"message","role":"assistant","model":"gpt-6-sol"}]}`, want: "gpt-6-sol"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, extractExplicitOpenAIResponseModel([]byte(tt.payload)))
		})
	}
}

func TestExtractExplicitOpenAIResponseModelIgnoresNonIdentityContent(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "generated text", payload: `{"output":[{"type":"message","content":[{"type":"output_text","text":"{\"model\":\"gpt-6-luna\"}"}]}]}`},
		{name: "function arguments", payload: `{"output":[{"type":"function_call","arguments":"{\"model\":\"gpt-6-luna\"}"}]}`},
		{name: "function call model", payload: `{"output":[{"type":"function_call","model":"tool-router-v2"}]}`},
		{name: "image generation model", payload: `{"response":{"output":[{"type":"image_generation_call","model":"gpt-image-2"}]}}`},
		{name: "user message model", payload: `{"output":[{"type":"message","role":"user","model":"echoed-request-model"}]}`},
		{name: "tool message model", payload: `{"output":[{"type":"message","role":"tool","model":"tool-output-model"}]}`},
		{name: "non-object output", payload: `{"output":["{\"model\":\"gpt-6-luna\"}",42,true]}`},
		{name: "reasoning object", payload: `{"response":{"reasoning":{"model":"reasoning-submodel"}}}`},
		{name: "absent identity", payload: `{"type":"response.completed","response":{"status":"completed","output":[]}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Empty(t, extractExplicitOpenAIResponseModel([]byte(tt.payload)))
		})
	}
}

func TestExtractExplicitOpenAIResponseModelEnvelopePrecedesOutputItems(t *testing.T) {
	payload := []byte(`{"model":"gpt-6-sol","metadata":{"model":"gpt-6-astra"},"output":[{"type":"message","model":"gpt-6-luna"}]}`)

	require.Equal(t, "gpt-6-sol", extractExplicitOpenAIResponseModel(payload))
}

func TestUpstreamResponseModelObserverTerminalOutputDeclarationWins(t *testing.T) {
	observer := &upstreamResponseModelObserver{}
	observer.ObserveOpenAI([]byte(`{"response":{"model":"gpt-6-astra"}}`), "response.created")
	observer.ObserveOpenAI([]byte(`{"response":{"output":[{"type":"message","model":"gpt-6-luna"}]}}`), "response.completed")

	require.Equal(t, "gpt-6-luna", observer.Model())
	require.True(t, observer.Conflict())
}

func TestUpstreamResponseModelObserverKeepsUnknownWhenIdentityAbsent(t *testing.T) {
	observer := &upstreamResponseModelObserver{}
	observer.ObserveOpenAI([]byte(`{"type":"response.completed","response":{"status":"completed","output":[]}}`), "response.completed")

	require.Empty(t, observer.Model())
	require.Nil(t, upstreamModelMismatch("gpt-6-astra", observer.Model()))
}

func TestUpstreamResponseModelObserverBoundsUntrustedModelName(t *testing.T) {
	observer := &upstreamResponseModelObserver{}
	observer.Observe("  "+strings.Repeat("模", upstreamResponseModelMaxLength+1)+"  ", false)

	require.Len(t, []rune(observer.Model()), upstreamResponseModelMaxLength)
}
