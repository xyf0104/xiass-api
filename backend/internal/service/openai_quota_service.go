package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/imroc/req/v3"
)

// ErrSparkShadowResetNotSupported is returned when ResetCredit is called on a
// spark shadow account. Shadow accounts do not hold credentials of their own;
// the caller must reset the parent account directly. It is a structured
// infraerrors value so the handler maps it to 409 Conflict (not a bare 500);
// errors.Is still matches it by identity since ResetCredit returns this var.
var ErrSparkShadowResetNotSupported = infraerrors.New(http.StatusConflict, "SPARK_SHADOW_RESET_NOT_SUPPORTED", "spark shadow account does not support credit reset; reset the parent account")

// Endpoints used by the OpenAI/ChatGPT/Codex quota query and reset feature.
const (
	chatGPTUsageURL                = "https://chatgpt.com/backend-api/wham/usage"
	chatGPTRateLimitCreditsURL     = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
	chatGPTRateLimitResetURL       = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
	openaiQuotaUpstreamTimeout     = 20 * time.Second
	openaiQuotaCodexBeta           = "codex-1"
	openaiQuotaCodexOriginator     = "Codex Desktop"
	openaiQuotaCodexLanguageTag    = "zh-CN"
	openaiQuotaSecFetchSite        = "none"
	openaiQuotaSecFetchMode        = "no-cors"
	openaiQuotaSecFetchDest        = "empty"
	openaiQuotaResetCreditsKey     = "codex_reset_credit_snapshot"
	openaiQuotaResetCreditsTimeKey = "codex_reset_credit_snapshot_updated_at"
)

// OpenAIRateLimitWindow describes a single rate-limit window returned by
// /wham/usage. The upstream returns an explicit `null` window when the slot
// is unused, so consumers should treat a nil pointer as "no data".
type OpenAIRateLimitWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
	invalidUsedPercent bool
}

