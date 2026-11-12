package benchmark

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	TestPelican    = "pelican"
	TestCandy      = "candy"
	MaxPromptChars = 8000
	MaxPromptBytes = 32000
	CandyPrompt    = `在一个黑色的袋子里放有三种口味的糖果（苹果味、桃子味、西瓜味），每种糖果有圆形和五角星形两种形状。你可以通过手感分辨形状，并在每一次摸取时选择形状，但无法通过手感分辨口味。允许根据已经取出糖果的结果调整后续选择形状的策略，取出的糖果不放回。
数量如下：
苹果味：圆形 7 个，五角星形 7 个（共 14 个）。
桃子味：圆形 9 个，五角星形 6 个（共 15 个）。
西瓜味：圆形 8 个，五角星形 4 个（共 12 个）。
问题：在允许通过手感选择形状摸取的最优策略下，最少取出多少个糖果，才能保证手中同时拥有不同形状的苹果味和桃子味的糖？即满足“圆形苹果味 + 五角星桃子味”，或者“圆形桃子味 + 五角星苹果味”中的任意一种组合。
请独立推理并给出结论，不调用工具或网络检索。最后一行严格使用“最终答案：N”，其中 N 为你得出的最少糖果个数。`
)

func NormalizeTestType(testType string) string {
	if testType == "" {
		return TestPelican
	}
	return testType
}

func ValidTestType(testType string) bool {
	return testType == TestPelican || testType == TestCandy
}

func ValidPrompt(prompt string) bool {
	return len(prompt) <= MaxPromptBytes && utf8.ValidString(prompt) &&
		utf8.RuneCountInString(prompt) <= MaxPromptChars && !strings.ContainsRune(prompt, '\x00')
}

func ResolvePrompt(testType, prompt string) string {
	if testType == TestCandy {
		return CandyPrompt
	}
	if strings.TrimSpace(prompt) == "" {
		return Prompt
	}
	return prompt
}

// SnapshotPrompt keeps the exact prompt of an existing task, even if a future
// release revises a default question. Only legacy rows lack a snapshot.
func SnapshotPrompt(testType, prompt string) string {
	if strings.TrimSpace(prompt) != "" {
		return prompt
	}
	return ResolvePrompt(testType, "")
}

type testContextKey struct{}
type testContext struct{ testType, prompt string }

// WithTest supplies the same immutable prompt to all existing protocol adapters.
func WithTest(ctx context.Context, testType, prompt string) context.Context {
	return context.WithValue(ctx, testContextKey{}, testContext{NormalizeTestType(testType), prompt})
}

func TestFromContext(ctx context.Context) (string, string, error) {
	test, _ := ctx.Value(testContextKey{}).(testContext)
	test.testType = NormalizeTestType(test.testType)
	if !ValidTestType(test.testType) || !ValidPrompt(test.prompt) {
		return "", "", errors.New("invalid benchmark test or prompt")
	}
	return test.testType, SnapshotPrompt(test.testType, test.prompt), nil
}

func RequestPrompt(ctx context.Context) string {
	_, prompt, _ := TestFromContext(ctx)
	return prompt
}

var (
	candyFinalLabel = regexp.MustCompile(`(?i)^(?:最终答案|最后答案|最终结论|final answer|答案)\s*[:：]\s*(.+)$`)
	candyInteger    = regexp.MustCompile(`^([0-9]{1,9})\s*(?:个(?:糖果)?|颗(?:糖果)?|candies)?[。.!！]?$`)
	candyBoxed      = regexp.MustCompile(`^\\boxed\{\s*([0-9]{1,9})\s*(?:\\text\{(?:个|颗)(?:糖果)?\})?\s*\}(?:\s*(?:个|颗)(?:糖果)?)?[。.!！]?$`)
)

// GradeCandy reads only an explicit final line (or a standalone numeric reply).
// Numbers in reasoning, quotations, alternatives and incomplete output cannot pass.
func GradeCandy(text string) (*int, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > MaxHTMLBytes || !utf8.ValidString(text) {
		return nil, false
	}
	lines := strings.Split(text, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if (last == `\]` || last == "$$") && len(lines) > 1 {
		last = strings.TrimSpace(lines[len(lines)-2])
		// Only a boxed answer can use a separate closing math delimiter.
		if !strings.Contains(last, `\boxed{`) {
			return nil, false
		}
	}
	// Markdown emphasis and math delimiters do not change a final answer.
	clean := func(value string) string {
		value = strings.TrimSpace(value)
		for _, pair := range [][2]string{{"**", "**"}, {"__", "__"}, {"*", "*"}, {"`", "`"}, {"$$", "$$"}, {"$", "$"}, {`\[`, `\]`}, {`\(`, `\)`}} {
			if len(value) > len(pair[0])+len(pair[1]) && strings.HasPrefix(value, pair[0]) && strings.HasSuffix(value, pair[1]) {
				value = strings.TrimSpace(value[len(pair[0]) : len(value)-len(pair[1])])
			}
		}
		return value
	}
	last = clean(last)
	explicit := false
	if match := candyFinalLabel.FindStringSubmatch(last); match != nil {
		last, explicit = clean(match[1]), true
	}
	match := candyBoxed.FindStringSubmatch(last)
	if match == nil && (explicit || len(lines) == 1) {
		match = candyInteger.FindStringSubmatch(last)
	}
	if match == nil {
		return nil, false
	}
	answer, err := strconv.Atoi(match[1])
	if err != nil {
		return nil, false
	}
	return &answer, answer == 21
}
