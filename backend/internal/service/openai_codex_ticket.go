package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

const (
	openAICodexTurnStateHeader       = openAIWSTurnStateHeader
	openAICodexTicketExtraKeyPrefix  = "codex_turn_ticket:"
	OpenAICodexTicketEnabledExtraKey = "xiass_openai_codex_ticket_enabled"
	openAICodexAstraMinVersion       = "0.153.4"
	openAICodexTicketStatePrefix     = "gAAAAA"
	openAICodexTicketDefaultLength   = 292
	openAICodexTicketMinLength       = 292
	openAICodexTicketMaxLength       = 512
	openAICodexTicketDefaultModel    = "gpt-6-astra"
	openAICodexTicketDefaultSolModel = "gpt-5.6-sol"
)

// ErrOpenAICodexTicketUnavailable 表示该号该模型没有可用的 Codex Ticket，
// 且 fail_closed 禁止裸打业务请求。
var ErrOpenAICodexTicketUnavailable = errors.New("codex turn-state ticket unavailable")

// ErrOpenAICodexTicketAccountDisabled requires an explicit per-account opt-in
// before either automatic or manual ticket probes may run.
var ErrOpenAICodexTicketAccountDisabled = errors.New("codex ticket is disabled for this account")

type openAICodexTicket struct {
	AccountID      int64                           `json:"account_id"`
	Model          string                          `json:"model"` // requested/gated model; kept for legacy records
	RequestedModel string                          `json:"requested_model,omitempty"`
	ObservedModel  string                          `json:"observed_model,omitempty"`
	State          string                          `json:"state"`
	Length         int                             `json:"length"`
	CapturedAt     time.Time                       `json:"captured_at"`
	ExpiresAt      time.Time                       `json:"expires_at"`
	Attempts       int                             `json:"attempts"`
	ProxyID        int64                           `json:"proxy_id,omitempty"`
	ProxyName      string                          `json:"proxy_name,omitempty"`
	LatencyMs      int64                           `json:"latency_ms,omitempty"`
	Probes         []openAICodexTicketProbeSummary `json:"probes,omitempty"`
}

// openAICodexTicketProbeSummary deliberately excludes the opaque state blob.
// It is retained only to audit all exits that participated in the selection.
type openAICodexTicketProbeSummary struct {
	ProxyID       int64  `json:"proxy_id,omitempty"`
	ProxyName     string `json:"proxy_name,omitempty"`
	ObservedModel string `json:"observed_model,omitempty"`
	Status        int    `json:"status,omitempty"`
	Valid         bool   `json:"valid"`
	LatencyMs     int64  `json:"latency_ms,omitempty"`
}

func openAICodexTicketKey(accountID int64, model string) string {
	return fmt.Sprintf("%d\x00%s", accountID, strings.TrimSpace(model))
}

func openAICodexTicketExtraKey(model string) string {
	return openAICodexTicketExtraKeyPrefix + strings.TrimSpace(model)
}

func normalizeOpenAICodexTicketModel(model string) string {
	return strings.TrimSpace(model)
}

func normalizeTicketModel(model string) string {
	trimmed := normalizeOpenAICodexTicketModel(model)
	if normalized := normalizeKnownOpenAICodexModel(trimmed); normalized != "" {
		return normalized
	}
	return strings.ToLower(trimmed)
}

func extractOpenAICodexTicketModel(body []byte) string {
	return normalizeOpenAICodexTicketModel(gjson.GetBytes(body, "model").String())
}

func (s *OpenAIGatewayService) openAICodexTicketConfig() config.OpenAICodexTicketConfig {
	cfg := config.OpenAICodexTicketConfig{}
	if s != nil && s.cfg != nil {
		cfg = s.cfg.Gateway.OpenAICodexTicket
	}
	if cfg.TargetLength <= 0 {
		cfg.TargetLength = openAICodexTicketDefaultLength
	}
	if cfg.TTLSeconds <= 0 {
		cfg.TTLSeconds = 3600
	}
	if cfg.RefreshBeforeSeconds <= 0 {
		cfg.RefreshBeforeSeconds = 600
	}
	if cfg.HarvestProbeIntervalSeconds <= 0 {
		cfg.HarvestProbeIntervalSeconds = 6
	}
	if cfg.HarvestAttemptTimeoutSeconds <= 0 {
		cfg.HarvestAttemptTimeoutSeconds = 25
	}
	if len(cfg.Models) == 0 {
		cfg.Models = []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}
	}
	return cfg
}

func (s *OpenAIGatewayService) openAICodexTicketGatedModel(model string) bool {
	model = normalizeOpenAICodexTicketModel(model)
	if model == "" || !s.openAICodexTicketEnabled() {
		return false
	}
	for _, item := range s.openAICodexTicketConfig().Models {
		if normalizeOpenAICodexTicketModel(item) == model {
			return true
		}
	}
	return false
}

