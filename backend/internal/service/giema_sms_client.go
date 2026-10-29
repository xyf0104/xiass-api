package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const giemaSMSAPIBaseURL = "https://giema.exnb.ccwu.cc/api/v1"
const giemaSMSResponseLimit = 64 << 10

// GiemaSMSClient handles provider HTTP calls; the runtime owns cards, billing,
// persistence and polling. No cancellation endpoint has been documented.
type GiemaSMSClient struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

func (*GiemaSMSClient) String() string   { return "GiemaSMSClient{redacted}" }
func (*GiemaSMSClient) GoString() string { return "GiemaSMSClient{redacted}" }

// GiemaSMSSession is persisted encrypted by the runtime and sent back verbatim.
// An empty token uses account-bound restore.
// Token is deliberately excluded from JSON and diagnostic output.
type GiemaSMSSession struct {
	Token string `json:"-"`
}

func (GiemaSMSSession) String() string   { return "GiemaSMSSession{redacted}" }
func (GiemaSMSSession) GoString() string { return "GiemaSMSSession{redacted}" }

// Pointer fields distinguish a missing value from an explicit zero or false.
// Config rejects missing fields rather than inventing provider defaults.
type GiemaSMSConfig struct {
	Provider       string `json:"provider"`
	PollSeconds    *int   `json:"poll_seconds"`
	RotateSeconds  *int   `json:"rotate_seconds"`
	MaxAttempts    *int   `json:"max_attempts"`
	Maintenance    *bool  `json:"maintenance"`
	ProtocolLocked *bool  `json:"protocol_locked"`
}

// GiemaSMSResponse contains fields confirmed by the authenticated API schema or
// the saved provider-app.js. Numeric orderId is optional, including on rotation.
// A valid response acknowledges state, not necessarily allocation or SMS success.
// Provider messages/errors are intentionally not exposed or retained.
type GiemaSMSResponse struct {
	Session             GiemaSMSSession `json:"-"`
	Status              string          `json:"status,omitempty"`
	Provider            string          `json:"provider,omitempty"`
	Platform            string          `json:"platform,omitempty"`
	OrderID             *int64          `json:"orderId,omitempty"`
	PhoneNumber         *string         `json:"phoneNumber,omitempty"`
	Phone               *string         `json:"phone,omitempty"`
	PhoneReady          *bool           `json:"phoneReady,omitempty"`
	Attempt             *int            `json:"attempt,omitempty"`
	AttemptCount        *int            `json:"attemptCount,omitempty"`
	MaxAttempts         *int            `json:"maxAttempts,omitempty"`
	RemainingAttempts   *int            `json:"remainingAttempts,omitempty"`
	AttemptsExhausted   *bool           `json:"attemptsExhausted,omitempty"`
	CanChange           *bool           `json:"canChange,omitempty"`
	CanRelease          *bool           `json:"canRelease,omitempty"`
	SwitchInSeconds     *int            `json:"switchInSeconds,omitempty"`
	WaitingCancellation *bool           `json:"waitingCancellation,omitempty"`
	Consumed            *bool           `json:"consumed,omitempty"`
	QuotaCharged        *bool           `json:"quotaCharged,omitempty"`
	CodePending         *bool           `json:"codePending,omitempty"`
	Code                string          `json:"code,omitempty"`
	CodeExpired         *bool           `json:"codeExpired,omitempty"`
	CodeExpiresAt       *float64        `json:"codeExpiresAt,omitempty"`
	Resumed             *bool           `json:"resumed,omitempty"`
}

func (GiemaSMSResponse) String() string   { return "GiemaSMSResponse{redacted}" }
func (GiemaSMSResponse) GoString() string { return "GiemaSMSResponse{redacted}" }

// Number follows the website's nullish phoneNumber/phone precedence, but hides
// stale numbers during transitions. Manual allocation requires explicit readiness.
func (r *GiemaSMSResponse) Number() string {
	if r == nil || r.WaitingForCancellation() || giemaSMSTrue(r.CodePending) || giemaSMSTrue(r.Consumed) ||
		(r.PhoneReady != nil && !*r.PhoneReady) {
		return ""
	}
	switch r.Status {
	case "allocating", "changing", "disabled", "exhausted", "replace_ready":
		return ""
	}
	manual := r.Provider == "sms688_manual" || r.Platform == "sms688_manual" || r.QuotaCharged != nil
	if manual && !giemaSMSTrue(r.PhoneReady) {
		return ""
	}
	return r.phone()
}

func (r *GiemaSMSResponse) phone() string {
	if r.PhoneNumber != nil {
		return *r.PhoneNumber
	}
	if r.Phone != nil {
		return *r.Phone
	}
	return ""
}

