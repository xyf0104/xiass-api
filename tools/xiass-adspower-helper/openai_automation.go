package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/pquerna/otp/totp"
)

const openAIAutomationCallback = "http://localhost:1455/auth/callback"

type oauthPageInput struct {
	Index    int    `json:"index"`
	Metadata string `json:"metadata"`
}

type oauthPageAction struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

type oauthPageSnapshot struct {
	URL     string            `json:"url"`
	Body    string            `json:"body"`
	Inputs  []oauthPageInput  `json:"inputs"`
	Actions []oauthPageAction `json:"actions"`
	Captcha bool              `json:"captcha"`
}

type automationState struct {
	Kind        string
	Input       int
	Inputs      []int
	Action      int
	VisibleMail string
}

var (
	emailAddressPattern       = regexp.MustCompile(`(?i)[A-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Z0-9.-]+\.[A-Z]{2,}`)
	invalidCredentialsPattern = regexp.MustCompile(`(?i)incorrect (?:email address or password|email or password|password)|invalid (?:email or password|credentials)|wrong password`)
	deletedAccountPattern     = regexp.MustCompile(`(?i)(?:your |this )?account (?:has been |is )?(?:deleted|deactivated|disabled)|account_(?:deleted|deactivated|disabled)|账号.*(?:已删除|删除|已停用|停用)`)
	blockedAccountPattern     = regexp.MustCompile(`(?i)(?:your |this )?account (?:has been |is )?(?:suspended|banned|restricted|limited)|account_(?:suspended|restricted|limited)|账号.*(?:封禁|受限|限制)`)
	invalidCodePattern        = regexp.MustCompile(`(?i)incorrect (?:verification )?code|invalid (?:verification )?code|wrong code|code (?:is|was) invalid|验证码.*(?:错误|无效)`)
)

const oauthSnapshotJS = `(() => {
  const visible = (element) => {
    const style = getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return style.visibility !== 'hidden' && style.display !== 'none' && rect.width > 0 && rect.height > 0;
  };
  const inputs = Array.from(document.querySelectorAll('input')).filter(visible).map((input, index) => ({
    index,
    metadata: ['type','name','id','autocomplete','aria-label','placeholder','inputmode'].map(key => input.getAttribute(key) || '').join(' ').toLowerCase()
  }));
  const actions = Array.from(document.querySelectorAll('button,a,[role="button"],[role="link"],[role="radio"],[role="option"]')).filter(visible).map((action, index) => ({
    index,
    text: String(action.innerText || action.textContent || action.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim()
  }));
  return {
    url: location.href,
    body: String(document.body?.innerText || '').replace(/\s+/g, ' ').trim().slice(0, 100000),
    inputs,
    actions,
    captcha: Boolean(document.querySelector('iframe[src*="captcha"],iframe[src*="challenges.cloudflare.com"]'))
  };
})()`

