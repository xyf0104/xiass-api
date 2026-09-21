package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const OpenAICodexTicketCaptureProxyIDsExtraKey = "xiass_codex_ticket_capture_proxy_ids"
const MaxOpenAICodexTicketCaptureProxyIDs = 64

// An empty list preserves legacy capture routing. Invalid stored configuration
// must not silently send probes through the account's business exits instead.
func OpenAICodexTicketCaptureProxyIDs(account *Account) ([]int64, error) {
	ids := []int64{}
	if account == nil || account.Extra == nil {
		return ids, nil
	}
	raw, exists := account.Extra[OpenAICodexTicketCaptureProxyIDsExtraKey]
	if !exists {
		return ids, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil || json.Unmarshal(encoded, &ids) != nil || ids == nil || len(ids) > MaxOpenAICodexTicketCaptureProxyIDs {
		return nil, infraerrors.BadRequest("CODEX_TICKET_CAPTURE_CONFIG_INVALID", "Invalid saved ticket capture exits; save the capture settings again")
	}
	unique := make([]int64, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, infraerrors.BadRequest("CODEX_TICKET_CAPTURE_CONFIG_INVALID", "Saved ticket capture exits must contain positive IDs")
		}
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	return unique, nil
}

func (s *OpenAIGatewayService) defaultCodexTicketCaptureProxies(ctx context.Context, account *Account) ([]*Proxy, error) {
	ids, err := OpenAICodexTicketCaptureProxyIDs(account)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	// SettingService already owns the shared proxy repository and is fully
	// wired before the gateway constructor starts the background harvester.
	if s.settingService == nil || s.settingService.proxyRepo == nil {
		return nil, infraerrors.ServiceUnavailable("CODEX_TICKET_CAPTURE_UNAVAILABLE", "Saved ticket capture exits cannot be loaded")
	}
	proxies, err := s.settingService.proxyRepo.ListByIDs(ctx, ids)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("CODEX_TICKET_CAPTURE_UNAVAILABLE", "Saved ticket capture exits cannot be loaded")
	}
	byID := make(map[int64]*Proxy, len(proxies))
	for i := range proxies {
		byID[proxies[i].ID] = &proxies[i]
	}
	now := time.Now()
	selected := make([]*Proxy, 0, len(ids))
	for _, id := range ids {
		proxy := byID[id]
		if proxy != nil && proxy.IsActive() && !proxy.IsExpired(now) && proxy.URL() != "" {
			selected = append(selected, proxy)
		}
	}
	if len(selected) == 0 {
		return nil, infraerrors.BadRequest("CODEX_TICKET_CAPTURE_NO_ACTIVE_PROXY", "No saved ticket capture exit is available; check the capture settings")
	}
	return selected, nil
}

// Capture requests using different exit sets must not share an in-flight
// result. The final state remains keyed only by account/model for business use.
func codexTicketCaptureFlightKey(accountID int64, model string, targets []openAICodexTicketProbeTarget) string {
	hash := sha256.New()
	for _, target := range targets {
		_, _ = fmt.Fprintf(hash, "%d\x00%s\x00", target.ProxyID, target.ProxyURL)
	}
	return openAICodexTicketKey(accountID, model) + "\x00" + hex.EncodeToString(hash.Sum(nil))
}
