package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

type adsPowerClient struct {
	baseURL       string
	apiKey        string
	client        *http.Client
	requestMu     sync.Mutex
	lastRequestAt time.Time
	minInterval   time.Duration
	sleep         func(context.Context, time.Duration) error
}

type adsPowerProfile struct {
	UserID          string              `json:"user_id"`
	SerialNumber    string              `json:"serial_number"`
	Name            string              `json:"name"`
	GroupID         string              `json:"group_id"`
	UserProxyConfig adsPowerProxyConfig `json:"user_proxy_config"`
}

type adsPowerBrowserSession struct {
	Status    string `json:"status"`
	DebugPort string `json:"debug_port"`
	WebDriver string `json:"webdriver"`
	WebSocket struct {
		Puppeteer string `json:"puppeteer"`
		Selenium  string `json:"selenium"`
	} `json:"ws"`
}

type adsPowerProxyConfig struct {
	ProxySoft     string `json:"proxy_soft"`
	ProxyType     string `json:"proxy_type"`
	ProxyHost     string `json:"proxy_host"`
	ProxyPort     string `json:"proxy_port"`
	ProxyUser     string `json:"proxy_user"`
	ProxyPassword string `json:"proxy_password"`
	LatestIP      string `json:"latest_ip,omitempty"`
}

type adsPowerEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func newAdsPowerClient(cfg *config) *adsPowerClient {
	return &adsPowerClient{
		baseURL: cfg.AdsPowerBaseURL,
		apiKey:  strings.TrimSpace(cfg.APIKey),
		client:  &http.Client{Timeout: 35 * time.Second},
		// AdsPower limits small installations to two requests per second and
		// some profile mutations to one request per second. Serialize local API
		// traffic and retry only its explicit rate-limit response.
		minInterval: 550 * time.Millisecond,
		sleep:       sleepWithContext,
	}
}

func (c *adsPowerClient) do(ctx context.Context, method, path string, body any, target any) error {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	for attempt := 0; attempt < 3; attempt++ {
		if wait := c.minInterval - time.Since(c.lastRequestAt); wait > 0 {
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
		}
		c.lastRequestAt = time.Now()
		err := c.doOnce(ctx, method, path, body, target)
		if err == nil || !isAdsPowerRateLimitError(err) || attempt == 2 {
			return err
		}
		if err := c.sleep(ctx, time.Duration(attempt+1)*1100*time.Millisecond); err != nil {
			return err
		}
	}
	return errors.New("AdsPower request failed")
}

func (c *adsPowerClient) doOnce(ctx context.Context, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(payload))
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 2<<20)
	var envelope adsPowerEnvelope
	if err := json.NewDecoder(limited).Decode(&envelope); err != nil {
		return fmt.Errorf("decode AdsPower response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || envelope.Code != 0 {
		return fmt.Errorf("AdsPower API rejected the request: %s", strings.TrimSpace(envelope.Msg))
	}
	if target != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			return err
		}
	}
	return nil
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isAdsPowerRateLimitError(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "too many request")
}

func (c *adsPowerClient) status(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/status", nil, nil)
}