// OpenAICodexTicketStatus 是给管理端看的门票摘要，不含 state blob。
type OpenAICodexTicketStatus struct {
	Model            string                         `json:"model"`
	ObservedModel    string                         `json:"observed_model,omitempty"`
	Length           int                            `json:"length,omitempty"`
	Ready            bool                           `json:"ready"`
	RemainingSeconds int64                          `json:"remaining_seconds"`
	Blocked          bool                           `json:"blocked"`
	ProxyID          int64                          `json:"proxy_id,omitempty"`
	ProxyName        string                         `json:"proxy_name,omitempty"`
	LatencyMs        int64                          `json:"latency_ms,omitempty"`
	Fallback         bool                           `json:"fallback"`
	ExpiresAt        *time.Time                     `json:"expires_at,omitempty"`
	Probes           []OpenAICodexTicketProbeStatus `json:"probes,omitempty"`
}

type OpenAICodexTicketProbeStatus struct {
	ProxyID       int64  `json:"proxy_id,omitempty"`
	ProxyName     string `json:"proxy_name,omitempty"`
	ObservedModel string `json:"observed_model,omitempty"`
	Status        int    `json:"status,omitempty"`
	Valid         bool   `json:"valid"`
	LatencyMs     int64  `json:"latency_ms,omitempty"`
}

func OpenAICodexTicketStatuses(account *Account, cfg config.OpenAICodexTicketConfig, now time.Time) []OpenAICodexTicketStatus {
	if !isOpenAICodexTicketAccount(account) {
		return nil
	}
	models, targetLen := append([]string(nil), cfg.Models...), cfg.TargetLength
	if len(models) == 0 {
		models = []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}
	}
	// Include persisted model keys even when the runtime setting was later
	// disabled or narrowed. The admin view is a status audit surface; it must
	// not make a saved ticket look missing just because forwarding is off.
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = normalizeOpenAICodexTicketModel(model)
		if model != "" {
			seen[model] = struct{}{}
		}
	}
	if account.Extra != nil {
		for key := range account.Extra {
			if !IsOpenAICodexTicketExtraKey(key) {
				continue
			}
			model := normalizeOpenAICodexTicketModel(strings.TrimPrefix(key, openAICodexTicketExtraKeyPrefix))
			if model == "" {
				continue
			}
			if _, ok := seen[model]; ok {
				continue
			}
			models = append(models, model)
			seen[model] = struct{}{}
		}
	}
	if targetLen <= 0 {
		targetLen = openAICodexTicketDefaultLength
	}
	out := make([]OpenAICodexTicketStatus, 0, len(models))
	for _, model := range models {
		model = normalizeOpenAICodexTicketModel(model)
		if model == "" {
			continue
		}
		status := OpenAICodexTicketStatus{Model: model}
		ticket := parseOpenAICodexTicketFromAny(0, model, nil)
		if account != nil && account.Extra != nil {
			ticket = parseOpenAICodexTicketFromAny(account.ID, model, account.Extra[openAICodexTicketExtraKey(model)])
		}
		if ticket != nil {
			status.ObservedModel = ticket.ObservedModel
			if status.ObservedModel == "" {
				status.ObservedModel = model
			}
			status.Length = ticket.Length
			status.ProxyID = ticket.ProxyID
			status.ProxyName = ticket.ProxyName
			status.LatencyMs = ticket.LatencyMs
			status.Fallback = normalizeTicketModel(status.ObservedModel) != normalizeTicketModel(model)
			if !ticket.ExpiresAt.IsZero() {
				exp := ticket.ExpiresAt
				status.ExpiresAt = &exp
			}
			if len(ticket.Probes) > 0 {
				status.Probes = make([]OpenAICodexTicketProbeStatus, 0, len(ticket.Probes))
				for _, probe := range ticket.Probes {
					status.Probes = append(status.Probes, OpenAICodexTicketProbeStatus(probe))
				}
			}
		}
		if ticket.valid(now, targetLen) {
			status.Ready = true
			remaining := int64(ticket.ExpiresAt.Sub(now) / time.Second)
			if remaining < 0 {
				remaining = 0
			}
			status.RemainingSeconds = remaining
			status.ExpiresAt = func() *time.Time { exp := ticket.ExpiresAt; return &exp }()
		}
		status.Blocked = cfg.Enabled && OpenAICodexTicketEnabledForAccount(account) && cfg.FailClosed && !status.Ready
		out = append(out, status)
	}
	return out
}

func (s *OpenAIGatewayService) openAICodexTicketEnabled() bool {
	return s.openAICodexTicketEnabledContext(context.Background())
}

func (s *OpenAIGatewayService) openAICodexTicketEnabledContext(ctx context.Context) bool {
	if s == nil {
		return false
	}
	fallback := s.cfg != nil && s.cfg.Gateway.OpenAICodexTicket.Enabled
	if s.settingService != nil {
		return s.settingService.GetOpenAICodexTicketEnabled(ctx, fallback)
	}
	return fallback
}

func (s *OpenAIGatewayService) openAICodexTicketHarvestProxyURL() string {
	return s.openAICodexTicketHarvestProxyURLContext(context.Background())
}

func (s *OpenAIGatewayService) openAICodexTicketHarvestProxyURLContext(ctx context.Context) string {
	if s.settingService != nil {
		if proxy := s.settingService.GetOpenAICodexTicketHarvestProxyURL(ctx); proxy != "" {
			return proxy
		}
	}
	return strings.TrimSpace(s.openAICodexTicketConfig().HarvestProxyURL)
}

