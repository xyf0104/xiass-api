package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type helperServer struct {
	cfg          *config
	adsPower     *adsPowerClient
	runtimeMu    sync.RWMutex
	client       *http.Client
	verifyProxy  func(context.Context, adsPowerProxyConfig) (string, error)
	profileLocks sync.Map
	callbacksMu  sync.Mutex
	callbacks    map[string]callbackRegistration
	closeDelay   time.Duration
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
	ExpiresAt    time.Time
}

type existingBinding struct {
	DeviceID       string `json:"device_id"`
	ProfileID      string `json:"profile_id"`
	EnvironmentKey string `json:"environment_key"`
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

func newHelperServer(cfg *config) *helperServer {
	return &helperServer{
		cfg:         cfg,
		adsPower:    newAdsPowerClient(cfg),
		client:      &http.Client{Timeout: 20 * time.Second},
		verifyProxy: verifyProxyExitIP,
		callbacks:   make(map[string]callbackRegistration),
		closeDelay:  1500 * time.Millisecond,
	}
}

func (s *helperServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /setup", s.setupPage)
	mux.HandleFunc("GET /api/setup", s.setupState)
	mux.HandleFunc("POST /api/setup", s.saveSetup)
	mux.HandleFunc("GET /launch", s.launch)
	return loopbackOnly(securityHeaders(mux))
}

func (s *helperServer) callbackRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/callback", s.callback)
	return loopbackOnly(securityHeaders(mux))
}

