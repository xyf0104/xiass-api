package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
)

const (
	openAIModelRotationStoreTimeout = 50 * time.Millisecond
	openAIModelRotationMemoryLimit  = 16384
)

type openAIModelResponseGuard struct {
	enabled  bool
	expected string
}

type openAIModelResponseMismatchObserverKey struct{}
type openAIModelResponseMismatchBypassKey struct{}
type openAIModelResponseMismatchStartedAtKey struct{}

type openAIModelResponseMismatchObserver struct {
	once     sync.Once
	observed atomic.Bool
	observe  func(requestedModel, upstreamModel, responseModel string, startedAt time.Time)
}

func WithOpenAIModelResponseMismatchObserver(ctx context.Context, observe func(string, string, string, time.Time)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIModelResponseMismatchObserverKey{}, &openAIModelResponseMismatchObserver{observe: observe})
}

func WithOpenAIModelResponseMismatchStartedAt(ctx context.Context, startedAt time.Time) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIModelResponseMismatchStartedAtKey{}, startedAt)
}

func notifyOpenAIModelResponseMismatch(ctx context.Context, requestedModel, upstreamModel, responseModel string, startedAt time.Time) {
	observer, _ := ctx.Value(openAIModelResponseMismatchObserverKey{}).(*openAIModelResponseMismatchObserver)
	if observer == nil || observer.observe == nil {
		return
	}
	if startedAt.IsZero() {
		startedAt, _ = ctx.Value(openAIModelResponseMismatchStartedAtKey{}).(time.Time)
	}
	observer.once.Do(func() {
		observer.observed.Store(true)
		observer.observe(requestedModel, upstreamModel, responseModel, startedAt)
	})
}

func openAIModelResponseMismatchObserved(ctx context.Context) bool {
	observer, _ := ctx.Value(openAIModelResponseMismatchObserverKey{}).(*openAIModelResponseMismatchObserver)
	return observer != nil && observer.observed.Load()
}

func (s *OpenAIGatewayService) ObserveOpenAIModelRotationImmediate(ctx context.Context, apiKey *APIKey, account *Account, requestedModel string, result *OpenAIForwardResult) {
	s.ObserveOpenAIModelRotation(context.WithValue(ctx, openAIModelResponseMismatchBypassKey{}, true), apiKey, account, requestedModel, result)
}

// OpenAIModelRotationStore is optional on GatewayCache, keeping other gateway
// cache implementations compatible. Observations contain no credentials.
type OpenAIModelRotationStore interface {
	GetOpenAIModelRotationFailures(ctx context.Context, scope string, now time.Time) ([]int64, error)
	ObserveOpenAIModelRotation(ctx context.Context, scope string, accountID int64, startedAt time.Time, blockedUntil time.Time, ttl time.Duration) error
}

type openAIModelRotationFallbackKey struct{}

type openAIModelRotationMemoryKey struct {
	scope     string
	accountID int64
}

type openAIModelRotationObservation struct {
	startedAt    time.Time
	blockedUntil time.Time
	expiresAt    time.Time
	pending      bool
}

type openAIModelRotationMemory struct {
	mu           sync.Mutex
	entries      map[openAIModelRotationMemoryKey]openAIModelRotationObservation
	pendingCount int
}

func (m *openAIModelRotationMemory) removeLocked(key openAIModelRotationMemoryKey) {
	if value, ok := m.entries[key]; ok && value.pending {
		m.pendingCount--
	}
	delete(m.entries, key)
}

func (m *openAIModelRotationMemory) observe(scope string, accountID int64, observation openAIModelRotationObservation, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = make(map[openAIModelRotationMemoryKey]openAIModelRotationObservation)
	}
	key := openAIModelRotationMemoryKey{scope: scope, accountID: accountID}
	if old, ok := m.entries[key]; ok && old.expiresAt.After(now) && old.startedAt.After(observation.startedAt) {
		return
	}
	if len(m.entries) >= openAIModelRotationMemoryLimit {
		var oldestKey openAIModelRotationMemoryKey
		var oldest time.Time
		for candidate, value := range m.entries {
			if !value.expiresAt.After(now) {
				m.removeLocked(candidate)
				continue
			}
			if oldest.IsZero() || value.expiresAt.Before(oldest) {
				oldestKey, oldest = candidate, value.expiresAt
			}
		}
		if len(m.entries) >= openAIModelRotationMemoryLimit {
			m.removeLocked(oldestKey)
		}
	}
	m.removeLocked(key)
	if observation.pending {
		m.pendingCount++
	}
	m.entries[key] = observation
}

func (m *openAIModelRotationMemory) failures(scope string, now time.Time) []int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var failures []int64
	for key, value := range m.entries {
		if !value.expiresAt.After(now) {
			m.removeLocked(key)
			continue
		}
		if key.scope == scope && value.blockedUntil.After(now) {
			failures = append(failures, key.accountID)
		}
	}
	return failures
}

