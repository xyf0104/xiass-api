package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIQCandyGradesOnlyFinalAnswer(t *testing.T) {
	for _, tc := range []struct {
		text           string
		answer         int
		parsed, passed bool
	}{
		{"21", 21, true, true},
		{"21个", 21, true, true},
		{" 21 个糖果。\n", 21, true, true},
		{"推理中考虑了22和41。\n最终答案：21", 21, true, true},
		{"推理中考虑了21。\n最终答案：22", 22, true, false},
		{"**最终答案：21**", 21, true, true},
		{"推理\n最终答案：**21**", 21, true, true},
		{"推理\n最终答案：$\\boxed{21}$", 21, true, true},
		{"推理\n\\boxed{22}", 22, true, false},
		{"推理\n\\[\\boxed{21}\\]", 21, true, true},
		{"推理\n\\[\n\\boxed{21}\n\\]", 21, true, true},
		{"推理\n$\\boxed{21\\text{个}}$", 21, true, true},
		{"Final answer: 21 candies.", 21, true, true},
		{"21 appears in reasoning but is not the answer", 0, false, false},
		{"取21个可能足够，还需要证明。", 0, false, false},
		{"推理中有21\n没有结论", 0, false, false},
		{"最终答案：21或22", 0, false, false},
		{"最终答案：21-1", 0, false, false},
		{"最终答案：21.5", 0, false, false},
		{"最终答案：不是21", 0, false, false},
		{"最终答案：\\boxed{21} 或 \\boxed{22}", 0, false, false},
		{"最终答案：99999999999999999999999", 0, false, false},
		{"最终答案：21\n但上述结论不成立", 0, false, false},
		{"推理\n21", 0, false, false},
		{"", 0, false, false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			answer, passed := GradeCandy(tc.text)
			require.Equal(t, tc.passed, passed)
			if tc.parsed {
				require.NotNil(t, answer)
				require.Equal(t, tc.answer, *answer)
			} else {
				require.Nil(t, answer)
			}
		})
	}
}

func TestIQPromptContractAndLimits(t *testing.T) {
	testType, prompt, err := TestFromContext(context.Background())
	require.NoError(t, err)
	require.Equal(t, TestPelican, testType)
	require.Equal(t, Prompt, prompt)
	for _, value := range []string{"", "\n  ", strings.Repeat("a", MaxPromptChars), strings.Repeat("糖", MaxPromptChars), strings.Repeat("😀", MaxPromptChars)} {
		require.True(t, ValidPrompt(value))
	}
	for _, value := range []string{strings.Repeat("a", MaxPromptChars+1), strings.Repeat("糖", MaxPromptChars+1), "a\x00b", "\xff"} {
		require.False(t, ValidPrompt(value))
	}
	custom := "创建3D动画的HTML"
	require.Equal(t, custom, RequestPrompt(WithTest(context.Background(), TestPelican, custom)))
	require.Equal(t, CandyPrompt, ResolvePrompt(TestCandy, custom))
	require.Equal(t, custom, RequestPrompt(WithTest(context.Background(), TestCandy, custom)), "internal tasks preserve their snapshot")
	require.NotContains(t, CandyPrompt, "21")
	for _, value := range []string{"圆形 7 个，五角星形 7 个", "圆形 9 个，五角星形 6 个", "圆形 8 个，五角星形 4 个", "每一次摸取时选择形状", "最终答案：N"} {
		require.Contains(t, CandyPrompt, value)
	}
	_, _, err = TestFromContext(WithTest(context.Background(), "invalid", custom))
	require.Error(t, err)
	metadata, err := json.Marshal(Task{TestType: TestCandy, Prompt: CandyPrompt})
	require.NoError(t, err)
	require.NotContains(t, string(metadata), "prompt")
	require.Contains(t, string(metadata), `"answer":null`)
	require.Contains(t, string(metadata), `"passed":null`)
}

