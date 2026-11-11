package benchmark

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPelicanModelIDsAcrossProviders(t *testing.T) {
	for _, model := range []string{DefaultModel, "claude-sonnet-4-5@20250929", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "vendor/custom+reasoning", "models/gemini-2.5-flash", "gemini-3.7-flash-medium", "MiniMax-M2.5", "deepseek-flash[1m]"} {
		require.True(t, ValidModel(model), model)
	}
	for _, model := range []string{"", "model\nheader", "model\x00", "model?key=secret", "model#fragment", " model", "model ", "model*", strings.Repeat("x", 201)} {
		require.False(t, ValidModel(model), model)
	}
	require.True(t, ValidModel(strings.Repeat("x", 200)))
	require.Equal(t, "创建一个HTML，内容是用SVG绘制一个鹈鹕骑自行车的2D动画，你不能进行任何测试，调用skills，网络检索，直接生成", Prompt)
}