func validOpenAICodexTicketState(state string, targetLen int) bool {
	state = strings.TrimSpace(state)
	if state == "" || !strings.HasPrefix(state, openAICodexTicketStatePrefix) {
		return false
	}
	length := len(state)
	// 292 was the original format. Current upstream responses observed in
	// production also use 312 and 356 bytes. Keep explicit bounds so a
	// malformed or unexpectedly huge header is never persisted as a ticket.
	if targetLen > 0 && targetLen != openAICodexTicketDefaultLength {
		if length != targetLen {
			return false
		}
	} else if length < openAICodexTicketMinLength || length > openAICodexTicketMaxLength {
		return false
	}
	for i := 0; i < len(state); i++ {
		c := state[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func (t *openAICodexTicket) valid(now time.Time, targetLen int) bool {
	if t == nil {
		return false
	}
	state := strings.TrimSpace(t.State)
	if t.Length != len(state) || !validOpenAICodexTicketState(state, targetLen) {
		return false
	}
	if t.ExpiresAt.IsZero() || !now.Before(t.ExpiresAt) {
		return false
	}
	return true
}

func (t *openAICodexTicket) needsRefresh(now time.Time, refreshBefore time.Duration) bool {
	if t == nil || t.ExpiresAt.IsZero() {
		return true
	}
	return !t.ExpiresAt.After(now.Add(refreshBefore))
}

func (s *OpenAIGatewayService) lookupOpenAICodexTicket(account *Account, model string) *openAICodexTicket {
	if s == nil || account == nil || account.ID <= 0 {
		return nil
	}
	model = normalizeOpenAICodexTicketModel(model)
	if model == "" {
		return nil
	}
	key := openAICodexTicketKey(account.ID, model)
	targetLen := openAICodexTicketDefaultLength
	if s != nil {
		targetLen = s.openAICodexTicketConfig().TargetLength
	}
	now := time.Now()
	var mem *openAICodexTicket
	if raw, ok := s.openaiCodexTickets.Load(key); ok {
		mem, _ = raw.(*openAICodexTicket)
	}
	var extra *openAICodexTicket
	if account.Extra != nil {
		extra = parseOpenAICodexTicketFromAny(account.ID, model, account.Extra[openAICodexTicketExtraKey(model)])
	}
	if extra.valid(now, targetLen) && (mem == nil || extra.CapturedAt.After(mem.CapturedAt)) {
		s.openaiCodexTickets.Store(key, extra)
		return extra
	}
	if mem.valid(now, targetLen) {
		return mem
	}
	if extra != nil {
		s.openaiCodexTickets.Store(key, extra)
		return extra
	}
	if mem != nil {
		s.openaiCodexTickets.Delete(key)
	}
	return nil
}

func parseOpenAICodexTicketFromAny(accountID int64, model string, raw any) *openAICodexTicket {
	if raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var ticket openAICodexTicket
	if err := json.Unmarshal(b, &ticket); err != nil {
		return nil
	}
	ticket.AccountID = accountID
	if strings.TrimSpace(model) != "" {
		ticket.Model = model
	}
	if strings.TrimSpace(ticket.RequestedModel) == "" {
		ticket.RequestedModel = ticket.Model
	}
	if strings.TrimSpace(ticket.ObservedModel) == "" {
		ticket.ObservedModel = ticket.Model
	}
	ticket.State = strings.TrimSpace(ticket.State)
	if ticket.Length == 0 {
		ticket.Length = len(ticket.State)
	}
	if ticket.State == "" {
		return nil
	}
	return &ticket
}

func (s *OpenAIGatewayService) storeOpenAICodexTicket(ctx context.Context, account *Account, ticket *openAICodexTicket) {
	if s == nil || account == nil || ticket == nil || account.ID <= 0 {
		return
	}
	model := normalizeOpenAICodexTicketModel(ticket.Model)
	ticket.Model = model
	ticket.AccountID = account.ID
	s.openaiCodexTickets.Store(openAICodexTicketKey(account.ID, model), ticket)
	if account.Extra == nil {
		account.Extra = make(map[string]any)
	}
	account.Extra[openAICodexTicketExtraKey(model)] = ticket
	if s.accountRepo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.accountRepo.UpdateExtra(ctx, account.ID, map[string]any{
		openAICodexTicketExtraKey(model): ticket,
	}); err != nil {
		logger.L().Warn("openai_codex_ticket persist failed",
			zap.Int64("account_id", account.ID),
			zap.String("model", model),
			zap.Error(err),
		)
	}
}

// applyOpenAICodexTicket 在出站请求上覆盖 x-codex-turn-state。
// 请求路径只注入已捕获的有效门票，不现场打票；无票则返回
// ErrOpenAICodexTicketUnavailable。打票由后台 harvester 完成。
func (s *OpenAIGatewayService) applyOpenAICodexTicket(ctx context.Context, account *Account, model string, h http.Header) error {
	if s == nil || h == nil || !OpenAICodexTicketEnabledForAccount(account) || !s.openAICodexTicketEnabledContext(ctx) {
		return nil
	}
	model = normalizeOpenAICodexTicketModel(model)
	if model == "" || !s.openAICodexTicketGatedModel(model) {
		return nil
	}
	cfg := s.openAICodexTicketConfig()
	ticket := s.lookupOpenAICodexTicket(account, model)
	if ticket.valid(time.Now(), cfg.TargetLength) {
		h.Set(openAICodexTurnStateHeader, ticket.State)
		return nil
	}
	if !cfg.FailClosed {
		return nil
	}
	return ErrOpenAICodexTicketUnavailable
}

// openAICodexTicketOutboundModel 预测本请求真正出站的模型名，也就是
// applyOpenAICodexTicket 注入时读到的 body.model。
//
// 调度门控与注入必须按同一个模型名判定门票。普通请求下二者同源：Forward 的
// upstreamModel 与本函数都走 resolveOpenAIAccountUpstreamModelForRequest，且
// Forward 会把 body.model 改写成该值后才注入。但 /responses/compact 例外——
// Forward 会把出站模型进一步改写为 compact 映射或 gateway.openai_compact_model
// （默认非空），此时若门控仍按客户端原始模型判定，就会把「实际出站是非门控
// 模型、根本不需要票」的 compact 请求整片误拦成不可调度。
func (s *OpenAIGatewayService) openAICodexTicketOutboundModel(account *Account, requestedModel string, requireCompact bool) string {
	model := strings.TrimSpace(requestedModel)
	if account == nil || model == "" {
		return model
	}
	if !account.IsOpenAI() {
		return canonicalOpenAIAccountSchedulingModel(account, model)
	}
	_, upstreamModel := resolveOpenAIForwardMappedModels(account, model, requireCompact)
	if requireCompact {
		// 与 Forward 同序：compact 兜底模型优先于普通/compact 映射结果。
		if compactModel := strings.TrimSpace(s.resolveOpenAICompactFallbackModel(account, model)); compactModel != "" {
			upstreamModel = compactModel
		}
	}
	if upstreamModel = strings.TrimSpace(upstreamModel); upstreamModel != "" {
		return upstreamModel
	}
	return model
}

// outboundModel 必须是真正会发给上游的模型名（openAICodexTicketOutboundModel），
// 不是客户端原始模型：注入侧读的是出站 body.model，两侧口径必须一致。
func (s *OpenAIGatewayService) openAICodexTicketBlocksAccount(account *Account, outboundModel string) bool {
	if s == nil || !OpenAICodexTicketEnabledForAccount(account) || !s.openAICodexTicketEnabled() {
		return false
	}
	cfg := s.openAICodexTicketConfig()
	if !cfg.FailClosed {
		return false
	}
	model := normalizeOpenAICodexTicketModel(outboundModel)
	if !s.openAICodexTicketGatedModel(model) {
		return false
	}
	ticket := s.lookupOpenAICodexTicket(account, model)
	return !ticket.valid(time.Now(), cfg.TargetLength)
}

type openAICodexTicketProbeResult struct {
	State         string
	Status        int
	ObservedModel string
	LatencyMs     int64
}

// fireOpenAICodexTicketProbe keeps the original narrow test/helper contract.
// The detailed variant below is used by the multi-egress harvester.
func (s *OpenAIGatewayService) fireOpenAICodexTicketProbe(ctx context.Context, account *Account, token, model, proxyURL string, attemptTimeout time.Duration) (state string, status int, err error) {
	result, err := s.fireOpenAICodexTicketProbeDetailed(ctx, account, token, model, proxyURL, attemptTimeout)
	if err != nil {
		return "", 0, err
	}
	return result.State, result.Status, nil
}

func (s *OpenAIGatewayService) fireOpenAICodexTicketProbeDetailed(ctx context.Context, account *Account, token, model, proxyURL string, attemptTimeout time.Duration) (openAICodexTicketProbeResult, error) {
	startedAt := time.Now()
	attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()

	body := []byte(`{"model":` + jsonString(model) + `,"store":false,"stream":true,"instructions":"Reply with exactly: pong","input":[{"role":"user","content":[{"type":"input_text","text":"ping"}]}]}`)
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, chatgptCodexURL, bytes.NewReader(body))
	if err != nil {
		return openAICodexTicketProbeResult{}, err
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAIHarvest))
	req.Close = true
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("session_id", uuid.NewString())
	if err := resolveAndSetOpenAIChatGPTAccountHeaders(attemptCtx, s.accountRepo, req.Header, account); err != nil {
		return openAICodexTicketProbeResult{}, err
	}
	applyOpenAICodexTicketHarvestIdentity(req.Header, model)

	// Synthetic probes must use the dedicated no-reuse transport even when the
	// production account is bound to a plugin. This also avoids reading pluginManager
	// while handlers are still wiring it during gateway construction.
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return openAICodexTicketProbeResult{}, err
	}
	if resp == nil {
		return openAICodexTicketProbeResult{}, errors.New("nil upstream response")
	}
	// Read only until the first SSE/JSON event that identifies the actual model.
	// Closing immediately after that keeps the metric close to first-token
	// latency instead of waiting for the full synthetic response.
	observedModel := ""
	if resp.Body != nil {
		observedModel = readOpenAICodexProbeModel(io.LimitReader(resp.Body, 128*1024))
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	return openAICodexTicketProbeResult{
		State:         extractOpenAICodexTurnState(resp.Header),
		Status:        resp.StatusCode,
		ObservedModel: observedModel,
		LatencyMs:     time.Since(startedAt).Milliseconds(),
	}, nil
}

func readOpenAICodexProbeModel(r io.Reader) string {
	if r == nil {
		return ""
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 128*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line == "data: [DONE]" || line == "[DONE]" {
			continue
		}
		if model := extractOpenAICodexProbeModel([]byte(line)); model != "" {
			return model
		}
	}
	return ""
}

func extractOpenAICodexProbeModel(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	readModel := func(raw []byte) string {
		for _, path := range []string{"model", "response.model", "response.output.model"} {
			if model := strings.TrimSpace(gjson.GetBytes(raw, path).String()); model != "" {
				return model
			}
		}
		return ""
	}
	if model := readModel(body); model != "" {
		return model
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "data:")
		line = strings.TrimSpace(line)
		if line == "" || line == "[DONE]" {
			continue
		}
		if model := readModel([]byte(line)); model != "" {
			return model
		}
	}
	return ""
}

