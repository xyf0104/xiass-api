package service

import (
	"context"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/stretchr/testify/require"
)

func TestPelicanOutputTokenBudgetRegression(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  int
	}{
		{"gemini-3.8-flash-high", 32768},
		{"gemini-3.8-flash-medium", 32768},
		{"gemini-3.8-flash-low", 32768},
		{"gemini-3.8-flash-tiered", 32768},
		{"gemini-3.7-flash-high", 32768},
		{"gemini-4-carbon-high", 32768},
		{"gemini-4-carbon-medium", 32768},
		{"gemini-4-carbon-low", 32768},
		{"gemini-4-argon-high", 32768},
		{"gemini-3-pro-preview", 32768},
		{"gemini-2.5-flash", 32768},
		{"gemini-2.5-pro", 32768},
		{" models/GEMINI-3.8-FLASH-HIGH ", 32768},
		{"claude-opus-5-5", 32768},
		{"claude-opus-5-5-high", 32768},
		{"claude-opus-5-5-medium", 32768},
		{"claude-opus-5-5-low", 32768},
		{"claude-opus-5.5", 32768},
		{"claude-sonnet-4-5", 32768},
		{"claude-opus-4-20250514", 32768},
		{"claude-3-7-sonnet-20250219", 32768},
		{"claude-mythos-preview", 32768},
		{"us.anthropic.claude-sonnet-4-5-20250929-v1:0", 32768},
		{"eu.anthropic.claude-3-7-sonnet-20250219-v1:0", 32768},
		{"global.anthropic.claude-opus-5-5-v1:0", 32768},
		{"claude-3-haiku-20240307", 4096},
		{"claude-3-sonnet-20240229", 4096},
		{"claude-3-opus-20240229", 4096},
		{"anthropic.claude-3-haiku-20240307-v1:0", 4096},
		{"apac.anthropic.claude-3-sonnet-20240229-v1:0", 4096},
		{"claude-3-5-sonnet-20241022", 8192},
		{"claude-3-5-haiku-20241022", 8192},
		{"us.anthropic.claude-3-5-sonnet-20241022-v2:0", 8192},
		{"gemini-2.0-flash", 8192},
		{"MiniMax-M2.5", 8192},
		{"vendor/custom-reasoning", 8192},
		{"", 8192},
	} {
		t.Run(tc.model, func(t *testing.T) {
			require.Equal(t, tc.want, pelicanOutputTokenBudget(tc.model))
		})
	}
}

func TestPelicanBudgetClaudeResolvedPayload(t *testing.T) {
	for _, tc := range []struct {
		typ, model string
		want       int
	}{
		{AccountTypeAPIKey, "claude-opus-5-5", 32768},
		{AccountTypeAPIKey, "claude-3-haiku-20240307", 4096},
		{AccountTypeAPIKey, "claude-3-5-sonnet-20241022", 8192},
		{AccountTypeAPIKey, "custom-reasoning", 8192},
		{AccountTypeBedrock, "us.anthropic.claude-sonnet-4-5-20250929-v1:0", 32768},
		{AccountTypeBedrock, "anthropic.claude-3-haiku-20240307-v1:0", 4096},
	} {
		t.Run(tc.typ+"/"+tc.model, func(t *testing.T) {
			wire := "claude"
			if tc.typ == AccountTypeBedrock {
				wire = "bedrock"
			}
			a, u, s := pelicanAdapterFixture(pelicanAdapterCase{platform: PlatformAnthropic, typ: tc.typ, wire: wire}, "<html></html>")
			a.Credentials["model_mapping"] = map[string]any{"benchmark-alias": tc.model}
			var actual string
			out, err := s.RunPelicanBenchmark(context.Background(), a.ID, "benchmark-alias", func(model string) error { actual = model; return nil })
			require.NoError(t, err)
			require.Equal(t, "<html></html>", out.HTML)
			require.Equal(t, 1, u.calls)
			require.Equal(t, tc.model, actual)
			require.Equal(t, float64(tc.want), u.body["max_tokens"])
			require.Equal(t, []any{map[string]any{"role": "user", "content": benchmark.Prompt}}, u.body["messages"])
			if tc.typ == AccountTypeBedrock {
				require.NotContains(t, u.body, "model", "Bedrock must use the resolved endpoint model to choose its budget")
				endpoint, err := url.Parse(u.url)
				require.NoError(t, err)
				require.Equal(t, "/model/"+tc.model+"/invoke", endpoint.Path)
			} else {
				require.Equal(t, tc.model, u.body["model"])
			}
		})
	}
}

