package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClassifyImageBillingTier(t *testing.T) {
	tests := []struct {
		name     string
		size     string
		wantTier string
		wantOK   bool
	}{
		{name: "explicit 2k square", size: "2048x2048", wantTier: "2K", wantOK: true},
		{name: "explicit 2k landscape", size: "2048x1152", wantTier: "2K", wantOK: true},
		{name: "explicit 4k landscape", size: "3840x2160", wantTier: "4K", wantOK: true},
		{name: "explicit 4k portrait", size: "2160x3840", wantTier: "4K", wantOK: true},
		{name: "long edge 1k", size: "1024X768", wantTier: "1K", wantOK: true},
		{name: "hd landscape stays 1k", size: "1280x720", wantTier: "1K", wantOK: true},
		{name: "openai landscape stays 1k", size: "1536x1024", wantTier: "1K", wantOK: true},
		{name: "openai portrait stays 1k", size: "1024x1536", wantTier: "1K", wantOK: true},
		{name: "long edge 2k", size: "2560x1600", wantTier: "2K", wantOK: true},
		{name: "tier string 1k", size: "1k", wantTier: "1K", wantOK: true},
		{name: "empty", size: "", wantOK: false},
		{name: "auto", size: "auto", wantOK: false},
		{name: "invalid", size: "not-a-size", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTier, gotOK := ClassifyImageBillingTier(tt.size)
			require.Equal(t, tt.wantOK, gotOK)
			require.Equal(t, tt.wantTier, gotTier)
		})
	}
}

func TestResolveOpenAIImageBillingSizeDefaultsTo1K(t *testing.T) {
	for _, input := range []string{"", "auto", "invalid"} {
		resolved := ResolveOpenAIImageBillingSize(input, nil)
		require.Equal(t, ImageBillingSize1K, resolved.BillingSize)
		require.Equal(t, ImageSizeSourceDefault, resolved.Source)
	}
}

func TestResolveOpenAIImageBillingSizePrefersCustomerRequest(t *testing.T) {
	tests := []struct {
		name        string
		inputSize   string
		outputSizes []string
		wantBilling string
		wantSource  string
	}{
		{name: "explicit 1k wins", inputSize: "1K", outputSizes: []string{"2048x1152"}, wantBilling: "1K", wantSource: ImageSizeSourceInput},
		{name: "explicit 2k wins", inputSize: "2K", outputSizes: []string{"1024x1024"}, wantBilling: "2K", wantSource: ImageSizeSourceInput},
		{name: "requested dimensions are classified", inputSize: "1280x720", outputSizes: []string{"2048x1152"}, wantBilling: "1K", wantSource: ImageSizeSourceInput},
		{name: "auto uses actual output dimensions", inputSize: "auto", outputSizes: []string{"2048x1152"}, wantBilling: "2K", wantSource: ImageSizeSourceOutput},
		{name: "missing request uses actual output dimensions", outputSizes: []string{"1024x1024"}, wantBilling: "1K", wantSource: ImageSizeSourceOutput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := ResolveOpenAIImageBillingSize(tt.inputSize, tt.outputSizes)
			require.Equal(t, tt.wantBilling, resolved.BillingSize)
			require.Equal(t, tt.wantSource, resolved.Source)
		})
	}
}

func TestResolveImageBillingSize(t *testing.T) {
	tests := []struct {
		name          string
		inputSize     string
		outputSizes   []string
		wantBilling   string
		wantOutput    string
		wantSource    string
		wantBreakdown map[string]int
	}{
		{
			name:          "output wins over input",
			inputSize:     "1024x1024",
			outputSizes:   []string{"3840x2160"},
			wantBilling:   "4K",
			wantOutput:    "3840x2160",
			wantSource:    ImageSizeSourceOutput,
			wantBreakdown: map[string]int{"4K": 1},
		},
		{
			name:        "input fallback",
			inputSize:   "1024x1024",
			wantBilling: "1K",
			wantSource:  ImageSizeSourceInput,
		},
		{
			name:        "auto defaults",
			inputSize:   "auto",
			wantBilling: "2K",
			wantSource:  ImageSizeSourceDefault,
		},
		{
			name:        "empty defaults",
			inputSize:   "",
			wantBilling: "2K",
			wantSource:  ImageSizeSourceDefault,
		},
		{
			name:        "invalid defaults",
			inputSize:   "largest",
			wantBilling: "2K",
			wantSource:  ImageSizeSourceDefault,
		},
		{
			name:          "mixed output chooses highest tier",
			inputSize:     "1024x1024",
			outputSizes:   []string{"1024x1024", "3840x2160", "2048x1152"},
			wantBilling:   "4K",
			wantOutput:    "1024x1024",
			wantSource:    ImageSizeSourceOutput,
			wantBreakdown: map[string]int{"1K": 1, "2K": 1, "4K": 1},
		},
		{
			name:        "unparseable output falls back to parseable input",
			inputSize:   "2048x1152",
			outputSizes: []string{"auto"},
			wantBilling: "2K",
			wantOutput:  "auto",
			wantSource:  ImageSizeSourceInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveImageBillingSize(tt.inputSize, tt.outputSizes)
			require.Equal(t, tt.wantBilling, got.BillingSize)
			require.Equal(t, tt.inputSize, got.InputSize)
			require.Equal(t, tt.wantOutput, got.OutputSize)
			require.Equal(t, tt.wantSource, got.Source)
			require.Equal(t, tt.wantBreakdown, got.Breakdown)
		})
	}
}
