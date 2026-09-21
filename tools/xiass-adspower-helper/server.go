package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type helperServer struct {
	cfg                *config
	adsPower           *adsPowerClient
	runtimeMu          sync.RWMutex
	client             *http.Client
	verifyProxy        func(context.Context, adsPowerProxyConfig) (string, error)
	profileLocks       sync.Map
	callbacksMu        sync.Mutex
	callbacks          map[string]callbackRegistration
	callbackDeliveryMu sync.Mutex
	closeDelay         time.Duration
	remoteSlots        chan struct{}
	poolMu             sync.Mutex
}

type launchPayload struct {
	AccountID         int64            `json:"account_id"`
	AccountName       string           `json:"account_name"`
	SessionID         string           `json:"session_id"`
	AuthURL           string           `json:"auth_url"`
	EnvironmentKey    string           `json:"environment_key"`
	Existing          *existingBinding `json:"existing_binding"`
	BindingToken      string           `json:"binding_token"`
	CallbackToken     string           `json:"callback_token"`
	LoginEmail        string           `json:"login_email"`
	LoginMethod       string           `json:"login_method"`
	Password          string           `json:"password"`
	TOTPSecret        string           `json:"totp_secret"`
	EmailCodeToken    string           `json:"email_code_token"`
	WorkflowMode      string           `json:"workflow_mode"`
	ExpiresAt         time.Time        `json:"expires_at"`
	CallbackExpiresAt time.Time        `json:"callback_expires_at"`
	oauthStartedAt    time.Time
	emailSession      *emailCodeSession
	isolatedContext   bool
	configOrigin      string
}

func (p *launchPayload) automated() bool {
	if p == nil || strings.TrimSpace(p.CallbackToken) == "" || strings.TrimSpace(p.LoginEmail) == "" {
		return false
	}
	if strings.TrimSpace(p.LoginMethod) == "email_code" {
		return strings.TrimSpace(p.EmailCodeToken) != ""
	}
	return strings.TrimSpace(p.Password) != ""
}

type callbackRegistration struct {
	ServerOrigin string
	Token        string
	ProfileID    string
	SessionID    string
	ExpiresAt    time.Time
	Delivered    bool
}

type existingBinding struct {
	DeviceID        string `json:"device_id"`
	ProfileID       string `json:"profile_id"`
	EnvironmentKey  string `json:"environment_key"`
	FingerprintSlot int    `json:"fingerprint_slot,omitempty"`
	SharedProfile   bool   `json:"shared_profile,omitempty"`
}

type apiEnvelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type launchView struct {
	Success        bool
	Title          string
	Message        string
	ProfileName    string
	EnvironmentKey string
	ExitIP         string
}

type callbackView struct {
	Success bool
	Title   string
	Message string
	URL     string
}

