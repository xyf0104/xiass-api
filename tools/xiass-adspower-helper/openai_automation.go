package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	cdpinput "github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
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
	Busy    bool              `json:"busy"`
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
	bannedAccountPattern      = regexp.MustCompile(`(?i)(?:your |this )?account (?:has been |is )?(?:suspended|banned)|account_(?:suspended|banned)|账号.*(?:封禁|封号)`)
	restrictedAccountPattern  = regexp.MustCompile(`(?i)(?:your |this )?account (?:has been |is )?(?:restricted|limited)|account_(?:restricted|limited)|账号.*(?:受限|限制)`)
	invalidCodePattern        = regexp.MustCompile(`(?i)incorrect (?:verification )?code|invalid (?:verification )?code|wrong code|code (?:is|was) invalid|验证码.*(?:错误|无效)`)
	phoneRejectedPattern      = regexp.MustCompile(`(?i)phone.*(?:invalid|not valid|unavailable|used too many|already (?:used|linked|associated)|maximum|max(?:imum)? number|not supported|cannot be used|can't be used)|(?:try|use) (?:a )?(?:different|another) phone number|too many (?:accounts|verification attempts)|too many.*phone|unable to (?:send|verify).*phone|无法使用.*号码|(?:手机号|电话号码).*(?:不可用|无效|次数过多|已使用|已绑定|不受支持)|请.*(?:更换|其他).*号码`)
	automationPhonePattern    = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)
	automationSMSCodePattern  = regexp.MustCompile(`^\d{4,10}$`)
	diagnosticCodePattern     = regexp.MustCompile(`\b\d{6,8}\b`)
	diagnosticTokenPattern    = regexp.MustCompile(`\b[A-Za-z0-9_-]{24,}\b`)
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
    captcha: Boolean(document.querySelector('iframe[src*="captcha"],iframe[src*="challenges.cloudflare.com"]')),
    busy: Array.from(document.querySelectorAll('button,[role="button"],form[aria-busy="true"]')).filter(visible).some(e =>
      e.getAttribute('aria-busy') === 'true' ||
      ((e.disabled || e.getAttribute('aria-disabled') === 'true') && /^(continue|next|verify|继续|下一步)$/i.test(String(e.innerText || e.textContent || '').trim())))
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
		if launch.emailSession != nil {
			launch.emailSession.token = ""
			launch.emailSession = nil
		}
	}()
	endpoint := ""
	if session != nil {
		if port := strings.TrimSpace(session.DebugPort); port != "" {
			endpoint = "http://127.0.0.1:" + port
		} else {
			endpoint = strings.TrimSpace(session.WebSocket.Puppeteer)
		}
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
	s.progress(origin, launch, "running", "opening", "")
	if strings.TrimSpace(launch.LoginMethod) == "email_code" {
		emailSession, err := newEmailCodeSession(ctx, launch.LoginEmail, launch.EmailCodeToken)
		launch.EmailCodeToken = ""
		if err != nil {
			s.finishAutomation(origin, launch, profileID, "failed", "failed", emailCodeReason(err))
			return
		}
		launch.emailSession = emailSession
	}
	launch.oauthStartedAt = time.Now()
	browser, closeBrowser, err := openAdsPowerOAuthTarget(ctx, endpoint, launch.AuthURL)
	if err != nil {
		log.Printf("AdsPower OAuth automation failed at opening: %s", automationErrorReason(err, "opening"))
		s.finishAutomation(origin, launch, profileID, "failed", "failed", automationErrorReason(err, "opening"))
		return
	}
	defer closeBrowser()
	if err := s.automateOpenAI(ctx, browser, origin, launch); err != nil {
		failure := automationFailure(err)
		stage := "blocked"
		if failure.Status == "failed" {
			stage = "failed"
		}
		log.Printf("AdsPower OAuth automation stopped at %s: %s", stage, failure.Reason)
		s.finishAutomation(origin, launch, profileID, failure.Status, stage, failure.Reason)
	}
}