func jsonString(v string) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `""`
	}
	return string(b)
}

func applyOpenAICodexTicketHarvestIdentity(h http.Header, model string) {
	ensureCodexIdentityHeaders(h)
	enforceCodexIdentityHeaders(h)
	version := strings.TrimSpace(h.Get("version"))
	if needsOpenAICodexAstraVersion(model) && (version == "" || CompareVersions(version, openAICodexAstraMinVersion) < 0) {
		h.Set("version", openAICodexAstraMinVersion)
		h.Set("user-agent", buildCodexCLIUserAgent(openAICodexAstraMinVersion))
		h.Set("originator", openai.CodexDefaultOriginator)
	}
}

func needsOpenAICodexAstraVersion(model string) bool {
	m := strings.ToLower(normalizeOpenAICodexTicketModel(model))
	return strings.Contains(m, "gpt-6") || strings.Contains(m, "astra")
}

func (s *OpenAIGatewayService) StartOpenAICodexTicketHarvester() {
	if s == nil {
		return
	}
	s.openaiCodexTicketLifecycleMu.Lock()
	defer s.openaiCodexTicketLifecycleMu.Unlock()
	if s.openaiCodexTicketStopped || s.openaiCodexTicketDone != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.openaiCodexTicketCancel = cancel
	s.openaiCodexTicketDone = done
	go func() {
		defer close(done)
		s.openAICodexTicketHarvestLoop(ctx)
	}()
	logger.L().Info("openai_codex_ticket harvester started",
		zap.Int("ttl_seconds", s.openAICodexTicketConfig().TTLSeconds),
		zap.Int("target_length", s.openAICodexTicketConfig().TargetLength),
		zap.Strings("models", s.openAICodexTicketConfig().Models),
	)
}

