package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"
)

const openAICreditsEnabledExtraKey = "codex_credits_enabled"

// Credit balances are decimal strings, not currencies or local token estimates.
func validOpenAICreditBalance(raw string) bool {
	if raw == "" || len(raw) > 128 {
		return false
	}
	dot, digit := false, false
	for _, c := range raw {
		if c == '.' && !dot {
			dot = true
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
		digit = true
	}
	return digit
}

func openAICreditsUsable(credits *OpenAICredits) bool {
	if credits == nil {
		return false
	}
	if credits.Unlimited {
		return true
	}
	if !credits.HasCredits || credits.Balance == nil || !validOpenAICreditBalance(*credits.Balance) {
		return false
	}
	balance, ok := new(big.Rat).SetString(*credits.Balance)
	return ok && balance.Sign() > 0
}

func accountHasUsableOpenAICredits(account *Account) bool {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.IsShadow() {
		return false
	}
	enabled, _ := account.Extra[openAICreditsEnabledExtraKey].(bool)
	if !enabled {
		return false
	}
	raw, ok := account.Extra[openaiQuotaCreditsKey]
	if !ok || raw == nil {
		return false
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	var snapshot openAICreditsSnapshot
	if json.Unmarshal(data, &snapshot) != nil || snapshot.FetchedAt <= 0 {
		return false
	}
	return openAICreditsUsable(snapshot.Credits)
}

// ParseOpenAICreditsHeaders ignores absent and partial snapshots. Missing
// headers must never erase a known balance or turn a reset card into credits.
func ParseOpenAICreditsHeaders(headers http.Header) *OpenAICredits {
	parseBool := func(key string) (bool, bool) {
		switch strings.TrimSpace(strings.ToLower(headers.Get(key))) {
		case "true":
			return true, true
		case "false":
			return false, true
		default:
			return false, false
		}
	}
	hasCredits, hasValid := parseBool("x-codex-credits-has-credits")
	unlimited, unlimitedValid := parseBool("x-codex-credits-unlimited")
	if !hasValid || !unlimitedValid {
		return nil
	}
	credits := &OpenAICredits{HasCredits: hasCredits, Unlimited: unlimited}
	if raw := strings.TrimSpace(headers.Get("x-codex-credits-balance")); raw != "" {
		if !validOpenAICreditBalance(raw) {
			return nil
		}
		credits.Balance = &raw
	} else if hasCredits && !unlimited {
		return nil
	}
	return credits
}

// Called with the frozen request identity. Persistence is detached from the
// response stream and guarded by the same credential/proxy CAS as quota reads.
func persistOpenAICreditsHeadersAsync(repo AccountRepository, expected *Account, headers http.Header) {
	persistOpenAICreditsAsync(repo, expected, ParseOpenAICreditsHeaders(headers))
}

func persistOpenAICreditsAsync(repo AccountRepository, expected *Account, credits *OpenAICredits) {
	if repo == nil || expected == nil || expected.Platform != PlatformOpenAI || expected.Type != AccountTypeOAuth || expected.IsShadow() {
		return
	}
	if credits == nil {
		return
	}
	observedAt := time.Now().UTC().Truncate(time.Microsecond)
	identity := &openAIQuotaRequestIdentity{target: expected, credential: expected, observedAt: observedAt}
	updates := make(map[string]any, 2)
	mergeOpenAICreditsExtraUpdates(updates, &OpenAIQuotaUsage{Credits: credits, FetchedAt: observedAt.Unix()}, observedAt)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := persistBoundOpenAIQuotaSnapshot(ctx, repo, expected.ID, identity, updates); err != nil {
			slog.Warn("openai_credits_snapshot_failed", "account_id", expected.ID, "error", err)
		}
	}()
}

// Codex sends the authoritative balance in codex.rate_limits on streaming
// transports as well as in handshake headers. Ordinary response payloads and
// model-generated text must never be interpreted as balance observations.
func parseOpenAICreditsEvent(payload []byte) *OpenAICredits {
	if len(payload) > 64*1024 || !bytes.Contains(payload, []byte(`"codex.rate_limits"`)) {
		return nil
	}
	var event struct {
		Type    string `json:"type"`
		Credits *struct {
			HasCredits *bool   `json:"has_credits"`
			Unlimited  *bool   `json:"unlimited"`
			Balance    *string `json:"balance"`
		} `json:"credits"`
	}
	if json.Unmarshal(payload, &event) != nil || event.Type != "codex.rate_limits" || event.Credits == nil || event.Credits.HasCredits == nil || event.Credits.Unlimited == nil {
		return nil
	}
	c := &OpenAICredits{HasCredits: *event.Credits.HasCredits, Unlimited: *event.Credits.Unlimited, Balance: event.Credits.Balance}
	if c.Balance != nil && !validOpenAICreditBalance(*c.Balance) {
		return nil
	}
	if c.HasCredits && !c.Unlimited && c.Balance == nil {
		return nil
	}
	return c
}

func (s *OpenAIGatewayService) observeOpenAICreditsEvent(account *Account, payload []byte) {
	if credits := parseOpenAICreditsEvent(payload); credits != nil {
		persistOpenAICreditsAsync(s.accountRepo, captureOpenAICreditsRequestIdentity(account), credits)
	}
}

func observeOpenAICreditsResponse(repo AccountRepository, expected *Account, response *http.Response) {
	if repo == nil || expected == nil || response == nil || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return
	}
	persistOpenAICreditsHeadersAsync(repo, expected, response.Header)
	if response.Body != nil && strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		response.Body = &openAICreditsStream{ReadCloser: response.Body, observe: func(data []byte) {
			persistOpenAICreditsAsync(repo, expected, parseOpenAICreditsEvent(data))
		}}
	}
}

// The tap never buffers delivery to the client. Its bounded SSE copy observes
// only complete events, preserving every original byte and read/close error.
type openAICreditsStream struct {
	io.ReadCloser
	line         []byte
	data         []byte
	discard      bool
	lineNonEmpty bool
	observe      func([]byte)
}

func (s *openAICreditsStream) Read(p []byte) (int, error) {
	n, err := s.ReadCloser.Read(p)
	remaining := p[:n]
	for len(remaining) > 0 {
		end := bytes.IndexByte(remaining, '\n')
		chunk := remaining
		if end >= 0 {
			chunk = remaining[:end]
		}
		if len(bytes.Trim(chunk, "\r")) > 0 {
			s.lineNonEmpty = true
		}
		if !s.discard {
			if len(s.line)+len(s.data)+len(chunk) > 64*1024 {
				s.discard = true
				s.line, s.data = nil, nil
			} else {
				s.line = append(s.line, chunk...)
			}
		}
		if end < 0 {
			break
		}
		line := bytes.TrimSuffix(s.line, []byte{'\r'})
		if !s.lineNonEmpty {
			if !s.discard && len(s.data) > 0 {
				s.observe(s.data)
			}
			s.data, s.discard = nil, false
		} else if !s.discard && bytes.HasPrefix(line, []byte("data:")) {
			value := bytes.TrimPrefix(line[5:], []byte{' '})
			if len(s.data) > 0 {
				s.data = append(s.data, '\n')
			}
			s.data = append(s.data, value...)
		}
		s.line = s.line[:0]
		s.lineNonEmpty = false
		remaining = remaining[end+1:]
	}
	return n, err
}

func captureOpenAICreditsRequestIdentity(account *Account) *Account {
	if account == nil || account.ID <= 0 || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth || account.IsShadow() {
		return nil
	}
	expected, err := cloneOpenAICodexSnapshotIdentity(account)
	if err != nil {
		return nil
	}
	return expected
}
