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
	ProxyID         string              `json:"proxyid"`
	IP              string              `json:"ip"`
	UserProxyConfig adsPowerProxyConfig `json:"user_proxy_config"`
}

type adsPowerSavedProxy struct {
	ProxyID      string `json:"proxy_id"`
	Type         string `json:"type"`
	Host         string `json:"host"`
	Port         string `json:"port"`
	Remark       string `json:"remark"`
	ProfileCount string `json:"profile_count"`
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

var errAdsPowerProfileLimit = errors.New("AdsPower profile capacity exhausted")

func adsPowerProfileLimitMessage(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "number of imported accounts exceeds the limit")
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
		if method == http.MethodPost && path == "/api/v2/browser-profile/create" && adsPowerProfileLimitMessage(envelope.Msg) {
			return fmt.Errorf("%w: %s", errAdsPowerProfileLimit, strings.TrimSpace(envelope.Msg))
		}
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

func (c *adsPowerClient) savedProxies(ctx context.Context) ([]adsPowerSavedProxy, error) {
	var data struct {
		List []adsPowerSavedProxy `json:"list"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v2/proxy-list/list", map[string]any{
		"page":  1,
		"limit": 200,
	}, &data); err != nil {
		return nil, err
	}
	return data.List, nil
}

func (c *adsPowerClient) savedProxy(ctx context.Context, proxyID string) (*adsPowerSavedProxy, error) {
	proxyID = strings.TrimSpace(proxyID)
	if !validOpaqueID(proxyID) {
		return nil, errors.New("AdsPower proxy ID is invalid")
	}
	proxies, err := c.savedProxies(ctx)
	if err != nil {
		return nil, err
	}
	for index := range proxies {
		if proxies[index].ProxyID == proxyID {
			return &proxies[index], nil
		}
	}
	return nil, errors.New("AdsPower saved proxy was not found")
}

func randomizedFingerprintConfig(slot int, forCreate bool) map[string]any {
	if slot < 1 || slot > 52 {
		slot = 1
	}
	resolutions := []string{
		"1920_1080", "1680_1050", "1600_900", "1536_864", "1440_900", "1366_768", "1280_800",
		"1280_720", "2560_1440", "2560_1600", "2048_1152", "1728_1117", "1512_982",
	}
	cores := []string{"4", "6", "8", "16"}
	resolution := resolutions[(slot-1)%len(resolutions)]
	coreCount := cores[(slot-1)/len(resolutions)]
	deviceMemory := "4"
	if coreCount == "8" || coreCount == "16" {
		deviceMemory = "8"
	}
	config := map[string]any{
		"automatic_timezone":   "1",
		"language_switch":      "1",
		"page_language_switch": "1",
		"screen_resolution":    resolution,
		"hardware_concurrency": coreCount,
		"device_memory":        deviceMemory,
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
			"ua_browser":        []string{"chrome"},
			"ua_system_version": []string{[]string{"Mac OS X 12", "Mac OS X 13"}[(slot-1)%2]},
		}
		// Value 3 asks AdsPower to generate matching WebGL metadata during
		// profile creation. AdsPower does not accept that value on updates.
		config["webgl"] = "3"
	}
	return config
}

func (c *adsPowerClient) createProfile(ctx context.Context, name string, template *adsPowerProfile, fingerprintSlot int) (*adsPowerProfile, error) {
	return c.createProfileWithReceipt(ctx, name, template, fingerprintSlot, nil)
}

func (c *adsPowerClient) createProfileWithReceipt(ctx context.Context, name string, template *adsPowerProfile, fingerprintSlot int, receipt func(string) error) (*adsPowerProfile, error) {
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
		"fingerprint_config": randomizedFingerprintConfig(fingerprintSlot, true),
	}
	if strings.TrimSpace(template.ProxyID) != "" {
		payload["proxyid"] = template.ProxyID
	} else {
		payload["user_proxy_config"] = proxyConfig
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
	if receipt != nil {
		if err := receipt(profileID); err != nil {
			return nil, fmt.Errorf("保存新建环境所有权失败: %w", err)
		}
	}
	return c.profile(ctx, profileID)
}

func (c *adsPowerClient) enforceProfilePolicy(ctx context.Context, profile *adsPowerProfile, template *adsPowerProfile, fingerprintSlot int) error {
	if profile == nil || template == nil {
		return errors.New("AdsPower profile policy cannot be applied")
	}
	proxyConfig := template.UserProxyConfig
	proxyConfig.LatestIP = ""
	fingerprintConfig := map[string]any{"webrtc": "disabled"}
	if fingerprintSlot > 0 {
		fingerprintConfig = randomizedFingerprintConfig(fingerprintSlot, false)
	}
	payload := map[string]any{
		"profile_id":         profile.UserID,
		"fingerprint_config": fingerprintConfig,
	}
	if strings.TrimSpace(template.ProxyID) != "" {
		payload["proxyid"] = template.ProxyID
	} else {
		payload["user_proxy_config"] = proxyConfig
	}
	return c.do(ctx, http.MethodPost, "/api/v2/browser-profile/update", payload, nil)
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
	switch strings.ToLower(strings.TrimSpace(data.Status)) {
	case "active":
		return true, nil
	case "inactive":
		return false, nil
	default:
		return false, errors.New("AdsPower did not confirm browser activity status")
	}
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
	probes := []struct {
		url  string
		json bool
	}{
		{url: "https://checkip.amazonaws.com"},
		{url: "https://icanhazip.com"},
		{url: "https://api.ipify.org?format=json", json: true},
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		exitIP, err := probeProxyExitIPRound(ctx, cfg, auth, probes)
		if err == nil {
			return exitIP, nil
		}
		lastErr = err
		if attempt == 0 {
			if err := sleepWithContext(ctx, 500*time.Millisecond); err != nil {
				return "", err
			}
		}
	}
	return "", fmt.Errorf("SOCKS5 proxy check failed: %w", lastErr)
}

func probeProxyExitIPRound(ctx context.Context, cfg adsPowerProxyConfig, auth *proxy.Auth, probes []struct {
	url  string
	json bool
}) (string, error) {
	type result struct {
		exitIP string
		err    error
	}
	roundCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	results := make(chan result, len(probes))
	for _, candidate := range probes {
		candidate := candidate
		go func() {
			exitIP, err := probeProxyExitIP(roundCtx, cfg, auth, candidate.url, candidate.json)
			results <- result{exitIP: exitIP, err: err}
		}()
	}
	var lastErr error
	for range probes {
		select {
		case candidate := <-results:
			if candidate.err == nil {
				cancel()
				return candidate.exitIP, nil
			}
			lastErr = candidate.err
		case <-roundCtx.Done():
			return "", roundCtx.Err()
		}
	}
	if lastErr == nil {
		lastErr = errors.New("all proxy probes failed")
	}
	return "", lastErr
}

func probeProxyExitIP(ctx context.Context, cfg adsPowerProxyConfig, auth *proxy.Auth, endpoint string, jsonResponse bool) (string, error) {
	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort(strings.TrimSpace(cfg.ProxyHost), strings.TrimSpace(cfg.ProxyPort)), auth, &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second})
	if err != nil {
		return "", err
	}
	transport := &http.Transport{
		DialContext: func(_ context.Context, network, address string) (net.Conn, error) {
			return dialer.Dial(network, address)
		},
		ForceAttemptHTTP2: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("proxy check returned %d", response.StatusCode)
	}
	var exitIP string
	if jsonResponse {
		var result struct {
			IP string `json:"ip"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil {
			return "", err
		}
		exitIP = result.IP
	} else {
		body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
		if err != nil {
			return "", err
		}
		exitIP = string(body)
	}
	exitIP = strings.TrimSpace(exitIP)
	if net.ParseIP(exitIP) == nil {
		return "", errors.New("SOCKS5 proxy did not return a valid exit IP")
	}
	return exitIP, nil
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