func (s *OpenAIGatewayService) StopOpenAICodexTicketHarvester() {
	if s == nil {
		return
	}
	s.openaiCodexTicketLifecycleMu.Lock()
	s.openaiCodexTicketStopped = true
	cancel, done := s.openaiCodexTicketCancel, s.openaiCodexTicketDone
	s.openaiCodexTicketLifecycleMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (s *OpenAIGatewayService) openAICodexTicketHarvestLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.refreshOpenAICodexTickets(ctx)
			timer.Reset(time.Duration(s.openAICodexTicketConfig().HarvestProbeIntervalSeconds) * time.Second)
		}
	}
}

// refreshOpenAICodexTickets probes each account/model with a missing or soon-to-expire
// ticket once. The loop waits for all probes, then waits the configured interval
// before starting the next cycle.
func (s *OpenAIGatewayService) refreshOpenAICodexTickets(ctx context.Context) {
	if s == nil || s.accountRepo == nil || ctx.Err() != nil || !s.openAICodexTicketEnabledContext(ctx) {
		return
	}
	accounts, err := s.accountRepo.ListByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		logger.L().Warn("openai_codex_ticket list accounts failed", zap.Error(err))
		return
	}
	cfg := s.openAICodexTicketConfig()
	now := time.Now()
	refreshBefore := time.Duration(cfg.RefreshBeforeSeconds) * time.Second
	var wg sync.WaitGroup
	probed := 0
	for i := range accounts {
		account := accounts[i]
		if account.Status != StatusActive || !OpenAICodexTicketEnabledForAccount(&account) {
			continue
		}
		models := cfg.Models
		if len(models) == 0 {
			models = []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}
		}
		for _, model := range models {
			model := normalizeOpenAICodexTicketModel(model)
			if model == "" {
				continue
			}
			// 已有一张有效且未临近过期的票 → 本周期不打，省得白刷。
			if t := s.lookupOpenAICodexTicket(&account, model); t.valid(now, cfg.TargetLength) && !t.needsRefresh(now, refreshBefore) {
				continue
			}
			acc := account
			// Token/header helpers may update account metadata; each model owns its maps.
			acc.Extra = maps.Clone(account.Extra)
			acc.Credentials = maps.Clone(account.Credentials)
			probed++
			wg.Add(1)
			go func(acc Account, model string) {
				defer wg.Done()
				_, _ = s.refreshOpenAICodexTicketForAccount(ctx, &acc, []string{model}, false)
			}(acc, model)
		}
	}
	wg.Wait()
	if probed > 0 {
		logger.L().Info("openai_codex_ticket probe cycle", zap.Int("probed", probed))
	}
}