func (m *openAIModelRotationMemory) pending(scope string, now time.Time) map[int64]openAIModelRotationObservation {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingCount == 0 {
		return nil
	}
	items := make(map[int64]openAIModelRotationObservation)
	for key, value := range m.entries {
		if !value.expiresAt.After(now) {
			m.removeLocked(key)
			continue
		}
		if key.scope == scope && value.pending && value.expiresAt.After(now) {
			items[key.accountID] = value
		}
	}
	return items
}

func (m *openAIModelRotationMemory) acknowledge(scope string, accountID int64, observation openAIModelRotationObservation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := openAIModelRotationMemoryKey{scope: scope, accountID: accountID}
	if current, ok := m.entries[key]; ok && current == observation {
		if current.pending {
			m.pendingCount--
		}
		current.pending = false
		m.entries[key] = current
	}
}

func openAIModelRotationScope(ctx context.Context, groupID *int64, model string) string {
	if ctx == nil || len(model) > 200 || strings.TrimSpace(model) == "" {
		return ""
	}
	userID, _ := ctx.Value(ctxkey.UserID).(int64)
	keyID, _ := ctx.Value(ctxkey.APIKeyID).(int64)
	if userID <= 0 || keyID <= 0 {
		return ""
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d:%s", userID, keyID, derefGroupID(groupID), strings.ToLower(strings.TrimSpace(model)))))
	return hex.EncodeToString(digest[:])
}

func (s *OpenAIGatewayService) openAIModelRotationCooldown(ctx context.Context, model string) time.Duration {
	if s == nil || s.settingService == nil {
		return 0
	}
	// Reuse the nonblocking immutable settings snapshot and its background refresh.
	s.settingService.resolveOpenAIModelPriorityPreference(ctx, model)
	cached, _ := s.settingService.openAIModelPriorityCache.Load().(*cachedOpenAIModelPrioritySettings)
	if cached == nil || !cached.compiled.enabled || !cached.compiled.smartRotationEnabled || cached.compiled.ruleForModel(model) == nil {
		return 0
	}
	minutes := cached.compiled.smartRotationCooldownMinutes
	if minutes <= 0 {
		minutes = 30
	}
	return time.Duration(minutes) * time.Minute
}

func (s *OpenAIGatewayService) newOpenAIModelResponseGuard(ctx context.Context, requestedModel, upstreamModel string) openAIModelResponseGuard {
	expected := normalizeOpenAIModelRotationModel(upstreamSentModel(requestedModel, upstreamModel))
	return openAIModelResponseGuard{enabled: expected != "" && s.openAIModelRotationCooldown(ctx, requestedModel) > 0, expected: expected}
}

func (g openAIModelResponseGuard) mismatch(responseModel string, conflict bool) (string, bool) {
	observed := normalizeOpenAIModelRotationModel(responseModel)
	if !g.enabled || conflict || observed == "" || observed == "unknown" || observed == "unknown-model" || observed == "n/a" || observed == g.expected {
		return "", false
	}
	return openAIModelResponseMismatchMessage(responseModel), true
}

func openAIModelResponseMismatchMessage(responseModel string) string {
	display := strings.TrimSpace(responseModel)
	if normalizeOpenAIModelRotationModel(display) == "gpt-5.6-luna" {
		display = "luna"
	}
	return fmt.Sprintf("检测到该条回复已降智（%s模型），将不予采纳。请重新发起请求。下一次请求将轮询健康账号。", display)
}

func writeOpenAIModelResponseMismatchHTTP(c *gin.Context, message string) {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusBadGateway, gin.H{"error": gin.H{"type": "model_mismatch", "code": "model_mismatch", "message": message}})
}

