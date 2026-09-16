package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type config struct {
	ListenAddress   string                  `json:"listen_address"`
	CallbackAddress string                  `json:"callback_address"`
	AdsPowerBaseURL string                  `json:"adspower_base_url"`
	APIKey          string                  `json:"api_key,omitempty"`
	DeviceID        string                  `json:"device_id"`
	Servers         map[string]serverConfig `json:"servers"`
	path            string
}

type serverConfig struct {
	EnvironmentKey    string `json:"environment_key"`
	TemplateProfileID string `json:"template_profile_id,omitempty"`
	ProxyHost         string `json:"proxy_host,omitempty"`
	ProxyPort         string `json:"proxy_port,omitempty"`
	ProxyUser         string `json:"proxy_user,omitempty"`
	ProxyPassword     string `json:"proxy_password,omitempty"`
}

func defaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "XIASS", "adspower-helper", "config.json"), nil
}

func loadConfig(path string) (*config, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = defaultConfigPath()
		if err != nil {
			return nil, err
		}
	}
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := &config{path: path}
		if _, err := cfg.normalize(); err != nil {
			return nil, err
		}
		if err := saveConfig(cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg config
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.path = path
	if envKey := strings.TrimSpace(os.Getenv("ADSPOWER_API_KEY")); envKey != "" {
		cfg.APIKey = envKey
	}
	changed, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	if changed {
		if err := saveConfig(&cfg); err != nil {
			return nil, err
		}
	}
	return &cfg, nil
}

func (c *config) normalize() (bool, error) {
	changed := false
	if c.ListenAddress == "" {
		c.ListenAddress = "127.0.0.1:34987"
		changed = true
	}
	if c.CallbackAddress == "" {
		c.CallbackAddress = "127.0.0.1:1455"
		changed = true
	}
	if c.AdsPowerBaseURL == "" {
		c.AdsPowerBaseURL = "http://local.adspower.net:50325"
		changed = true
	}
	adsPowerURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(c.AdsPowerBaseURL), "/"))
	if err != nil || adsPowerURL.Scheme != "http" || !adsPowerLoopbackHost(adsPowerURL.Hostname()) {
		return false, errors.New("adspower_base_url must use loopback HTTP")
	}
	c.AdsPowerBaseURL = adsPowerURL.String()
	if c.DeviceID == "" {
		c.DeviceID, err = randomID(24)
		if err != nil {
			return false, err
		}
		changed = true
	}
	if !validOpaqueID(c.DeviceID) {
		return false, errors.New("device_id is invalid")
	}
	if c.Servers == nil {
		c.Servers = make(map[string]serverConfig)
		changed = true
	}
	normalizedServers := make(map[string]serverConfig, len(c.Servers))
	for rawOrigin, server := range c.Servers {
		origin, err := normalizeServerOrigin(rawOrigin)
		if err != nil {
			return false, err
		}
		server.EnvironmentKey = strings.TrimSpace(server.EnvironmentKey)
		server.TemplateProfileID = strings.TrimSpace(server.TemplateProfileID)
		server.ProxyHost = normalizeProxyHost(server.ProxyHost)
		server.ProxyPort = strings.TrimSpace(server.ProxyPort)
		server.ProxyUser = strings.TrimSpace(server.ProxyUser)
		server.ProxyPassword = strings.TrimSpace(server.ProxyPassword)
		if !validOpaqueID(server.EnvironmentKey) {
			return false, fmt.Errorf("server %s has an invalid environment or template profile", origin)
		}
		if server.TemplateProfileID != "" && !validOpaqueID(server.TemplateProfileID) {
			return false, fmt.Errorf("server %s has an invalid template profile", origin)
		}
		if server.TemplateProfileID == "" && !server.validDirectProxy() {
			return false, fmt.Errorf("server %s must configure a template profile or SOCKS5 proxy", origin)
		}
		normalizedServers[origin] = server
		if origin != rawOrigin || server != c.Servers[rawOrigin] {
			changed = true
		}
	}
	c.Servers = normalizedServers
	if c.APIKey != "" {
		if info, statErr := os.Stat(c.path); statErr == nil && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			return false, errors.New("config containing api_key must have file mode 0600")
		}
	}
	return changed, nil
}

func (s serverConfig) validDirectProxy() bool {
	if s.ProxyHost == "" {
		return false
	}
	port, err := strconv.Atoi(strings.TrimSpace(s.ProxyPort))
	return err == nil && port > 0 && port <= 65535
}

func (s serverConfig) adsPowerProxy() (adsPowerProxyConfig, bool) {
	if !s.validDirectProxy() {
		return adsPowerProxyConfig{}, false
	}
	return adsPowerProxyConfig{
		ProxySoft:     "other",
		ProxyType:     "socks5",
		ProxyHost:     s.ProxyHost,
		ProxyPort:     s.ProxyPort,
		ProxyUser:     s.ProxyUser,
		ProxyPassword: s.ProxyPassword,
	}, true
}

func normalizeProxyHost(value string) string {
	value = strings.Trim(strings.TrimSpace(value), "[]")
	if value == "" || strings.ContainsAny(value, "/:@?#") {
		return value
	}
	if net.ParseIP(value) != nil {
		return value
	}
	return strings.ToLower(value)
}

func saveConfig(c *config) error {
	if c == nil || c.path == "" {
		return errors.New("config path is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	temporary := c.path + ".tmp"
	if err := os.WriteFile(temporary, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil && runtime.GOOS != "windows" {
		return err
	}
	return os.Rename(temporary, c.path)
}

func normalizeServerOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("invalid XIASS server origin %q", raw)
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopbackHost(parsed.Hostname())) {
		return "", fmt.Errorf("XIASS server %q must use HTTPS", raw)
	}
	parsed.Path = ""
	return parsed.String(), nil
}

func loopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func adsPowerLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	return loopbackHost(host) || host == "local.adspower.net"
}

func randomID(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func validOpaqueID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}