func TestIQSameAccountBothTypesRunConcurrentlyAndStopIndependently(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &pelicanDispatchStore{tasks: []Task{
			{ID: "html", BatchID: "batch", AccountID: 7, Status: "queued", TestType: TestPelican, Prompt: "custom HTML"},
			{ID: "candy", BatchID: "batch", AccountID: 7, Status: "queued", TestType: TestCandy, Prompt: CandyPrompt},
		}}
		entered := make(chan string, 2)
		canceled := make(chan string, 2)
		release := make(chan struct{})
		manager := NewManager(store, pelicanRunnerFunc(func(ctx context.Context, id int64, _ string, _ func(string) error) (Output, error) {
			testType, _, err := TestFromContext(ctx)
			if err != nil {
				return Output{}, err
			}
			entered <- testType
			<-ctx.Done()
			canceled <- testType
			<-release
			return Output{}, ctx.Err()
		}))
		defer manager.Stop()
		defer close(release)
		manager.Start()
		synctest.Wait()
		require.Len(t, entered, 2, "both types must start before either completes")
		require.ElementsMatch(t, []string{TestPelican, TestCandy}, []string{<-entered, <-entered})
		store.mu.Lock()
		store.tasks[0].Status = "canceling"
		store.mu.Unlock()
		time.Sleep(time.Second)
		synctest.Wait()
		require.Len(t, canceled, 1)
		require.Equal(t, TestPelican, <-canceled)
		store.mu.Lock()
		require.Equal(t, "running", store.tasks[1].Status)
		require.Len(t, store.owners, 2, "canceling type retains its guard until upstream exits")
		store.mu.Unlock()
	})
}

func TestIQRetryAndContinuePreservePromptAndType(t *testing.T) {
	for _, testType := range []string{TestPelican, TestCandy} {
		for _, action := range []string{"retry", "continue"} {
			store := &pelicanContinuationStore{detail: &Detail{Task: Task{AccountID: 7, TestType: testType, Prompt: "custom HTML", Status: "failed"}, HTML: "<html>partial"}}
			called := false
			manager := NewManager(store, pelicanRunnerFunc(func(ctx context.Context, _ int64, _ string, _ func(string) error) (Output, error) {
				called = true
				kind, prompt, err := TestFromContext(ctx)
				require.NoError(t, err)
				require.Equal(t, testType, kind)
				require.Equal(t, "custom HTML", prompt)
				_, continuing := Continuation(ctx)
				require.Equal(t, action == "continue", continuing)
				return Output{}, nil
			}))
			_, err := manager.invoke(context.Background(), &Task{AccountID: 7, TestType: testType, Prompt: "custom HTML", Action: action, SourceID: "source"})
			manager.Stop()
			if testType == TestCandy && action == "continue" {
				require.Error(t, err)
				require.False(t, called)
			} else {
				require.NoError(t, err)
				require.True(t, called)
			}
		}
	}
}

func TestIQManagerWrongAnswerSucceedsButTransportFailureRemainsFailed(t *testing.T) {
	for _, tc := range []struct {
		text   string
		err    error
		status string
	}{
		{"最终答案：21", nil, "succeeded"},
		{"最终答案：22", nil, "succeeded"},
		{"no clear answer", nil, "succeeded"},
		{"最终答案：21", errors.New("connection lost"), "failed"},
	} {
		store := &pelicanManagerStore{status: "running", finished: make(chan string, 1)}
		manager := NewManager(store, pelicanRunnerFunc(func(context.Context, int64, string, func(string) error) (Output, error) {
			return Output{TextOutput: tc.text}, tc.err
		}))
		manager.run(&Task{ID: "candy", TestType: TestCandy})
		manager.Stop()
		require.Equal(t, tc.status, <-store.finished)
		require.Equal(t, tc.text, store.finishOutput.TextOutput)
		require.Empty(t, store.finishOutput.HTML)
		if tc.err != nil {
			require.Equal(t, "upstream_failed", store.finishOutput.ErrorCode)
		}
	}
}