type openAICodexTicketProbeTarget struct {
	ProxyID   int64
	ProxyName string
	ProxyURL  string
}

type openAICodexTicketCandidate struct {
	openAICodexTicketProbeResult
	ProxyID   int64
	ProxyName string
}

func (s *OpenAIGatewayService) codexTicketProbeTargets(account *Account, fallbackProxyURL string) []openAICodexTicketProbeTarget {
	if account == nil {
		return nil
	}
	targets := make([]openAICodexTicketProbeTarget, 0)
	seen := make(map[string]struct{})
	for _, binding := range account.UsableProxyBindings() {
		if binding.Proxy == nil {
			continue
		}
		proxyURL := binding.Proxy.URL()
		if proxyURL == "" {
			continue
		}
		key := fmt.Sprintf("%d:%s", binding.ProxyID, proxyURL)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, openAICodexTicketProbeTarget{
			ProxyID: binding.ProxyID, ProxyName: binding.Proxy.Name, ProxyURL: proxyURL,
		})
	}
	if len(targets) > 0 {
		return targets
	}
	if proxy := account.requestProxy(); proxy != nil {
		proxyURL := proxy.URL()
		if proxyURL != "" {
			return []openAICodexTicketProbeTarget{{ProxyID: proxy.ID, ProxyName: proxy.Name, ProxyURL: proxyURL}}
		}
	}
	if strings.TrimSpace(fallbackProxyURL) != "" {
		return []openAICodexTicketProbeTarget{{ProxyURL: strings.TrimSpace(fallbackProxyURL)}}
	}
	return nil
}

func chooseOpenAICodexTicketCandidate(candidates []openAICodexTicketCandidate, requestedModel string) *openAICodexTicketCandidate {
	if len(candidates) == 0 {
		return nil
	}
	target := normalizeTicketModel(requestedModel)
	chooseFastest := func(items []openAICodexTicketCandidate) *openAICodexTicketCandidate {
		if len(items) == 0 {
			return nil
		}
		selected := items[0]
		for _, candidate := range items[1:] {
			if candidate.LatencyMs < selected.LatencyMs || (candidate.LatencyMs == selected.LatencyMs && candidate.ProxyID > 0 && (selected.ProxyID <= 0 || candidate.ProxyID < selected.ProxyID)) {
				selected = candidate
			}
		}
		return &selected
	}
	correct := make([]openAICodexTicketCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if normalizeTicketModel(candidate.ObservedModel) == target {
			correct = append(correct, candidate)
		}
	}
	if selected := chooseFastest(correct); selected != nil {
		return selected
	}
	// If no egress returned the requested model, explicitly fall back to the
	// fastest valid observed model (normally gpt-5.6-luna) instead of discarding
	// every usable ticket and blocking the account.
	return chooseFastest(candidates)
}

