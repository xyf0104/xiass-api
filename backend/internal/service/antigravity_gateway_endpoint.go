package service

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
)

const antigravityEndpointPreferenceTTL = 30 * time.Minute

type antigravityEndpointPreferenceKey struct {
	accountID int64
	projectID string
	model     string
	proxyHash [sha256.Size]byte
}

type antigravityEndpointPreference struct {
	tokenHash [sha256.Size]byte
	baseURL   string
	expiresAt time.Time
}

type antigravityEndpointFallbackReason int

const (
	antigravityEndpointFallbackNone antigravityEndpointFallbackReason = iota
	antigravityEndpointFallbackExhausted
	antigravityEndpointFallbackAuthCompatibility
)

func antigravityForwardMode() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv(antigravityForwardBaseURLEnv)))
}

func isAutomaticAntigravityForwardMode() bool {
	mode := antigravityForwardMode()
	return mode == "" || mode == "auto"
}

// configuredAntigravityForwardBaseURLs keeps explicit environment overrides
// strict. Automatic mode starts with prod and may negotiate daily only for a
// narrowly classified endpoint-compatibility response.
func configuredAntigravityForwardBaseURLs() []string {
	baseURLs := append([]string(nil), antigravity.BaseURLs...)
	if len(baseURLs) == 0 {
		return nil
	}

	switch antigravityForwardMode() {
	case "", "auto":
		return baseURLs
	case "daily", "sandbox":
		if len(baseURLs) > 1 {
			return []string{baseURLs[1]}
		}
		return []string{baseURLs[0]}
	case "prod", "production":
		return []string{baseURLs[0]}
	default:
		return []string{baseURLs[0]}
	}
}

func antigravityEndpointPreferenceIdentity(p antigravityRetryLoopParams) (antigravityEndpointPreferenceKey, [sha256.Size]byte) {
	var envelope struct {
		Project string `json:"project"`
		Model   string `json:"model"`
	}
	_ = json.Unmarshal(p.body, &envelope)
	if envelope.Project == "" && p.account != nil {
		envelope.Project = p.account.GetCredential("project_id")
	}
	if envelope.Model == "" {
		envelope.Model = p.requestedModel
	}

	accountID := int64(0)
	if p.account != nil {
		accountID = p.account.ID
	}
	return antigravityEndpointPreferenceKey{
		accountID: accountID,
		projectID: strings.TrimSpace(envelope.Project),
		model:     strings.TrimSpace(envelope.Model),
		proxyHash: sha256.Sum256([]byte(p.proxyURL)),
	}, sha256.Sum256([]byte(p.accessToken))
}

func (s *AntigravityGatewayService) antigravityForwardBaseURLs(p antigravityRetryLoopParams) []string {
	baseURLs := configuredAntigravityForwardBaseURLs()
	if s == nil || len(baseURLs) < 2 || !isAutomaticAntigravityForwardMode() {
		return baseURLs
	}

	key, tokenHash := antigravityEndpointPreferenceIdentity(p)
	s.endpointPreferenceMu.RLock()
	preference, ok := s.endpointPreferences[key]
	s.endpointPreferenceMu.RUnlock()
	if !ok {
		return baseURLs
	}
	if preference.tokenHash != tokenHash || time.Now().After(preference.expiresAt) || preference.baseURL == "" || preference.baseURL == baseURLs[0] {
		s.deleteAntigravityEndpointPreferenceIfMatch(key, preference)
		return baseURLs
	}
	preferenceKnown := false
	for _, baseURL := range baseURLs {
		if baseURL == preference.baseURL {
			preferenceKnown = true
			break
		}
	}
	if !preferenceKnown {
		s.deleteAntigravityEndpointPreferenceIfMatch(key, preference)
		return baseURLs
	}

	preferred := make([]string, 0, len(baseURLs))
	preferred = append(preferred, preference.baseURL)
	for _, baseURL := range baseURLs {
		if baseURL != preference.baseURL {
			preferred = append(preferred, baseURL)
		}
	}
	return preferred
}