func (s *helperServer) runOpenAIAutomation(origin string, launch *launchPayload, profileID string, session *adsPowerBrowserSession) {
	if launch == nil {
		return
	}
	defer func() {
		launch.Password = ""
		launch.TOTPSecret = ""
		launch.EmailCodeToken = ""
	}()
	endpoint := ""
	if session != nil {
		endpoint = strings.TrimSpace(session.WebSocket.Puppeteer)
	}
	if endpoint == "" {
		s.finishAutomation(origin, launch, profileID, "failed", "failed", "browser_context_lost")
		return
	}
	deadline := launch.CallbackExpiresAt
	if deadline.IsZero() || !deadline.After(time.Now()) {
		deadline = time.Now().Add(15 * time.Minute)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	allocator, cancelAllocator := chromedp.NewRemoteAllocator(ctx, endpoint)
	defer cancelAllocator()
	browser, cancelBrowser := chromedp.NewContext(allocator)
	defer cancelBrowser()

	s.progress(origin, launch, "running", "opening", "")
	if err := chromedp.Run(browser, chromedp.Navigate(launch.AuthURL)); err != nil {
		s.finishAutomation(origin, launch, profileID, "failed", "failed", automationErrorReason(err, "opening"))
		return
	}
	if err := s.automateOpenAI(ctx, browser, origin, launch); err != nil {
		failure := automationFailure(err)
		stage := "blocked"
		if failure.Status == "failed" {
			stage = "failed"
		}
		s.finishAutomation(origin, launch, profileID, failure.Status, stage, failure.Reason)
	}
}

type automationFailureResult struct {
	Status string
	Reason string
}

type automationFailureError struct {
	Status string
	Reason string
}

func (e automationFailureError) Error() string { return e.Reason }

func automationFailure(err error) automationFailureResult {
	var failure automationFailureError
	if errors.As(err, &failure) {
		return automationFailureResult{Status: failure.Status, Reason: failure.Reason}
	}
	return automationFailureResult{Status: "failed", Reason: automationErrorReason(err, "")}
}

func automationErrorReason(err error, stage string) string {
	message := strings.ToLower(fmt.Sprint(err))
	if strings.Contains(message, "context deadline") {
		return "task_expired"
	}
	if stage == "opening" && strings.Contains(message, "timeout") {
		return "navigation_timeout"
	}
	if strings.Contains(message, "target closed") || strings.Contains(message, "browser") && strings.Contains(message, "closed") {
		return "browser_context_lost"
	}
	return "page_interaction_failed"
}

func blocked(reason string) error { return automationFailureError{Status: "blocked", Reason: reason} }

func (s *helperServer) automateOpenAI(ctx, browser context.Context, origin string, launch *launchPayload) error {
	attempted := make(map[string]time.Time)
	emailSubmitted := false
	var emailSession *emailCodeSession
	if strings.TrimSpace(launch.LoginMethod) == "email_code" {
		var err error
		emailSession, err = newEmailCodeSession(ctx, launch.LoginEmail, launch.EmailCodeToken)
		launch.EmailCodeToken = ""
		if err != nil {
			return blocked(emailCodeReason(err))
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var snapshot oauthPageSnapshot
		if err := chromedp.Run(browser, chromedp.Evaluate(oauthSnapshotJS, &snapshot)); err != nil {
			return err
		}
		state := inspectOAuthPage(snapshot)
		if state.Kind == "callback" {
			return nil
		}
		if state.VisibleMail != "" && !strings.EqualFold(state.VisibleMail, strings.TrimSpace(launch.LoginEmail)) {
			return blocked("invalid_credentials")
		}
		switch state.Kind {
		case "oauth_session_expired", "openai_route_error":
			return automationFailureError{Status: "failed", Reason: state.Kind}
		case "captcha":
			return blocked("captcha_required")
		case "account_deleted_or_disabled", "account_blocked", "invalid_credentials", "invalid_email_code", "invalid_totp", "invalid_sms_code":
			return blocked(state.Kind)
		case "phone", "phone_rejected", "sms_code":
			return blocked("reauthorization_phone_required")
		case "login":
			if recentAttempt(attempted, state.Kind) {
				if attemptExpired(attempted, state.Kind) {
					return automationFailureError{Status: "failed", Reason: "page_interaction_failed"}
				}
				break
			}
			s.progress(origin, launch, "running", "login", "")
			if err := clickOAuthAction(browser, state.Action); err != nil {
				return err
			}
		case "email":
			if recentAttempt(attempted, state.Kind) {
				if attemptExpired(attempted, state.Kind) {
					return blocked("invalid_credentials")
				}
				break
			}
			s.progress(origin, launch, "running", "email", "")
			if err := fillOAuthInput(browser, state.Input, launch.LoginEmail); err != nil {
				return err
			}
			if err := clickOAuthContinue(browser); err != nil {
				return err
			}
			emailSubmitted = true
		case "password":
			if state.VisibleMail != "" {
				emailSubmitted = true
			}
			if strings.TrimSpace(launch.LoginMethod) == "email_code" {
				if recentAttempt(attempted, "email_code_switch") {
					if attemptExpired(attempted, "email_code_switch") {
						return blocked("email_code_required")
					}
					break
				}
				if err := clickOAuthText(browser, []string{"continue with code", "use a code", "use verification code", "email me a code", "send login code", "使用验证码", "发送验证码"}); err != nil {
					return blocked("email_code_required")
				}
				break
			}
			if recentAttempt(attempted, state.Kind) {
				if attemptExpired(attempted, state.Kind) {
					return blocked("invalid_credentials")
				}
				break
			}
			s.progress(origin, launch, "running", "password", "")
			if err := fillOAuthInput(browser, state.Input, launch.Password); err != nil {
				return err
			}
			launch.Password = ""
			if err := clickOAuthContinue(browser); err != nil {
				return err
			}
		case "totp":
			if recentAttempt(attempted, state.Kind) {
				if attemptExpired(attempted, state.Kind) {
					return blocked("invalid_totp")
				}
				break
			}
			if strings.TrimSpace(launch.TOTPSecret) == "" {
				return blocked("authenticator_required")
			}
			s.progress(origin, launch, "running", "totp", "")
			code, err := totp.GenerateCode(strings.ReplaceAll(launch.TOTPSecret, " ", ""), time.Now().UTC())
			launch.TOTPSecret = ""
			if err != nil {
				return blocked("invalid_totp")
			}
			if err := fillOAuthCode(browser, state.Inputs, code); err != nil {
				return err
			}
		case "email_code":
			if emailSession == nil {
				return blocked("email_code_required")
			}
			if recentAttempt(attempted, state.Kind) {
				if attemptExpired(attempted, state.Kind) {
					return blocked("invalid_email_code")
				}
				break
			}
			s.progress(origin, launch, "running", "email_code_waiting", "")
			code, err := emailSession.waitForCode(ctx, time.Now())
			if err != nil {
				return blocked(emailCodeReason(err))
			}
			s.progress(origin, launch, "running", "email_code_submitting", "")
			if err := fillOAuthCode(browser, state.Inputs, code); err != nil {
				return err
			}
			emailSubmitted = true
		case "workspace":
			if recentAttempt(attempted, state.Kind) {
				if attemptExpired(attempted, state.Kind) {
					return automationFailureError{Status: "failed", Reason: "page_interaction_failed"}
				}
				break
			}
			if !emailSubmitted && state.VisibleMail == "" {
				// A bound AdsPower profile may already hold the matching OpenAI
				// session. The backend still verifies the OAuth identity before it
				// updates the account, so continuing here cannot swap identities.
				emailSubmitted = true
			}
			s.progress(origin, launch, "running", "workspace", "")
			_ = clickOAuthText(browser, []string{"default workspace", "默认工作空间"})
			if err := clickOAuthContinue(browser); err != nil {
				return err
			}
			s.progress(origin, launch, "running", "callback_waiting", "")
		case "unknown":
			if attempted["unknown"].IsZero() {
				attempted["unknown"] = time.Now()
			} else if time.Since(attempted["unknown"]) > 20*time.Second {
				return blocked("manual_challenge")
			}
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func recentAttempt(attempted map[string]time.Time, key string) bool {
	last := attempted[key]
	if last.IsZero() {
		attempted[key] = time.Now()
		return false
	}
	return true
}

func attemptExpired(attempted map[string]time.Time, key string) bool {
	started := attempted[key]
	return !started.IsZero() && time.Since(started) > 20*time.Second
}

func inspectOAuthPage(snapshot oauthPageSnapshot) automationState {
	parsed, _ := url.Parse(snapshot.URL)
	if parsed != nil && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1") && parsed.Port() == "1455" && parsed.Path == "/auth/callback" {
		return automationState{Kind: "callback"}
	}
	body := snapshot.Body
	state := automationState{}
	if match := emailAddressPattern.FindString(body); match != "" {
		state.VisibleMail = strings.ToLower(match)
	}
	lower := strings.ToLower(body)
	switch {
	case strings.Contains(lower, "error_code: invalid_state") || strings.Contains(lower, "sign-in session is no longer valid") || strings.Contains(lower, "session ended"):
		state.Kind = "oauth_session_expired"
	case strings.Contains(lower, "route error") && strings.Contains(lower, "invalid content type"):
		state.Kind = "openai_route_error"
	case snapshot.Captcha || strings.Contains(lower, "verify that you are human") || strings.Contains(lower, "checking your browser") || strings.Contains(lower, "人机验证"):
		state.Kind = "captcha"
	case deletedAccountPattern.MatchString(body):
		state.Kind = "account_deleted_or_disabled"
	case blockedAccountPattern.MatchString(body):
		state.Kind = "account_blocked"
	case invalidCredentialsPattern.MatchString(body):
		state.Kind = "invalid_credentials"
	default:
		codeInputs := matchingInputs(snapshot.Inputs, func(value string) bool {
			return strings.Contains(value, "one-time-code") || strings.Contains(value, "inputmode numeric") || strings.Contains(value, "code") || strings.Contains(value, "otp")
		})
		if len(codeInputs) > 0 {
			invalid := invalidCodePattern.MatchString(body)
			switch {
			case strings.Contains(lower, "authenticator") || strings.Contains(lower, "authentication app") || strings.Contains(lower, "身份验证器"):
				if invalid {
					state.Kind = "invalid_totp"
				} else {
					state.Kind = "totp"
				}
			case strings.Contains(lower, "text message") || strings.Contains(lower, "sms") || strings.Contains(lower, "phone") || strings.Contains(lower, "短信"):
				if invalid {
					state.Kind = "invalid_sms_code"
				} else {
					state.Kind = "sms_code"
				}
			default:
				if invalid {
					state.Kind = "invalid_email_code"
				} else {
					state.Kind = "email_code"
				}
			}
			state.Inputs = codeInputs
			return state
		}
		if input, ok := firstMatchingInput(snapshot.Inputs, func(value string) bool {
			return strings.Contains(value, "tel") || strings.Contains(value, "phone") || strings.Contains(value, "mobile")
		}); ok {
			state.Kind, state.Input = "phone", input
			return state
		}
		if input, ok := firstMatchingInput(snapshot.Inputs, func(value string) bool { return strings.Contains(value, "password") }); ok {
			state.Kind, state.Input = "password", input
			return state
		}
		if input, ok := firstMatchingInput(snapshot.Inputs, func(value string) bool {
			return strings.Contains(value, "email") && !strings.Contains(value, "code") && !strings.Contains(value, "otp")
		}); ok {
			state.Kind, state.Input = "email", input
			return state
		}
		if strings.Contains(lower, "workspace") || strings.Contains(lower, "organization") || strings.Contains(lower, "continue to codex") || strings.Contains(lower, "authorize codex") || strings.Contains(lower, "工作空间") {
			state.Kind = "workspace"
			return state
		}
		if action, ok := firstMatchingAction(snapshot.Actions, []string{"log in", "sign in", "use another account", "登录"}); ok {
			state.Kind, state.Action = "login", action
			return state
		}
		state.Kind = "unknown"
	}
	return state
}

func matchingInputs(inputs []oauthPageInput, match func(string) bool) []int {
	result := make([]int, 0, len(inputs))
	for _, input := range inputs {
		if match(input.Metadata) {
			result = append(result, input.Index)
		}
	}
	return result
}

func firstMatchingInput(inputs []oauthPageInput, match func(string) bool) (int, bool) {
	for _, input := range inputs {
		if match(input.Metadata) {
			return input.Index, true
		}
	}
	return 0, false
}

func firstMatchingAction(actions []oauthPageAction, patterns []string) (int, bool) {
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		for _, action := range actions {
			text := strings.ToLower(strings.TrimSpace(action.Text))
			if text == pattern || strings.Contains(text, pattern) {
				return action.Index, true
			}
		}
	}
	return 0, false
}

func fillOAuthInput(browser context.Context, index int, value string) error {
	payload, _ := json.Marshal(value)
	expression := fmt.Sprintf(`(() => { const visible=e=>{const s=getComputedStyle(e),r=e.getBoundingClientRect();return s.visibility!=='hidden'&&s.display!=='none'&&r.width>0&&r.height>0}; const items=Array.from(document.querySelectorAll('input')).filter(visible); const input=items[%d]; if(!input) throw new Error('input missing'); input.focus(); const setter=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set; setter.call(input,%s); input.dispatchEvent(new Event('input',{bubbles:true})); input.dispatchEvent(new Event('change',{bubbles:true})); return true; })()`, index, payload)
	return chromedp.Run(browser, chromedp.Evaluate(expression, nil))
}

func fillOAuthCode(browser context.Context, inputs []int, code string) error {
	if len(inputs) == 0 {
		return errors.New("verification input missing")
	}
	if len(inputs) == 1 {
		if err := fillOAuthInput(browser, inputs[0], code); err != nil {
			return err
		}
	} else {
		if len(inputs) != len(code) {
			return errors.New("verification input count mismatch")
		}
		for i, index := range inputs {
			if err := fillOAuthInput(browser, index, string(code[i])); err != nil {
				return err
			}
		}
	}
	_ = clickOAuthContinue(browser)
	return nil
}

func clickOAuthAction(browser context.Context, index int) error {
	expression := fmt.Sprintf(`(() => { const visible=e=>{const s=getComputedStyle(e),r=e.getBoundingClientRect();return s.visibility!=='hidden'&&s.display!=='none'&&r.width>0&&r.height>0}; const items=Array.from(document.querySelectorAll('button,a,[role="button"],[role="link"],[role="radio"],[role="option"]')).filter(visible); const action=items[%d]; if(!action) throw new Error('action missing'); action.click(); return true; })()`, index)
	return chromedp.Run(browser, chromedp.Evaluate(expression, nil))
}

func clickOAuthContinue(browser context.Context) error {
	return clickOAuthText(browser, []string{"continue", "next", "verify", "allow", "authorize", "继续", "下一步"})
}

func clickOAuthText(browser context.Context, patterns []string) error {
	payload, _ := json.Marshal(patterns)
	expression := fmt.Sprintf(`(() => { const wanted=%s.map(v=>String(v).toLowerCase()); const visible=e=>{const s=getComputedStyle(e),r=e.getBoundingClientRect();return s.visibility!=='hidden'&&s.display!=='none'&&r.width>0&&r.height>0}; const items=Array.from(document.querySelectorAll('button,a,[role="button"],[role="link"],[role="radio"],[role="option"]')).filter(visible); const action=items.find(e=>{const text=String(e.innerText||e.textContent||e.getAttribute('aria-label')||'').replace(/\s+/g,' ').trim().toLowerCase();return wanted.some(v=>text===v||text.includes(v))}); if(!action) throw new Error('action missing'); action.click(); return true; })()`, payload)
	return chromedp.Run(browser, chromedp.Evaluate(expression, nil))
}

func (s *helperServer) progress(origin string, launch *launchPayload, status, stage, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.reportProgress(ctx, origin, launch, status, stage, reason)
}

func (s *helperServer) finishAutomation(origin string, launch *launchPayload, profileID, status, stage, reason string) {
	s.progress(origin, launch, status, stage, reason)
	if parsed, err := url.Parse(launch.AuthURL); err == nil {
		s.removeCallback(strings.TrimSpace(parsed.Query().Get("state")))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, adsPower := s.runtimeSnapshot()
	_ = adsPower.stopProfile(ctx, profileID)
}

func validCallback(raw, expectedState string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "localhost" || parsed.Port() != "1455" || parsed.Path != "/auth/callback" || parsed.Query().Get("error") != "" {
		return false
	}
	state := []byte(parsed.Query().Get("state"))
	expected := []byte(expectedState)
	return parsed.Query().Get("code") != "" && len(state) == len(expected) && subtle.ConstantTimeCompare(state, expected) == 1
}