func TestPelicanBudgetAntigravityPreservesPromptAndTiers(t *testing.T) {
	for _, tc := range []struct {
		model, upstream string
		thinking        int
	}{
		{"gemini-3.8-flash-high", "gemini-3.8-flash-tiered", -1},
		{"gemini-3.8-flash-medium", "gemini-3.8-flash-tiered", 4000},
		{"gemini-3.8-flash-low", "gemini-3.8-flash-tiered", 1000},
		{"gemini-4-carbon-high", "gemini-4-carbon-high", 0},
		{"gemini-4-carbon-medium", "gemini-4-carbon-medium", 0},
		{"gemini-4-carbon-low", "gemini-4-carbon-low", 0},
		{"claude-opus-5-5", "claude-opus-5-5-high", 0},
		{"claude-opus-5-5-high", "claude-opus-5-5-high", 0},
		{"claude-opus-5-5-medium", "claude-opus-5-5-medium", 0},
		{"claude-opus-5-5-low", "claude-opus-5-5-low", 0},
	} {
		t.Run(tc.model, func(t *testing.T) {
			a, u, s := pelicanAdapterFixture(pelicanAdapterCase{platform: PlatformAntigravity, typ: AccountTypeOAuth, wire: "wrapped"}, "<html></html>")
			a.Credentials["plan_type"] = "Ultra"
			var actual string
			out, err := s.RunPelicanBenchmark(context.Background(), a.ID, tc.model, func(model string) error { actual = model; return nil })
			require.NoError(t, err)
			require.Equal(t, "<html></html>", out.HTML)
			require.Equal(t, 1, u.calls)
			require.Equal(t, tc.upstream, actual)
			require.Equal(t, tc.upstream, u.body["model"])
			request, ok := u.body["request"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": benchmark.Prompt}}}}, request["contents"])
			generation, ok := request["generationConfig"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, float64(32768), generation["maxOutputTokens"])
			if tc.thinking != 0 {
				thinking, ok := generation["thinkingConfig"].(map[string]any)
				require.True(t, ok)
				require.Equal(t, float64(tc.thinking), thinking["thinkingBudget"])
			}
		})
	}
}

func TestPelicanBudgetAntigravityMetadataLimit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		limit, want int64
	}{
		{"lower upstream limit", 16384, 16384},
		{"higher upstream limit", 65536, 32768},
		{"unspecified upstream limit", 0, 32768},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, u, s := pelicanAdapterFixture(pelicanAdapterCase{platform: PlatformAntigravity, typ: AccountTypeOAuth, wire: "wrapped"}, "<html></html>")
			// Metadata belongs to the resolved route, not the public tier alias.
			a.SetUpstreamModelMetadataSnapshot(UpstreamModelMetadataSnapshot{
				Source: "upstream",
				Models: map[string]UpstreamModelMetadata{
					"gemini-3.8-flash-tiered": {ID: "gemini-3.8-flash-tiered", MaxOutputTokens: tc.limit},
				},
			})
			out, err := s.RunPelicanBenchmark(context.Background(), a.ID, "gemini-3.8-flash-medium", nil)
			require.NoError(t, err)
			require.Equal(t, "<html></html>", out.HTML)
			require.Equal(t, 1, u.calls)
			request, ok := u.body["request"].(map[string]any)
			require.True(t, ok)
			generation, ok := request["generationConfig"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, float64(tc.want), generation["maxOutputTokens"])
			thinking, ok := generation["thinkingConfig"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, float64(4000), thinking["thinkingBudget"])
		})
	}
}
