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
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed web/*.html
var webFiles embed.FS

type helperServer struct {
	manager        *ConfigManager
	applyConfig    func(ApplyConfig) (ApplyResult, error)
	restoreConfig  func(string) (RestoreResult, error)
	repairHistory  func() (HistoryRepairResult, error)
	restoreHistory func(string) error
	state          string
	operationMu    sync.Mutex
	siteMu         sync.RWMutex
	siteURL        *url.URL
	codexMu        sync.RWMutex
	selectedCodex  *CodexInstallation
	index          *template.Template
	callback       []byte
	shutdown       chan struct{}
	shutdownOnce   sync.Once
	detect         func() CodexInstallation
	selectApp      func() (CodexInstallation, error)
	stop           func(CodexInstallation) error
	start          func(CodexInstallation) error
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
	OK             bool                 `json:"ok"`
	Message        string               `json:"message"`
	BackupID       string               `json:"backup_id,omitempty"`
	SafetyBackupID string               `json:"safety_backup_id,omitempty"`
	Restarted      bool                 `json:"restarted"`
	ConfigVerified bool                 `json:"config_verified"`
	History        *HistoryRepairResult `json:"history,omitempty"`
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
	repairer := NewHistoryRepairer(filepath.Dir(manager.ConfigPath))
	return &helperServer{
		manager:        manager,
		applyConfig:    manager.Apply,
		restoreConfig:  manager.Restore,
		repairHistory:  repairer.RepairCurrentProvider,
		restoreHistory: repairer.RestoreBackup,
		state:          state,
		siteURL:        parsedSite,
		index:          index,
		callback:       callback,
		shutdown:       make(chan struct{}),
		detect:         detectCodexInstallation,
		selectApp:      selectCodexInstallation,
		stop:           stopCodex,
		start:          startCodex,
	}, nil
}

func (s *helperServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /callback", s.handleCallback)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/backups", s.handleBackups)
	mux.HandleFunc("POST /api/site", s.handleSite)
	mux.HandleFunc("POST /api/select-app", s.handleSelectApp)
	mux.HandleFunc("POST /api/apply", s.handleApply)
	mux.HandleFunc("POST /api/restore", s.handleRestore)
	mux.HandleFunc("POST /api/repair-history", s.handleRepairHistory)
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
		Codex:       s.codexInstallation(),
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

func (s *helperServer) handleSelectApp(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	installation, err := s.selectApp()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.codexMu.Lock()
	s.selectedCodex = &installation
	s.codexMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"codex": installation,
	})
}

func (s *helperServer) codexInstallation() CodexInstallation {
	s.codexMu.RLock()
	if s.selectedCodex != nil {
		installation := *s.selectedCodex
		s.codexMu.RUnlock()
		return installation
	}
	s.codexMu.RUnlock()
	return s.detect()
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

	if !s.beginOperation(w) {
		return
	}
	defer s.operationMu.Unlock()
	releaseLifecycle, err := acquireLifecycleLock(filepath.Dir(s.manager.ConfigPath))
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	defer releaseLifecycle()

	installation := s.codexInstallation()
	if err := s.stop(installation); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("Codex could not be stopped safely; no configuration was changed: %w", err))
		return
	}
	result, err := s.applyConfig(input)
	if err != nil {
		if configRollbackFailed(err) {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("配置写入失败，自动回滚也未能确认成功：%v。Codex 保持关闭，避免使用不确定的配置启动。", err),
				ConfigVerified: false,
			})
			return
		}
		startErr := s.startWithRetry(installation)
		writeError(w, http.StatusInternalServerError, operationFailure("configuration was not changed", err, startErr))
		return
	}
	history, err := s.repairHistory()
	if err != nil {
		historyRollbackUnsafe := historyRollbackFailed(err)
		_, rollbackErr := s.restoreConfig(result.BackupID)
		if rollbackErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("History repair failed: %v. Configuration rollback also failed: %v. Codex was left closed to protect the existing conversations.", err, rollbackErr),
				BackupID:       result.BackupID,
				ConfigVerified: false,
			})
			return
		}
		if historyRollbackUnsafe {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        "历史会话修复和自动回滚均失败。原配置已恢复，但 Codex 保持关闭，避免在会话索引不一致时启动。恢复备份仍完整保留。",
				BackupID:       result.BackupID,
				ConfigVerified: true,
			})
			return
		}
		startErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        operationFailure("History repair failed, so the original configuration and conversations were restored", err, startErr).Error(),
			BackupID:       result.BackupID,
			Restarted:      startErr == nil,
			ConfigVerified: true,
		})
		return
	}
	if err := s.startWithRetry(installation); err != nil {
		if stopErr := s.stopBeforeRollback(installation); stopErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("新配置启动检测失败：%v；无法再次确认 Codex 已退出：%v。为避免在线覆盖数据库，未执行回滚。", err, stopErr),
				BackupID:       result.BackupID,
				ConfigVerified: true,
				History:        &history,
			})
			return
		}
		historyRollbackErr := s.restoreHistory(history.BackupID)
		_, configRollbackErr := s.restoreConfig(result.BackupID)
		if historyRollbackErr != nil || configRollbackErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("新配置启动失败：%v；安全回滚未完整完成（历史：%v，配置：%v）。Codex 保持关闭。", err, historyRollbackErr, configRollbackErr),
				BackupID:       result.BackupID,
				ConfigVerified: configRollbackErr == nil,
			})
			return
		}
		recoveryStartErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        operationFailure("新配置无法启动，已恢复原配置和原历史会话", err, recoveryStartErr).Error(),
			BackupID:       result.BackupID,
			Restarted:      recoveryStartErr == nil,
			ConfigVerified: true,
		})
		return
	}
	writeJSON(w, http.StatusOK, operationResponse{
		OK:             true,
		Message:        "XIASS API 配置已写入；" + historySummary(history) + " Codex 已重新启动。",
		BackupID:       result.BackupID,
		Restarted:      true,
		ConfigVerified: true,
		History:        &history,
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
	if !s.beginOperation(w) {
		return
	}
	defer s.operationMu.Unlock()
	releaseLifecycle, err := acquireLifecycleLock(filepath.Dir(s.manager.ConfigPath))
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	defer releaseLifecycle()

	installation := s.codexInstallation()
	if err := s.stop(installation); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("Codex could not be stopped safely; no configuration was restored: %w", err))
		return
	}
	result, err := s.restoreConfig(request.BackupID)
	if err != nil {
		if configRollbackFailed(err) {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("配置恢复失败，自动回滚也未能确认成功：%v。Codex 保持关闭，避免使用不确定的配置启动。", err),
				ConfigVerified: false,
			})
			return
		}
		startErr := s.startWithRetry(installation)
		writeError(w, http.StatusInternalServerError, operationFailure("configuration was not restored", err, startErr))
		return
	}
	if _, _, err := s.manager.UpgradeLegacyProvider(); err != nil {
		_, rollbackErr := s.restoreConfig(result.SafetyBackupID)
		if rollbackErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("旧版 XIASS 配置升级失败：%v；恢复前配置回滚也失败：%v。Codex 保持关闭。", err, rollbackErr),
				BackupID:       result.RestoredBackupID,
				SafetyBackupID: result.SafetyBackupID,
			})
			return
		}
		startErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        operationFailure("旧版 XIASS 配置无法安全升级，已恢复到操作前状态", err, startErr).Error(),
			BackupID:       result.RestoredBackupID,
			SafetyBackupID: result.SafetyBackupID,
			Restarted:      startErr == nil,
			ConfigVerified: true,
		})
		return
	}
	history, err := s.repairHistory()
	if err != nil {
		historyRollbackUnsafe := historyRollbackFailed(err)
		_, rollbackErr := s.restoreConfig(result.SafetyBackupID)
		if rollbackErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("History repair failed: %v. Configuration rollback also failed: %v. Codex was left closed to protect the existing conversations.", err, rollbackErr),
				BackupID:       result.RestoredBackupID,
				SafetyBackupID: result.SafetyBackupID,
			})
			return
		}
		if historyRollbackUnsafe {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        "历史会话修复和自动回滚均失败。恢复前配置已还原，但 Codex 保持关闭，避免在会话索引不一致时启动。",
				BackupID:       result.RestoredBackupID,
				SafetyBackupID: result.SafetyBackupID,
				ConfigVerified: true,
			})
			return
		}
		startErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        operationFailure("History repair failed, so the pre-restore configuration and conversations were restored", err, startErr).Error(),
			BackupID:       result.RestoredBackupID,
			SafetyBackupID: result.SafetyBackupID,
			Restarted:      startErr == nil,
			ConfigVerified: true,
		})
		return
	}
	if err := s.startWithRetry(installation); err != nil {
		if stopErr := s.stopBeforeRollback(installation); stopErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("恢复状态启动检测失败：%v；无法再次确认 Codex 已退出：%v。为避免在线覆盖数据库，未执行回滚。", err, stopErr),
				BackupID:       result.RestoredBackupID,
				SafetyBackupID: result.SafetyBackupID,
				ConfigVerified: true,
				History:        &history,
			})
			return
		}
		historyRollbackErr := s.restoreHistory(history.BackupID)
		_, configRollbackErr := s.restoreConfig(result.SafetyBackupID)
		if historyRollbackErr != nil || configRollbackErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:             false,
				Message:        fmt.Sprintf("恢复后的配置无法启动：%v；恢复到操作前状态也未完整完成（历史：%v，配置：%v）。Codex 保持关闭。", err, historyRollbackErr, configRollbackErr),
				BackupID:       result.RestoredBackupID,
				SafetyBackupID: result.SafetyBackupID,
				ConfigVerified: configRollbackErr == nil,
			})
			return
		}
		recoveryStartErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:             false,
			Message:        operationFailure("所选恢复状态无法启动，已回到恢复前配置和历史会话", err, recoveryStartErr).Error(),
			BackupID:       result.RestoredBackupID,
			SafetyBackupID: result.SafetyBackupID,
			Restarted:      recoveryStartErr == nil,
			ConfigVerified: true,
		})
		return
	}
	writeJSON(w, http.StatusOK, operationResponse{
		OK:             true,
		Message:        "原配置已恢复；" + historySummary(history) + " Codex 已重新启动。",
		BackupID:       result.RestoredBackupID,
		SafetyBackupID: result.SafetyBackupID,
		Restarted:      true,
		ConfigVerified: true,
		History:        &history,
	})
}

func (s *helperServer) handleRepairHistory(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	if !s.beginOperation(w) {
		return
	}
	defer s.operationMu.Unlock()
	releaseLifecycle, err := acquireLifecycleLock(filepath.Dir(s.manager.ConfigPath))
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	defer releaseLifecycle()

	installation := s.codexInstallation()
	if err := s.stop(installation); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("Codex could not be stopped safely; no conversation data was changed: %w", err))
		return
	}
	history, err := s.repairHistory()
	if err != nil {
		if historyRollbackFailed(err) {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:      false,
				Message: "历史会话修复和自动回滚均失败。Codex 保持关闭，请使用已创建的历史备份恢复后再启动。",
			})
			return
		}
		startErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:        false,
			Message:   operationFailure("Conversation history repair failed and all attempted changes were rolled back", err, startErr).Error(),
			Restarted: startErr == nil,
		})
		return
	}
	if err := s.startWithRetry(installation); err != nil {
		if stopErr := s.stopBeforeRollback(installation); stopErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:      false,
				Message: fmt.Sprintf("历史修复后的启动检测失败：%v；无法再次确认 Codex 已退出：%v。为避免在线覆盖数据库，未执行回滚。", err, stopErr),
				History: &history,
			})
			return
		}
		if rollbackErr := s.restoreHistory(history.BackupID); rollbackErr != nil {
			writeJSON(w, http.StatusInternalServerError, operationResponse{
				OK:      false,
				Message: fmt.Sprintf("历史修复后 Codex 无法启动：%v；历史快照回滚也失败：%v。Codex 保持关闭。", err, rollbackErr),
			})
			return
		}
		recoveryStartErr := s.startWithRetry(installation)
		writeJSON(w, http.StatusInternalServerError, operationResponse{
			OK:        false,
			Message:   operationFailure("历史修复后 Codex 无法启动，已恢复修复前历史", err, recoveryStartErr).Error(),
			Restarted: recoveryStartErr == nil,
		})
		return
	}
	writeJSON(w, http.StatusOK, operationResponse{
		OK:        true,
		Message:   historySummary(history) + " Codex 已重新启动。",
		Restarted: true,
		History:   &history,
	})
}

func operationFailure(prefix string, operationErr, startErr error) error {
	if startErr == nil {
		return fmt.Errorf("%s: %w; Codex was restarted with the previous safe state", prefix, operationErr)
	}
	return fmt.Errorf("%s: %v; Codex also could not be restarted: %w", prefix, operationErr, startErr)
}

func (s *helperServer) startWithRetry(installation CodexInstallation) error {
	firstErr := s.start(installation)
	if firstErr == nil {
		return nil
	}
	time.Sleep(300 * time.Millisecond)
	redetected := s.detect()
	if !redetected.Found {
		redetected = installation
	}
	if secondErr := s.start(redetected); secondErr != nil {
		return fmt.Errorf("first launch attempt failed: %v; retry failed: %w", firstErr, secondErr)
	}
	return nil
}

func (s *helperServer) stopBeforeRollback(fallback CodexInstallation) error {
	installation := s.codexInstallation()
	if !installation.Found {
		installation = fallback
	}
	return s.stop(installation)
}

func historyRollbackFailed(err error) bool {
	var repairErr *HistoryRepairApplyError
	return errors.As(err, &repairErr) && repairErr.RollbackErr != nil
}

func configRollbackFailed(err error) bool {
	var mutationErr *ConfigMutationError
	return errors.As(err, &mutationErr) && mutationErr.RollbackErr != nil
}

func historySummary(result HistoryRepairResult) string {
	return fmt.Sprintf(
		"已扫描 %d 个会话文件和 %d 个会话数据库，校验 %d 行会话索引，修复 %d 个文件和 %d 行索引；会话数量未减少。",
		result.ScannedSessionFiles,
		result.ScannedDatabases,
		result.ThreadCount,
		result.UpdatedSessionFiles,
		result.UpdatedDatabaseRows,
	)
}

func (s *helperServer) beginOperation(w http.ResponseWriter) bool {
	if s.operationMu.TryLock() {
		return true
	}
	writeError(w, http.StatusConflict, errors.New("another Codex configuration operation is already running"))
	return false
}

func (s *helperServer) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if !s.validState(r) {
		writeError(w, http.StatusForbidden, errors.New("invalid local helper session"))
		return
	}
	if !s.beginOperation(w) {
		return
	}
	defer s.operationMu.Unlock()
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