func openAdsPowerOAuthTarget(parent context.Context, endpoint, authURL string) (context.Context, func(), error) {
	allocator, cancelAllocator := chromedp.NewRemoteAllocator(parent, endpoint)
	root, cancelRoot := chromedp.NewContext(allocator)

	cleanup := func() {
		cancelRoot()
		cancelAllocator()
	}

	type connectionResult struct {
		targets []*target.Info
		err     error
	}
	result := make(chan connectionResult, 1)
	go func() {
		targets, err := chromedp.Targets(root)
		result <- connectionResult{targets: targets, err: err}
	}()

	select {
	case connected := <-result:
		if connected.err != nil {
			cleanup()
			return nil, func() {}, connected.err
		}
		log.Printf("AdsPower CDP connected; existing targets: %s", summarizeOAuthTargets(connected.targets))
	case <-time.After(30 * time.Second):
		cleanup()
		return nil, func() {}, context.DeadlineExceeded
	case <-parent.Done():
		cleanup()
		return nil, func() {}, parent.Err()
	}

	browser, cancelBrowser := chromedp.NewContext(root)
	if err := chromedp.Run(browser); err != nil {
		cancelBrowser()
		cleanup()
		return nil, func() {}, err
	}
	navigationCtx, cancelNavigation := context.WithTimeout(browser, 10*time.Second)
	err := chromedp.Run(navigationCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, _, _, navigateErr := page.Navigate(authURL).Do(ctx)
		return navigateErr
	}))
	cancelNavigation()
	if err != nil {
		targets, targetsErr := chromedp.Targets(root)
		currentURL := browserTargetURL(browser, targets)
		if targetsErr != nil || currentURL == "" || currentURL == "about:blank" {
			cancelBrowser()
			cleanup()
			return nil, func() {}, err
		}
		log.Printf("AdsPower OAuth navigation returned before page load completed; continuing at %s", safeTargetLocation(currentURL))
	}
	if err := closeOtherOAuthPages(root, browser); err != nil {
		log.Printf("AdsPower OAuth cleanup of extra pages failed: %v", err)
	}
	return browser, func() {
		cancelBrowser()
		cleanup()
	}, nil
}

func closeOtherOAuthPages(root, browser context.Context) error {
	browserContext := chromedp.FromContext(browser)
	if browserContext == nil || browserContext.Target == nil {
		return errors.New("OAuth browser target is unavailable")
	}
	if browserContext.Browser == nil {
		return errors.New("OAuth browser connection is unavailable")
	}
	targets, err := chromedp.Targets(root)
	if err != nil {
		return err
	}
	keep := browserContext.Target.TargetID
	closeCtx, cancel := context.WithTimeout(root, 8*time.Second)
	defer cancel()
	for _, candidate := range targets {
		if candidate == nil || candidate.Type != "page" || candidate.TargetID == keep {
			continue
		}
		if err := target.CloseTarget(candidate.TargetID).Do(cdp.WithExecutor(closeCtx, browserContext.Browser)); err != nil {
			return err
		}
	}
	return nil
}

func browserTargetURL(browser context.Context, targets []*target.Info) string {
	browserContext := chromedp.FromContext(browser)
	if browserContext == nil || browserContext.Target == nil {
		return ""
	}
	for _, candidate := range targets {
		if candidate != nil && candidate.TargetID == browserContext.Target.TargetID {
			return strings.TrimSpace(candidate.URL)
		}
	}
	return ""
}

func safeTargetLocation(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "invalid-url"
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.Path
}

func summarizeOAuthTargets(targets []*target.Info) string {
	if len(targets) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(targets))
	for _, candidate := range targets {
		if candidate == nil {
			continue
		}
		parts = append(parts, candidate.Type+":"+safeTargetLocation(candidate.URL))
	}
	return strings.Join(parts, ",")
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
	if stage == "opening" && (strings.Contains(message, "timeout") || strings.Contains(message, "context deadline")) {
		return "navigation_timeout"
	}
	if strings.Contains(message, "context deadline") {
		return "task_expired"
	}
	if strings.Contains(message, "target closed") || strings.Contains(message, "browser") && strings.Contains(message, "closed") {
		return "browser_context_lost"
	}
	return "page_interaction_failed"
}

func blocked(reason string) error {
	status := "failed"
	if reason == "account_banned" || reason == "account_deleted_or_disabled" {
		status = "blocked"
	}
	return automationFailureError{Status: status, Reason: reason}
}

