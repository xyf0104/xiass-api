package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/benchmark"
	"github.com/gin-gonic/gin"
)

const pelicanProbeKey = "xiass.internal.pelican_benchmark"

type openAIPelicanProbe struct{ beforeSend func(string) error }

func pelicanProbe(c *gin.Context) *openAIPelicanProbe {
	value, _ := c.Get(pelicanProbeKey)
	probe, _ := value.(*openAIPelicanProbe)
	return probe
}

func PelicanBenchmarkEligibility(account *Account, model string) string {
	if account == nil || account.Platform != PlatformOpenAI ||
		(account.Type != AccountTypeOAuth && account.Type != AccountTypeAPIKey) ||
		account.IsCredentialShadow() || account.IsSyntheticUITest() {
		return "ineligible"
	}
	if strings.EqualFold(strings.TrimSpace(account.GetCredential("plan_type")), "free") {
		return "free_plan"
	}
	if account.Type == AccountTypeOAuth && !account.IsOpenAIChatGPTSubscription() {
		return "unknown_plan"
	}
	// Agent Identity registration/recovery mutates credentials. Do not quietly
	// use that flow in this explicitly read-only benchmark adapter.
	if account.IsOpenAIAgentIdentity() {
		return "unsupported_auth_mode"
	}
	if !account.IsModelSupported(model) || isOpenAIImageModel(account.GetMappedModel(model)) {
		return "model_not_supported"
	}
	return ""
}

func (s *AccountTestService) RunPelicanBenchmark(ctx context.Context, accountID int64, model string, beforeSend func(string) error) (benchmark.Output, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return benchmark.Output{ErrorCode: "account_unavailable"}, err
	}
	if reason := PelicanBenchmarkEligibility(account, model); reason != "" {
		return benchmark.Output{ErrorCode: reason}, errors.New(reason)
	}
	if !benchmark.ValidModel(model) {
		return benchmark.Output{ErrorCode: "invalid_model"}, errors.New("invalid model")
	}
	// Benchmarking must use the account owner's fixed egress and reject offline owners.
	policy := resolveExecutionNodeRoutingPolicy(ctx, s.cfg, s.settingService)
	if policy.unavailable || !policy.accountEgressIDAllowed(account) ||
		(policy.enabled && !policy.nodeHealthy(policy.nodeID(account))) ||
		(account.ProxyID != nil && (account.Proxy == nil || account.Proxy.ID != *account.ProxyID ||
			!account.Proxy.IsActive() || account.Proxy.IsExpired(time.Now()))) {
		return benchmark.Output{ErrorCode: "fixed_egress_unavailable"}, errors.New("fixed egress unavailable")
	}
	local := *account
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	capture := &pelicanCapture{header: make(http.Header), cancel: cancel}
	c, _ := gin.CreateTestContext(capture)
	c.Request, _ = http.NewRequestWithContext(ctx, http.MethodPost, "/internal/pelican-benchmark", nil)
	c.Set(pelicanProbeKey, &openAIPelicanProbe{beforeSend: beforeSend})
	// Call the existing OpenAI-only test path, bypassing TestAccountConnection's
	// last_used write and the admin handler's scheduling recovery.
	err = s.testOpenAIAccountConnection(c, &local, model, benchmark.Prompt, AccountTestModeDefault)
	if capture.tooLarge {
		return benchmark.Output{ErrorCode: "html_too_large"}, benchmark.ErrTooLarge
	}
	if err != nil || !capture.complete || capture.failed {
		return benchmark.Output{ErrorCode: "upstream_failed"}, errors.New("benchmark upstream failed")
	}
	html := strings.TrimSpace(capture.text.String())
	if strings.HasPrefix(html, "```") {
		if start := strings.IndexByte(html, '\n'); start >= 0 && strings.HasSuffix(html, "```") {
			html = strings.TrimSpace(html[start+1 : len(html)-3])
		}
	}
	if !utf8.ValidString(html) || strings.ContainsRune(html, '\x00') ||
		!strings.Contains(strings.ToLower(html), "<html") || !strings.Contains(strings.ToLower(html), "</html>") {
		return benchmark.Output{ErrorCode: "invalid_html"}, errors.New("model did not return a complete HTML document")
	}
	return benchmark.Output{HTML: html}, nil
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
			if w.text.Len()+len(event.Text) > benchmark.MaxHTMLBytes {
				w.tooLarge = true
				w.cancel()
				return n, nil
			}
			_, _ = w.text.WriteString(event.Text)
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
