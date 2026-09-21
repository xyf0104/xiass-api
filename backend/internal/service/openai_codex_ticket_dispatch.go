package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

type codexTicketDispatchSummary struct {
	Enabled      bool
	Present      bool
	Length       int
	Fingerprint  string
	ProxyID      int64
	ProxyMatches bool
}

// Inspect only the final outgoing header. Captured model/shape is not evidence
// that an upstream honored a ticket; the response model is recorded separately.
func inspectCodexTicketDispatch(account *Account, headers http.Header, proxyURL string) codexTicketDispatchSummary {
	result := codexTicketDispatchSummary{Enabled: OpenAICodexTicketEnabledForAccount(account)}
	if !result.Enabled {
		return result
	}
	if proxy := account.requestProxy(); proxy != nil {
		result.ProxyID = proxy.ID
	}
	result.ProxyMatches = strings.TrimSpace(proxyURL) == strings.TrimSpace(account.requestProxyURL())
	state := extractOpenAICodexTurnState(headers)
	result.Present = state != ""
	result.Length = len(state)
	if result.Present {
		sum := sha256.Sum256([]byte(state))
		result.Fingerprint = hex.EncodeToString(sum[:8])
	}
	return result
}

func logCodexTicketDispatch(ctx context.Context, account *Account, headers http.Header, proxyURL, transport string) {
	log := logger.FromContext(ctx)
	if !log.Core().Enabled(zap.DebugLevel) {
		return
	}
	summary := inspectCodexTicketDispatch(account, headers, proxyURL)
	if !summary.Enabled {
		return
	}
	log.Debug("openai codex ticket dispatch",
		zap.Int64("account_id", account.ID),
		zap.String("transport", transport),
		zap.Int64("proxy_id", summary.ProxyID),
		zap.Bool("proxy_binding_matches", summary.ProxyMatches),
		zap.Bool("turn_state_present", summary.Present),
		zap.Int("turn_state_length", summary.Length),
		zap.String("turn_state_fingerprint", summary.Fingerprint),
	)
}