func (r *GiemaSMSResponse) WaitingForCancellation() bool {
	return r != nil && (giemaSMSTrue(r.WaitingCancellation) || r.Status == "canceling")
}

type GiemaSMSErrorKind string

const (
	GiemaSMSInvalidRequest  GiemaSMSErrorKind = "invalid_request"
	GiemaSMSAuthentication  GiemaSMSErrorKind = "authentication"
	GiemaSMSConflict        GiemaSMSErrorKind = "conflict"
	GiemaSMSParameters      GiemaSMSErrorKind = "parameters"
	GiemaSMSRateLimited     GiemaSMSErrorKind = "rate_limited"
	GiemaSMSHTTPError       GiemaSMSErrorKind = "http_error"
	GiemaSMSRedirect        GiemaSMSErrorKind = "redirect"
	GiemaSMSTransport       GiemaSMSErrorKind = "transport"
	GiemaSMSInvalidResponse GiemaSMSErrorKind = "invalid_response"
)

type GiemaSMSErrorReason string

const (
	GiemaSMSReasonNoSession    GiemaSMSErrorReason = "no_session"
	GiemaSMSReasonCardUnusable GiemaSMSErrorReason = "card_unusable"
	GiemaSMSReasonAttemptLimit GiemaSMSErrorReason = "attempt_limit"
)

// GiemaSMSError carries only safe metadata; only an explicit reason can identify
// an unusable card.
// Ambiguous requires retaining prior state and reconciling before another mutation.
// RetryAfter is advisory; no operation is automatically retried by this client.
type GiemaSMSError struct {
	Kind          GiemaSMSErrorKind
	Reason        GiemaSMSErrorReason
	StatusCode    int
	Ambiguous     bool
	RetryAfter    time.Duration
	HasRetryAfter bool
}

func (e *GiemaSMSError) Error() string {
	switch e.Kind {
	case GiemaSMSInvalidRequest:
		return "giema sms: invalid client request"
	case GiemaSMSAuthentication:
		return "giema sms: provider authentication failed"
	case GiemaSMSConflict:
		return "giema sms: request conflicts with provider state"
	case GiemaSMSParameters:
		return "giema sms: provider rejected request parameters"
	case GiemaSMSRateLimited:
		return "giema sms: provider rate limit reached"
	case GiemaSMSRedirect:
		return "giema sms: redirect refused"
	case GiemaSMSTransport:
		return "giema sms: transport failed; preserve session state"
	case GiemaSMSInvalidResponse:
		return "giema sms: invalid response; preserve session state"
	default:
		return "giema sms: provider request failed"
	}
}

// NewGiemaSMSClient takes a previously decrypted key. Construction performs no
// network or database operations; the runtime loads security_secrets.
func NewGiemaSMSClient(apiKey string) (*GiemaSMSClient, error) {
	return newGiemaSMSClient(apiKey, giemaSMSAPIBaseURL, nil)
}

func newGiemaSMSClient(apiKey, baseURL string, client *http.Client) (*GiemaSMSClient, error) {
	if apiKey == "" || strings.ContainsFunc(apiKey, func(r rune) bool { return r < 33 || r > 126 }) {
		return nil, &GiemaSMSError{Kind: GiemaSMSInvalidRequest}
	}
	httpClient := http.Client{Timeout: 20 * time.Second}
	if client != nil {
		httpClient = *client
		if httpClient.Timeout <= 0 {
			httpClient.Timeout = 20 * time.Second
		}
	}
	httpClient.Jar = nil
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &GiemaSMSClient{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), client: &httpClient}, nil
}

func (c *GiemaSMSClient) Config(ctx context.Context) (*GiemaSMSConfig, error) {
	data, err := c.request(ctx, "/store/config", nil)
	if err != nil {
		return nil, err
	}
	var config GiemaSMSConfig
	if json.Unmarshal(data, &config) != nil || strings.TrimSpace(config.Provider) == "" ||
		config.PollSeconds == nil || *config.PollSeconds <= 0 ||
		config.RotateSeconds == nil || *config.RotateSeconds < 0 ||
		config.MaxAttempts == nil || !giemaSMSValidCount(config.MaxAttempts) ||
		config.Maintenance == nil || config.ProtocolLocked == nil {
		return nil, &GiemaSMSError{Kind: GiemaSMSInvalidResponse, Ambiguous: true}
	}
	return &config, nil
}

type giemaSMSRequest struct {
	Card            string `json:"card"`
	Session         string `json:"session,omitempty"`
	ExpectedAttempt *int   `json:"expectedAttempt,omitempty"`
}

// Start and Status accept an optional persisted session. Omitting it allows the
// documented account-bound restore; neither method manufactures a session token.
func (c *GiemaSMSClient) Start(ctx context.Context, card string, sessions ...GiemaSMSSession) (*GiemaSMSResponse, error) {
	return c.optionalSessionRequest(ctx, "/store/start", card, sessions)
}