func (s *AntigravityGatewayService) rememberAntigravityForwardBaseURL(p antigravityRetryLoopParams, baseURL string) {
	if s == nil || p.account == nil || p.account.ID <= 0 || strings.TrimSpace(baseURL) == "" || !isAutomaticAntigravityForwardMode() {
		return
	}
	known := false
	for _, candidate := range antigravity.BaseURLs {
		if candidate == baseURL {
			known = true
			break
		}
	}
	if !known {
		return
	}
	defaultURLs := configuredAntigravityForwardBaseURLs()
	if len(defaultURLs) > 0 && baseURL == defaultURLs[0] {
		s.forgetAntigravityForwardBaseURL(p, "")
		return
	}

	key, tokenHash := antigravityEndpointPreferenceIdentity(p)
	s.endpointPreferenceMu.Lock()
	if s.endpointPreferences == nil {
		s.endpointPreferences = make(map[antigravityEndpointPreferenceKey]antigravityEndpointPreference)
	}
	now := time.Now()
	for staleKey, stalePreference := range s.endpointPreferences {
		if !now.Before(stalePreference.expiresAt) {
			delete(s.endpointPreferences, staleKey)
		}
	}
	s.endpointPreferences[key] = antigravityEndpointPreference{
		tokenHash: tokenHash,
		baseURL:   baseURL,
		expiresAt: now.Add(antigravityEndpointPreferenceTTL),
	}
	s.endpointPreferenceMu.Unlock()
}

func (s *AntigravityGatewayService) forgetAntigravityForwardBaseURL(p antigravityRetryLoopParams, baseURL string) {
	if s == nil {
		return
	}
	key, tokenHash := antigravityEndpointPreferenceIdentity(p)
	s.endpointPreferenceMu.Lock()
	preference, ok := s.endpointPreferences[key]
	if ok && preference.tokenHash == tokenHash && (baseURL == "" || preference.baseURL == baseURL) {
		delete(s.endpointPreferences, key)
	}
	s.endpointPreferenceMu.Unlock()
}

func (s *AntigravityGatewayService) deleteAntigravityEndpointPreferenceIfMatch(
	key antigravityEndpointPreferenceKey,
	expected antigravityEndpointPreference,
) {
	if s == nil {
		return
	}
	s.endpointPreferenceMu.Lock()
	if current, ok := s.endpointPreferences[key]; ok && current == expected {
		delete(s.endpointPreferences, key)
	}
	s.endpointPreferenceMu.Unlock()
}

func isBareAntigravityEndpointExhaustion(body []byte) bool {
	var envelope struct {
		Error struct {
			Status  string            `json:"status"`
			Message string            `json:"message"`
			Details []json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	return envelope.Error.Status == googleRPCStatusResourceExhausted &&
		strings.TrimSpace(envelope.Error.Message) == "Resource has been exhausted (e.g. check quota)." &&
		len(envelope.Error.Details) == 0
}

func isAntigravityEndpointAuthIncompatible(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized {
		return false
	}
	var envelope struct {
		Error struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	message := strings.ToLower(envelope.Error.Message)
	return envelope.Error.Status == "UNAUTHENTICATED" || strings.Contains(message, "invalid bearer token")
}

func isAntigravityDailyEndpoint(baseURL string) bool {
	return len(antigravity.BaseURLs) > 1 && baseURL == antigravity.BaseURLs[1]
}

func classifyAntigravityEndpointFallback(baseURL string, statusCode int, body []byte) antigravityEndpointFallbackReason {
	if len(antigravity.BaseURLs) > 0 &&
		baseURL == antigravity.BaseURLs[0] &&
		statusCode == http.StatusTooManyRequests &&
		isBareAntigravityEndpointExhaustion(body) {
		return antigravityEndpointFallbackExhausted
	}
	if isAntigravityDailyEndpoint(baseURL) && isAntigravityEndpointAuthIncompatible(statusCode, body) {
		return antigravityEndpointFallbackAuthCompatibility
	}
	return antigravityEndpointFallbackNone
}