// Missing/null percentages are not evidence of a recovered zero-use window.
func (w *OpenAIRateLimitWindow) UnmarshalJSON(data []byte) error {
	type window OpenAIRateLimitWindow
	var decoded struct {
		window
		UsedPercent *float64 `json:"used_percent"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*w = OpenAIRateLimitWindow(decoded.window)
	w.invalidUsedPercent = decoded.UsedPercent == nil
	if decoded.UsedPercent != nil {
		w.UsedPercent = *decoded.UsedPercent
	}
	return nil
}

// OpenAIRateLimit is a rate-limit envelope (primary + optional secondary window).
type OpenAIRateLimit struct {
	Allowed         bool                   `json:"allowed"`
	LimitReached    bool                   `json:"limit_reached"`
	PrimaryWindow   *OpenAIRateLimitWindow `json:"primary_window,omitempty"`
	SecondaryWindow *OpenAIRateLimitWindow `json:"secondary_window,omitempty"`
}

// OpenAIAdditionalRateLimit describes a per-feature rate limit (e.g. Codex Spark).
type OpenAIAdditionalRateLimit struct {
	LimitName      string           `json:"limit_name"`
	MeteredFeature string           `json:"metered_feature"`
	RateLimit      *OpenAIRateLimit `json:"rate_limit,omitempty"`
}

// OpenAIRateLimitResetCreditDetail is the sanitized metadata surfaced for one
// available reset credit. Do not add upstream ids or tokens here.
type OpenAIRateLimitResetCreditDetail struct {
	ExpiresAt string `json:"expires_at,omitempty"`
}

// OpenAIRateLimitResetCredits captures the "available_count" surfaced for the
// rate_limit_reset_credit grant type, which the reset action consumes.
type OpenAIRateLimitResetCredits struct {
	AvailableCount  int                                `json:"available_count"`
	Credits         []OpenAIRateLimitResetCreditDetail `json:"credits,omitempty"`
	requestIdentity *openAIQuotaRequestIdentity
}

// OpenAIQuotaUsage is the typed projection of /wham/usage we expose to the UI.
// Fields not relevant to the quota card are intentionally omitted to keep the
// surface narrow; full upstream payload preservation is unnecessary.
type OpenAIQuotaUsage struct {
	UserID                string                       `json:"user_id,omitempty"`
	AccountID             string                       `json:"account_id,omitempty"`
	Email                 string                       `json:"email,omitempty"`
	PlanType              string                       `json:"plan_type,omitempty"`
	RateLimit             *OpenAIRateLimit             `json:"rate_limit,omitempty"`
	AdditionalRateLimits  []OpenAIAdditionalRateLimit  `json:"additional_rate_limits,omitempty"`
	RateLimitResetCredits *OpenAIRateLimitResetCredits `json:"rate_limit_reset_credits,omitempty"`
	FetchedAt             int64                        `json:"fetched_at"`
	requestIdentity       *openAIQuotaRequestIdentity
	resetCandidates       []openAIResetCreditCandidate
}

// Kept out of JSON and never reconstructed from a later account read.
type openAIQuotaRequestIdentity struct {
	target     *Account
	credential *Account
	observedAt time.Time
}

// OpenAIQuotaResetCredit captures the redeemed credit metadata returned by the
// reset endpoint.
type OpenAIQuotaResetCredit struct {
	ID              string `json:"id,omitempty"`
	ResetType       string `json:"reset_type,omitempty"`
	Status          string `json:"status,omitempty"`
	GrantedAt       string `json:"granted_at,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	RedeemStartedAt string `json:"redeem_started_at,omitempty"`
	RedeemedAt      string `json:"redeemed_at,omitempty"`
}

// OpenAIQuotaResetResult is the typed projection of /wham/rate-limit-reset-credits/consume.
// The inner Credit also carries `redeemed_at` (RFC3339 string); we deliberately do
// NOT add a top-level redeemed_at to avoid ambiguity with the nested field.
type OpenAIQuotaResetResult struct {
	Code         string                  `json:"code"`
	Credit       *OpenAIQuotaResetCredit `json:"credit,omitempty"`
	WindowsReset int                     `json:"windows_reset"`
	// Internal handoff: the HTTP handler must not repeat coordinated recovery.
	PostResetQuota *OpenAIQuotaUsage `json:"-"`
}

// OpenAIQuotaService queries and consumes ChatGPT/Codex rate-limit reset credits
// for OpenAI OAuth accounts. It reuses the privacy client factory so all calls
// flow through the impersonated HTTP client (Cloudflare-friendly TLS fingerprint).
type OpenAIQuotaService struct {
	accountRepo          AccountRepository
	proxyRepo            ProxyRepository
	tokenProvider        *OpenAITokenProvider
	privacyClientFactory PrivacyClientFactory
	agentIdentityTaskMu  sync.Mutex
	agentIdentityWS      agentIdentityWSConnectionInvalidator
	autoReset            *OpenAIAutoResetService
}

// NewOpenAIQuotaService constructs a quota service. token provider is required —
// it ensures we always invoke upstream with a valid (refreshed-if-needed)
// access_token, sharing the same refresh/locking machinery used by the gateway.
func NewOpenAIQuotaService(
	accountRepo AccountRepository,
	proxyRepo ProxyRepository,
	tokenProvider *OpenAITokenProvider,
	privacyClientFactory PrivacyClientFactory,
) *OpenAIQuotaService {
	return &OpenAIQuotaService{
		accountRepo:          accountRepo,
		proxyRepo:            proxyRepo,
		tokenProvider:        tokenProvider,
		privacyClientFactory: privacyClientFactory,
	}
}

// QueryUsage fetches the latest rate-limit/usage snapshot for the given OpenAI
// OAuth account. Returns infraerrors so the handler layer can map them to
// stable error codes / HTTP statuses.
func (s *OpenAIQuotaService) QueryUsage(ctx context.Context, accountID int64) (*OpenAIQuotaUsage, error) {
	identity := &openAIQuotaRequestIdentity{}
	accessToken, chatGPTAccountID, proxyURL, fedRAMP, err := s.prepareUpstreamCall(ctx, accountID, identity)
	if err != nil {
		return nil, err
	}

	client, err := s.privacyClientFactory(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_QUOTA_CLIENT_ERROR", "failed to build upstream client: %v", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, openaiQuotaUpstreamTimeout)
	defer cancel()
	agentIdentity := identity.credential.IsOpenAIAgentIdentity()

	var payload OpenAIQuotaUsage
	var quotaHeaders map[string]string
	for recovered := false; ; {
		var expectedTaskID string
		var headerErr error
		quotaHeaders, expectedTaskID, headerErr = buildBoundCodexQuotaHeaders(identity.credential, accessToken, chatGPTAccountID, fedRAMP)
		if headerErr != nil {
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_QUOTA_AUTH_FAILED", "failed to build upstream authentication: %v", headerErr)
		}
		resp, err := client.R().
			SetContext(callCtx).
			SetHeaders(quotaHeaders).
			SetSuccessResult(&payload).
			Get(chatGPTUsageURL)
		if err != nil {
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_QUOTA_REQUEST_FAILED", "upstream request failed: %v", err)
		}
		if !resp.IsSuccessState() {
			if agentIdentity && !recovered && isAgentIdentityTaskInvalidHTTPResponse(resp.StatusCode, []byte(resp.String())) {
				recovered = true
				if err := s.recoverAgentIdentityTask(ctx, accountID, expectedTaskID); err != nil {
					return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_QUOTA_AUTH_FAILED", "agent identity task recovery failed: %v", err)
				}
				// Recovery starts a fresh authenticated request, not a relabeling of
				// the rejected response. Rebuild its proxy and all identity headers.
				identity = &openAIQuotaRequestIdentity{}
				accessToken, chatGPTAccountID, proxyURL, fedRAMP, err = s.prepareUpstreamCall(callCtx, accountID, identity)
				if err != nil {
					return nil, err
				}
				client, err = s.privacyClientFactory(proxyURL)
				if err != nil {
					return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_QUOTA_CLIENT_ERROR", "failed to rebuild upstream client")
				}
				payload = OpenAIQuotaUsage{}
				continue
			}
			status := resp.StatusCode
			body := truncate(s.redactQuotaErrorBody(ctx, accountID, resp.String()), 240)
			slog.Warn("openai_quota_query_failed", "account_id", accountID, "status", status, "body", body)
			return nil, infraerrors.Newf(mapUpstreamStatus(status), "OPENAI_QUOTA_UPSTREAM_ERROR", "upstream returned %d: %s", status, body)
		}
		break
	}

	identity.observedAt = time.Now().UTC().Truncate(time.Microsecond)
	payload.FetchedAt = identity.observedAt.Unix()
	payload.requestIdentity = identity
	details := s.queryResetCreditDetails(callCtx, client, quotaHeaders, accountID)
	if details != nil {
		payload.resetCandidates = details.Candidates
		hasDetailCount := details.AvailableCount != nil
		if payload.RateLimitResetCredits == nil {
			payload.RateLimitResetCredits = &OpenAIRateLimitResetCredits{}
		}
		if details.CreditListPresent {
			payload.RateLimitResetCredits.Credits = details.Credits
		}
		switch {
		case hasDetailCount:
			payload.RateLimitResetCredits.AvailableCount = *details.AvailableCount
		case details.CreditListPresent:
			payload.RateLimitResetCredits.AvailableCount = details.AvailableCreditCount
		}
	}
	if payload.RateLimitResetCredits != nil {
		payload.RateLimitResetCredits.requestIdentity = identity
	}
	return &payload, nil
}

// CacheResetCreditsSnapshot persists a complete reset-credit snapshot after an
// explicit UI refresh. The snapshot is written to the account that was queried
// (for a spark shadow that is the shadow row, even though the credits belong to
// its parent) because it is a per-row display cache: each row caches exactly
// what its own card renders, and shadows cannot consume credits anyway.
//
// Missing expiration details leave the old cache intact:
// a snapshot claiming N>0 available credits without their expiration timestamps
// cannot be aged out by readers, so it would keep showing (and offering to
// consume) credits that already expired. Callers must treat this rejection as a
// partial success — the upstream read itself is still valid.
func (s *OpenAIQuotaService) CacheResetCreditsSnapshot(ctx context.Context, accountID int64, credits *OpenAIRateLimitResetCredits) error {
	return s.cacheResetCreditsSnapshot(ctx, accountID, credits, nil)
}

// CachePostResetSnapshot persists the post-reset credit state together with the
// fresh 5h / 7d snapshot. It deliberately updates only the raw Codex window
// fields: XIASS's weekly estimate baseline is maintained independently by the
// usage service and must survive a manual reset-credit operation.
func (s *OpenAIQuotaService) CachePostResetSnapshot(ctx context.Context, accountID int64, usage *OpenAIQuotaUsage) error {
	if usage == nil || usage.requestIdentity == nil || usage.RateLimitResetCredits == nil ||
		usage.RateLimitResetCredits.requestIdentity != usage.requestIdentity {
		return missingOpenAIQuotaRequestIdentity()
	}
	identity := usage.requestIdentity
	updates := buildOpenAIQuotaUsageExtraUpdates(usage, identity.observedAt)
	if identity.target.IsShadow() {
		updates = buildCodexSparkWindowExtraUpdates(usage, identity.observedAt)
		if len(updates) > 0 {
			updates["codex_usage_updated_at"] = identity.observedAt.Format(time.RFC3339Nano)
		}
	}
	return s.cacheResetCreditsSnapshot(
		ctx,
		accountID,
		usage.RateLimitResetCredits,
		updates,
	)
}

func (s *OpenAIQuotaService) cacheResetCreditsSnapshot(ctx context.Context, accountID int64, credits *OpenAIRateLimitResetCredits, updates map[string]any) error {
	if credits == nil || (credits.AvailableCount > 0 && len(credits.Credits) == 0) {
		return infraerrors.New(
			http.StatusBadGateway,
			"OPENAI_QUOTA_RESET_CREDITS_REFRESH_FAILED",
			"failed to refresh reset-credit expiration details; cached data was preserved",
		)
	}
	if updates == nil {
		updates = make(map[string]any, 1)
	}
	identity := credits.requestIdentity
	if identity == nil {
		return missingOpenAIQuotaRequestIdentity()
	}
	updates[openaiQuotaResetCreditsKey] = credits
	updates[openaiQuotaResetCreditsTimeKey] = identity.observedAt.Format(time.RFC3339Nano)
	applied, err := persistBoundOpenAIQuotaSnapshot(ctx, s.accountRepo, accountID, identity, updates)
	if err != nil {
		return infraerrors.New(
			http.StatusInternalServerError,
			"OPENAI_QUOTA_CACHE_WRITE_FAILED",
			"failed to cache reset-credit details",
		).WithCause(err)
	}
	if !applied {
		return infraerrors.New(http.StatusConflict, "OPENAI_QUOTA_SNAPSHOT_SUPERSEDED", "quota request identity or observation is no longer current; query again")
	}
	return nil
}

func missingOpenAIQuotaRequestIdentity() error {
	return infraerrors.New(http.StatusConflict, "OPENAI_QUOTA_REQUEST_IDENTITY_UNAVAILABLE", "quota snapshot has no captured request identity; query again")
}

func persistBoundOpenAIQuotaSnapshot(ctx context.Context, repo AccountRepository, accountID int64, identity *openAIQuotaRequestIdentity, updates map[string]any) (bool, error) {
	if identity == nil || identity.target == nil || identity.credential == nil || identity.observedAt.IsZero() || identity.target.ID != accountID {
		return false, missingOpenAIQuotaRequestIdentity()
	}
	bound, ok := repo.(OpenAIQuotaBoundSnapshotRepository)
	if !ok {
		return false, fmt.Errorf("account repository does not support identity-bound OpenAI quota queries")
	}
	return bound.UpdateOpenAIQuotaSnapshotIfIdentityMatches(ctx, identity.target, identity.credential, identity.observedAt, updates)
}

func buildOpenAIQuotaUsageExtraUpdates(usage *OpenAIQuotaUsage, now time.Time) map[string]any {
	if usage == nil || usage.RateLimit == nil {
		return nil
	}

	snapshot := &OpenAICodexUsageSnapshot{
		UpdatedAt: now.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano),
	}
	applyWindow := func(window *OpenAIRateLimitWindow, primary bool) {
		if window == nil {
			return
		}
		usedPercent := window.UsedPercent
		resetAfterSeconds := int(window.ResetAfterSeconds)
		windowMinutes := int(window.LimitWindowSeconds / 60)
		if primary {
			snapshot.PrimaryUsedPercent = &usedPercent
			snapshot.PrimaryResetAfterSeconds = &resetAfterSeconds
			snapshot.PrimaryWindowMinutes = &windowMinutes
			return
		}
		snapshot.SecondaryUsedPercent = &usedPercent
		snapshot.SecondaryResetAfterSeconds = &resetAfterSeconds
		snapshot.SecondaryWindowMinutes = &windowMinutes
	}

	applyWindow(usage.RateLimit.PrimaryWindow, true)
	applyWindow(usage.RateLimit.SecondaryWindow, false)
	return buildCodexUsageExtraUpdates(snapshot, now)
}

func (s *OpenAIQuotaService) queryResetCreditDetails(ctx context.Context, client *req.Client, quotaHeaders map[string]string, accountID int64) *openAIRateLimitResetCreditDetails {
	resp, err := client.R().
		SetContext(ctx).
		SetHeaders(quotaHeaders).
		Get(chatGPTRateLimitCreditsURL)
	if err != nil {
		slog.Warn("openai_quota_reset_credit_details_failed", "account_id", accountID, "error", err)
		return nil
	}
	if !resp.IsSuccessState() {
		slog.Warn("openai_quota_reset_credit_details_failed", "account_id", accountID, "status", resp.StatusCode)
		return nil
	}

	details, err := parseOpenAIRateLimitResetCreditDetails(resp.Bytes())
	if err != nil {
		slog.Warn("openai_quota_reset_credit_details_parse_failed", "account_id", accountID, "error", err)
		if details.AvailableCount == nil {
			return nil
		}
	}
	if details.AvailableCount == nil && !details.CreditListPresent {
		return nil
	}
	return &details
}

// ResetCredit consumes one rate_limit_reset_credit for the given OpenAI account.
// The redeem_request_id is auto-generated (uuid-like) — upstream uses it for
// idempotency. Returns the consumed credit metadata so the UI can refresh.
func (s *OpenAIQuotaService) ResetCredit(ctx context.Context, accountID int64) (*OpenAIQuotaResetResult, error) {
	if s == nil || s.accountRepo == nil {
		return nil, ErrOpenAIResetUnavailable
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account != nil && account.IsShadow() {
		return nil, ErrSparkShadowResetNotSupported
	}
	if s.autoReset == nil {
		return nil, ErrOpenAIResetUnavailable
	}
	return s.autoReset.reset(ctx, accountID, false)
}

func (s *OpenAIQuotaService) GetAutoResetConfig(ctx context.Context, id int64) (OpenAIAutoResetConfig, error) {
	if s == nil || s.autoReset == nil {
		return OpenAIAutoResetConfig{}, ErrOpenAIResetUnavailable
	}
	return s.autoReset.GetConfig(ctx, id)
}

func (s *OpenAIQuotaService) SetAutoResetConfig(ctx context.Context, id int64, enabled bool, fiveHour, sevenDay float64) (OpenAIAutoResetConfig, error) {
	if s == nil || s.autoReset == nil {
		return OpenAIAutoResetConfig{}, ErrOpenAIResetUnavailable
	}
	return s.autoReset.SetConfig(ctx, id, enabled, fiveHour, sevenDay)
}

func (s *OpenAIQuotaService) Stop() {
	if s != nil {
		s.autoReset.Stop()
	}
}

func (s *OpenAIQuotaService) consumeResetCredit(ctx context.Context, accountID int64, creditID, redeemRequestID string, check func(context.Context) error, onSend func()) (_ *OpenAIQuotaResetResult, err error) {
	// Only an explicit authentication refusal proves the exchange was not
	// accepted. Preserve that fact if task recovery fails before another send.
	rejected := false
	defer func() {
		if rejected && err != nil {
			err = &openAIResetRejectedError{err}
		}
	}()
	// Shadow guard: resetting credits via a shadow account would silently
	// operate on the parent's quota; that is surprising and unwanted. Callers
	// must reset the parent account directly.
	//
	// Fail-closed: if the account cannot be loaded (transient DB error), we
	// must NOT fall through to prepareUpstreamCall. That function resolves a
	// shadow to its parent and would perform a parent-level reset — exactly
	// what this guard must prevent. Return the load error instead.
	if s.accountRepo != nil {
		acc, loadErr := s.accountRepo.GetByID(ctx, accountID)
		if loadErr != nil {
			return nil, infraerrors.Newf(http.StatusNotFound, "OPENAI_QUOTA_ACCOUNT_NOT_FOUND", "account not found: %v", loadErr)
		}
		if acc.IsShadow() {
			return nil, ErrSparkShadowResetNotSupported
		}
	}

	var capture *openAIQuotaRequestIdentity
	if check != nil && creditID != "" {
		capture = &openAIQuotaRequestIdentity{}
	}
	accessToken, chatGPTAccountID, proxyURL, fedRAMP, err := s.prepareUpstreamCall(ctx, accountID, capture)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(redeemRequestID) == "" {
		return nil, ErrOpenAIResetUnavailable
	}

	client, err := s.privacyClientFactory(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_QUOTA_CLIENT_ERROR", "failed to build upstream client: %v", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, openaiQuotaUpstreamTimeout)
	defer cancel()
	agentIdentity := s.isAgentIdentityAccount(ctx, accountID)

	var payload OpenAIQuotaResetResult
	for recovered := false; ; {
		if check != nil {
			if err := check(callCtx); err != nil {
				return nil, err
			}
		}
		headers, expectedTaskID, headerErr := s.buildCodexQuotaHeaders(callCtx, accountID, accessToken, chatGPTAccountID, fedRAMP)
		if headerErr != nil {
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_QUOTA_AUTH_FAILED", "failed to build upstream authentication: %v", headerErr)
		}
		headers["content-type"] = "application/json"
		body := map[string]string{"redeem_request_id": redeemRequestID}
		if creditID != "" {
			body["credit_id"] = creditID
		}
		if check != nil {
			if err := check(callCtx); err != nil {
				return nil, err
			}
		}
		if onSend != nil {
			onSend()
		}
		rejected = false
		resp, err := client.R().
			SetContext(callCtx).
			SetHeaders(headers).
			SetBody(body).
			SetSuccessResult(&payload).
			Post(chatGPTRateLimitResetURL)
		if err != nil {
			if creditID != "" {
				return nil, ErrOpenAIResetPending
			}
			return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_QUOTA_RESET_REQUEST_FAILED", "upstream request failed: %v", err)
		}
		if !resp.IsSuccessState() {
			rejected = resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
			if agentIdentity && !recovered && isAgentIdentityTaskInvalidHTTPResponse(resp.StatusCode, []byte(resp.String())) {
				recovered = true
				if err := s.recoverAgentIdentityTask(ctx, accountID, expectedTaskID); err != nil {
					return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_QUOTA_AUTH_FAILED", "agent identity task recovery failed: %v", err)
				}
				continue
			}
			status := resp.StatusCode
			if rejected {
				return nil, infraerrors.Newf(mapUpstreamStatus(status), "OPENAI_QUOTA_RESET_REJECTED", "upstream rejected reset authentication (HTTP %d); no credit was consumed", status)
			}
			if creditID != "" {
				return nil, ErrOpenAIResetPending
			}
			body := truncate(s.redactQuotaErrorBody(callCtx, accountID, resp.String()), 240)
			slog.Warn("openai_quota_reset_failed", "account_id", accountID, "status", status, "body", body)
			return nil, infraerrors.Newf(mapUpstreamStatus(status), "OPENAI_QUOTA_RESET_UPSTREAM_ERROR", "upstream returned %d: %s", status, body)
		}
		break
	}

	slog.Info("openai_quota_reset_success",
		"account_id", accountID,
		"code", payload.Code,
		"windows_reset", payload.WindowsReset,
	)
	return &payload, nil
}

// prepareUpstreamCall loads the account, validates it, obtains a fresh access
// token via the shared TokenProvider, and resolves the chatgpt-account-id and
// proxy URL. Centralized so QueryUsage / ResetCredit share validation.
func (s *OpenAIQuotaService) prepareUpstreamCall(ctx context.Context, accountID int64, capture ...*openAIQuotaRequestIdentity) (accessToken, chatGPTAccountID, proxyURL string, fedRAMP bool, err error) {
	if s == nil || s.accountRepo == nil || s.privacyClientFactory == nil {
		return "", "", "", false, infraerrors.New(http.StatusInternalServerError, "OPENAI_QUOTA_NOT_CONFIGURED", "openai quota service is not configured")
	}

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return "", "", "", false, infraerrors.Newf(http.StatusNotFound, "OPENAI_QUOTA_ACCOUNT_NOT_FOUND", "account not found: %v", err)
	}
	if account == nil {
		return "", "", "", false, infraerrors.New(http.StatusNotFound, "OPENAI_QUOTA_ACCOUNT_NOT_FOUND", "account not found")
	}
	if account.Platform != PlatformOpenAI {
		return "", "", "", false, infraerrors.New(http.StatusBadRequest, "OPENAI_QUOTA_INVALID_PLATFORM", "account is not an OpenAI account")
	}
	if account.Type != AccountTypeOAuth {
		return "", "", "", false, infraerrors.New(http.StatusBadRequest, "OPENAI_QUOTA_INVALID_TYPE", "account is not an OAuth account")
	}
	var identity *openAIQuotaRequestIdentity
	if len(capture) > 0 && capture[0] != nil {
		identity = capture[0]
		identity.target, err = cloneOpenAICodexSnapshotIdentity(account)
		if err != nil {
			return "", "", "", false, missingOpenAIQuotaRequestIdentity()
		}
	}

	// Spark shadow accounts do not hold their own credentials; resolve to the
	// parent account so that chatgpt_account_id / access_token / proxy all come
	// from the parent. This must happen BEFORE the chatgpt_account_id check.
	if account.IsShadow() {
		resolved, rerr := resolveCredentialAccount(ctx, s.accountRepo, account)
		if rerr != nil {
			return "", "", "", false, infraerrors.Newf(http.StatusBadGateway, "OPENAI_QUOTA_SHADOW_RESOLVE_FAILED", "failed to resolve shadow account: %v", rerr)
		}
		account = resolved
	}
	if identity != nil {
		// Give token/task helpers an owned copy. The CAS snapshot is frozen before
		// a token provider can refresh or substitute credentials underneath it.
		frozen, cloneErr := cloneOpenAICodexSnapshotIdentity(account)
		if cloneErr != nil {
			return "", "", "", false, missingOpenAIQuotaRequestIdentity()
		}
		requestAccount := *account
		requestAccount.Credentials, requestAccount.ProxyID = frozen.Credentials, frozen.ProxyID
		if account.Proxy != nil {
			proxy := *account.Proxy
			requestAccount.Proxy = &proxy
		}
		account = &requestAccount
		if account.IsOpenAIAgentIdentity() {
			if err := ensureAgentIdentityTaskForAccount(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, account, ""); err != nil {
				return "", "", "", false, err
			}
		}
		identity.credential, err = cloneOpenAICodexSnapshotIdentity(account)
		if err != nil {
			return "", "", "", false, missingOpenAIQuotaRequestIdentity()
		}
		if !identity.target.IsShadow() {
			identity.target = identity.credential
		}
	}

	chatGPTAccountID = strings.TrimSpace(account.GetCredential("chatgpt_account_id"))
	if chatGPTAccountID == "" {
		// Fall back to organization_id — some legacy accounts only persisted poid.
		chatGPTAccountID = strings.TrimSpace(account.GetCredential("organization_id"))
	}
	if chatGPTAccountID == "" {
		return "", "", "", false, infraerrors.New(http.StatusBadRequest, "OPENAI_QUOTA_MISSING_ACCOUNT_ID", "chatgpt_account_id is missing; please re-authorize this account")
	}

	if !account.IsOpenAIAgentIdentity() {
		if s.tokenProvider == nil {
			return "", "", "", false, infraerrors.New(http.StatusInternalServerError, "OPENAI_QUOTA_NOT_CONFIGURED", "openai quota token provider is not configured")
		}
		accessToken, err = s.tokenProvider.GetAccessToken(ctx, account)
		if err != nil {
			return "", "", "", false, infraerrors.Newf(http.StatusBadGateway, "OPENAI_QUOTA_TOKEN_UNAVAILABLE", "failed to acquire access token: %v", err)
		}
		if strings.TrimSpace(accessToken) == "" {
			return "", "", "", false, infraerrors.New(http.StatusBadGateway, "OPENAI_QUOTA_TOKEN_UNAVAILABLE", "access token is empty")
		}
		if identity != nil && accessToken != identity.credential.GetOpenAIAccessToken() {
			return "", "", "", false, infraerrors.New(http.StatusConflict, "OPENAI_QUOTA_TOKEN_IDENTITY_MISMATCH", "token provider returned a token that does not match the captured credentials; retry with a fresh account request")
		}
	}
	fedRAMP = account.IsChatGPTAccountFedRAMP()

	// account.Proxy is eager-loaded by accountRepo.GetByID (see
	// repository.accountsToService), so we can read the proxy URL directly
	// instead of round-tripping the DB again. Fall back to proxyRepo only
	// when Proxy isn't pre-populated (defensive — e.g. callers that built
	// the Account by hand).
	if account.ProxyID != nil {
		switch {
		case account.Proxy != nil:
			proxyURL = account.requestProxyURL()
		case s.proxyRepo != nil:
			if proxy, perr := s.proxyRepo.GetByID(ctx, *account.ProxyID); perr == nil && proxy != nil {
				proxyURL = proxy.URL()
			}
		}
		if identity != nil && proxyURL == "" {
			return "", "", "", false, infraerrors.New(http.StatusConflict, "OPENAI_QUOTA_PROXY_IDENTITY_UNAVAILABLE", "captured request proxy is unavailable; query again")
		}
	}

	return accessToken, chatGPTAccountID, proxyURL, fedRAMP, nil
}

func buildBoundCodexQuotaHeaders(account *Account, accessToken, chatGPTAccountID string, fedRAMP bool) (map[string]string, string, error) {
	headers := buildCodexCommonHeaders(accessToken, chatGPTAccountID, fedRAMP)
	if !account.IsOpenAIAgentIdentity() {
		return headers, "", nil
	}
	key, err := agentIdentityKeyFromAccount(account)
	if err != nil {
		return nil, "", err
	}
	assertion, err := buildAgentAssertion(key, time.Now())
	if err != nil {
		return nil, "", err
	}
	headers["authorization"] = assertion
	return headers, key.taskID, nil
}

func (s *OpenAIQuotaService) recoverAgentIdentityTask(ctx context.Context, accountID int64, expectedTaskID string) error {
	if s == nil || s.accountRepo == nil {
		return fmt.Errorf("account repository is unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil {
		return fmt.Errorf("account is unavailable")
	}
	if account.IsShadow() {
		account, err = resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil || account == nil {
			return fmt.Errorf("credential account is unavailable")
		}
	}
	if !account.IsOpenAIAgentIdentity() {
		return nil
	}
	return ensureAgentIdentityTaskForAccount(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, account, expectedTaskID)
}

func (s *OpenAIQuotaService) isAgentIdentityAccount(ctx context.Context, accountID int64) bool {
	if s == nil || s.accountRepo == nil {
		return false
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil {
		return false
	}
	if account.IsShadow() {
		account, err = resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil || account == nil {
			return false
		}
	}
	return account.IsOpenAIAgentIdentity()
}

func (s *OpenAIQuotaService) buildCodexQuotaHeaders(ctx context.Context, accountID int64, accessToken, chatGPTAccountID string, fedRAMP bool) (map[string]string, string, error) {
	headers := buildCodexCommonHeaders(accessToken, chatGPTAccountID, fedRAMP)
	if s == nil || s.accountRepo == nil {
		return headers, "", nil
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil {
		if strings.TrimSpace(accessToken) == "" {
			return nil, "", fmt.Errorf("agent identity account credentials are unavailable")
		}
		return headers, "", nil
	}
	if account.IsShadow() {
		if resolved, resolveErr := resolveCredentialAccount(ctx, s.accountRepo, account); resolveErr == nil && resolved != nil {
			account = resolved
		} else if strings.TrimSpace(accessToken) == "" {
			return nil, "", fmt.Errorf("agent identity shadow credentials are unavailable")
		}
	}
	if !account.IsOpenAIAgentIdentity() {
		return headers, "", nil
	}
	if err := ensureAgentIdentityTaskForAccount(ctx, s.accountRepo, s.agentIdentityWS, &s.agentIdentityTaskMu, account, ""); err != nil {
		return nil, "", err
	}
	key, err := agentIdentityKeyFromAccount(account)
	if err != nil {
		return nil, "", err
	}
	assertion, err := buildAgentAssertion(key, time.Now())
	if err != nil {
		return nil, "", err
	}
	headers["authorization"] = assertion
	return headers, key.taskID, nil
}

func (s *OpenAIQuotaService) redactQuotaErrorBody(ctx context.Context, accountID int64, body string) string {
	if s == nil || s.accountRepo == nil {
		return body
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil {
		return body
	}
	return string(redactAgentIdentitySensitiveBodyForAccount(ctx, s.accountRepo, account, []byte(body)))
}

// buildCodexCommonHeaders sets the request headers expected by the chatgpt.com
// backend so calls succeed past Cloudflare/WASM checks.
func buildCodexCommonHeaders(accessToken, chatGPTAccountID string, fedRAMP bool) map[string]string {
	headers := map[string]string{
		"authorization":      "Bearer " + accessToken,
		"chatgpt-account-id": chatGPTAccountID,
		"openai-beta":        openaiQuotaCodexBeta,
		"oai-language":       openaiQuotaCodexLanguageTag,
		"originator":         openaiQuotaCodexOriginator,
		"accept":             "application/json",
		"sec-fetch-site":     openaiQuotaSecFetchSite,
		"sec-fetch-mode":     openaiQuotaSecFetchMode,
		"sec-fetch-dest":     openaiQuotaSecFetchDest,
		"priority":           "u=4, i",
	}
	if fedRAMP {
		headers["x-openai-fedramp"] = "true"
	}
	return headers
}

// generateRedeemRequestID produces a UUID-v4-shaped string without pulling in a
// new dependency. ChatGPT uses this as an idempotency key for the consume call.
func generateRedeemRequestID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// Set version (4) and variant (RFC 4122) bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexStr := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:]), nil
}

// buildCodexSparkWindowExtraUpdates extracts Codex Spark usage windows from the
// /wham/usage response body's additional_rate_limits, matching the entry with
// MeteredFeature == "codex_bengalfox". It produces plain codex_* keys (NOT the
// Method-Z "codex_spark_" prefix) so that a spark shadow account's extra map
// is populated with the same key names used by the scheduling / frontend layers.
// Returns nil when no codex_bengalfox entry is present or when the RateLimit
// yields no window data.
func buildCodexSparkWindowExtraUpdates(usage *OpenAIQuotaUsage, now time.Time) map[string]any {
	if usage == nil {
		return nil
	}
	var spark *OpenAIRateLimit
	for i := range usage.AdditionalRateLimits {
		a := usage.AdditionalRateLimits[i]
		if a.MeteredFeature == "codex_bengalfox" {
			spark = a.RateLimit
			break
		}
	}
	if spark == nil {
		return nil
	}

	// Reuse OpenAICodexUsageSnapshot / Normalize to map primary/secondary windows
	// to canonical 5h/7d buckets (same logic as probeOpenAICodexSnapshot).
	snap := &OpenAICodexUsageSnapshot{}
	if w := spark.PrimaryWindow; w != nil {
		p := w.UsedPercent
		snap.PrimaryUsedPercent = &p
		ra := int(w.ResetAfterSeconds)
		snap.PrimaryResetAfterSeconds = &ra
		wm := int(w.LimitWindowSeconds / 60)
		snap.PrimaryWindowMinutes = &wm
	}
	if w := spark.SecondaryWindow; w != nil {
		p := w.UsedPercent
		snap.SecondaryUsedPercent = &p
		ra := int(w.ResetAfterSeconds)
		snap.SecondaryResetAfterSeconds = &ra
		wm := int(w.LimitWindowSeconds / 60)
		snap.SecondaryWindowMinutes = &wm
	}

	normalized := snap.Normalize()
	if normalized == nil || (normalized.Used5hPercent == nil && normalized.Reset5hSeconds == nil &&
		normalized.Window5hMinutes == nil && normalized.Used7dPercent == nil &&
		normalized.Reset7dSeconds == nil && normalized.Window7dMinutes == nil) {
		return nil
	}

	// Treat the current upstream response as the source of truth for window
	// presence. Explicit nulls replace stale values from a previous plan.
	updates := map[string]any{
		"codex_5h_used_percent":        nil,
		"codex_5h_reset_after_seconds": nil,
		"codex_5h_window_minutes":      nil,
		"codex_5h_reset_at":            nil,
		"codex_7d_used_percent":        nil,
		"codex_7d_reset_after_seconds": nil,
		"codex_7d_window_minutes":      nil,
		"codex_7d_reset_at":            nil,
	}
	if normalized.Used5hPercent != nil {
		updates["codex_5h_used_percent"] = *normalized.Used5hPercent
	}
	if normalized.Reset5hSeconds != nil {
		updates["codex_5h_reset_after_seconds"] = *normalized.Reset5hSeconds
	}
	if normalized.Window5hMinutes != nil {
		updates["codex_5h_window_minutes"] = *normalized.Window5hMinutes
	}
	if normalized.Used7dPercent != nil {
		updates["codex_7d_used_percent"] = *normalized.Used7dPercent
	}
	if normalized.Reset7dSeconds != nil {
		updates["codex_7d_reset_after_seconds"] = *normalized.Reset7dSeconds
	}
	if normalized.Window7dMinutes != nil {
		updates["codex_7d_window_minutes"] = *normalized.Window7dMinutes
	}
	if r := codexResetAtRFC3339(now, normalized.Reset5hSeconds); r != nil {
		updates["codex_5h_reset_at"] = *r
	}
	if r := codexResetAtRFC3339(now, normalized.Reset7dSeconds); r != nil {
		updates["codex_7d_reset_at"] = *r
	}
	updates["codex_usage_updated_at"] = now.Format(time.RFC3339)
	return updates
}

// mapUpstreamStatus collapses upstream HTTP statuses into a stable set we
// surface from the admin handler. 4xx upstream errors are surfaced as 502
// (BadGateway) so callers can distinguish "your input is bad" (400) from
// "upstream said no" (502); 401/403 are bubbled directly to hint at re-auth.
func mapUpstreamStatus(status int) int {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return status
	case status == http.StatusTooManyRequests:
		return http.StatusTooManyRequests
	case status >= 400 && status < 500:
		return http.StatusBadGateway
	case status >= 500:
		return http.StatusBadGateway
	default:
		return http.StatusBadGateway
	}
}