type adsPowerSMSActionResult struct {
	Status    string     `json:"status"`
	Number    string     `json:"number"`
	Code      string     `json:"code"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func newHelperServer(cfg *config) *helperServer {
	return &helperServer{
		cfg:         cfg,
		adsPower:    newAdsPowerClient(cfg),
		client:      &http.Client{Timeout: 20 * time.Second},
		verifyProxy: verifyProxyExitIP,
		callbacks:   make(map[string]callbackRegistration),
		closeDelay:  1500 * time.Millisecond,
		remoteSlots: make(chan struct{}, 3),
	}
}

func (s *helperServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("OPTIONS /healthz", s.healthOptions)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /setup", s.setupPage)
	mux.HandleFunc("GET /api/setup", s.setupState)
	mux.HandleFunc("POST /api/setup", s.saveSetup)
	mux.HandleFunc("GET /pair", s.pair)
	mux.HandleFunc("GET /launch", s.launch)
	return loopbackOnly(securityHeaders(mux))
}

func (s *helperServer) callbackRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/callback", s.callback)
	return loopbackOnly(securityHeaders(mux))
}

func (s *helperServer) health(w http.ResponseWriter, r *http.Request) {
	setHealthCORSHeaders(w)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	_, adsPower := s.runtimeSnapshot()
	if err := adsPower.status(ctx); err != nil {
		http.Error(w, "AdsPower Local API is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

func (s *helperServer) healthOptions(w http.ResponseWriter, _ *http.Request) {
	setHealthCORSHeaders(w)
	w.WriteHeader(http.StatusNoContent)
}

func setHealthCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Private-Network", "true")
	w.Header().Set("Cache-Control", "no-store")
}

func (s *helperServer) launch(w http.ResponseWriter, r *http.Request) {
	cfg, adsPower := s.runtimeSnapshot()
	serverOrigin, err := normalizeServerOrigin(r.URL.Query().Get("server"))
	if err != nil {
		s.renderLaunch(w, http.StatusBadRequest, launchView{Title: "XIASS 地址无效", Message: err.Error()})
		return
	}
	_, ok := cfg.Servers[serverOrigin]
	if !ok {
		s.renderLaunch(w, http.StatusForbidden, launchView{Title: "未授权的 XIASS 服务器", Message: "请先在本机助手配置中登记该服务器。"})
		return
	}
	ticket := strings.TrimSpace(r.URL.Query().Get("ticket"))
	if !validOpaqueID(ticket) {
		s.renderLaunch(w, http.StatusBadRequest, launchView{Title: "启动票据无效", Message: "请从 XIASS 管理页面重新点击固定指纹浏览器。"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	payload, err := s.redeem(ctx, serverOrigin, ticket)
	if err != nil {
		s.renderLaunch(w, http.StatusBadGateway, launchView{Title: "无法读取启动任务", Message: err.Error()})
		return
	}
	targetConfigOrigin, serverCfg, ok := cfg.serverForEnvironment(serverOrigin, payload.EnvironmentKey)
	if !ok {
		s.renderLaunch(w, http.StatusConflict, launchView{Title: "服务器出口不匹配", Message: "当前账号不属于这个 AdsPower 出口环境。"})
		return
	}
	payload.configOrigin = targetConfigOrigin

	var pending *pendingProfileBinding
	identity := payload.LoginEmail
	if strings.TrimSpace(identity) == "" {
		identity = payload.AccountName
	}
	pendingKey := pendingProfileKey(targetConfigOrigin, payload.EnvironmentKey, identity)
	lock := s.profileLock(payload)
	lock.Lock()
	defer lock.Unlock()
	// Allocation, reclamation, binding and browser start share one boundary.
	// The persisted profile lease then protects the entire OAuth lifetime.
	s.poolMu.Lock()
	defer s.poolMu.Unlock()
	if payload.Existing == nil {
		pending = s.pendingProfile(pendingKey)
	}
	profileID := ""
	if payload.Existing != nil {
		profileID = payload.Existing.ProfileID
	} else if pending != nil {
		profileID = pending.ProfileID
	}
	if profileID != "" {
		if err := s.checkProfileIdle(ctx, adsPower, profileID); err != nil {
			s.progress(serverOrigin, payload, "failed", "failed", "adspower_profile_busy")
			s.renderLaunch(w, http.StatusConflict, launchView{Title: "指纹环境正在使用", Message: err.Error()})
			return
		}
	}
	fingerprintSlot := 0
	if payload.Existing != nil {
		fingerprintSlot = payload.Existing.FingerprintSlot
	} else if pending != nil {
		fingerprintSlot = pending.FingerprintSlot
	}
	if fingerprintSlot < 1 || fingerprintSlot > 52 {
		if payload.Existing != nil || pending != nil {
			fingerprintSlot = 0
		} else {
			fingerprintSlot, err = s.allocateFingerprintSlot(targetConfigOrigin)
			if err != nil {
				s.progress(serverOrigin, payload, "failed", "failed", "automation_start_failed")
				s.renderLaunch(w, http.StatusInternalServerError, launchView{Title: "指纹环境未启动", Message: err.Error(), EnvironmentKey: payload.EnvironmentKey})
				return
			}
		}
	}
	profile, templateProfile, exitIP, err := s.prepareProfile(ctx, payload, serverCfg, cfg.DeviceID, fingerprintSlot, pending, adsPower)
	if errors.Is(err, errAdsPowerProfileLimit) && payload.Existing == nil && pending == nil {
		if _, inventoryErr := s.reconcileProfiles(ctx, targetConfigOrigin, adsPower); inventoryErr != nil {
			log.Printf("AdsPower capacity reconciliation unavailable; no profiles deleted: %v", inventoryErr)
		} else {
			profile, templateProfile, exitIP, err = s.prepareProfile(ctx, payload, serverCfg, cfg.DeviceID, fingerprintSlot, nil, adsPower)
			if errors.Is(err, errAdsPowerProfileLimit) && payload.automated() {
				profile, templateProfile, exitIP, fingerprintSlot, err = s.reusePoolProfile(ctx, targetConfigOrigin, payload, serverCfg, adsPower)
			}
		}
	}
	if err != nil {
		reason := "automation_start_failed"
		if errors.Is(err, errAdsPowerProfileLimit) {
			reason = "adspower_profile_limit"
		} else if errors.Is(err, errAdsPowerProfileBusy) {
			reason = "adspower_profile_busy"
		} else if strings.Contains(err.Error(), "出口代理不可用") {
			reason = "proxy_unavailable"
		}
		s.progress(serverOrigin, payload, "failed", "failed", reason)
		s.renderLaunch(w, http.StatusConflict, launchView{Title: "指纹环境未启动", Message: err.Error(), EnvironmentKey: payload.EnvironmentKey})
		return
	}
	if err := s.reserveManagedProfile(targetConfigOrigin, payload, profile, fingerprintSlot); err != nil {
		s.progress(serverOrigin, payload, "failed", "failed", "automation_start_failed")
		s.renderLaunch(w, http.StatusInternalServerError, launchView{Title: "环境租约未保存", Message: err.Error()})
		return
	}
	started := false
	defer func() {
		if !started {
			_ = s.releaseProfileLease(profile.UserID)
		}
	}()
	current, _ := s.runtimeSnapshot()
	payload.isolatedContext = current.ManagedProfiles[profile.UserID].Shared || (payload.Existing != nil && payload.Existing.SharedProfile)
	if payload.isolatedContext && !payload.automated() {
		s.progress(serverOrigin, payload, "failed", "failed", "automation_start_failed")
		s.renderLaunch(w, http.StatusConflict, launchView{Title: "共享槽位需要自动授权", Message: "请使用工作台自动授权，以隔离账号登录会话。"})
		return
	}
	if payload.Existing == nil {
		if err := s.rememberPendingProfile(pendingKey, pendingProfileBinding{ProfileID: profile.UserID, EnvironmentKey: payload.EnvironmentKey, FingerprintSlot: fingerprintSlot}); err != nil {
			s.progress(serverOrigin, payload, "failed", "failed", "automation_start_failed")
			s.renderLaunch(w, http.StatusInternalServerError, launchView{Title: "环境绑定未保存", Message: err.Error(), ProfileName: profile.Name, EnvironmentKey: payload.EnvironmentKey, ExitIP: exitIP})
			return
		}
	}
	if err := s.reportBinding(ctx, serverOrigin, payload, profile, templateProfile, exitIP, cfg.DeviceID, fingerprintSlot); err != nil {
		s.progress(serverOrigin, payload, "failed", "failed", "automation_start_failed")
		s.renderLaunch(w, http.StatusBadGateway, launchView{Title: "环境绑定未保存", Message: err.Error(), ProfileName: profile.Name, EnvironmentKey: payload.EnvironmentKey, ExitIP: exitIP})
		return
	}
	state, registered, err := s.registerCallback(serverOrigin, payload, profile.UserID)
	if err != nil {
		s.progress(serverOrigin, payload, "failed", "failed", "automation_start_failed")
		s.renderLaunch(w, http.StatusConflict, launchView{Title: "授权回调无法绑定", Message: err.Error(), ProfileName: profile.Name, EnvironmentKey: payload.EnvironmentKey, ExitIP: exitIP})
		return
	}
	browserSession, err := adsPower.startProfile(ctx, profile.UserID)
	if err != nil {
		if registered {
			s.removeCallback(state)
		}
		s.progress(serverOrigin, payload, "failed", "failed", "automation_start_failed")
		s.renderLaunch(w, http.StatusBadGateway, launchView{Title: "AdsPower 启动失败", Message: err.Error(), ProfileName: profile.Name, EnvironmentKey: payload.EnvironmentKey, ExitIP: exitIP})
		return
	}
	started = true
	if payload.automated() {
		automationPayload := *payload
		go s.runOpenAIAutomation(serverOrigin, &automationPayload, profile.UserID, browserSession)
	}
	title := "固定指纹环境已打开"
	message := "此账号以后会继续使用同一个 AdsPower 环境。完成登录后，授权结果会自动返回 XIASS。"
	if payload.automated() {
		title = "固定指纹自动授权已启动"
		message = "XIASS 正在此账号的固定 AdsPower 环境中自动登录、授权并等待回调。"
	}
	s.renderLaunch(w, http.StatusOK, launchView{
		Success: true, Title: title,
		Message:     message,
		ProfileName: profile.Name, EnvironmentKey: payload.EnvironmentKey, ExitIP: exitIP,
	})
}

func (s *helperServer) prepareProfile(ctx context.Context, payload *launchPayload, serverCfg serverConfig, deviceID string, fingerprintSlot int, pending *pendingProfileBinding, adsPower *adsPowerClient) (*adsPowerProfile, *adsPowerProfile, string, error) {
	templateProfile, err := resolveAdsPowerTemplate(ctx, adsPower, serverCfg)
	if err != nil {
		return nil, nil, "", fmt.Errorf("读取 %s 出口模板失败: %w", serverCfg.EnvironmentKey, err)
	}
	exitIP := strings.TrimSpace(templateProfile.IP)
	if templateProfile.ProxyID == "" {
		exitIP, err = s.verifyProxy(ctx, templateProfile.UserProxyConfig)
		if err != nil {
			return nil, nil, "", fmt.Errorf("%s 出口代理不可用: %w", serverCfg.EnvironmentKey, err)
		}
	}
	if payload.Existing != nil {
		if payload.Existing.DeviceID != deviceID {
			return nil, nil, "", errors.New("该账号已绑定到另一台设备，必须先在 XIASS 中解除绑定")
		}
		if canonicalEnvironmentKey(payload.Existing.EnvironmentKey) != canonicalEnvironmentKey(serverCfg.EnvironmentKey) {
			return nil, nil, "", errors.New("该账号绑定的服务器出口与当前页面不一致")
		}
		profile, err := adsPower.profile(ctx, payload.Existing.ProfileID)
		if err != nil {
			return nil, nil, "", errors.New("账号原有 AdsPower 环境已不存在；请先解除绑定后再创建新环境")
		}
		if err := adsPower.enforceProfilePolicy(ctx, profile, templateProfile, fingerprintSlot); err != nil {
			return nil, nil, "", fmt.Errorf("刷新既有环境安全策略失败: %w", err)
		}
		profile, err = adsPower.profile(ctx, payload.Existing.ProfileID)
		if err != nil || !adsPowerProfileUsesTemplate(profile, templateProfile) {
			return nil, nil, "", errors.New("刷新后的 AdsPower 环境没有使用当前服务器代理")
		}
		profileExitIP := strings.TrimSpace(profile.IP)
		if profileExitIP == "" {
			profileExitIP = strings.TrimSpace(profile.UserProxyConfig.LatestIP)
		}
		if templateProfile.ProxyID == "" {
			profileExitIP, err = s.verifyProxy(ctx, profile.UserProxyConfig)
			if err != nil || profileExitIP != exitIP {
				return nil, nil, "", errors.New("账号固定环境没有使用当前服务器出口")
			}
		}
		return profile, templateProfile, profileExitIP, nil
	}
	if pending != nil {
		if canonicalEnvironmentKey(pending.EnvironmentKey) != canonicalEnvironmentKey(serverCfg.EnvironmentKey) {
			return nil, nil, "", errors.New("待重试 AdsPower 环境与当前服务器出口不一致")
		}
		profile, err := adsPower.profile(ctx, pending.ProfileID)
		if err != nil {
			return nil, nil, "", errors.New("待重试 AdsPower 环境已不存在；请清理该任务后重新添加")
		}
		cfg, _ := s.runtimeSnapshot()
		managed := cfg.ManagedProfiles[profile.UserID]
		shared := managed.Shared && canonicalEnvironmentKey(managed.EnvironmentKey) == canonicalEnvironmentKey(payload.EnvironmentKey)
		if (!shared && profile.Name != sanitizeProfileName(payload.AccountName)) || !adsPowerProfileUsesTemplate(profile, templateProfile) {
			return nil, nil, "", errors.New("待重试 AdsPower 环境与当前账号或服务器代理不匹配")
		}
		if err := adsPower.enforceProfilePolicy(ctx, profile, templateProfile, pending.FingerprintSlot); err != nil {
			return nil, nil, "", fmt.Errorf("刷新待重试环境安全策略失败: %w", err)
		}
		profile, err = adsPower.profile(ctx, pending.ProfileID)
		if err != nil || !adsPowerProfileUsesTemplate(profile, templateProfile) {
			return nil, nil, "", errors.New("刷新后的待重试 AdsPower 环境没有使用当前服务器代理")
		}
		profileExitIP := strings.TrimSpace(profile.IP)
		if profileExitIP == "" {
			profileExitIP = strings.TrimSpace(profile.UserProxyConfig.LatestIP)
		}
		if templateProfile.ProxyID == "" {
			profileExitIP, err = s.verifyProxy(ctx, profile.UserProxyConfig)
			if err != nil || profileExitIP != exitIP {
				return nil, nil, "", errors.New("待重试 AdsPower 环境没有使用当前服务器出口")
			}
		}
		return profile, templateProfile, profileExitIP, nil
	}
	profile, err := adsPower.createProfileWithReceipt(ctx, payload.AccountName, templateProfile, fingerprintSlot, func(id string) error {
		if payload.configOrigin == "" {
			return errors.New("新建环境缺少服务器归属")
		}
		return s.rememberCreatedProfile(payload.configOrigin, payload, id, fingerprintSlot)
	})
	if err != nil {
		return nil, nil, "", fmt.Errorf("创建账号专属环境失败: %w", err)
	}
	profileExitIP := strings.TrimSpace(profile.IP)
	if profileExitIP == "" {
		profileExitIP = strings.TrimSpace(profile.UserProxyConfig.LatestIP)
	}
	if templateProfile.ProxyID == "" {
		profileExitIP, err = s.verifyProxy(ctx, profile.UserProxyConfig)
		if err != nil || profileExitIP != exitIP {
			return nil, nil, "", errors.New("新建 AdsPower 环境没有使用当前服务器出口")
		}
	}
	return profile, templateProfile, profileExitIP, nil
}

func adsPowerProfileUsesTemplate(profile, templateProfile *adsPowerProfile) bool {
	if profile == nil || templateProfile == nil {
		return false
	}
	if strings.TrimSpace(templateProfile.ProxyID) != "" {
		if strings.TrimSpace(profile.ProxyID) != "" && strings.TrimSpace(profile.ProxyID) != strings.TrimSpace(templateProfile.ProxyID) {
			return false
		}
		if strings.TrimSpace(profile.ProxyID) == strings.TrimSpace(templateProfile.ProxyID) {
			return true
		}
		// Some AdsPower builds omit proxyid from /user/list even though the
		// profile was created from a saved proxy. Compare the resolved SOCKS
		// endpoint as a compatibility fallback.
	}
	actual := profile.UserProxyConfig
	expected := templateProfile.UserProxyConfig
	return strings.EqualFold(strings.TrimSpace(actual.ProxyType), strings.TrimSpace(expected.ProxyType)) &&
		normalizeProxyHost(actual.ProxyHost) == normalizeProxyHost(expected.ProxyHost) &&
		strings.TrimSpace(actual.ProxyPort) == strings.TrimSpace(expected.ProxyPort) &&
		strings.TrimSpace(actual.ProxyUser) == strings.TrimSpace(expected.ProxyUser) &&
		(templateProfile.ProxyID != "" || actual.ProxyPassword == expected.ProxyPassword)
}

func (s *helperServer) pendingProfile(key string) *pendingProfileBinding {
	if key == "" {
		return nil
	}
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	binding, ok := s.cfg.PendingProfiles[key]
	if !ok {
		return nil
	}
	copy := binding
	return &copy
}

func (s *helperServer) rememberPendingProfile(key string, binding pendingProfileBinding) error {
	if key == "" || !validOpaqueID(binding.ProfileID) || binding.FingerprintSlot < 0 || binding.FingerprintSlot > 52 {
		return errors.New("待重试 AdsPower 环境无效")
	}
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	next := cloneConfig(s.cfg)
	if next.PendingProfiles == nil {
		next.PendingProfiles = make(map[string]pendingProfileBinding)
	}
	binding.EnvironmentKey = canonicalEnvironmentKey(binding.EnvironmentKey)
	next.PendingProfiles[key] = binding
	if err := saveConfig(next); err != nil {
		return fmt.Errorf("保存待重试 AdsPower 环境失败: %w", err)
	}
	s.cfg = next
	return nil
}

func (s *helperServer) allocateFingerprintSlot(origin string) (int, error) {
	s.runtimeMu.Lock()
	defer s.runtimeMu.Unlock()
	server, ok := s.cfg.Servers[origin]
	if !ok {
		return 0, errors.New("XIASS 节点尚未配置")
	}
	slot := server.NextFingerprintSlot
	if slot < 1 || slot > 52 {
		slot = 1
	}
	server.NextFingerprintSlot = slot%52 + 1
	next := cloneConfig(s.cfg)
	next.Servers[origin] = server
	if err := saveConfig(next); err != nil {
		return 0, fmt.Errorf("保存指纹槽失败: %w", err)
	}
	s.cfg = next
	return slot, nil
}

func resolveAdsPowerTemplate(ctx context.Context, adsPower *adsPowerClient, serverCfg serverConfig) (*adsPowerProfile, error) {
	if strings.TrimSpace(serverCfg.TemplateProfileID) != "" {
		return adsPower.profile(ctx, serverCfg.TemplateProfileID)
	}
	if strings.TrimSpace(serverCfg.ProxyID) != "" {
		saved, err := adsPower.savedProxy(ctx, serverCfg.ProxyID)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(strings.TrimSpace(saved.Type), "socks5") {
			return nil, errors.New("AdsPower saved proxy must use SOCKS5")
		}
		return &adsPowerProfile{
			Name:    "XIASS " + serverCfg.EnvironmentKey,
			GroupID: "0",
			ProxyID: saved.ProxyID,
			UserProxyConfig: adsPowerProxyConfig{
				ProxySoft: "other",
				ProxyType: "socks5",
				ProxyHost: saved.Host,
				ProxyPort: saved.Port,
			},
		}, nil
	}
	proxyConfig, ok := serverCfg.adsPowerProxy()
	if !ok {
		return nil, errors.New("SOCKS5 proxy is not configured")
	}
	return &adsPowerProfile{
		Name:            "XIASS " + serverCfg.EnvironmentKey,
		GroupID:         "0",
		UserProxyConfig: proxyConfig,
	}, nil
}

func (s *helperServer) profileLock(payload *launchPayload) *sync.Mutex {
	key := payload.SessionID
	if payload.Existing != nil && payload.Existing.ProfileID != "" {
		key = payload.Existing.ProfileID
	} else if identity := strings.TrimSpace(payload.LoginEmail); identity != "" {
		key = canonicalEnvironmentKey(payload.EnvironmentKey) + "\x00" + strings.ToLower(identity)
	} else if identity := strings.TrimSpace(payload.AccountName); identity != "" {
		key = canonicalEnvironmentKey(payload.EnvironmentKey) + "\x00" + strings.ToLower(identity)
	}
	value, _ := s.profileLocks.LoadOrStore(key, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (s *helperServer) redeem(ctx context.Context, origin, ticket string) (*launchPayload, error) {
	for attempt := 0; attempt < 3; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
		var result apiEnvelope[launchPayload]
		err := s.serverRequest(attemptCtx, origin, "/api/v1/tools/adspower/launch-tickets/redeem", map[string]string{"ticket": ticket}, &result)
		cancel()
		if err == nil {
			if result.Data.SessionID == "" || result.Data.BindingToken == "" || result.Data.AuthURL == "" {
				return nil, errors.New("XIASS returned an incomplete AdsPower launch task")
			}
			return &result.Data, nil
		}
		if attempt == 2 || !retryableServerNetworkError(err) {
			return nil, err
		}
		if err := sleepWithContext(ctx, time.Duration(attempt+1)*700*time.Millisecond); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("XIASS launch ticket could not be redeemed")
}

func retryableServerNetworkError(err error) bool {
	// Launch tickets are single-use. Retry only connection establishment,
	// never an ambiguous timeout after the POST might have been delivered.
	var operation *net.OpError
	return errors.As(err, &operation) && operation.Op == "dial"
}

func (s *helperServer) registerCallback(origin string, launch *launchPayload, profileID string) (string, bool, error) {
	if launch == nil || launch.CallbackToken == "" {
		return "", false, nil
	}
	profileID = strings.TrimSpace(profileID)
	if !validOpaqueID(profileID) {
		return "", false, errors.New("AdsPower 授权环境无效")
	}
	authURL, err := url.Parse(launch.AuthURL)
	if err != nil {
		return "", false, errors.New("OpenAI 授权地址无效")
	}
	state := strings.TrimSpace(authURL.Query().Get("state"))
	if !validOpaqueID(state) || !validOpaqueID(launch.CallbackToken) {
		return "", false, errors.New("OpenAI 授权回调标识无效")
	}
	expiresAt := launch.CallbackExpiresAt
	if expiresAt.IsZero() {
		expiresAt = launch.ExpiresAt
	}
	if !expiresAt.After(time.Now()) {
		return "", false, errors.New("OpenAI 授权任务已过期")
	}
	s.callbacksMu.Lock()
	s.pruneCallbacksLocked(time.Now())
	s.callbacks[state] = callbackRegistration{ServerOrigin: origin, Token: launch.CallbackToken, ProfileID: profileID, SessionID: launch.SessionID, ExpiresAt: expiresAt}
	s.callbacksMu.Unlock()
	return state, true, nil
}

func (s *helperServer) removeCallback(state string) {
	s.callbacksMu.Lock()
	delete(s.callbacks, state)
	s.callbacksMu.Unlock()
}

func (s *helperServer) callbackRegistration(state string) (callbackRegistration, bool) {
	s.callbacksMu.Lock()
	defer s.callbacksMu.Unlock()
	s.pruneCallbacksLocked(time.Now())
	registration, ok := s.callbacks[state]
	return registration, ok
}

func (s *helperServer) pruneCallbacksLocked(now time.Time) {
	for state, registration := range s.callbacks {
		if !registration.ExpiresAt.After(now) {
			delete(s.callbacks, state)
		}
	}
}

func (s *helperServer) reportCallback(ctx context.Context, registration callbackRegistration, callbackURL string) error {
	var result apiEnvelope[map[string]any]
	return s.serverRequest(ctx, registration.ServerOrigin, "/api/v1/tools/adspower/callbacks/report", map[string]string{
		"callback_token": registration.Token,
		"callback_url":   callbackURL,
	}, &result)
}

func (s *helperServer) deliverCallback(ctx context.Context, state, callbackURL string) (bool, error) {
	if !validCallback(callbackURL, state) {
		return false, errors.New("OpenAI callback URL is invalid")
	}
	// The loopback receiver and CDP observer can see the same redirect at once.
	// Keep a settled receipt until expiry so both paths acknowledge it without
	// consuming the server's one-time callback token a second time.
	s.callbackDeliveryMu.Lock()
	defer s.callbackDeliveryMu.Unlock()
	registration, ok := s.callbackRegistration(state)
	if !ok {
		return false, nil
	}
	if registration.Delivered {
		return true, nil
	}
	if err := s.reportCallback(ctx, registration, callbackURL); err != nil {
		return false, err
	}
	s.callbacksMu.Lock()
	s.callbacks[state] = callbackRegistration{ExpiresAt: registration.ExpiresAt, Delivered: true}
	s.callbacksMu.Unlock()
	s.stopProfileAfterCallback(registration.ProfileID, registration.SessionID)
	return true, nil
}

func (s *helperServer) reportProgress(ctx context.Context, origin string, launch *launchPayload, status, stage, reason string) error {
	if launch == nil || strings.TrimSpace(launch.CallbackToken) == "" {
		return nil
	}
	var result apiEnvelope[map[string]any]
	return s.serverRequest(ctx, origin, "/api/v1/tools/adspower/progress/report", map[string]string{
		"callback_token": launch.CallbackToken,
		"status":         status,
		"stage":          stage,
		"reason":         reason,
	}, &result)
}

func (s *helperServer) smsAction(ctx context.Context, origin string, launch *launchPayload, action string) (*adsPowerSMSActionResult, error) {
	if launch == nil || strings.TrimSpace(launch.CallbackToken) == "" {
		return nil, errors.New("AdsPower SMS task is unavailable")
	}
	var result apiEnvelope[adsPowerSMSActionResult]
	if err := s.serverRequest(ctx, origin, "/api/v1/tools/adspower/sms/action", map[string]string{
		"callback_token": launch.CallbackToken,
		"action":         action,
	}, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

func (s *helperServer) stopProfileAfterCallback(profileID, sessionID string) {
	if !validOpaqueID(profileID) {
		return
	}
	go func() {
		if s.closeDelay > 0 {
			time.Sleep(s.closeDelay)
		}
		s.poolMu.Lock()
		defer s.poolMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := s.stopOwnedProfile(ctx, profileID, sessionID); err != nil {
			log.Printf("stop AdsPower profile after OAuth callback: %v", err)
		}
	}()
}

func (s *helperServer) reportBinding(ctx context.Context, origin string, launch *launchPayload, profile, templateProfile *adsPowerProfile, exitIP, deviceID string, fingerprintSlot int) error {
	payload := map[string]any{
		"binding_token":          launch.BindingToken,
		"device_id":              deviceID,
		"profile_id":             profile.UserID,
		"profile_no":             profile.SerialNumber,
		"profile_name":           profile.Name,
		"environment_key":        launch.EnvironmentKey,
		"proxy_type":             strings.ToLower(templateProfile.UserProxyConfig.ProxyType),
		"proxy_host":             strings.ToLower(templateProfile.UserProxyConfig.ProxyHost),
		"proxy_port":             templateProfile.UserProxyConfig.ProxyPort,
		"proxy_exit_ip":          exitIP,
		"webrtc_disabled":        true,
		"fingerprint_randomized": true,
		"fingerprint_slot":       fingerprintSlot,
		"shared_profile":         launch.isolatedContext,
	}
	var result apiEnvelope[map[string]any]
	return s.serverRequest(ctx, origin, "/api/v1/tools/adspower/bindings/report", payload, &result)
}

func (s *helperServer) serverRequest(ctx context.Context, origin, path string, body any, target any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+path, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "XIASS-AdsPower-Helper/1")
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, 1<<20)
	if err := json.NewDecoder(limited).Decode(target); err != nil {
		return fmt.Errorf("decode XIASS response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("XIASS returned status %d", response.StatusCode)
	}
	return nil
}

func (s *helperServer) callback(w http.ResponseWriter, r *http.Request) {
	fullURL := "http://localhost:1455" + r.URL.RequestURI()
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	view := callbackView{Title: "授权回调已收到", Message: "未找到对应的 XIASS 任务，请复制完整地址后手动粘贴。", URL: fullURL}
	if _, ok := s.callbackRegistration(state); ok {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		delivered, err := s.deliverCallback(ctx, state, fullURL)
		cancel()
		if err == nil && delivered {
			view.Success = true
			view.Title = "授权已返回 XIASS"
			view.Message = "XIASS 会继续核验并保存账号，固定指纹浏览器将自动关闭。"
		} else {
			view.Title = "自动返回失败"
			view.Message = "XIASS 暂时没有接收成功，请保留本页并复制完整地址。"
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = callbackTemplate.Execute(w, view)
}

func (s *helperServer) renderLaunch(w http.ResponseWriter, status int, view launchView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := launchTemplate.Execute(w, view); err != nil {
		log.Printf("render launch page: %v", err)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

var launchTemplate = template.Must(template.New("launch").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style>body{margin:0;background:#071b26;color:#e9f7ff;font:15px system-ui,sans-serif;display:grid;min-height:100vh;place-items:center}.panel{width:min(560px,calc(100% - 32px));border:1px solid #1d5369;background:#082330;padding:28px;border-radius:8px;box-sizing:border-box}h1{font-size:22px;margin:0 0 12px;color:{{if .Success}}#4ade80{{else}}#fb7185{{end}}}p{color:#a8c3cf;line-height:1.7}.meta{display:grid;gap:8px;margin-top:18px}.meta div{background:#061923;border:1px solid #173e50;padding:10px 12px;border-radius:6px}.meta b{color:#67d9ff;margin-right:8px}</style></head><body><main class="panel"><h1>{{.Title}}</h1><p>{{.Message}}</p>{{if .ProfileName}}<div class="meta"><div><b>环境</b>{{.ProfileName}}</div><div><b>出口</b>{{.EnvironmentKey}} · {{.ExitIP}}</div><div><b>隐私</b>随机指纹 · WebRTC 已关闭</div></div>{{end}}</main></body></html>`))

