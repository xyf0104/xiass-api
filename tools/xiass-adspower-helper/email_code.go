package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const emailCodeProviderOrigin = "https://ic.g-c.cc"

var (
	emailCodeGatewayPattern   = regexp.MustCompile(`(?i)data-public-mail-gateway=["'](/m/[A-Za-z0-9_-]{16,128})["']`)
	emailCodeHTMLPattern      = regexp.MustCompile(`(?s)<[^>]+>`)
	emailCodeContextPattern   = regexp.MustCompile(`(?i)(?:verification|verify|login|sign[- ]?in|security|temporary|one[- ]?time|验证码|登录代码|临时代码)[^0-9]{0,80}([0-9]{6})`)
	emailCodeSixDigitsPattern = regexp.MustCompile(`\b[0-9]{6}\b`)
)

type emailCodeError string

func (e emailCodeError) Error() string { return string(e) }

type emailCodeSession struct {
	client     *http.Client
	email      string
	token      string
	gateway    string
	proof      string
	proofUntil time.Time
	baseline   map[string]bool
}

type emailCodeMessage struct {
	ID          string `json:"id"`
	Date        string `json:"date"`
	From        string `json:"from"`
	Subject     string `json:"subject"`
	Snippet     string `json:"snippet"`
	Text        string `json:"text"`
	HTML        string `json:"html"`
	HTMLPreview string `json:"htmlPreview"`
}

func newEmailCodeSession(ctx context.Context, address, token string) (*emailCodeSession, error) {
	address = strings.ToLower(strings.TrimSpace(address))
	token = strings.TrimSpace(token)
	if len(address) > 254 || !strings.Contains(address, "@") || len(token) != 64 {
		return nil, emailCodeError("email_code_access_denied")
	}
	for _, char := range token {
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return nil, emailCodeError("email_code_access_denied")
		}
	}
	session := &emailCodeSession{
		client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		email:  address, token: token, baseline: make(map[string]bool),
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, emailCodeProviderOrigin+"/", nil)
	if err != nil {
		return nil, emailCodeError("email_code_unavailable")
	}
	request.Header.Set("Accept", "text/html")
	response, err := session.client.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		if response != nil {
			_ = response.Body.Close()
		}
		return nil, emailCodeError("email_code_unavailable")
	}
	body, err := readBounded(response.Body, 256<<10)
	_ = response.Body.Close()
	if err != nil {
		return nil, emailCodeError("email_code_unavailable")
	}
	match := emailCodeGatewayPattern.FindSubmatch(body)
	if len(match) != 2 {
		return nil, emailCodeError("email_code_unavailable")
	}
	session.gateway = string(match[1])
	if err := session.authorize(ctx); err != nil {
		return nil, err
	}
	messages, err := session.list(ctx)
	if err != nil {
		return nil, err
	}
	for _, message := range messages {
		if message.ID != "" {
			session.baseline[message.ID] = true
		}
	}
	return session, nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	limited := io.LimitReader(reader, limit+1)
	payload, err := io.ReadAll(limited)
	if err != nil || int64(len(payload)) > limit {
		return nil, errors.New("response too large")
	}
	return payload, nil
}