func (s *helperServer) health(w http.ResponseWriter, r *http.Request) {
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

func (s *helperServer) launch(w http.ResponseWriter, r *http.Request) {
	cfg, adsPower := s.runtimeSnapshot()
	serverOrigin, err := normalizeServerOrigin(r.URL.Query().Get("server"))
	if err != nil {
		s.renderLaunch(w, http.StatusBadRequest, launchView{Title: "XIASS 地址无效", Message: err.Error()})
		return
	}
	serverCfg, ok := cfg.Servers[serverOrigin]
	if !ok {
		s.renderLaunch(w, http.StatusForbidden, launchView{Title: "未授权的 XIASS 服务器", Message: "请先在本机助手配置中登记该服务器。"})
		return
	}
	ticket := strings.TrimSpace(r.URL.Query().Get("ticket"))
	if !validOpaqueID(ticket) {
		s.renderLaunch(w, http.StatusBadRequest, launchView{Title: "启动票据无效", Message: "请从 XIASS 管理页面重新点击固定指纹浏览器。"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	payload, err := s.redeem(ctx, serverOrigin, ticket)
	if err != nil {
		s.renderLaunch(w, http.StatusBadGateway, launchView{Title: "无法读取启动任务", Message: err.Error()})
		return
	}
	if payload.EnvironmentKey != serverCfg.EnvironmentKey {
		s.renderLaunch(w, http.StatusConflict, launchView{Title: "服务器出口不匹配", Message: "当前账号不属于这个 AdsPower 出口环境。"})
		return
	}

	lock := s.profileLock(payload)
	lock.Lock()
	defer lock.Unlock()
	profile, templateProfile, exitIP, err := s.prepareProfile(ctx, payload, serverCfg, cfg.DeviceID, adsPower)
	if err != nil {
		s.renderLaunch(w, http.StatusConflict, launchView{Title: "指纹环境未启动", Message: err.Error(), EnvironmentKey: payload.EnvironmentKey})
		return
	}
	if err := s.reportBinding(ctx, serverOrigin, payload, profile, templateProfile, exitIP, cfg.DeviceID); err != nil {
		s.renderLaunch(w, http.StatusBadGateway, launchView{Title: "环境绑定未保存", Message: err.Error(), ProfileName: profile.Name, EnvironmentKey: payload.EnvironmentKey, ExitIP: exitIP})
		return
	}
	state, registered, err := s.registerCallback(serverOrigin, payload, profile.UserID)
	if err != nil {
		s.renderLaunch(w, http.StatusConflict, launchView{Title: "授权回调无法绑定", Message: err.Error(), ProfileName: profile.Name, EnvironmentKey: payload.EnvironmentKey, ExitIP: exitIP})
		return
	}
	browserSession, err := adsPower.startProfile(ctx, profile.UserID)
	if err != nil {
		if registered {
			s.removeCallback(state)
		}
		s.renderLaunch(w, http.StatusBadGateway, launchView{Title: "AdsPower 启动失败", Message: err.Error(), ProfileName: profile.Name, EnvironmentKey: payload.EnvironmentKey, ExitIP: exitIP})
		return
	}
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

func (s *helperServer) prepareProfile(ctx context.Context, payload *launchPayload, serverCfg serverConfig, deviceID string, adsPower *adsPowerClient) (*adsPowerProfile, *adsPowerProfile, string, error) {
	templateProfile, err := resolveAdsPowerTemplate(ctx, adsPower, serverCfg)
	if err != nil {
		return nil, nil, "", fmt.Errorf("读取 %s 出口模板失败: %w", serverCfg.EnvironmentKey, err)
	}
	exitIP, err := s.verifyProxy(ctx, templateProfile.UserProxyConfig)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%s 出口代理不可用: %w", serverCfg.EnvironmentKey, err)
	}
	if payload.Existing != nil {
		if payload.Existing.DeviceID != deviceID {
			return nil, nil, "", errors.New("该账号已绑定到另一台设备，必须先在 XIASS 中解除绑定")
		}
		if payload.Existing.EnvironmentKey != serverCfg.EnvironmentKey {
			return nil, nil, "", errors.New("该账号绑定的服务器出口与当前页面不一致")
		}
		profile, err := adsPower.profile(ctx, payload.Existing.ProfileID)
		if err != nil {
			return nil, nil, "", errors.New("账号原有 AdsPower 环境已不存在；请先解除绑定后再创建新环境")
		}
		if err := adsPower.enforceProfilePolicy(ctx, profile, templateProfile); err != nil {
			return nil, nil, "", fmt.Errorf("刷新既有环境安全策略失败: %w", err)
		}
		profile, err = adsPower.profile(ctx, payload.Existing.ProfileID)
		if err != nil {
			return nil, nil, "", errors.New("刷新后的 AdsPower 环境无法读取")
		}
		profileExitIP, err := s.verifyProxy(ctx, profile.UserProxyConfig)
		if err != nil || profileExitIP != exitIP {
			return nil, nil, "", errors.New("账号固定环境没有使用当前服务器出口")
		}
		return profile, templateProfile, profileExitIP, nil
	}
	profile, err := adsPower.createProfile(ctx, payload.AccountName, templateProfile)
	if err != nil {
		return nil, nil, "", fmt.Errorf("创建账号专属环境失败: %w", err)
	}
	profileExitIP, err := s.verifyProxy(ctx, profile.UserProxyConfig)
	if err != nil || profileExitIP != exitIP {
		return nil, nil, "", errors.New("新建 AdsPower 环境没有使用当前服务器出口")
	}
	return profile, templateProfile, profileExitIP, nil
}

func resolveAdsPowerTemplate(ctx context.Context, adsPower *adsPowerClient, serverCfg serverConfig) (*adsPowerProfile, error) {
	if strings.TrimSpace(serverCfg.TemplateProfileID) != "" {
		return adsPower.profile(ctx, serverCfg.TemplateProfileID)
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
	}
	value, _ := s.profileLocks.LoadOrStore(key, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (s *helperServer) redeem(ctx context.Context, origin, ticket string) (*launchPayload, error) {
	var result apiEnvelope[launchPayload]
	if err := s.serverRequest(ctx, origin, "/api/v1/tools/adspower/launch-tickets/redeem", map[string]string{"ticket": ticket}, &result); err != nil {
		return nil, err
	}
	if result.Data.SessionID == "" || result.Data.BindingToken == "" || result.Data.AuthURL == "" {
		return nil, errors.New("XIASS returned an incomplete AdsPower launch task")
	}
	return &result.Data, nil
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
	s.callbacks[state] = callbackRegistration{ServerOrigin: origin, Token: launch.CallbackToken, ProfileID: profileID, ExpiresAt: expiresAt}
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

func (s *helperServer) stopProfileAfterCallback(profileID string) {
	if !validOpaqueID(profileID) {
		return
	}
	go func() {
		if s.closeDelay > 0 {
			time.Sleep(s.closeDelay)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, adsPower := s.runtimeSnapshot()
		if err := adsPower.stopProfile(ctx, profileID); err != nil {
			log.Printf("stop AdsPower profile after OAuth callback: %v", err)
		}
	}()
}

func (s *helperServer) reportBinding(ctx context.Context, origin string, launch *launchPayload, profile, templateProfile *adsPowerProfile, exitIP, deviceID string) error {
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
	if registration, ok := s.callbackRegistration(state); ok {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		err := s.reportCallback(ctx, registration, fullURL)
		cancel()
		if err == nil {
			s.removeCallback(state)
			s.stopProfileAfterCallback(registration.ProfileID)
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
