package service

import (
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"testing"
)

func TestApplyGeminiCompatThinkingConfig(t *testing.T) {
	budget := func(v int64) *int64 { return &v }
	const baseReq = `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":8192}}`
	tests := []struct {
		name            string
		model           string
		claudeBody      string
		includeThoughts bool
		wantLevel       string
		wantBudget      *int64
		wantThoughts    bool
		wantNoConfig    bool
	}{
		{name: "effort medium on flash", model: "gemini-3.8-flash", claudeBody: `{"output_config":{"effort":"medium"},"thinking":{"type":"enabled","budget_tokens":4096}}`, wantLevel: "medium"},
		{name: "effort low", model: "gemini-3.7-flash", claudeBody: `{"output_config":{"effort":"low"}}`, wantLevel: "low"},
		{name: "effort none on flash", model: "gemini-3.8-flash", claudeBody: `{"output_config":{"effort":"none"},"thinking":{"type":"enabled","budget_tokens":10240}}`, wantLevel: "minimal"},
		{name: "effort minimal on pro", model: "gemini-3.1-pro-preview", claudeBody: `{"output_config":{"effort":"minimal"}}`, wantLevel: "low"},
		{name: "effort medium on gemini 3 pro", model: "gemini-3-pro-preview", claudeBody: `{"output_config":{"effort":"medium"}}`, wantLevel: "high"},
		{name: "effort medium on gemini 3.1 pro", model: "gemini-3.1-pro-preview", claudeBody: `{"output_config":{"effort":"medium"}}`, wantLevel: "medium"},
		{name: "effort max", model: "gemini-3.7-flash", claudeBody: `{"output_config":{"effort":"max"}}`, wantLevel: "high"},
		{name: "effort xhigh", model: "gemini-3.7-flash", claudeBody: `{"output_config":{"effort":"xhigh"}}`, wantLevel: "high"},
		{name: "thinking disabled wins over effort", model: "gemini-3.8-flash", claudeBody: `{"thinking":{"type":"disabled"},"output_config":{"effort":"high"}}`, wantLevel: "minimal"},
		{name: "budget only maps to high", model: "gemini-3.8-flash", claudeBody: `{"thinking":{"type":"enabled","budget_tokens":31999}}`, wantLevel: "high"},
		{name: "budget only maps to medium", model: "gemini-3.8-flash", claudeBody: `{"thinking":{"type":"enabled","budget_tokens":4096}}`, wantLevel: "medium"},
		{name: "budget only maps to low", model: "gemini-3.8-flash", claudeBody: `{"thinking":{"type":"enabled","budget_tokens":1024}}`, wantLevel: "low"},
		{name: "adaptive keeps default level", model: "gemini-3.7-flash", claudeBody: `{"thinking":{"type":"adaptive"}}`, includeThoughts: true, wantThoughts: true},
		{name: "no thinking params streams thoughts only", model: "gemini-3.7-flash", claudeBody: `{}`, includeThoughts: true, wantThoughts: true},
		{name: "unknown effort ignored", model: "gemini-3.7-flash", claudeBody: `{"output_config":{"effort":"turbo"}}`, wantNoConfig: true},
		{name: "level with thoughts", model: "gemini-3.8-flash", claudeBody: `{"output_config":{"effort":"high"}}`, includeThoughts: true, wantLevel: "high", wantThoughts: true},
		{name: "carbon low", model: "gemini-4-carbon", claudeBody: `{"output_config":{"effort":"low"}}`, wantLevel: "low"},
		{name: "carbon medium", model: "models/gemini-4-carbon", claudeBody: `{"output_config":{"effort":"medium"}}`, wantLevel: "medium"},
		{name: "carbon high", model: "gemini-4-carbon", claudeBody: `{"output_config":{"effort":"high"}}`, wantLevel: "high"},
		{name: "carbon medium budget", model: "gemini-4-carbon", claudeBody: `{"thinking":{"type":"enabled","budget_tokens":4096}}`, wantLevel: "medium"},
		{name: "carbon cannot disable thinking", model: "gemini-4-carbon", claudeBody: `{"thinking":{"type":"disabled"}}`, wantLevel: "low"},
		{name: "carbon preserves upstream default", model: "gemini-4-carbon", claudeBody: `{}`, wantNoConfig: true},
		{name: "gemini 4 uses level", model: "gemini-4-argon", claudeBody: `{"output_config":{"effort":"low"}}`, wantLevel: "low"},
		{name: "medium outside verified models becomes high", model: "gemini-4-argon", claudeBody: `{"output_config":{"effort":"medium"}}`, wantLevel: "high"},
		{name: "medium budget outside verified models becomes high", model: "gemini-4-argon", claudeBody: `{"thinking":{"type":"enabled","budget_tokens":4096}}`, wantLevel: "high"},
		{name: "medium on unverified pro becomes high", model: "gemini-3.5-pro", claudeBody: `{"output_config":{"effort":"medium"}}`, wantLevel: "high"},
		{name: "medium on flash lite", model: "gemini-3.1-flash-lite", claudeBody: `{"output_config":{"effort":"medium"}}`, wantLevel: "medium"},
		{name: "tier suffix model keeps name tier", model: "gemini-4-argon-high", claudeBody: `{"output_config":{"effort":"low"}}`, includeThoughts: true, wantThoughts: true},
		{name: "tiered suffix model keeps name tier", model: "gemini-3.8-flash-tiered", claudeBody: `{"output_config":{"effort":"low"}}`, includeThoughts: true, wantThoughts: true},
		{name: "2.5 flash low", model: "gemini-2.5-flash", claudeBody: `{"output_config":{"effort":"low"}}`, wantBudget: budget(1024)},
		{name: "2.5 flash high", model: "gemini-2.5-flash", claudeBody: `{"output_config":{"effort":"high"}}`, includeThoughts: true, wantBudget: budget(24576), wantThoughts: true},
		{name: "2.5 flash none disables thinking and thoughts", model: "gemini-2.5-flash", claudeBody: `{"output_config":{"effort":"none"}}`, includeThoughts: true, wantBudget: budget(0)},
		{name: "2.5 pro cannot disable thinking", model: "gemini-2.5-pro", claudeBody: `{"thinking":{"type":"disabled"}}`, includeThoughts: true, wantBudget: budget(128), wantThoughts: true},
		{name: "2.5 pro high", model: "gemini-2.5-pro", claudeBody: `{"output_config":{"effort":"max"}}`, wantBudget: budget(32768)},
		{name: "2.5 pro budget clamps up", model: "gemini-2.5-pro", claudeBody: `{"thinking":{"type":"enabled","budget_tokens":50}}`, wantBudget: budget(128)},
		{name: "2.5 flash budget clamps down", model: "gemini-2.5-flash", claudeBody: `{"thinking":{"type":"enabled","budget_tokens":31999}}`, wantBudget: budget(24576)},
		{name: "2.5 flash lite budget clamps up", model: "gemini-2.5-flash-lite", claudeBody: `{"thinking":{"type":"enabled","budget_tokens":100}}`, wantBudget: budget(512)},
		{name: "2.5 adaptive keeps dynamic budget", model: "gemini-2.5-flash", claudeBody: `{"thinking":{"type":"adaptive"}}`, wantNoConfig: true},
		{name: "2.0 untouched", model: "gemini-2.0-flash", claudeBody: `{"output_config":{"effort":"high"}}`, includeThoughts: true, wantNoConfig: true},
		{name: "image model untouched", model: "gemini-3-pro-image-preview", claudeBody: `{"output_config":{"effort":"high"}}`, includeThoughts: true, wantNoConfig: true},
		{name: "unparsable model untouched", model: "gemini-flash-latest", claudeBody: `{"output_config":{"effort":"high"}}`, includeThoughts: true, wantNoConfig: true},
		{name: "non gemini model untouched", model: "custom-model", claudeBody: `{"output_config":{"effort":"high"}}`, includeThoughts: true, wantNoConfig: true},
		{name: "models prefix", model: "models/gemini-3.8-flash", claudeBody: `{"output_config":{"effort":"low"}}`, wantLevel: "low"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := applyGeminiCompatThinkingConfig([]byte(baseReq), []byte(tt.claudeBody), tt.model, tt.includeThoughts)
			require.Equal(t, int64(8192), gjson.GetBytes(out, "generationConfig.maxOutputTokens").Int())
			require.Equal(t, "hi", gjson.GetBytes(out, "contents.0.parts.0.text").String())
			cfg := gjson.GetBytes(out, "generationConfig.thinkingConfig")
			if tt.wantNoConfig {
				require.False(t, cfg.Exists(), cfg.Raw)
				return
			}
			require.Equal(t, tt.wantLevel, cfg.Get("thinkingLevel").String())
			if tt.wantBudget != nil {
				require.True(t, cfg.Get("thinkingBudget").Exists(), cfg.Raw)
				require.Equal(t, *tt.wantBudget, cfg.Get("thinkingBudget").Int())
			} else {
				require.False(t, cfg.Get("thinkingBudget").Exists(), cfg.Raw)
			}
			require.Equal(t, tt.wantThoughts, cfg.Get("includeThoughts").Bool(), cfg.Raw)
			require.False(t, cfg.Get("thinkingLevel").Exists() && cfg.Get("thinkingBudget").Exists(), "thinkingLevel and thinkingBudget are mutually exclusive")
		})
	}
}
