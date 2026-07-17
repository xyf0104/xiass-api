package main

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

//go:embed web/*.html
var webFiles embed.FS

type helperServer struct {
	manager      *ConfigManager
	state        string
	siteMu       sync.RWMutex
	siteURL      *url.URL
	index        *template.Template
	callback     []byte
	shutdown     chan struct{}
	shutdownOnce sync.Once
	detect       func() CodexInstallation
	restart      func(CodexInstallation) error
}

type statusResponse struct {
	Version     string            `json:"version"`
	ConfigPath  string            `json:"config_path"`
	Codex       CodexInstallation `json:"codex"`
	ConnectURL  string            `json:"connect_url"`
	SiteURL     string            `json:"site_url"`
	BackupCount int               `json:"backup_count"`
}

type operationResponse struct {
	OK             bool   `json:"ok"`
	Message        string `json:"message"`
	BackupID       string `json:"backup_id,omitempty"`
	SafetyBackupID string `json:"safety_backup_id,omitempty"`
	Restarted      bool   `json:"restarted"`
	ConfigVerified bool   `json:"config_verified"`
}

func newHelperServer(manager *ConfigManager, site string, state string) (*helperServer, error) {
	var parsedSite *url.URL
	if strings.TrimSpace(site) != "" {
		var err error
		parsedSite, err = parseSiteURL(site)
		if err != nil {
			return nil, err
		}
	}
	indexBytes, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		return nil, err
	}
	callback, err := webFiles.ReadFile("web/callback.html")
	if err != nil {
		return nil, err
	}
	index, err := template.New("index").Parse(string(indexBytes))
	if err != nil {
		return nil, err
	}
	return &helperServer{
		manager:  manager,
		state:    state,
		siteURL:  parsedSite,
		index:    index,
		callback: callback,
		shutdown: make(chan struct{}),
		detect:   detectCodexInstallation,
		restart:  restartCodex,
	}, nil
}

func (s *helperServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /callback", s.handleCallback)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/backups", s.handleBackups)
	mux.HandleFunc("POST /api/site", s.handleSite)
	mux.HandleFunc("POST /api/apply", s.handleApply)
	mux.HandleFunc("POST /api/restore", s.handleRestore)
	mux.HandleFunc("POST /api/shutdown", s.handleShutdown)
	return s.localOnly(s.securityHeaders(mux))
}

func (s *helperServer) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	siteURL := defaultXIASSAPIURL
	if configured := s.currentSiteURL(); configured != nil {
		siteURL = configured.String()
	}
	_ = s.index.Execute(w, map[string]string{
		"State":   s.state,
		"SiteURL": siteURL,
	})
}

func (s *helperServer) handleCallback(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.callback)
}

func (s *helperServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	backups, _ := s.manager.ListBackups()
	connectURL, siteURL := s.connectionDetails(r.Host)
	writeJSON(w, http.StatusOK, statusResponse{
		Version:     version,
		ConfigPath:  s.manager.ConfigPath,
		Codex:       s.detect(),
		ConnectURL:  connectURL,
		SiteURL:     siteURL,
		BackupCount: len(backups),
	})
}

func (s *helperServer) handleSite(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	var request struct {
		SiteURL string `json:"site_url"`
	}
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	parsed, err := parseSiteURL(request.SiteURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.siteMu.Lock()
	s.siteURL = parsed
	s.siteMu.Unlock()
	connectURL, siteURL := s.connectionDetails(r.Host)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"site_url":    siteURL,
		"connect_url": connectURL,
	})
}

func (s *helperServer) handleBackups(w http.ResponseWriter, _ *http.Request) {
	backups, err := s.manager.ListBackups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": backups})
}

func (s *helperServer) handleApply(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	var input ApplyConfig
	if err := decodeJSONBody(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	site := s.currentSiteURL()
	if site == nil {
		writeError(w, http.StatusBadRequest, errors.New("XIASS API site has not been configured"))
		return
	}
	parsedBase, err := url.Parse(input.BaseURL)
	if err != nil || parsedBase.Scheme != "https" || !strings.EqualFold(parsedBase.Host, site.Host) {
		writeError(w, http.StatusBadRequest, errors.New("configuration does not belong to this XIASS API site"))
		return
	}

	result, err := s.manager.Apply(input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	installation := s.detect()
	if err := s.restart(installation); err != nil {
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        "Configuration was written and verified, but Codex could not be restarted: " + err.Error(),
			BackupID:       result.BackupID,
			ConfigVerified: true,
		})
		return
	}
	writeJSON(w, http.StatusOK, operationResponse{
		OK:             true,
		Message:        "XIASS API configuration was written, verified, and Codex was restarted.",
		BackupID:       result.BackupID,
		Restarted:      true,
		ConfigVerified: true,
	})
}

func parseSiteURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("site must be a valid HTTPS URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

func (s *helperServer) currentSiteURL() *url.URL {
	s.siteMu.RLock()
	defer s.siteMu.RUnlock()
	if s.siteURL == nil {
		return nil
	}
	copy := *s.siteURL
	return &copy
}

func (s *helperServer) connectionDetails(callbackHost string) (string, string) {
	site := s.currentSiteURL()
	if site == nil {
		return "", ""
	}
	connect := *site
	connect.Path = "/codex-helper/connect"
	query := connect.Query()
	query.Set("callback", "http://"+callbackHost+"/callback")
	query.Set("state", s.state)
	connect.RawQuery = query.Encode()
	connect.Fragment = ""
	return connect.String(), site.String()
}

func (s *helperServer) handleRestore(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	var request struct {
		BackupID string `json:"backup_id"`
	}
	if err := decodeJSONBody(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := s.manager.Restore(request.BackupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	installation := s.detect()
	if err := s.restart(installation); err != nil {
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        "Original configuration was restored and verified, but Codex could not be restarted: " + err.Error(),
			BackupID:       result.RestoredBackupID,
			SafetyBackupID: result.SafetyBackupID,
			ConfigVerified: true,
		})
		return
	}
	writeJSON(w, http.StatusOK, operationResponse{
		OK:             true,
		Message:        "Original configuration was restored, verified, and Codex was restarted.",
		BackupID:       result.RestoredBackupID,
		SafetyBackupID: result.SafetyBackupID,
		Restarted:      true,
		ConfigVerified: true,
	})
}

func (s *helperServer) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	go func() {
		time.Sleep(150 * time.Millisecond)
		s.requestShutdown()
	}()
}

func (s *helperServer) validState(r *http.Request) bool {
	return r.Header.Get("X-XIASS-Helper-State") == s.state
}

func (s *helperServer) requestShutdown() {
	s.shutdownOnce.Do(func() { close(s.shutdown) })
}

func (s *helperServer) localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "loopback access only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *helperServer) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func decodeJSONBody(r *http.Request, destination any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		body = []byte("{}")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"ok": false, "message": err.Error()})
}

func randomState() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