func (s *emailCodeSession) requestJSON(ctx context.Context, method, path string, body any, headers map[string]string, target any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return emailCodeError("email_code_unavailable")
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, emailCodeProviderOrigin+s.gateway+path, reader)
	if err != nil {
		return emailCodeError("email_code_unavailable")
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return emailCodeError("email_code_unavailable")
	}
	defer response.Body.Close()
	payload, err := readBounded(response.Body, 256<<10)
	if err != nil {
		return emailCodeError("email_code_unavailable")
	}
	if response.StatusCode == http.StatusUnauthorized {
		return emailCodeError("email_code_access_denied")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return emailCodeError("email_code_unavailable")
	}
	var envelope struct {
		OK    *bool           `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &envelope) != nil || envelope.OK != nil && !*envelope.OK {
		if strings.EqualFold(envelope.Error.Code, "UNAUTHORIZED") {
			return emailCodeError("email_code_access_denied")
		}
		return emailCodeError("email_code_unavailable")
	}
	if target != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if json.Unmarshal(envelope.Data, target) != nil {
			return emailCodeError("email_code_unavailable")
		}
	}
	return nil
}

func (s *emailCodeSession) authorize(ctx context.Context) error {
	var data struct {
		AccessProof          string `json:"accessProof"`
		AccessProofExpiresAt any    `json:"accessProofExpiresAt"`
	}
	if err := s.requestJSON(ctx, http.MethodPost, "/authorize", map[string]string{
		"email": s.email, "token": s.token, "turnstileToken": "",
	}, nil, &data); err != nil {
		return err
	}
	s.proof = strings.TrimSpace(data.AccessProof)
	if s.proof == "" || len(s.proof) > 4096 {
		return emailCodeError("email_code_unavailable")
	}
	s.proofUntil = time.Now().Add(2 * time.Minute)
	switch value := data.AccessProofExpiresAt.(type) {
	case float64:
		if value > 1_000_000_000_000 {
			value /= 1000
		}
		s.proofUntil = time.Unix(int64(value), 0)
	case string:
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			s.proofUntil = parsed
		}
	}
	return nil
}

func (s *emailCodeSession) authHeaders() map[string]string {
	return map[string]string{"X-Public-Email": s.email, "X-Public-Email-Token": s.token, "X-Mail-Access-Proof": s.proof}
}

func (s *emailCodeSession) list(ctx context.Context) ([]emailCodeMessage, error) {
	if s.proof == "" || time.Until(s.proofUntil) < 5*time.Second {
		if err := s.authorize(ctx); err != nil {
			return nil, err
		}
	}
	var data struct {
		Messages []emailCodeMessage `json:"messages"`
	}
	err := s.requestJSON(ctx, http.MethodGet, "/items?limit=10", nil, s.authHeaders(), &data)
	if errors.Is(err, emailCodeError("email_code_access_denied")) {
		s.proof = ""
		if authErr := s.authorize(ctx); authErr != nil {
			return nil, authErr
		}
		err = s.requestJSON(ctx, http.MethodGet, "/items?limit=10", nil, s.authHeaders(), &data)
	}
	return data.Messages, err
}

func (s *emailCodeSession) detail(ctx context.Context, id string) (emailCodeMessage, error) {
	var data struct {
		Message emailCodeMessage `json:"message"`
	}
	err := s.requestJSON(ctx, http.MethodGet, "/items/"+url.PathEscape(id), nil, s.authHeaders(), &data)
	return data.Message, err
}

func (s *emailCodeSession) waitForCode(ctx context.Context, notBefore time.Time) (string, error) {
	timer := time.NewTimer(10 * time.Second)
	select {
	case <-ctx.Done():
		timer.Stop()
		return "", emailCodeError("email_code_timeout")
	case <-timer.C:
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		messages, err := s.list(ctx)
		if err != nil {
			return "", err
		}
		now := time.Now()
		candidate, ok := latestEmailCodeCandidate(messages, s.baseline, notBefore, now)
		if ok {
			if code := extractEmailCode(candidate); code != "" {
				log.Printf("email code candidate accepted: age=%s", now.Sub(parseMessageTime(candidate.Date)).Round(time.Second))
				return code, nil
			}
			// Do not fall back to an older message. The newest eligible message is
			// the only one that can belong to this authorization attempt. A detail
			// request may fail temporarily, so leave the candidate eligible for the
			// next poll instead of permanently marking it as processed.
			detail, detailErr := s.detail(ctx, candidate.ID)
			if detailErr == nil {
				if code := extractEmailCode(detail); code != "" {
					log.Printf("email code detail accepted: age=%s", now.Sub(parseMessageTime(candidate.Date)).Round(time.Second))
					return code, nil
				}
			}
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", emailCodeError("email_code_timeout")
		case <-timer.C:
		}
	}
	return "", emailCodeError("email_code_timeout")
}

func latestEmailCodeCandidate(messages []emailCodeMessage, baseline map[string]bool, requestedAt, now time.Time) (emailCodeMessage, bool) {
	ordered := append([]emailCodeMessage(nil), messages...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, right := parseMessageTime(ordered[i].Date), parseMessageTime(ordered[j].Date)
		if left.IsZero() {
			return false
		}
		if right.IsZero() {
			return true
		}
		return left.After(right)
	})
	for _, message := range ordered {
		if message.ID == "" || baseline[message.ID] || !eligibleEmailCodeMessage(message, requestedAt, now) {
			continue
		}
		return message, true
	}
	return emailCodeMessage{}, false
}

func eligibleEmailCodeMessage(message emailCodeMessage, requestedAt, now time.Time) bool {
	messageAt := parseMessageTime(message.Date)
	if messageAt.IsZero() {
		return false
	}
	if requestedAt.IsZero() || messageAt.Before(requestedAt.Add(-5*time.Second)) {
		return false
	}
	if messageAt.After(now.Add(15 * time.Second)) {
		return false
	}
	return now.Sub(messageAt) <= time.Minute
}

func parseMessageTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	if numeric, err := strconv.ParseInt(value, 10, 64); err == nil {
		if numeric > 1_000_000_000_000 {
			return time.UnixMilli(numeric)
		}
		if numeric > 0 {
			return time.Unix(numeric, 0)
		}
	}
	return time.Time{}
}

func messagePredates(value string, cutoff time.Time) bool {
	parsed := parseMessageTime(value)
	return !parsed.IsZero() && parsed.Before(cutoff)
}

func extractEmailCode(message emailCodeMessage) string {
	source := strings.Join([]string{message.From, message.Subject, message.Snippet, message.Text, message.HTMLPreview, emailCodeHTMLPattern.ReplaceAllString(html.UnescapeString(message.HTML), " ")}, " ")
	if !regexp.MustCompile(`(?i)openai|chatgpt`).MatchString(source) {
		return ""
	}
	if match := emailCodeContextPattern.FindStringSubmatch(source); len(match) == 2 {
		return match[1]
	}
	matches := emailCodeSixDigitsPattern.FindAllString(source, -1)
	unique := make(map[string]bool)
	for _, match := range matches {
		unique[match] = true
	}
	if len(unique) == 1 {
		for value := range unique {
			return value
		}
	}
	return ""
}

func emailCodeReason(err error) string {
	var coded emailCodeError
	if errors.As(err, &coded) {
		switch string(coded) {
		case "email_code_access_denied", "email_code_unavailable", "email_code_timeout":
			return string(coded)
		}
	}
	return "email_code_unavailable"
}

func (s *emailCodeSession) String() string {
	return fmt.Sprintf("email code session for %s", s.email)
}