func (s *OpenAIGatewayService) refreshOpenAICodexTicketForAccount(ctx context.Context, account *Account, models []string, force bool) ([]OpenAICodexTicketStatus, error) {
	if s == nil || account == nil || !isOpenAICodexTicketAccount(account) || ctx.Err() != nil {
		return nil, nil
	}
	if !OpenAICodexTicketEnabledForAccount(account) {
		return nil, ErrOpenAICodexTicketAccountDisabled
	}
	if !force && !s.openAICodexTicketEnabledContext(ctx) {
		return nil, nil
	}
	cfg := s.openAICodexTicketConfig()
	if len(models) == 0 {
		models = cfg.Models
	}
	if len(models) == 0 {
		models = []string{openAICodexTicketDefaultModel, openAICodexTicketDefaultSolModel}
	}
	token, _, err := s.GetAccessToken(ctx, account)
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, err
	}
	targets := s.codexTicketProbeTargets(account, s.openAICodexTicketHarvestProxyURLContext(ctx))
	if len(targets) == 0 {
		return nil, errors.New("no usable proxy exit for Codex Ticket probe")
	}
	for _, model := range models {
		model = normalizeOpenAICodexTicketModel(model)
		if model == "" {
			continue
		}
		now := time.Now()
		if !force {
			if existing := s.lookupOpenAICodexTicket(account, model); existing.valid(now, cfg.TargetLength) && !existing.needsRefresh(now, time.Duration(cfg.RefreshBeforeSeconds)*time.Second) {
				continue
			}
		}
		candidates := make([]openAICodexTicketCandidate, 0, len(targets))
		probeSummaries := make([]openAICodexTicketProbeSummary, 0, len(targets))
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, target := range targets {
			target := target
			wg.Add(1)
			go func() {
				defer wg.Done()
				probeAccount := *account
				probeAccount.Extra = maps.Clone(account.Extra)
				probeAccount.Credentials = maps.Clone(account.Credentials)
				probe, probeErr := s.fireOpenAICodexTicketProbeDetailed(ctx, &probeAccount, token, model, target.ProxyURL, time.Duration(cfg.HarvestAttemptTimeoutSeconds)*time.Second)
				mu.Lock()
				probeSummary := openAICodexTicketProbeSummary{ProxyID: target.ProxyID, ProxyName: target.ProxyName}
				if probeErr == nil {
					probeSummary.ObservedModel = probe.ObservedModel
					probeSummary.Status = probe.Status
					probeSummary.LatencyMs = probe.LatencyMs
					probeSummary.Valid = probe.Status == http.StatusOK && validOpenAICodexTicketState(probe.State, cfg.TargetLength) && strings.TrimSpace(probe.ObservedModel) != ""
				}
				probeSummaries = append(probeSummaries, probeSummary)
				if probeErr == nil && probeSummary.Valid {
					candidates = append(candidates, openAICodexTicketCandidate{openAICodexTicketProbeResult: probe, ProxyID: target.ProxyID, ProxyName: target.ProxyName})
				}
				mu.Unlock()
			}()
		}
		wg.Wait()
		sort.Slice(probeSummaries, func(i, j int) bool {
			if probeSummaries[i].ProxyID != probeSummaries[j].ProxyID {
				return probeSummaries[i].ProxyID < probeSummaries[j].ProxyID
			}
			return probeSummaries[i].ProxyName < probeSummaries[j].ProxyName
		})
		selected := chooseOpenAICodexTicketCandidate(candidates, model)
		if selected == nil {
			continue
		}
		now = time.Now()
		ticket := &openAICodexTicket{
			AccountID: account.ID, Model: model, RequestedModel: model,
			ObservedModel: selected.ObservedModel, State: selected.State,
			Length: len(selected.State), CapturedAt: now,
			ExpiresAt: now.Add(time.Duration(cfg.TTLSeconds) * time.Second), Attempts: len(probeSummaries),
			ProxyID: selected.ProxyID, ProxyName: selected.ProxyName, LatencyMs: selected.LatencyMs,
			Probes: probeSummaries,
		}
		s.storeOpenAICodexTicket(ctx, account, ticket)
		logger.L().Info("openai_codex_ticket harvested across account exits",
			zap.Int64("account_id", account.ID), zap.String("requested_model", model),
			zap.String("observed_model", selected.ObservedModel), zap.Int("attempts", len(probeSummaries)),
			zap.Int("valid_candidates", len(candidates)),
			zap.Int64("latency_ms", selected.LatencyMs), zap.Int64("proxy_id", selected.ProxyID))
	}
	return OpenAICodexTicketStatuses(account, cfg, time.Now()), nil
}