func (c *adsPowerClient) profilePage(ctx context.Context, page int, profileID string) ([]adsPowerProfile, error) {
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("page_size", "100")
	if profileID != "" {
		query.Set("user_id", profileID)
	}
	var data struct {
		List []adsPowerProfile `json:"list"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/user/list?"+query.Encode(), nil, &data); err != nil {
		return nil, err
	}
	return data.List, nil
}

func (c *adsPowerClient) profile(ctx context.Context, profileID string) (*adsPowerProfile, error) {
	profileID = strings.TrimSpace(profileID)
	if !validOpaqueID(profileID) {
		return nil, errors.New("AdsPower profile ID is invalid")
	}
	// Newer AdsPower builds honor user_id and return the profile directly.
	// If an older build ignores that filter, walk all pages so installations
	// with more than 100 account environments still resolve stable bindings.
	if profiles, err := c.profilePage(ctx, 1, profileID); err == nil {
		for i := range profiles {
			if profiles[i].UserID == profileID {
				return &profiles[i], nil
			}
		}
	}
	for page := 1; page <= 1000; page++ {
		profiles, err := c.profilePage(ctx, page, "")
		if err != nil {
			return nil, err
		}
		for i := range profiles {
			if profiles[i].UserID == profileID {
				return &profiles[i], nil
			}
		}
		if len(profiles) < 100 {
			break
		}
	}
	return nil, errors.New("AdsPower profile was not found")
}

func randomizedFingerprintConfig(forCreate bool) map[string]any {
	config := map[string]any{
		"automatic_timezone":   "1",
		"language_switch":      "1",
		"page_language_switch": "1",
		"screen_resolution":    "random",
		"canvas":               "1",
		"webgl_image":          "1",
		"audio":                "1",
		"media_devices":        "1",
		"client_rects":         "1",
		"device_name_switch":   "1",
		"speech_switch":        "1",
		"webrtc":               "disabled",
	}
	if forCreate {
		config["random_ua"] = map[string]any{
			"ua_browser": []string{"chrome"},
			"ua_system_version": []string{
				"Mac OS X 12",
				"Mac OS X 13",
			},
		}
		// Value 3 asks AdsPower to generate matching WebGL metadata during
		// profile creation. AdsPower does not accept that value on updates.
		config["webgl"] = "3"
	}
	return config
}

func (c *adsPowerClient) createProfile(ctx context.Context, name string, template *adsPowerProfile) (*adsPowerProfile, error) {
	if template == nil {
		return nil, errors.New("AdsPower template profile is unavailable")
	}
	proxyConfig := template.UserProxyConfig
	proxyConfig.LatestIP = ""
	var data struct {
		ProfileID string `json:"profile_id"`
		UserID    string `json:"user_id"`
	}
	payload := map[string]any{
		"name":               sanitizeProfileName(name),
		"domain_name":        "https://auth.openai.com",
		"user_proxy_config":  proxyConfig,
		"fingerprint_config": randomizedFingerprintConfig(true),
	}
	if strings.TrimSpace(template.GroupID) != "" {
		payload["group_id"] = template.GroupID
	}
	err := c.do(ctx, http.MethodPost, "/api/v2/browser-profile/create", payload, &data)
	if err != nil {
		return nil, err
	}
	profileID := strings.TrimSpace(data.ProfileID)
	if profileID == "" {
		profileID = strings.TrimSpace(data.UserID)
	}
	if !validOpaqueID(profileID) {
		return nil, errors.New("AdsPower did not return a valid profile ID")
	}
	return c.profile(ctx, profileID)
}

func (c *adsPowerClient) enforceProfilePolicy(ctx context.Context, profile *adsPowerProfile, template *adsPowerProfile) error {
	if profile == nil || template == nil {
		return errors.New("AdsPower profile policy cannot be applied")
	}
	proxyConfig := template.UserProxyConfig
	proxyConfig.LatestIP = ""
	return c.do(ctx, http.MethodPost, "/api/v2/browser-profile/update", map[string]any{
		"profile_id":         profile.UserID,
		"user_proxy_config":  proxyConfig,
		"fingerprint_config": randomizedFingerprintConfig(false),
	}, nil)
}

func (c *adsPowerClient) startProfile(ctx context.Context, profileID string) (*adsPowerBrowserSession, error) {
	var session adsPowerBrowserSession
	err := c.do(ctx, http.MethodPost, "/api/v2/browser-profile/start", map[string]any{
		"profile_id": profileID,
		"launch_args": []string{
			"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
			"--webrtc-ip-handling-policy=disable_non_proxied_udp",
		},
	}, &session)
	if err == nil {
		return &session, nil
	}
	active, activeErr := c.activeProfile(ctx, profileID)
	if activeErr == nil && strings.EqualFold(strings.TrimSpace(active.Status), "active") {
		return active, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, errors.New("AdsPower did not return a browser automation endpoint")
}

func (c *adsPowerClient) stopProfile(ctx context.Context, profileID string) error {
	profileID = strings.TrimSpace(profileID)
	if !validOpaqueID(profileID) {
		return errors.New("AdsPower profile ID is invalid")
	}
	return c.do(ctx, http.MethodPost, "/api/v2/browser-profile/stop", map[string]any{
		"profile_id": profileID,
	}, nil)
}

func (c *adsPowerClient) profileActive(ctx context.Context, profileID string) (bool, error) {
	data, err := c.activeProfile(ctx, profileID)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(data.Status), "active"), nil
}

func (c *adsPowerClient) activeProfile(ctx context.Context, profileID string) (*adsPowerBrowserSession, error) {
	var data adsPowerBrowserSession
	path := "/api/v1/browser/active?user_id=" + url.QueryEscape(profileID)
	if err := c.do(ctx, http.MethodGet, path, nil, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func verifyProxyExitIP(ctx context.Context, cfg adsPowerProxyConfig) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(cfg.ProxyType), "socks5") {
		return "", errors.New("template profile must use SOCKS5")
	}
	port, err := strconv.Atoi(strings.TrimSpace(cfg.ProxyPort))
	if err != nil || port <= 0 || port > 65535 {
		return "", errors.New("template proxy port is invalid")
	}
	var auth *proxy.Auth
	if cfg.ProxyUser != "" || cfg.ProxyPassword != "" {
		auth = &proxy.Auth{User: cfg.ProxyUser, Password: cfg.ProxyPassword}
	}
	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort(strings.TrimSpace(cfg.ProxyHost), strconv.Itoa(port)), auth, proxy.Direct)
	if err != nil {
		return "", err
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialer.Dial(network, address)
		},
		ForceAttemptHTTP2: true,
	}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org?format=json", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("SOCKS5 proxy check failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SOCKS5 proxy check returned %d", response.StatusCode)
	}
	var result struct {
		IP string `json:"ip"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result) != nil || net.ParseIP(strings.TrimSpace(result.IP)) == nil {
		return "", errors.New("SOCKS5 proxy did not return a valid exit IP")
	}
	return strings.TrimSpace(result.IP), nil
}

func sanitizeProfileName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "OpenAI OAuth"
	}
	value = strings.Map(func(char rune) rune {
		if char < 32 || char == 127 {
			return -1
		}
		return char
	}, value)
	name := "XIASS " + value
	if len(name) > 96 {
		name = name[:96]
	}
	return name
}
