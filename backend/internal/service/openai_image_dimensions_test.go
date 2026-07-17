package service

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectOpenAIImageResultSize(t *testing.T) {
	encoded := encodeOpenAIImageDimensionTestPNG(t, 1024, 1024)

	require.Equal(t, "1024x1024", detectOpenAIImageResultSize(encoded))
	require.Equal(t, "1024x1024", detectOpenAIImageResultSize("data:image/png;base64,"+encoded))
	require.Empty(t, detectOpenAIImageResultSize("not-image-data"))
}

func TestOpenAIImageOutputCounterUsesDecodedRasterDimensions(t *testing.T) {
	encoded := encodeOpenAIImageDimensionTestPNG(t, 1280, 720)
	body := []byte(fmt.Sprintf(`{"data":[{"b64_json":%q,"size":"auto"}]}`, encoded))

	sizes := collectOpenAIResponseImageOutputSizesFromJSONBytes(body)
	require.Equal(t, []string{"1280x720"}, sizes)

	resolved := ResolveOpenAIImageBillingSize("auto", sizes)
	require.Equal(t, ImageBillingSize1K, resolved.BillingSize)
	require.Equal(t, "1280x720", resolved.OutputSize)
	require.Equal(t, ImageSizeSourceOutput, resolved.Source)
}

func TestCollectOpenAIImagesFromResponsesBodyUsesDecodedRasterDimensions(t *testing.T) {
	encoded := encodeOpenAIImageDimensionTestPNG(t, 1024, 1024)
	body := []byte(fmt.Sprintf(
		"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000000,\"tools\":[{\"type\":\"image_generation\",\"size\":\"auto\"}],\"output\":[{\"id\":\"ig_actual_size\",\"type\":\"image_generation_call\",\"size\":\"auto\",\"result\":%q}]}}\n\ndata: [DONE]\n\n",
		encoded,
	))

	results, _, _, meta, foundFinal, err := collectOpenAIImagesFromResponsesBody(body)
	require.NoError(t, err)
	require.True(t, foundFinal)
	require.Len(t, results, 1)
	require.Equal(t, "1024x1024", results[0].Size)
	require.Equal(t, "1024x1024", meta.Size)
}

func encodeOpenAIImageDimensionTestPNG(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	img.SetNRGBA(0, 0, color.NRGBA{R: 0xff, A: 0xff})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}