// RefreshOpenAICodexTicket is the explicit admin operation. It always probes
// every usable account exit for the selected model, even when the current
// ticket is still valid, so a faster or newly-correct exit can replace it.
func (s *OpenAIGatewayService) RefreshOpenAICodexTicket(ctx context.Context, accountID int64, model string) ([]OpenAICodexTicketStatus, error) {
	if s == nil || s.accountRepo == nil || accountID <= 0 {
		return nil, errors.New("codex ticket refresh is unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil || !isOpenAICodexTicketAccount(account) {
		return nil, errors.New("account is not an OpenAI OAuth account")
	}
	return s.refreshOpenAICodexTicketForAccount(ctx, account, []string{model}, true)
}

// probeOnceOpenAICodexTicket 走打票代理打一发。命中合格 292（HTTP 200、长度==target、
// gAAAAA 前缀）就落库；否则记 Info miss，交给下个周期重试。同一 key 并发去重，避免上一发还没
// 回来又叠一发。
func (s *OpenAIGatewayService) probeOnceOpenAICodexTicket(ctx context.Context, account *Account, model string) {
	if s == nil || !OpenAICodexTicketEnabledForAccount(account) || ctx.Err() != nil || !s.openAICodexTicketEnabledContext(ctx) {
		return
	}
	cfg := s.openAICodexTicketConfig()
	proxyURL := s.openAICodexTicketHarvestProxyURLContext(ctx)
	if proxyURL == "" || s.httpUpstream == nil || ctx.Err() != nil {
		return
	}
	key := openAICodexTicketKey(account.ID, model)
	_, _, _ = s.openaiCodexTicketFlight.Do(key, func() (any, error) {
		token, _, err := s.GetAccessToken(ctx, account)
		if err != nil || strings.TrimSpace(token) == "" {
			logger.L().Info("openai_codex_ticket probe miss",
				zap.Int64("account_id", account.ID), zap.String("model", model),
				zap.String("reason", "token"), zap.Error(err))
			return nil, nil
		}
		probe, perr := s.fireOpenAICodexTicketProbeDetailed(ctx, account, token, model, proxyURL, time.Duration(cfg.HarvestAttemptTimeoutSeconds)*time.Second)
		if perr != nil {
			logger.L().Info("openai_codex_ticket probe miss",
				zap.Int64("account_id", account.ID), zap.String("model", model),
				zap.String("reason", "error"), zap.Error(perr))
			return nil, nil
		}
		if probe.Status != http.StatusOK || !validOpenAICodexTicketState(probe.State, cfg.TargetLength) {
			logger.L().Info("openai_codex_ticket probe miss",
				zap.Int64("account_id", account.ID), zap.String("model", model),
				zap.Int("http", probe.Status), zap.Int("len", len(probe.State)))
			return nil, nil
		}
		now := time.Now()
		observedModel := probe.ObservedModel
		if observedModel == "" {
			observedModel = model
		}
		ticket := &openAICodexTicket{
			AccountID:      account.ID,
			Model:          model,
			RequestedModel: model,
			ObservedModel:  observedModel,
			State:          probe.State,
			Length:         len(probe.State),
			CapturedAt:     now,
			ExpiresAt:      now.Add(time.Duration(cfg.TTLSeconds) * time.Second),
			Attempts:       1,
			LatencyMs:      probe.LatencyMs,
		}
		s.storeOpenAICodexTicket(ctx, account, ticket)
		logger.L().Info("openai_codex_ticket harvested",
			zap.Int64("account_id", account.ID), zap.String("model", model),
			zap.Int("length", ticket.Length), zap.String("mode", "continuous"))
		return nil, nil
	})
}

// IsOpenAICodexTicketExtraKey identifies server-managed ticket material.
func IsOpenAICodexTicketExtraKey(key string) bool {
	return strings.HasPrefix(key, openAICodexTicketExtraKeyPrefix)
}

// MergeOpenAICodexTicketExtra preserves only persisted tickets, never summaries or
// blobs supplied by an account edit. The repository repeats this under the row
// lock so a concurrent harvest cannot be overwritten by a stale admin snapshot.
func MergeOpenAICodexTicketExtra(extra, current map[string]any) map[string]any {
	result := maps.Clone(extra)
	for key := range result {
		if IsOpenAICodexTicketPrivateExtraKey(key) {
			delete(result, key)
		}
	}
	for key, value := range current {
		if IsOpenAICodexTicketExtraKey(key) || key == OpenAICodexTicketEnabledExtraKey {
			if result == nil {
				result = make(map[string]any)
			}
			result[key] = value
		}
	}
	return result
}

// ValidateOpenAICodexTicketHarvestProxyURL validates only syntax, without making
// a network request or including credentials in validation errors.
func ValidateOpenAICodexTicketHarvestProxyURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("harvest proxy must be an HTTP(S) or SOCKS5(h) URL with a host and no path, query or fragment")
	}
	switch parsed.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return errors.New("harvest proxy scheme must be http, https, socks5 or socks5h")
	}
	if port := parsed.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("harvest proxy port must be between 1 and 65535")
		}
	}
	return nil
}

// MaskProxyURL never returns a stored proxy password, even for invalid legacy data.
func MaskProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || ValidateOpenAICodexTicketHarvestProxyURL(raw) != nil {
		return ""
	}
	parsed, _ := url.Parse(raw)
	if parsed.User != nil {
		if _, ok := parsed.User.Password(); ok {
			parsed.User = url.UserPassword(parsed.User.Username(), "***")
		}
	}
	return parsed.String()
}

// IsMaskedProxyURL recognizes the exact password placeholder emitted by the API.
func IsMaskedProxyURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return false
	}
	password, ok := parsed.User.Password()
	return ok && password == "***"
}

// Credential shadows do not own tickets. Keep their existing forwarding policy
// instead of imposing a gate for a key the harvester never populates.
func isOpenAICodexTicketAccount(account *Account) bool {
	return account != nil && account.IsOpenAIOAuthLike() && !account.IsShadow()
}

// OpenAICodexTicketEnabledForAccount is deliberately strict: only a boolean
// true written by the dedicated admin endpoint enables ticket behavior.
// Missing, imported, string, or numeric values all remain disabled.
func OpenAICodexTicketEnabledForAccount(account *Account) bool {
	if !isOpenAICodexTicketAccount(account) || account.Extra == nil {
		return false
	}
	enabled, ok := account.Extra[OpenAICodexTicketEnabledExtraKey].(bool)
	return ok && enabled
}

func IsOpenAICodexTicketAccount(account *Account) bool {
	return isOpenAICodexTicketAccount(account)
}

// IsOpenAICodexTicketPrivateExtraKey also covers the retired account-level proxy
// override, whose credentials may remain in older account records.
func IsOpenAICodexTicketPrivateExtraKey(key string) bool {
	return IsOpenAICodexTicketExtraKey(key) || key == "codex_harvest_proxy_url" || key == OpenAICodexTicketEnabledExtraKey
}

// RedactOpenAICodexTicketExtra strips ephemeral ticket material from exports
// without changing the source account or unrelated backup fields.
func RedactOpenAICodexTicketExtra(extra map[string]any) map[string]any {
	redacted := maps.Clone(extra)
	for key := range redacted {
		if IsOpenAICodexTicketPrivateExtraKey(key) {
			delete(redacted, key)
		}
	}
	return redacted
}