func (c *GiemaSMSClient) Status(ctx context.Context, card string, sessions ...GiemaSMSSession) (*GiemaSMSResponse, error) {
	return c.optionalSessionRequest(ctx, "/store/status", card, sessions)
}

func (c *GiemaSMSClient) optionalSessionRequest(ctx context.Context, path, card string, sessions []GiemaSMSSession) (*GiemaSMSResponse, error) {
	if len(sessions) > 1 {
		return nil, &GiemaSMSError{Kind: GiemaSMSInvalidRequest}
	}
	payload := giemaSMSRequest{Card: card}
	if len(sessions) == 1 {
		payload.Session = sessions[0].Token
	}
	return c.sessionRequest(ctx, path, payload)
}

// Rotate uses the last returned attempt, never attemptCount, an increment or a
// default. An absent snapshot/attempt is omitted as the schema permits. It leaves
// the supplied snapshot untouched even on ambiguous failures.
func (c *GiemaSMSClient) Rotate(ctx context.Context, card string, previous *GiemaSMSResponse) (*GiemaSMSResponse, error) {
	if previous == nil {
		return c.sessionRequest(ctx, "/store/rotate", giemaSMSRequest{Card: card})
	}
	if previous.WaitingForCancellation() || giemaSMSTrue(previous.Consumed) || giemaSMSTrue(previous.CodePending) || giemaSMSTrue(previous.AttemptsExhausted) ||
		(previous.SwitchInSeconds != nil && *previous.SwitchInSeconds > 0) ||
		(previous.CanChange != nil && !*previous.CanChange) || (previous.PhoneReady != nil && !*previous.PhoneReady) {
		return nil, &GiemaSMSError{Kind: GiemaSMSInvalidRequest}
	}
	switch previous.Status {
	case "allocating", "changing", "exhausted", "disabled", "replace_ready", "ok":
		return nil, &GiemaSMSError{Kind: GiemaSMSInvalidRequest}
	}
	return c.sessionRequest(ctx, "/store/rotate", giemaSMSRequest{
		Card: card, Session: previous.Session.Token, ExpectedAttempt: previous.Attempt,
	})
}

func (c *GiemaSMSClient) sessionRequest(ctx context.Context, path string, payload giemaSMSRequest) (*GiemaSMSResponse, error) {
	if !utf8.ValidString(payload.Card) || strings.TrimSpace(payload.Card) == "" || utf8.RuneCountInString(payload.Card) > 64 ||
		!utf8.ValidString(payload.Session) || utf8.RuneCountInString(payload.Session) > 128 ||
		(payload.ExpectedAttempt != nil && (*payload.ExpectedAttempt < 1 || *payload.ExpectedAttempt > 100001)) {
		return nil, &GiemaSMSError{Kind: GiemaSMSInvalidRequest}
	}
	data, err := c.request(ctx, path, &payload)
	if err != nil {
		return nil, err
	}
	result := &GiemaSMSResponse{}
	wire := struct {
		*GiemaSMSResponse
		Session *string `json:"session"`
	}{GiemaSMSResponse: result}
	if json.Unmarshal(data, &wire) != nil {
		return nil, &GiemaSMSError{Kind: GiemaSMSInvalidResponse, Ambiguous: true}
	}
	result.Session.Token = payload.Session
	if wire.Session != nil && *wire.Session != "" {
		result.Session.Token = *wire.Session
	}
	if !result.valid() || utf8.RuneCountInString(result.Session.Token) > 128 {
		return nil, &GiemaSMSError{Kind: GiemaSMSInvalidResponse, Ambiguous: true}
	}
	return result, nil
}

func (r *GiemaSMSResponse) valid() bool {
	if !giemaSMSValidCount(r.Attempt) || !giemaSMSValidCount(r.AttemptCount) || !giemaSMSValidCount(r.MaxAttempts) || !giemaSMSValidCount(r.RemainingAttempts) || !giemaSMSValidCount(r.SwitchInSeconds) ||
		(r.OrderID != nil && *r.OrderID < 0) ||
		(r.CodeExpiresAt != nil && *r.CodeExpiresAt < 0) {
		return false
	}
	switch r.Status {
	case "active", "wait", "allocating", "changing", "canceling", "ok", "exhausted", "disabled", "replace_ready":
	case "":
		if r.PhoneReady == nil && !giemaSMSTrue(r.Consumed) && !giemaSMSTrue(r.WaitingCancellation) &&
			(r.phone() == "" || r.Attempt == nil) {
			return false
		}
	default:
		return false
	}
	if giemaSMSTrue(r.PhoneReady) && !r.terminal() && !r.WaitingForCancellation() && strings.TrimSpace(r.phone()) == "" {
		return false
	}
	if r.Status == "ok" && strings.TrimSpace(r.Code) == "" && !giemaSMSTrue(r.Consumed) {
		return false
	}
	return true
}

