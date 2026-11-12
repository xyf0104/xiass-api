package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/stretchr/testify/require"
)

func iqAdapterCases() []pelicanAdapterCase {
	return append(pelicanAdapterCases(),
		pelicanAdapterCase{"openai responses", PlatformOpenAI, AccountTypeAPIKey, benchmark.DefaultModel, "", "responses"},
		pelicanAdapterCase{"openai oauth", PlatformOpenAI, AccountTypeOAuth, benchmark.DefaultModel, "", "responses"},
		pelicanAdapterCase{"openai chat", PlatformOpenAI, AccountTypeAPIKey, benchmark.DefaultModel, "", "chat"},
	)
}

func iqAdapterFixture(tc pelicanAdapterCase, text string) (*Account, *pelicanRecordingUpstream, *AccountTestService) {
	a, u, s := pelicanAdapterFixture(tc, text)
	if tc.platform == PlatformOpenAI {
		a.Credentials["plan_type"] = "plus"
		a.Extra = map[string]any{"openai_responses_mode": "force_responses"}
		if tc.wire == "chat" {
			a.Extra["openai_responses_mode"] = "force_chat_completions"
		}
	}
	return a, u, s
}

func TestIQInstructionsKeepOriginalHTMLDefault(t *testing.T) {
	require.Equal(t, "Return only the requested HTML document.", pelicanInstructions(context.Background()))
	require.Equal(t, "Return only the requested HTML document.", pelicanInstructions(benchmark.WithTest(context.Background(), benchmark.TestPelican, "custom")))
	instructions := pelicanInstructions(benchmark.WithTest(context.Background(), benchmark.TestCandy, benchmark.CandyPrompt))
	require.NotEmpty(t, instructions)
	require.NotContains(t, instructions, "HTML")
	require.NotContains(t, instructions, "21")
}

func TestIQAllAdaptersUseCustomPromptAndCandyTextWithoutStateMutation(t *testing.T) {
	for _, tc := range iqAdapterCases() {
		for _, testType := range []string{benchmark.TestPelican, benchmark.TestCandy} {
			t.Run(tc.name+"/"+testType, func(t *testing.T) {
				text := "<html>custom 3D scene</html>"
				custom := "生成一个自包含的3D鹈鹕骑车HTML"
				if testType == benchmark.TestCandy {
					text = "先考虑了22个的情况。\n最终答案：21"
				}
				a, u, s := iqAdapterFixture(tc, text)
				before, err := json.Marshal(a)
				require.NoError(t, err)
				ctx := benchmark.WithTest(context.Background(), testType, benchmark.ResolvePrompt(testType, custom))
				out, err := s.RunPelicanBenchmark(ctx, a.ID, tc.model, nil)
				require.NoError(t, err)
				if testType == benchmark.TestCandy {
					require.Empty(t, out.HTML)
					require.Equal(t, text, out.TextOutput)
				} else {
					require.Equal(t, text, out.HTML)
					require.Empty(t, out.TextOutput)
				}
				payload, err := json.Marshal(u.body)
				require.NoError(t, err)
				want, err := json.Marshal(benchmark.ResolvePrompt(testType, custom))
				require.NoError(t, err)
				require.Contains(t, string(payload), string(want))
				original, _ := json.Marshal(benchmark.Prompt)
				require.NotContains(t, string(payload), string(original))
				if testType == benchmark.TestCandy {
					require.NotContains(t, string(payload), custom)
					instructions, _ := u.body["instructions"].(string)
					require.NotContains(t, instructions, "HTML")
					require.NotContains(t, string(payload), "最终答案：21")
				}
				require.Equal(t, 1, u.calls)
				require.Equal(t, a.Proxy.URL(), u.proxy)
				after, err := json.Marshal(a)
				require.NoError(t, err)
				require.JSONEq(t, string(before), string(after))
			})
		}
	}
}

func TestIQCandyWrongAnswerCompletesButProtocolFailureDoesNot(t *testing.T) {
	for _, text := range []string{"最终答案：22", "最终答案：21或22", "推理里出现21但没有最终答案"} {
		a, u, s := iqAdapterFixture(iqAdapterCases()[0], text)
		out, err := s.RunPelicanBenchmark(benchmark.WithTest(context.Background(), benchmark.TestCandy, ""), a.ID, "claude-sonnet-4-5", nil)
		require.NoError(t, err)
		require.Empty(t, out.ErrorCode)
		require.Empty(t, out.HTML)
		require.Equal(t, text, out.TextOutput)
		_, passed := benchmark.GradeCandy(out.TextOutput)
		require.False(t, passed)
		require.Equal(t, 1, u.calls)
	}
	for _, response := range []string{
		`data: {"type":"response.output_text.delta","delta":"最终答案：21"}` + "\n\n",
		`data: {"type":"response.output_text.delta","delta":"最终答案：21"}` + "\n\n" + `data: {"type":"response.incomplete"}` + "\n\n",
		`data: {"type":"response.failed","response":{"error":{"message":"failed"}}}` + "\n\n",
	} {
		a := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Credentials: map[string]any{"api_key": "synthetic"}, Extra: map[string]any{"openai_responses_mode": "force_responses"}}
		u := &pelicanRecordingUpstream{status: 200, response: response}
		out, err := pelicanTestService(a, u).RunPelicanBenchmark(benchmark.WithTest(context.Background(), benchmark.TestCandy, ""), a.ID, benchmark.DefaultModel, nil)
		require.Error(t, err)
		require.NotEmpty(t, out.ErrorCode)
		require.Empty(t, out.HTML)
	}
}

func TestIQCustomContinuationAndInvalidTestFailBeforeNetwork(t *testing.T) {
	a := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "synthetic"}, Extra: map[string]any{"openai_responses_mode": "force_responses"}}
	custom := "Create a 3D scene as HTML"
	ctx := benchmark.WithContinuation(benchmark.WithTest(context.Background(), benchmark.TestPelican, custom), "<html>partial")
	u := &pelicanRecordingUpstream{status: 200, response: pelicanResponses("</html>")}
	out, err := pelicanTestService(a, u).RunPelicanBenchmark(ctx, a.ID, benchmark.DefaultModel, nil)
	require.NoError(t, err)
	require.Equal(t, "<html>partial</html>", out.HTML)
	inputs := u.body["input"].([]any)
	require.Len(t, inputs, 3)
	require.Equal(t, custom, inputs[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"])
	for _, ctx := range []context.Context{
		benchmark.WithTest(context.Background(), "invalid", ""),
		benchmark.WithTest(context.Background(), benchmark.TestPelican, strings.Repeat("x", benchmark.MaxPromptChars+1)),
		benchmark.WithContinuation(benchmark.WithTest(context.Background(), benchmark.TestCandy, ""), "<html>partial"),
	} {
		u := &pelicanRecordingUpstream{}
		_, err := pelicanTestService(a, u).RunPelicanBenchmark(ctx, a.ID, benchmark.DefaultModel, nil)
		require.Error(t, err)
		require.Zero(t, u.calls)
	}
}
