package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAICodexTicketOutboundModel_PassthroughKeepsRequestedModel(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.6-luna": "gpt-6-astra",
			},
		},
		Extra: map[string]any{
			"openai_passthrough": true,
		},
	}

	svc := &OpenAIGatewayService{}
	require.Equal(t, "gpt-5.6-luna", svc.openAICodexTicketOutboundModel(account, "gpt-5.6-luna", false))
}

func TestOpenAICodexTicketOutboundModel_PassthroughCompactUsesCompactMapping(t *testing.T) {
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.6-luna": "gpt-6-astra",
			},
			"compact_model_mapping": map[string]any{
				"gpt-5.6-luna": "gpt-5.6-terra",
			},
		},
		Extra: map[string]any{
			"openai_passthrough": true,
		},
	}

	svc := &OpenAIGatewayService{}
	require.Equal(t, "gpt-5.6-terra", svc.openAICodexTicketOutboundModel(account, "gpt-5.6-luna", true))
}