func writeOpenAIModelResponseMismatchSSE(w io.Writer, flusher http.Flusher, message string) error {
	if w == nil {
		return nil
	}
	payload := `data: {"type":"error","sequence_number":0,"error":{"type":"model_mismatch","message":` + strconv.Quote(message) + `,"code":"model_mismatch"}}` + "\n\ndata: [DONE]\n\n"
	if _, err := io.WriteString(w, payload); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

func (s *OpenAIGatewayService) openAIModelRotationExclusions(ctx context.Context, groupID *int64, model string) []int64 {
	if s.openAIModelRotationCooldown(ctx, model) == 0 {
		return nil
	}
	scope := openAIModelRotationScope(ctx, groupID, model)
	if scope == "" {
		return nil
	}
	now := time.Now()
	if store, ok := s.cache.(OpenAIModelRotationStore); ok {
		storeCtx, cancel := context.WithTimeout(ctx, openAIModelRotationStoreTimeout)
		defer cancel()
		// Replay only pending metadata writes, never model requests. CAS protects
		// newer remote observations when Redis recovers after a failed write.
		for accountID, observation := range s.openaiModelRotation.pending(scope, now) {
			if err := store.ObserveOpenAIModelRotation(storeCtx, scope, accountID, observation.startedAt, observation.blockedUntil, observation.expiresAt.Sub(now)); err == nil {
				s.openaiModelRotation.acknowledge(scope, accountID, observation)
			}
			if storeCtx.Err() != nil {
				break
			}
		}
		if failures, err := store.GetOpenAIModelRotationFailures(storeCtx, scope, now); err == nil {
			pending := s.openaiModelRotation.pending(scope, now)
			if len(pending) == 0 {
				return failures
			}
			merged := make(map[int64]struct{}, len(failures)+len(pending))
			for _, id := range failures {
				merged[id] = struct{}{}
			}
			for id, observation := range pending {
				if observation.blockedUntil.After(now) {
					merged[id] = struct{}{}
				} else {
					delete(merged, id)
				}
			}
			result := make([]int64, 0, len(merged))
			for id := range merged {
				result = append(result, id)
			}
			return result
		}
	}
	return s.openaiModelRotation.failures(scope, now)
}

// ShouldRotateOpenAIModelAccount allows a long-lived transport to reject a new
// stateless turn before forwarding it; the client can reconnect and reschedule.
func (s *OpenAIGatewayService) ShouldRotateOpenAIModelAccount(ctx context.Context, groupID *int64, model string, accountID int64) bool {
	for _, failedID := range s.openAIModelRotationExclusions(ctx, groupID, model) {
		if failedID == accountID {
			return true
		}
	}
	return false
}

var openAIModelRotationDateSuffix = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

// Keep different model families distinct. Broad substring normalization could
// accidentally classify an unrelated model as a confirmed match.
func normalizeOpenAIModelRotationModel(model string) string {
	model = strings.ToLower(lastOpenAIModelSegment(model))
	model = openAIModelRotationDateSuffix.ReplaceAllString(model, "")
	switch model {
	case "gpt-6":
		return "gpt-6-astra"
	case "gpt-5.6":
		return "gpt-5.6-sol"
	default:
		return model
	}
}

// ObserveOpenAIModelRotation runs before async usage recording, so the next
// request sees the outcome even when the usage queue is delayed. It never
// replays the completed response or changes billing or global account health.
func (s *OpenAIGatewayService) ObserveOpenAIModelRotation(ctx context.Context, apiKey *APIKey, account *Account, requestedModel string, result *OpenAIForwardResult) {
	if s == nil || apiKey == nil || account == nil || account.Platform != PlatformOpenAI || result == nil || ctx == nil {
		return
	}
	if bypass, _ := ctx.Value(openAIModelResponseMismatchBypassKey{}).(bool); !bypass && openAIModelResponseMismatchObserved(ctx) {
		return
	}
	observed := normalizeOpenAIModelRotationModel(result.UpstreamResponseModel)
	expected := normalizeOpenAIModelRotationModel(upstreamSentModel(requestedModel, result.UpstreamModel))
	if observed == "" || expected == "" || account.ID <= 0 {
		return
	}
	mismatch := expected != observed
	if !mismatch && result.UpstreamResponseModelConflict {
		return
	}
	cooldown := s.openAIModelRotationCooldown(ctx, requestedModel)
	if cooldown == 0 {
		return
	}
	userID := apiKey.UserID
	if userID == 0 && apiKey.User != nil {
		userID = apiKey.User.ID
	}
	ctx = context.WithValue(ctx, ctxkey.UserID, userID)
	ctx = context.WithValue(ctx, ctxkey.APIKeyID, apiKey.ID)
	scope := openAIModelRotationScope(ctx, apiKey.GroupID, requestedModel)
	if scope == "" {
		return
	}
	now := time.Now()
	startedAt := now
	if result.Duration > 0 {
		startedAt = now.Add(-result.Duration)
	}
	startedAt = startedAt.Truncate(time.Millisecond)
	observation := openAIModelRotationObservation{startedAt: startedAt, expiresAt: now.Add(cooldown), pending: true}
	if mismatch {
		observation.blockedUntil = observation.expiresAt
	}
	s.openaiModelRotation.observe(scope, account.ID, observation, now)
	if store, ok := s.cache.(OpenAIModelRotationStore); ok {
		storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIModelRotationStoreTimeout)
		defer cancel()
		if err := store.ObserveOpenAIModelRotation(storeCtx, scope, account.ID, startedAt, observation.blockedUntil, cooldown); err != nil {
			slog.Warn("openai.smart_model_rotation_store_failed", "account_id", account.ID, "error", err)
		} else {
			s.openaiModelRotation.acknowledge(scope, account.ID, observation)
		}
	}
	if mismatch {
		slog.Info("openai.smart_model_rotation_mismatch", "user_id", userID, "api_key_id", apiKey.ID,
			"account_id", account.ID, "requested_model", requestedModel,
			"sent_model", upstreamSentModel(requestedModel, result.UpstreamModel),
			"response_model", result.UpstreamResponseModel, "cooldown_minutes", int(cooldown.Minutes()))
	}
}
