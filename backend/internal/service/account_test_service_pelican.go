package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"github.com/gin-gonic/gin"
)

const pelicanProbeKey = "xiass.internal.pelican_benchmark"

type openAIPelicanProbe struct {
	beforeSend   func(string) error
	errorCode    string
	errorMessage string
	secrets      []string
	completion   *pelicanCompletion
}

var pelicanSensitiveText = regexp.MustCompile(`(?i)https?://[^\s]+|socks5?://[^\s]+|bearer\s+[^\s]+|sk-[A-Za-z0-9_-]+|eyJ[A-Za-z0-9_.-]+|[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}`)

func pelicanUpstreamMessage(raw []byte, secrets []string) string {
	if len(raw) > pelicanErrorBodyLimit {
		return ""
	}
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	if response, ok := value["response"].(map[string]any); ok {
		value = response
	}
	var message string
	switch detail := value["error"].(type) {
	case map[string]any:
		message, _ = detail["message"].(string)
	case string:
		message = detail
	}
	if message == "" {
		message, _ = value["message"].(string)
	}
	if message == "" {
		message, _ = value["detail"].(string)
	}
	for _, secret := range secrets {
		if len(secret) >= 4 {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	message = logredact.RedactText(message, "authorization", "api_key", "token", "cookie", "secret")
	message = pelicanSensitiveText.ReplaceAllString(message, "[redacted]")
	message = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, message)
	if len(message) > 2048 {
		message = message[:2048]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return strings.TrimSpace(message)
}

func (s *AccountTestService) pelicanHTTPError(c *gin.Context, resp *http.Response) error {
	code := fmt.Sprintf("upstream_http_%d", resp.StatusCode)
	if resp.Body != nil {
		body, err := io.ReadAll(io.LimitReader(resp.Body, pelicanErrorBodyLimit+1))
		if err == nil && len(body) <= pelicanErrorBodyLimit {
			pelicanProbe(c).errorMessage = pelicanUpstreamMessage(body, pelicanProbe(c).secrets)
			copy := *resp
			copy.Body = io.NopCloser(bytes.NewReader(body))
			classified := pelicanHTTPErrorCode(&copy)
			if classified != code {
				pelicanProbe(c).errorMessage = "HTTP 400: " + pelicanProbe(c).errorMessage
				code = classified
			}
		}
	}
	return s.pelicanError(c, code)
}

func (s *AccountTestService) pelicanStreamError(c *gin.Context, raw string) error {
	pelicanProbe(c).errorMessage = pelicanUpstreamMessage([]byte(raw), pelicanProbe(c).secrets)
	return s.pelicanError(c, "upstream_stream_error")
}

func pelicanMessages(ctx context.Context, responses bool) []map[string]any {
	message := func(role, text string) map[string]any {
		var content any = text
		if responses {
			kind := "input_text"
			if role == "assistant" {
				kind = "output_text"
			}
			content = []map[string]any{{"type": kind, "text": text}}
		}
		return map[string]any{"role": role, "content": content}
	}
	messages := []map[string]any{message("user", benchmark.RequestPrompt(ctx))}
	if previous, ok := benchmark.Continuation(ctx); ok {
		messages = append(messages, message("assistant", previous), message("user", "继续"))
	}
	return messages
}

func pelicanInstructions(ctx context.Context) string {
	testType, _, _ := benchmark.TestFromContext(ctx)
	if testType == benchmark.TestCandy {
		return "Solve the requested problem. End with the final answer in the format requested by the user."
	}
	return "Return only the requested HTML document."
}

func (s *AccountTestService) pelicanError(c *gin.Context, code string) error {
	pelicanProbe(c).errorCode = code
	return s.sendErrorAndEnd(c, code)
}

const pelicanErrorBodyLimit = 16 * 1024

func pelicanHTTPErrorCode(resp *http.Response) string {
	code := fmt.Sprintf("upstream_http_%d", resp.StatusCode)
	if resp.StatusCode != http.StatusBadRequest || resp.Body == nil {
		return code
	}
	// Inspect only a bounded structured message. Neither the body nor its text
	// escapes this classifier, including on malformed/oversized responses.
	body, err := io.ReadAll(io.LimitReader(resp.Body, pelicanErrorBodyLimit+1))
	if err != nil || len(body) > pelicanErrorBodyLimit {
		return code
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
		return code
	}
	raw, hasError := envelope["error"]
	var message string
	if hasError {
		if json.Unmarshal(raw, &message) != nil {
			var object map[string]json.RawMessage
			if json.Unmarshal(raw, &object) != nil || json.Unmarshal(object["message"], &message) != nil {
				return code
			}
		}
	} else if json.Unmarshal(envelope["detail"], &message) != nil {
		return code
	}
	model, ok := strings.CutPrefix(message, "The '")
	if !ok {
		return code
	}
	model, ok = strings.CutSuffix(model, "' model requires a newer version of Codex. Please upgrade to the latest app or CLI and try again.")
	if ok && benchmark.ValidModel(model) {
		return "upstream_client_upgrade_required"
	}
	return code
}

func pelicanProbe(c *gin.Context) *openAIPelicanProbe {
	value, _ := c.Get(pelicanProbeKey)
	probe, _ := value.(*openAIPelicanProbe)
	return probe
}

func PelicanBenchmarkEligibility(account *Account, model string) string {
	if account == nil || !pelicanAccountSupported(account) ||
		account.IsCredentialShadow() || account.IsSyntheticUITest() {
		return "ineligible"
	}
	if account.Platform == PlatformOpenAI {
		if strings.EqualFold(strings.TrimSpace(account.GetCredential("plan_type")), "free") {
			return "free_plan"
		}
		if account.Type == AccountTypeOAuth && !account.IsOpenAIChatGPTSubscription() {
			return "unknown_plan"
		}
		// Agent Identity registration/recovery mutates credentials.
		if account.IsOpenAIAgentIdentity() {
			return "unsupported_auth_mode"
		}
	}
	if !account.IsModelSupported(model) || pelicanNonTextModel(model) || pelicanNonTextModel(account.GetMappedModel(model)) {
		return "model_not_supported"
	}
	return ""
}

func (s *AccountTestService) RunPelicanBenchmark(ctx context.Context, accountID int64, model string, beforeSend func(string) error) (benchmark.Output, error) {
	testType, prompt, err := benchmark.TestFromContext(ctx)
	if err != nil {
		return benchmark.Output{ErrorCode: "invalid_test"}, err
	}
	if _, continuing := benchmark.Continuation(ctx); testType == benchmark.TestCandy && continuing {
		return benchmark.Output{ErrorCode: "invalid_continuation_source"}, errors.New("candy tests cannot continue HTML")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return benchmark.Output{ErrorCode: "account_unavailable"}, errors.New("account_unavailable")
	}
	if reason := PelicanBenchmarkEligibility(account, model); reason != "" {
		return benchmark.Output{ErrorCode: reason}, errors.New(reason)
	}
	if !benchmark.ValidModel(model) {
		return benchmark.Output{ErrorCode: "invalid_model"}, errors.New("invalid model")
	}
	if account.ProxyID != nil && (account.Proxy == nil || account.Proxy.ID != *account.ProxyID ||
		!account.Proxy.IsActive() || account.Proxy.IsExpired(time.Now())) {
		return benchmark.Output{ErrorCode: "proxy_unavailable"}, errors.New("proxy unavailable")
	}
	local := *account
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	capture := &pelicanCapture{header: make(http.Header), cancel: cancel}
	previous, _ := benchmark.Continuation(ctx)
	if len(previous) > benchmark.MaxHTMLBytes {
		return benchmark.Output{HTML: pelicanBoundedText(previous), ErrorCode: "html_too_large"}, benchmark.ErrTooLarge
	}
	c, _ := gin.CreateTestContext(capture)
	c.Request, _ = http.NewRequestWithContext(ctx, http.MethodPost, "/internal/pelican-benchmark", nil)
	probe := &openAIPelicanProbe{beforeSend: beforeSend}
	probe.addSecrets(local.Credentials)
	if proxy := local.requestProxy(); proxy != nil {
		probe.secrets = append(probe.secrets, proxy.Password, proxy.Username, proxy.Host)
	}
	c.Set(pelicanProbeKey, probe)
	c.Request = c.Request.WithContext(context.WithValue(ctx, pelicanContextKey{}, probe))
	// Call the existing protocol adapters, bypassing TestAccountConnection's
	// last_used write and the admin handler's scheduling recovery.
	err = s.testAccountProtocolConnection(c, &local, model, prompt, AccountTestModeDefault)
	text := capture.text.String()
	if testType != benchmark.TestCandy {
		text = pelicanUnwrapHTML(text)
		if !pelicanCompleteHTML(text) {
			text = pelicanUnwrapHTML(pelicanUnwrapHTML(previous) + text)
		}
	}
	output := benchmark.Output{}
	if testType == benchmark.TestCandy {
		output.TextOutput = pelicanBoundedText(text)
	} else {
		output.HTML = pelicanBoundedText(text)
	}
	if capture.tooLarge || len(text) > benchmark.MaxHTMLBytes {
		output.ErrorCode = "html_too_large"
		if testType == benchmark.TestCandy {
			output.ErrorCode = "text_too_large"
		}
		return output, benchmark.ErrTooLarge
	}
	if err != nil || !capture.complete || capture.failed {
		code := probe.errorCode
		if code == "" {
			code = "upstream_incomplete"
			if err != nil {
				code = "upstream_failed"
			}
		}
		output.ErrorCode, output.ErrorMessage = code, probe.errorMessage
		return output, errors.New(code)
	}
	if testType == benchmark.TestCandy {
		return output, nil
	}
	html := pelicanUnwrapHTML(text)
	if !pelicanCompleteHTML(html) {
		message := "cause=html_incomplete; terminal=received"
		if probe.completion != nil {
			message = probe.completion.message("html_incomplete")
		}
		return benchmark.Output{HTML: text, ErrorCode: "upstream_incomplete", ErrorMessage: message}, errors.New("upstream_incomplete")
	}
	return benchmark.Output{HTML: html}, nil
}

func pelicanBoundedText(text string) string {
	if len(text) <= benchmark.MaxHTMLBytes {
		return text
	}
	text = text[:benchmark.MaxHTMLBytes]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

func pelicanUnwrapHTML(text string) string {
	html := strings.TrimSpace(text)
	if pelicanCompleteHTML(html) {
		return html
	}
	// Raw documents may contain Markdown examples inside scripts. Only remove
	// an orphan closing fence after a complete document (a continuation tail).
	rawHTML := pelicanStartsHTML(html)
	var marker byte
	fenceLen, contentStart := 0, 0
	eligible := false
	for offset := 0; offset < len(text); {
		line, rest, found := strings.Cut(text[offset:], "\n")
		next := len(text)
		if found {
			next -= len(rest)
		}
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= 3 && (trimmed[0] == '`' || trimmed[0] == '~') {
			count := 1
			for count < len(trimmed) && trimmed[count] == trimmed[0] {
				count++
			}
			info := strings.TrimSpace(trimmed[count:])
			if rawHTML {
				if count >= 3 && info == "" && pelicanCompleteHTML(strings.TrimSpace(text[:offset])) {
					return strings.TrimSpace(text[:offset])
				}
			} else if marker == 0 && count >= 3 {
				marker, fenceLen, contentStart = trimmed[0], count, next
				eligible = strings.EqualFold(info, "html") || (info == "" && pelicanStartsHTML(strings.TrimSpace(text[next:])))
			} else if trimmed[0] == marker && count >= fenceLen && info == "" {
				if eligible {
					return pelicanNormalizeHTMLFragment(text[contentStart:offset])
				}
				marker = 0
			}
		}
		offset = next
	}
	if marker != 0 && eligible {
		return pelicanNormalizeHTMLFragment(text[contentStart:])
	}
	return text
}

// Preserve whitespace in partial output: a stream can stop inside a text
// node, attribute, or script token, and the next fragment must join exactly.
func pelicanNormalizeHTMLFragment(html string) string {
	if trimmed := strings.TrimSpace(html); pelicanCompleteHTML(trimmed) {
		return trimmed
	}
	return html
}

func pelicanStartsHTML(html string) bool {
	document := strings.ToLower(html)
	if strings.HasPrefix(document, "<!doctype html>") {
		document = strings.TrimSpace(strings.TrimPrefix(document, "<!doctype html>"))
	}
	if !strings.HasPrefix(document, "<html") || len(document) <= len("<html") {
		return false
	}
	switch document[len("<html")] {
	case '>', ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func pelicanCompleteHTML(html string) bool {
	return utf8.ValidString(html) && !strings.ContainsRune(html, '\x00') &&
		pelicanStartsHTML(html) && strings.HasSuffix(strings.ToLower(html), "</html>")
}

// The normal background test uses an unbounded httptest recorder. This writer
// consumes its same SSE events incrementally and retains only bounded HTML.
type pelicanCapture struct {
	header   http.Header
	pending  []byte
	text     strings.Builder
	cancel   context.CancelFunc
	complete bool
	failed   bool
	tooLarge bool
}

func (w *pelicanCapture) Header() http.Header { return w.header }
func (w *pelicanCapture) WriteHeader(int)     {}
func (w *pelicanCapture) Flush()              {}
func (w *pelicanCapture) appendText(text string) {
	remaining := benchmark.MaxHTMLBytes - w.text.Len()
	if len(text) > remaining {
		text = text[:remaining]
		for len(text) > 0 && !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		w.tooLarge = true
		w.cancel()
	}
	_, _ = w.text.WriteString(text)
}
func (w *pelicanCapture) Write(p []byte) (int, error) {
	n := len(p)
	if w.tooLarge {
		return n, nil
	}
	if len(w.pending)+n > 8*benchmark.MaxHTMLBytes {
		w.tooLarge = true
		w.cancel()
		return n, nil
	}
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(w.pending[:i])
		w.pending = w.pending[i+1:]
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		var event TestEvent
		if json.Unmarshal(bytes.TrimSpace(line[5:]), &event) != nil {
			w.failed = true
			continue
		}
		switch event.Type {
		case "content":
			w.appendText(event.Text)
			if w.tooLarge {
				return n, nil
			}
		case "test_complete":
			w.complete = event.Success
		case "error":
			w.failed = true
		}
	}
	if len(w.pending) == 0 {
		w.pending = nil
	}
	return n, nil
}

type pelicanLimitedBody struct {
	io.Reader
	io.Closer
}

func boundPelicanResponse(c *gin.Context, resp *http.Response) {
	if pelicanProbe(c) != nil {
		resp.Body = &pelicanLimitedBody{Reader: io.LimitReader(resp.Body, 16*benchmark.MaxHTMLBytes), Closer: resp.Body}
	}
}

// Keep diagnostics out of model text, and consume buffered data even when the
// request context has been canceled. Only model text deltas reach the capture.
func (s *AccountTestService) processPelicanStream(c *gin.Context, body io.Reader, chat bool) error {
	probe := pelicanProbe(c)
	completion := &pelicanCompletion{protocol: "responses"}
	if chat {
		completion.protocol = "chat"
	}
	probe.completion = completion
	defer probe.recordCompletionError()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 8*benchmark.MaxHTMLBytes)
	var data strings.Builder
	finished := false
	done := false
	process := func() error {
		if data.Len() == 0 {
			return nil
		}
		raw := strings.TrimSpace(data.String())
		data.Reset()
		if raw == "[DONE]" {
			if !finished {
				return s.pelicanError(c, "upstream_incomplete")
			}
			done = true
			return nil
		}
		var event map[string]any
		if json.Unmarshal([]byte(raw), &event) != nil {
			return s.pelicanError(c, "upstream_stream_error")
		}
		if _, ok := event["error"]; ok {
			return s.pelicanStreamError(c, raw)
		}
		if chat {
			choices, _ := event["choices"].([]any)
			for _, value := range choices {
				choice, _ := value.(map[string]any)
				// Only the first completion belongs to the continuation context.
				if index, ok := choice["index"].(float64); ok && index != 0 {
					continue
				}
				delta, _ := choice["delta"].(map[string]any)
				if text, ok := delta["content"].(string); ok {
					s.sendEvent(c, TestEvent{Type: "content", Text: text})
				}
				if reason, _ := choice["finish_reason"].(string); reason != "" {
					completion.setReason(reason)
					completion.terminal = true
					if reason != "stop" {
						completion.cause = completion.stopCause()
						return s.pelicanError(c, "upstream_incomplete")
					}
					finished = true
				}
			}
			return nil
		}
		switch event["type"] {
		case "response.output_text.delta":
			if text, ok := event["delta"].(string); ok {
				s.sendEvent(c, TestEvent{Type: "content", Text: text})
			}
		case "response.completed", "response.done":
			completion.terminal = true
			completion.setReason("completed")
			response, _ := event["response"].(map[string]any)
			if status, _ := response["status"].(string); status != "" && status != "completed" {
				completion.setReason(status)
				completion.cause = completion.stopCause()
				if status == "failed" {
					return s.pelicanStreamError(c, raw)
				}
				return s.pelicanError(c, "upstream_incomplete")
			}
			finished = true
		case "response.incomplete":
			completion.terminal = true
			completion.setReason("incomplete")
			response, _ := event["response"].(map[string]any)
			details, _ := response["incomplete_details"].(map[string]any)
			if reason, _ := details["reason"].(string); reason != "" {
				completion.setReason(reason)
			}
			completion.cause = completion.stopCause()
			return s.pelicanError(c, "upstream_incomplete")
		case "response.failed":
			completion.terminal = true
			completion.setReason("failed")
			return s.pelicanStreamError(c, raw)
		case "error":
			return s.pelicanStreamError(c, raw)
		}
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := process(); err != nil {
				return err
			}
			if finished && (!chat || done) {
				s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len()+len(line) > 8*benchmark.MaxHTMLBytes {
				return s.pelicanError(c, "upstream_stream_error")
			}
			_, _ = data.WriteString(strings.TrimPrefix(line, "data:"))
			_ = data.WriteByte('\n')
		}
	}
	if err := process(); err != nil {
		return err
	}
	if scanner.Err() != nil {
		return s.pelicanError(c, "upstream_stream_error")
	}
	if !finished {
		return s.pelicanError(c, "upstream_incomplete")
	}
	s.sendEvent(c, TestEvent{Type: "test_complete", Success: true})
	return nil
}
