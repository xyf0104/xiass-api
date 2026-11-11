package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageDTOOpenAICacheTTL(t *testing.T) {
	log := &service.UsageLog{CacheCreationTokens: 100, CacheCreation30mTokens: 30, CacheCreation12hTokens: 70}
	for _, dto := range []any{UsageLogFromService(log), UsageLogFromServiceAdmin(log)} {
		body, err := json.Marshal(dto)
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(body, &fields))
		require.Equal(t, float64(100), fields["cache_creation_tokens"])
		require.Equal(t, float64(30), fields["cache_creation_30m_tokens"])
		require.Equal(t, float64(70), fields["cache_creation_12h_tokens"])
		require.Equal(t, float64(0), fields["cache_creation_5m_tokens"])
		require.Equal(t, float64(0), fields["cache_creation_1h_tokens"])
	}
}