func (s *helperServer) automateOpenAI(ctx, browser context.Context, origin string, launch *launchPayload) error {
	attempted := make(map[string]time.Time)
	emailSubmitted := false
	oauthResumeAttempted := false
	phoneSubmitted := ""
	var phoneSubmittedAt time.Time
	phoneReplacementPending := false
	rejectedPhones := make(map[string]bool)
	var smsDeadline time.Time
	smsReserved := false
	smsCodeSubmitted := false
	var lastSMSAcquire time.Time
	var lastSMSPoll time.Time
	lastProgress := ""
	reportProgress := func(status, stage, reason string) bool {
		key := status + "\x00" + stage + "\x00" + reason
		if key == lastProgress {
			return true
		}
		reportCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := s.reportProgress(reportCtx, origin, launch, status, stage, reason)
		cancel()
		if err != nil {
			return false
		}
		lastProgress = key
		return true
	}
	defer func() {
		if !smsReserved {
			return
		}
		cancelCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, _ = s.smsAction(cancelCtx, origin, launch, "cancel")
	}()
	var snapshotFailureSince time.Time
	emailSession := launch.emailSession
	emailCodeRequestedAt := launch.oauthStartedAt
	lastPageKind := ""
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var snapshot oauthPageSnapshot
		if err := chromedp.Run(browser, chromedp.Evaluate(oauthSnapshotJS, &snapshot)); err != nil {
			if snapshotFailureSince.IsZero() {
				snapshotFailureSince = time.Now()
			}
			if time.Since(snapshotFailureSince) < 20*time.Second {
				if err := sleepWithContext(ctx, 300*time.Millisecond); err != nil {
					return err
				}
				continue
			}
			return err
		}
		snapshotFailureSince = time.Time{}
		state := inspectOAuthPage(snapshot)
		if state.Kind != lastPageKind {
			log.Printf("AdsPower OAuth stage: kind=%s location=%s", state.Kind, safeTargetLocation(snapshot.URL))
			lastPageKind = state.Kind
		}
		if state.Kind == "callback" {
			if err := s.submitObservedCallback(ctx, launch, snapshot.URL); err != nil {
				return err
			}
			return nil
		}
		if state.Kind != "account_choice" && state.Kind != "workspace" && state.VisibleMail != "" && !strings.EqualFold(state.VisibleMail, strings.TrimSpace(launch.LoginEmail)) {
			if action, ok := accountSwitchAction(snapshot); ok {
				if recentAttempt(attempted, "account_switch") {
					if attemptExpired(attempted, "account_switch") {
						return blocked("invalid_credentials")
					}
					if err := sleepWithContext(ctx, time.Second); err != nil {
						return err
					}
					continue
				}
				reportProgress("running", "login", "")
				if err := clickOAuthAction(browser, action); err != nil {
					return err
				}
				emailSubmitted = false
				for _, key := range []string{"login", "email", "password", "totp", "email_code_submitted", "email_code_switch", "workspace", "unknown"} {
					delete(attempted, key)
				}
				continue
			}
			return blocked("invalid_credentials")
		}
		switch state.Kind {
		case "oauth_session_expired", "openai_route_error":
			return automationFailureError{Status: "failed", Reason: state.Kind}
		case "captcha":
			return blocked("captcha_required")
		case "account_deleted_or_disabled", "account_banned", "unknown_error", "proxy_unavailable", "invalid_credentials", "invalid_email_code", "invalid_totp", "invalid_sms_code":
			return blocked(state.Kind)
		case "phone", "phone_rejected":
			// A previous rejection can remain visible while the new submission
			// is in flight. Do not replace its reservation before it settles.
			if phoneSubmitted != "" && !phoneReplacementPending && !phoneSubmittedAt.IsZero() &&
				(time.Since(phoneSubmittedAt) < 3*time.Second || snapshot.Busy && time.Since(phoneSubmittedAt) < 20*time.Second) {
				break
			}
			if state.Kind == "phone" && phoneSubmitted != "" && !phoneReplacementPending {
				if recentAttempt(attempted, "phone_transition") && attemptExpired(attempted, "phone_transition") {
					return automationFailureError{Status: "failed", Reason: "page_interaction_failed"}
				}
				break
			}
			action := "acquire"
			reason := ""
			if state.Kind == "phone_rejected" && phoneSubmitted != "" {
				rejectedPhones[phoneSubmitted] = true
				phoneReplacementPending = true
			}
			if phoneReplacementPending {
				action = "change"
				reason = "phone_rejected"
			}
			if !reportProgress("running", "phone_required", reason) {
				break
			}
			if !lastSMSAcquire.IsZero() && time.Since(lastSMSAcquire) < 2*time.Second {
				break
			}
			lastSMSAcquire = time.Now()
			result, err := s.smsAction(ctx, origin, launch, action)
			if err != nil || result == nil || !validAutomationPhone(result.Number) || rejectedPhones[normalizeAutomationPhone(result.Number)] {
				if recentAttempt(attempted, "sms_acquire") && time.Since(attempted["sms_acquire"]) > 5*time.Minute {
					return automationFailureError{Status: "failed", Reason: "sms_confirmation_timeout"}
				}
				break
			}
			delete(attempted, "sms_acquire")
			smsReserved = true
			smsCodeSubmitted = false
			phoneSubmitted = normalizeAutomationPhone(result.Number)
			smsDeadline = time.Now().Add(3 * time.Minute)
			reportProgress("running", "phone_submitting", "")
			if err := fillOAuthPhone(browser, state.Input, phoneSubmitted); err != nil {
				log.Printf("AdsPower phone input failed: %v", err)
				return err
			}
			if err := selectOAuthTextMessage(browser); err != nil {
				log.Printf("AdsPower SMS channel selection failed: %v", err)
				return automationFailureError{Status: "failed", Reason: "sms_channel_selection_failed"}
			}
			if err := clickOAuthContinue(browser); err != nil {
				log.Printf("AdsPower phone Continue failed: %v", err)
				return err
			}
			phoneSubmittedAt = time.Now()
			phoneReplacementPending = false
			delete(attempted, "phone_transition")
			if !reportProgress("running", "sms_waiting", "") {
				break
			}
		case "sms_code":
			if smsCodeSubmitted {
				if recentAttempt(attempted, "sms_transition") && attemptExpired(attempted, "sms_transition") {
					return automationFailureError{Status: "failed", Reason: "page_interaction_failed"}
				}
				break
			}
			if !smsReserved {
				smsReserved = true
				if smsDeadline.IsZero() {
					smsDeadline = time.Now().Add(3 * time.Minute)
				}
			}
			delete(attempted, "phone_transition")
			reportProgress("running", "sms_waiting", "")
			if !smsDeadline.IsZero() && !time.Now().Before(smsDeadline) {
				cancelCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				_, cancelErr := s.smsAction(cancelCtx, origin, launch, "cancel")
				cancel()
				if cancelErr == nil {
					smsReserved = false
				}
				return automationFailureError{Status: "failed", Reason: "sms_timeout"}
			}
			if !lastSMSPoll.IsZero() && time.Since(lastSMSPoll) < 2*time.Second {
				break
			}
			lastSMSPoll = time.Now()
			result, err := s.smsAction(ctx, origin, launch, "check")
			if err != nil {
				break
			}
			code := strings.TrimSpace(result.Code)
			if code == "" {
				break
			}
			if !validAutomationSMSCode(code) {
				return blocked("invalid_sms_code")
			}
			smsReserved = false
			smsCodeSubmitted = true
			reportProgress("running", "sms_submitting", "")
			if err := fillOAuthCode(browser, state.Inputs, code); err != nil {
				return err
			}
		case "profile":
			if recentAttempt(attempted, state.Kind) {
				if attemptExpired(attempted, state.Kind) {
					return automationFailureError{Status: "failed", Reason: "page_interaction_failed"}
				}
				break
			}
			reportProgress("running", "profile", "")
			if err := fillOAuthProfile(browser, state.Inputs); err != nil {
				return err
			}
		case "retry_page":
			if recentAttempt(attempted, state.Kind) {
				if attemptExpired(attempted, state.Kind) {
					return automationFailureError{Status: "failed", Reason: "openai_route_error"}
				}
				break
			}
			reportProgress("running", "opening", "")
			if err := clickOAuthAction(browser, state.Action); err != nil {
				return err
			}
		case "account_choice":
			if recentAttempt(attempted, state.Kind) {
				if attemptExpired(attempted, state.Kind) {
					return automationFailureError{Status: "failed", Reason: "page_interaction_failed"}
				}
				break
			}
			reportProgress("running", "workspace", "")
			if err := clickOAuthAction(browser, state.Action); err != nil {
				return err
			}
			emailSubmitted = true
		case "login":
			if recentAttempt(attempted, state.Kind) {
				if attemptExpired(attempted, state.Kind) {
					return automationFailureError{Status: "failed", Reason: "page_interaction_failed"}
				}
				break
			}
			reportProgress("running", "login", "")
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
			reportProgress("running", "email", "")
			if err := fillOAuthInput(browser, state.Input, launch.LoginEmail); err != nil {
				return err
			}
			emailCodeRequestedAt = time.Now()
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
				emailCodeRequestedAt = time.Now()
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
			reportProgress("running", "password", "")
			if err := fillOAuthInput(browser, state.Input, launch.Password); err != nil {
				return err
			}
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
			reportProgress("running", "totp", "")
			code, err := totp.GenerateCode(strings.ReplaceAll(launch.TOTPSecret, " ", ""), time.Now().UTC())
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
			if emailCodeRequestedAt.IsZero() {
				emailCodeRequestedAt = time.Now()
			}
			if !attempted["email_code_submitted"].IsZero() {
				if attemptExpired(attempted, "email_code_submitted") {
					return automationFailureError{Status: "failed", Reason: "page_interaction_failed"}
				}
				break
			}
			reportProgress("running", "email_code_waiting", "")
			requestedAt := emailCodeRequestedAt
			if requestedAt.IsZero() {
				requestedAt = time.Now()
			}
			code, err := emailSession.waitForCode(ctx, requestedAt)
			if err != nil {
				return blocked(emailCodeReason(err))
			}
			reportProgress("running", "email_code_submitting", "")
			if err := fillOAuthCode(browser, state.Inputs, code); err != nil {
				return err
			}
			attempted["email_code_submitted"] = time.Now()
			emailSubmitted = true
		case "workspace":
			if recentAttempt(attempted, state.Kind) {
				if attemptExpired(attempted, state.Kind) {
					return automationFailureError{Status: "failed", Reason: "page_interaction_failed"}
				}
				break
			}
			if !emailSubmitted {
				// A bound AdsPower profile may already hold the matching OpenAI
				// session. The backend still verifies the OAuth identity before it
				// updates the account, so continuing here cannot swap identities.
				emailSubmitted = true
			}
			reportProgress("running", "workspace", "")
			_ = clickOAuthText(browser, []string{"default workspace", "默认工作空间"})
			if err := clickOAuthContinue(browser); err != nil {
				return err
			}
			reportProgress("running", "callback_waiting", "")
		case "unknown":
			if shouldResumeOAuthFromChatGPTHomepage(snapshot.URL, emailSubmitted, oauthResumeAttempted) {
				oauthResumeAttempted = true
				delete(attempted, "unknown")
				reportProgress("running", "opening", "")
				if err := navigateOAuthURL(browser, launch.AuthURL); err != nil {
					return err
				}
				continue
			}
			if attempted["unknown"].IsZero() {
				attempted["unknown"] = time.Now()
			} else if time.Since(attempted["unknown"]) > 20*time.Second {
				log.Printf("AdsPower OAuth page was not recognized: %s", summarizeOAuthSnapshot(snapshot))
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

func normalizeAutomationPhone(value string) string {
	value = strings.NewReplacer(" ", "", "(", "", ")", "", "-", "").Replace(strings.TrimSpace(value))
	if value != "" && !strings.HasPrefix(value, "+") {
		value = "+" + value
	}
	return value
}

func validAutomationPhone(value string) bool {
	return automationPhonePattern.MatchString(normalizeAutomationPhone(value))
}

func validAutomationSMSCode(value string) bool {
	return automationSMSCodePattern.MatchString(strings.TrimSpace(value))
}

func accountSwitchAction(snapshot oauthPageSnapshot) (int, bool) {
	return firstMatchingAction(snapshot.Actions, []string{
		"use another account",
		"log in to another account",
		"choose another account",
		"switch account",
		"sign in with another account",
		"使用其他账号",
		"选择其他账号",
		"切换账号",
	})
}

func (s *helperServer) submitObservedCallback(ctx context.Context, launch *launchPayload, callbackURL string) error {
	parsed, err := url.Parse(launch.AuthURL)
	if err != nil {
		return errors.New("OpenAI authorization URL is invalid")
	}
	state := strings.TrimSpace(parsed.Query().Get("state"))
	if !validCallback(callbackURL, state) {
		return errors.New("OpenAI callback URL is invalid")
	}
	reportCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	delivered, err := s.deliverCallback(reportCtx, state, callbackURL)
	if err != nil {
		return err
	}
	if !delivered {
		return errors.New("OpenAI callback registration is unavailable")
	}
	return nil
}

func shouldResumeOAuthFromChatGPTHomepage(rawURL string, emailSubmitted, alreadyAttempted bool) bool {
	if !emailSubmitted || alreadyAttempted {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Hostname() != "chatgpt.com" && parsed.Hostname() != "www.chatgpt.com") {
		return false
	}
	return parsed.Path == "" || parsed.Path == "/"
}

func navigateOAuthURL(browser context.Context, authURL string) error {
	navigationCtx, cancel := context.WithTimeout(browser, 10*time.Second)
	defer cancel()
	return chromedp.Run(navigationCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, _, _, err := page.Navigate(authURL).Do(ctx)
		return err
	}))
}

func summarizeOAuthSnapshot(snapshot oauthPageSnapshot) string {
	body := redactOAuthDiagnostic(snapshot.Body)
	if len(body) > 500 {
		body = body[:500]
	}
	inputs := make([]string, 0, len(snapshot.Inputs))
	for _, input := range snapshot.Inputs {
		inputs = append(inputs, redactOAuthDiagnostic(input.Metadata))
	}
	actions := make([]string, 0, len(snapshot.Actions))
	for _, action := range snapshot.Actions {
		text := redactOAuthDiagnostic(action.Text)
		if len(text) > 120 {
			text = text[:120]
		}
		actions = append(actions, text)
		if len(actions) == 12 {
			break
		}
	}
	return fmt.Sprintf("url=%s body=%q inputs=%q actions=%q", safeTargetLocation(snapshot.URL), body, inputs, actions)
}

func redactOAuthDiagnostic(value string) string {
	value = emailAddressPattern.ReplaceAllString(value, "[email]")
	value = diagnosticCodePattern.ReplaceAllString(value, "[code]")
	value = diagnosticTokenPattern.ReplaceAllString(value, "[token]")
	return strings.TrimSpace(value)
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
	if action, ok := firstMatchingAction(snapshot.Actions, []string{"try again", "retry", "重试"}); ok &&
		(strings.Contains(lower, "oops, an error occurred") || strings.Contains(lower, "not valid json") || strings.Contains(lower, "unexpected token")) {
		state.Kind, state.Action = "retry_page", action
		return state
	}
	switch {
	case strings.Contains(lower, "error_code: invalid_state") || strings.Contains(lower, "sign-in session is no longer valid") || strings.Contains(lower, "session ended"):
		state.Kind = "oauth_session_expired"
	case strings.Contains(lower, "route error") && strings.Contains(lower, "invalid content type"):
		state.Kind = "openai_route_error"
	case snapshot.Captcha || strings.Contains(lower, "verify that you are human") || strings.Contains(lower, "checking your browser") || strings.Contains(lower, "人机验证"):
		state.Kind = "captcha"
	case deletedAccountPattern.MatchString(body):
		state.Kind = "account_deleted_or_disabled"
	case bannedAccountPattern.MatchString(body):
		state.Kind = "account_banned"
	case restrictedAccountPattern.MatchString(body):
		state.Kind = "unknown_error"
	case strings.Contains(lower, "err_proxy_connection_failed") || strings.Contains(lower, "there is something wrong with the proxy server"):
		state.Kind = "proxy_unavailable"
	case invalidCredentialsPattern.MatchString(body):
		state.Kind = "invalid_credentials"
	default:
		// Phone pages contain numeric inputs and SMS radios whose names may
		// include "code". Identify the actual phone field before OTP/profile.
		phoneInput, hasPhoneInput := firstMatchingInput(snapshot.Inputs, func(value string) bool {
			return oauthEditableInput(value) && (strings.Contains(value, "tel") || strings.Contains(value, "phone") || strings.Contains(value, "mobile"))
		})
		phonePage := parsed != nil && parsed.Path == "/add-phone"
		if hasPhoneInput && (phonePage || strings.Contains(lower, "phone number required") || phoneRejectedPattern.MatchString(body)) {
			state.Kind, state.Input = "phone", phoneInput
			if phoneRejectedPattern.MatchString(body) {
				state.Kind = "phone_rejected"
			}
			return state
		}
		if (parsed != nil && (strings.Contains(parsed.Path, "/about-you") || strings.Contains(parsed.Path, "/profile"))) ||
			strings.Contains(lower, "tell us about yourself") || strings.Contains(lower, "about you") ||
			(strings.Contains(lower, "name") && (strings.Contains(lower, "age") || strings.Contains(lower, "birth"))) ||
			strings.Contains(lower, "姓名") || strings.Contains(lower, "年龄") {
			if profileInputs := oauthProfileInputs(snapshot.Inputs); len(profileInputs) >= 2 {
				state.Kind, state.Inputs = "profile", profileInputs
				return state
			}
		}
		codeInputs := matchingInputs(snapshot.Inputs, func(value string) bool {
			return oauthEditableInput(value) && (strings.Contains(value, "one-time-code") || strings.Contains(value, "code") || strings.Contains(value, "otp"))
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
		if hasPhoneInput && phoneRejectedPattern.MatchString(body) {
			state.Kind = "phone_rejected"
			if input, ok := firstMatchingInput(snapshot.Inputs, func(value string) bool {
				return strings.Contains(value, "tel") || strings.Contains(value, "phone") || strings.Contains(value, "mobile")
			}); ok {
				state.Input = input
			}
			return state
		}
		if input, ok := firstMatchingInput(snapshot.Inputs, func(value string) bool {
			return oauthEditableInput(value) && (strings.Contains(value, "tel") || strings.Contains(value, "phone") || strings.Contains(value, "mobile"))
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
		if parsed != nil && parsed.Path == "/choose-an-account" {
			if action, ok := firstMatchingAction(snapshot.Actions, []string{"select account", "选择账号"}); ok {
				state.Kind, state.Action = "account_choice", action
				return state
			}
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

func oauthProfileInputs(inputs []oauthPageInput) []int {
	if len(inputs) < 2 {
		return nil
	}
	nameIndex, nameFound := firstMatchingInput(inputs, func(value string) bool {
		return oauthEditableInput(value) && !strings.Contains(value, "username") && (strings.Contains(value, "name") || strings.Contains(value, "姓名"))
	})
	ageIndex, ageFound := firstMatchingInput(inputs, func(value string) bool {
		return oauthEditableInput(value) && (strings.Contains(value, "age") || strings.Contains(value, "birth") || strings.Contains(value, "年龄") || strings.Contains(value, "出生"))
	})
	if !nameFound || !ageFound || nameIndex == ageIndex {
		return nil
	}
	return []int{nameIndex, ageIndex}
}

func oauthEditableInput(metadata string) bool {
	fields := strings.Fields(metadata)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "radio", "checkbox", "hidden", "button", "submit", "reset", "file":
		return false
	default:
		return true
	}
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

func fillOAuthPhone(browser context.Context, index int, value string) error {
	normalized := normalizeAutomationPhone(value)
	if !validAutomationPhone(normalized) {
		return errors.New("phone number is invalid")
	}
	digits := strings.TrimPrefix(normalized, "+")
	for attempt := 0; attempt < 3; attempt++ {
		if err := replaceOAuthInputText(browser, index, normalized); err != nil {
			return err
		}
		if err := sleepWithContext(browser, 1200*time.Millisecond); err != nil {
			return err
		}
		actual, dialCode, err := readOAuthPhoneValue(browser, index)
		if err != nil {
			return err
		}
		if oauthPhoneInputMatches(actual, digits, dialCode) {
			return nil
		}
		actualDigits := strings.Map(func(char rune) rune {
			if char >= '0' && char <= '9' {
				return char
			}
			return -1
		}, actual)
		log.Printf("AdsPower phone input incomplete: attempt=%d actual_digits=%d expected_digits=%d", attempt+1, len(actualDigits), len(digits))
	}
	return errors.New("phone input did not retain the complete number")
}

func replaceOAuthInputText(browser context.Context, index int, value string) error {
	clearExpression := fmt.Sprintf(`(() => {
  const visible=e=>{const s=getComputedStyle(e),r=e.getBoundingClientRect();return s.visibility!=='hidden'&&s.display!=='none'&&r.width>0&&r.height>0};
  const input=Array.from(document.querySelectorAll('input')).filter(visible)[%d];
  if(!input) throw new Error('input missing');
  input.focus();
  input.select();
  return true;
})()`, index)
	commitExpression := fmt.Sprintf(`(() => {
  const visible=e=>{const s=getComputedStyle(e),r=e.getBoundingClientRect();return s.visibility!=='hidden'&&s.display!=='none'&&r.width>0&&r.height>0};
  const input=Array.from(document.querySelectorAll('input')).filter(visible)[%d];
  if(!input) throw new Error('input missing');
  input.dispatchEvent(new Event('change',{bubbles:true}));
  input.blur();
  return input.value;
})()`, index)
	return chromedp.Run(browser,
		chromedp.Evaluate(clearExpression, nil),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return cdpinput.InsertText(value).Do(ctx)
		}),
		chromedp.Evaluate(commitExpression, nil),
	)
}

func readOAuthPhoneValue(browser context.Context, index int) (string, string, error) {
	expression := fmt.Sprintf(`(() => {
  const visible=e=>{const s=getComputedStyle(e),r=e.getBoundingClientRect();return s.visibility!=='hidden'&&s.display!=='none'&&r.width>0&&r.height>0};
  const input=Array.from(document.querySelectorAll('input')).filter(visible)[%d];
  if(!input) throw new Error('phone input missing');
  const form=input.closest('form')||document.body;
  const controls=Array.from(form.querySelectorAll('button,[role="combobox"],select')).filter(visible);
  const codes=controls.map(e=>String(e.tagName==='SELECT'?e.selectedOptions[0]?.textContent:e.innerText||e.getAttribute('aria-label')||'').match(/\(\+\s*(\d{1,3})\)/)?.[1]).filter(Boolean);
  return {value:String(input.value||''),dialCode:codes.length===1?codes[0]:''};
})()`, index)
	var value struct {
		Value    string `json:"value"`
		DialCode string `json:"dialCode"`
	}
	if err := chromedp.Run(browser, chromedp.Evaluate(expression, &value)); err != nil {
		return "", "", err
	}
	return value.Value, value.DialCode, nil
}

func oauthPhoneInputMatches(actual, phoneDigits, dialCode string) bool {
	actualDigits := strings.Map(func(char rune) rune {
		if char >= '0' && char <= '9' {
			return char
		}
		return -1
	}, actual)
	if len(actualDigits) < 7 {
		return false
	}
	if actualDigits == phoneDigits {
		return true
	}
	return dialCode != "" && dialCode+actualDigits == phoneDigits
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

func fillOAuthProfile(browser context.Context, inputs []int) error {
	if len(inputs) < 2 {
		return errors.New("profile inputs missing")
	}
	if err := fillOAuthInput(browser, inputs[0], "black"); err != nil {
		return err
	}
	if err := fillOAuthInput(browser, inputs[1], "26"); err != nil {
		return err
	}
	return clickOAuthContinue(browser)
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

const oauthSelectTextMessageJS = `(() => {
  const visible=e=>{const s=getComputedStyle(e),r=e.getBoundingClientRect();return s.visibility!=='hidden'&&s.display!=='none'&&r.width>0&&r.height>0};
  const text=e=>String(e.innerText||e.textContent||e.getAttribute('aria-label')||'').replace(/\s+/g,' ').trim().toLowerCase();
  const selected=e=>e.getAttribute('aria-checked')==='true'||e.getAttribute('aria-selected')==='true'||e.getAttribute('aria-pressed')==='true'||e.checked===true||e.getAttribute('data-state')==='checked'||e.matches(':checked');
  const labels=Array.from(document.querySelectorAll('label')).filter(visible);
  const radios=Array.from(document.querySelectorAll('input[type="radio"],[role="radio"],button,option,[role="option"]')).filter(visible);
  const candidate=radios.find(e=>/text message|sms|短信/.test(text(e))) || labels.find(e=>/text message|sms|短信/.test(text(e)));
  if(!candidate) throw new Error('text message option missing');
  candidate.click();
  const control=candidate.matches('label') ? (candidate.control || candidate.querySelector('input,[role="radio"]')) : candidate;
  if(!selected(candidate) && (!control || !selected(control))) throw new Error('text message option not selected');
  return true;
})()`

func selectOAuthTextMessage(browser context.Context) error {
	return chromedp.Run(browser, chromedp.Evaluate(oauthSelectTextMessageJS, nil))
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