func (r *GiemaSMSResponse) terminal() bool {
	if giemaSMSTrue(r.CodePending) || r.WaitingForCancellation() {
		return false
	}
	return giemaSMSTrue(r.Consumed) || r.Status == "ok" || r.Status == "exhausted" || r.Status == "disabled" || r.Status == "replace_ready"
}

func giemaSMSTrue(value *bool) bool { return value != nil && *value }

func giemaSMSValidCount(value *int) bool {
	return value == nil || (*value >= 0 && int64(*value) <= 9007199254740991)
}

func (c *GiemaSMSClient) request(ctx context.Context, path string, payload *giemaSMSRequest) ([]byte, error) {
	if c == nil || c.client == nil || ctx == nil {
		return nil, &GiemaSMSError{Kind: GiemaSMSInvalidRequest}
	}
	method := http.MethodGet
	var body []byte
	if payload != nil {
		method = http.MethodPost
		body, _ = json.Marshal(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, &GiemaSMSError{Kind: GiemaSMSInvalidRequest}
	}
	// Prevent net/http from replaying a mutation even after a stale connection.
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, &GiemaSMSError{Kind: GiemaSMSTransport, Ambiguous: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		failure := &GiemaSMSError{Kind: GiemaSMSHTTPError, StatusCode: resp.StatusCode, Ambiguous: resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500}
		detail := giemaSMSErrorDetail(resp.Body)
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			failure.Kind = GiemaSMSAuthentication
		case http.StatusConflict:
			failure.Kind = GiemaSMSConflict
		case http.StatusUnprocessableEntity:
			failure.Kind = GiemaSMSParameters
		case http.StatusTooManyRequests:
			failure.Kind = GiemaSMSRateLimited
			failure.RetryAfter, failure.HasRetryAfter = giemaSMSRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			failure.Kind, failure.Ambiguous = GiemaSMSRedirect, true
		}
		failure.Reason = giemaSMSClassifyErrorReason(resp.StatusCode, path, detail)
		return nil, failure
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, giemaSMSResponseLimit+1))
	if err != nil || len(data) > giemaSMSResponseLimit || !json.Valid(data) || !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return nil, &GiemaSMSError{Kind: GiemaSMSInvalidResponse, StatusCode: resp.StatusCode, Ambiguous: true}
	}
	return data, nil
}

func giemaSMSErrorDetail(body io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(body, 8<<10))
	if err != nil || !json.Valid(data) {
		return ""
	}
	var envelope struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return ""
	}
	return strings.TrimSpace(envelope.Detail)
}

func giemaSMSClassifyErrorReason(status int, path, detail string) GiemaSMSErrorReason {
	// Authentication, rate limiting, transport and server failures never prove
	// anything about a card. Only narrowly classified client/state errors may
	// drive the caller's next-card policy.
	if status != http.StatusBadRequest && status != http.StatusConflict && status != http.StatusUnprocessableEntity {
		return ""
	}
	if path != "/store/start" && path != "/store/status" && path != "/store/rotate" {
		return ""
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(detail), " "))
	if normalized == "卡密尚无可恢复的 api 会话，请先调用 start 或提供原 session" ||
		normalized == "card has no recoverable api session; call start first or provide the original session" {
		return GiemaSMSReasonNoSession
	}
	if path == "/store/rotate" && giemaSMSContainsAny(normalized, "attempt limit", "attempts exhausted", "达到次数上限", "次数已用尽", "换号次数已用尽", "换号次数达到上限") {
		return GiemaSMSReasonAttemptLimit
	}
	if status == http.StatusBadRequest || status == http.StatusConflict || status == http.StatusUnprocessableEntity {
		if giemaSMSContainsAny(normalized, "card invalid", "invalid card", "card used", "card already used", "card disabled", "card exhausted", "卡密无效", "卡密已使用", "卡密已用", "卡密禁用", "卡密已禁用", "卡密耗尽", "卡密已耗尽") {
			return GiemaSMSReasonCardUnusable
		}
	}
	return ""
}

func giemaSMSContainsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func giemaSMSRetryAfter(raw string, now time.Time) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.ContainsFunc(raw, func(r rune) bool { return r < '0' || r > '9' }) {
		seconds, err := strconv.ParseUint(raw, 10, 64)
		if err == nil && seconds <= uint64((1<<63-1)/time.Second) {
			return time.Duration(seconds) * time.Second, true
		}
		return 0, false
	}
	if deadline, err := http.ParseTime(raw); err == nil {
		if deadline.Before(now) {
			return 0, true
		}
		return deadline.Sub(now), true
	}
	return 0, false
}