var callbackTemplate = template.Must(template.New("callback").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style>body{margin:0;background:#071b26;color:#e9f7ff;font:15px system-ui,sans-serif;display:grid;min-height:100vh;place-items:center}.panel{width:min(760px,calc(100% - 32px));border:1px solid #1d5369;background:#082330;padding:28px;border-radius:8px;box-sizing:border-box}h1{font-size:22px;margin:0 0 10px;color:{{if .Success}}#4ade80{{else}}#fb7185{{end}}}p{color:#a8c3cf}textarea{width:100%;box-sizing:border-box;min-height:120px;padding:12px;background:#061923;color:#dff7ff;border:1px solid #27556a;border-radius:6px;overflow-wrap:anywhere}button{margin-top:12px;border:0;border-radius:6px;background:#0ea5e9;color:white;padding:10px 16px;font-weight:650;cursor:pointer}</style></head><body><main class="panel"><h1>{{.Title}}</h1><p>{{.Message}}</p><textarea id="callback" readonly>{{.URL}}</textarea><button onclick="navigator.clipboard.writeText(document.getElementById('callback').value).then(()=>this.textContent='已复制')">复制完整回调地址</button></main></body></html>`))

func runServers(ctx context.Context, cfg *config) error {
	helper := newHelperServer(cfg)
	mainServer := &http.Server{Addr: cfg.ListenAddress, Handler: helper.routes(), ReadHeaderTimeout: 5 * time.Second}
	callbackServer := &http.Server{Addr: cfg.CallbackAddress, Handler: helper.callbackRoutes(), ReadHeaderTimeout: 5 * time.Second}
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- mainServer.ListenAndServe() }()
	go func() { errorsCh <- callbackServer.ListenAndServe() }()
	go helper.runRemoteWorker(ctx)
	go helper.runProfileMaintenance(ctx)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = mainServer.Shutdown(shutdownCtx)
		_ = callbackServer.Shutdown(shutdownCtx)
		return nil
	case err := <-errorsCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func launchURLFor(origin, ticket string) string {
	query := make(url.Values)
	query.Set("server", origin)
	query.Set("ticket", ticket)
	return "/launch?" + query.Encode()
}
