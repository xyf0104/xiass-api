package main

import (
	"context"
	"encoding/json"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"
)

type setupPageView struct {
	ServerOrigin   string
	EnvironmentKey string
}

type setupStateResponse struct {
	AdsPowerBaseURL         string `json:"adspower_base_url"`
	APIKeyConfigured        bool   `json:"api_key_configured"`
	ServerOrigin            string `json:"server_origin"`
	EnvironmentKey          string `json:"environment_key"`
	ProxyHost               string `json:"proxy_host"`
	ProxyPort               string `json:"proxy_port"`
	ProxyUser               string `json:"proxy_user"`
	ProxyPasswordConfigured bool   `json:"proxy_password_configured"`
	Configured              bool   `json:"configured"`
}

type setupSaveRequest struct {
	AdsPowerBaseURL string `json:"adspower_base_url"`
	APIKey          string `json:"api_key"`
	ServerOrigin    string `json:"server_origin"`
	EnvironmentKey  string `json:"environment_key"`
	ProxyHost       string `json:"proxy_host"`
	ProxyPort       string `json:"proxy_port"`
	ProxyUser       string `json:"proxy_user"`
	ProxyPassword   string `json:"proxy_password"`
}

type setupSaveResponse struct {
	Saved      bool   `json:"saved"`
	ExitIP     string `json:"exit_ip"`
	ConfigPath string `json:"config_path"`
}

func (s *helperServer) runtimeSnapshot() (*config, *adsPowerClient) {
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	return cloneConfig(s.cfg), s.adsPower
}

func (s *helperServer) replaceRuntime(cfg *config) {
	s.runtimeMu.Lock()
	s.cfg = cfg
	s.adsPower = newAdsPowerClient(cfg)
	s.runtimeMu.Unlock()
}

func cloneConfig(source *config) *config {
	if source == nil {
		return &config{Servers: make(map[string]serverConfig)}
	}
	cloned := *source
	cloned.Servers = make(map[string]serverConfig, len(source.Servers))
	for origin, server := range source.Servers {
		cloned.Servers[origin] = server
	}
	return &cloned
}

func (s *helperServer) setupPage(w http.ResponseWriter, r *http.Request) {
	serverOrigin := strings.TrimSpace(r.URL.Query().Get("server"))
	if normalized, err := normalizeServerOrigin(serverOrigin); err == nil {
		serverOrigin = normalized
	}
	environmentKey := strings.TrimSpace(r.URL.Query().Get("environment"))
	if !validOpaqueID(environmentKey) {
		environmentKey = "api"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = setupTemplate.Execute(w, setupPageView{ServerOrigin: serverOrigin, EnvironmentKey: environmentKey})
}

func (s *helperServer) setupState(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.runtimeSnapshot()
	serverOrigin, _ := normalizeServerOrigin(r.URL.Query().Get("server"))
	server := cfg.Servers[serverOrigin]
	writeSetupJSON(w, http.StatusOK, setupStateResponse{
		AdsPowerBaseURL:         cfg.AdsPowerBaseURL,
		APIKeyConfigured:        strings.TrimSpace(cfg.APIKey) != "",
		ServerOrigin:            serverOrigin,
		EnvironmentKey:          server.EnvironmentKey,
		ProxyHost:               server.ProxyHost,
		ProxyPort:               server.ProxyPort,
		ProxyUser:               server.ProxyUser,
		ProxyPasswordConfigured: server.ProxyPassword != "",
		Configured:              server.validDirectProxy() || server.TemplateProfileID != "",
	})
}

func (s *helperServer) saveSetup(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var request setupSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeSetupError(w, http.StatusBadRequest, "设置内容无效")
		return
	}
	serverOrigin, err := normalizeServerOrigin(request.ServerOrigin)
	if err != nil {
		writeSetupError(w, http.StatusBadRequest, "XIASS 地址无效")
		return
	}
	request.EnvironmentKey = strings.TrimSpace(request.EnvironmentKey)
	if !validOpaqueID(request.EnvironmentKey) {
		writeSetupError(w, http.StatusBadRequest, "节点名称只能使用字母、数字、点、横线或下划线")
		return
	}

	current, _ := s.runtimeSnapshot()
	next := cloneConfig(current)
	if value := strings.TrimSpace(request.AdsPowerBaseURL); value != "" {
		next.AdsPowerBaseURL = value
	}
	if value := strings.TrimSpace(request.APIKey); value != "" {
		next.APIKey = value
	}
	existing := next.Servers[serverOrigin]
	proxyPassword := strings.TrimSpace(request.ProxyPassword)
	if proxyPassword == "" {
		proxyPassword = existing.ProxyPassword
	}
	server := serverConfig{
		EnvironmentKey: request.EnvironmentKey,
		ProxyHost:      request.ProxyHost,
		ProxyPort:      request.ProxyPort,
		ProxyUser:      request.ProxyUser,
		ProxyPassword:  proxyPassword,
	}
	next.Servers[serverOrigin] = server
	if _, err := next.normalize(); err != nil {
		writeSetupError(w, http.StatusBadRequest, err.Error())
		return
	}
	server = next.Servers[serverOrigin]
	proxyConfig, ok := server.adsPowerProxy()
	if !ok {
		writeSetupError(w, http.StatusBadRequest, "请填写有效的 SOCKS5 主机和端口")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	testClient := newAdsPowerClient(next)
	if err := testClient.status(ctx); err != nil {
		writeSetupError(w, http.StatusBadGateway, "无法连接 AdsPower Local API，请确认 AdsPower 已启动且 API Key 正确")
		return
	}
	exitIP, err := s.verifyProxy(ctx, proxyConfig)
	if err != nil {
		writeSetupError(w, http.StatusBadGateway, "SOCKS5 节点检测失败："+err.Error())
		return
	}
	if err := saveConfig(next); err != nil {
		writeSetupError(w, http.StatusInternalServerError, "本机配置保存失败")
		return
	}
	s.replaceRuntime(next)
	writeSetupJSON(w, http.StatusOK, setupSaveResponse{Saved: true, ExitIP: exitIP, ConfigPath: next.path})
}

func writeSetupError(w http.ResponseWriter, status int, message string) {
	writeSetupJSON(w, status, map[string]any{"saved": false, "error": message})
}

func writeSetupJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if parsedHost, _, err := net.SplitHostPort(r.Host); err == nil {
			host = parsedHost
		}
		if !loopbackHost(host) {
			http.Error(w, "loopback access required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

var setupTemplate = template.Must(template.New("setup").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>XIASS AdsPower 助手设置</title>
<style>
*{box-sizing:border-box}body{margin:0;background:#061722;color:#e8f5fb;font:15px system-ui,-apple-system,sans-serif;min-height:100vh;padding:28px}.page{width:min(820px,100%);margin:auto}.heading{margin-bottom:20px}.heading h1{font-size:24px;margin:0 0 8px}.heading p,.hint{color:#91adba;line-height:1.7}.panel{border:1px solid #1d5369;background:#082330;padding:22px;border-radius:8px}.grid{display:grid;grid-template-columns:1fr 1fr;gap:14px}.full{grid-column:1/-1}label{display:grid;gap:7px;color:#bad3de;font-weight:650}input{width:100%;border:1px solid #27556a;background:#061923;color:#e8f5fb;border-radius:6px;padding:11px 12px;font:inherit}input:focus{outline:2px solid #0ea5e9;outline-offset:1px}.actions{display:flex;align-items:center;gap:12px;margin-top:18px;flex-wrap:wrap}button{border:0;border-radius:6px;background:#0ea5e9;color:white;padding:11px 17px;font-weight:750;cursor:pointer}button:disabled{opacity:.55;cursor:wait}.status{min-height:22px;color:#91adba}.status.ok{color:#4ade80}.status.error{color:#fb7185}.security{margin-top:18px;padding-top:16px;border-top:1px solid #173e50;color:#91adba;font-size:13px;line-height:1.7}@media(max-width:640px){body{padding:14px}.panel{padding:16px}.grid{grid-template-columns:1fr}.full{grid-column:auto}}
</style></head><body><main class="page"><header class="heading"><h1>XIASS AdsPower 助手设置</h1><p>所有密钥与 SOCKS5 凭据只保存在这台电脑。保存前会检测 AdsPower 和真实出口 IP。</p></header><section class="panel"><form id="setup"><div class="grid">
<label class="full">AdsPower Local API 地址<input id="base" value="http://local.adspower.net:50325" autocomplete="off"></label>
<label class="full">AdsPower API Key<input id="key" type="password" autocomplete="off" placeholder="未启用安全校验可留空；已保存时留空不修改"></label>
<label class="full">XIASS 网站地址<input id="server" value="{{.ServerOrigin}}" autocomplete="off"></label>
<label>节点名称<input id="environment" value="{{.EnvironmentKey}}" autocomplete="off"></label>
<label>SOCKS5 主机<input id="proxyHost" placeholder="例如 api.example.com" autocomplete="off"></label>
<label>SOCKS5 端口<input id="proxyPort" inputmode="numeric" placeholder="例如 1104" autocomplete="off"></label>
<label>SOCKS5 账号<input id="proxyUser" autocomplete="off"></label>
<label>SOCKS5 密码<input id="proxyPassword" type="password" autocomplete="off" placeholder="已保存时留空不修改"></label>
</div><div class="actions"><button id="save" type="submit">检测并保存</button><span id="status" class="status"></span></div></form><p class="security">保存成功后即可回到 XIASS 工作台选择“Ads 指纹浏览器”。新账号会自动创建独立随机 Mac 指纹环境并关闭 WebRTC；同一账号后续授权继续使用原环境。</p></section></main>
<script>
const form=document.getElementById('setup'),statusEl=document.getElementById('status'),save=document.getElementById('save');
const fields={base:'adspower_base_url',key:'api_key',server:'server_origin',environment:'environment_key',proxyHost:'proxy_host',proxyPort:'proxy_port',proxyUser:'proxy_user',proxyPassword:'proxy_password'};
const value=id=>document.getElementById(id).value.trim();
async function load(){try{const server=value('server');const response=await fetch('/api/setup?server='+encodeURIComponent(server));const data=await response.json();if(data.adspower_base_url)document.getElementById('base').value=data.adspower_base_url;if(data.environment_key)document.getElementById('environment').value=data.environment_key;if(data.proxy_host)document.getElementById('proxyHost').value=data.proxy_host;if(data.proxy_port)document.getElementById('proxyPort').value=data.proxy_port;if(data.proxy_user)document.getElementById('proxyUser').value=data.proxy_user;if(data.api_key_configured)document.getElementById('key').placeholder='API Key 已保存，留空不修改';if(data.proxy_password_configured)document.getElementById('proxyPassword').placeholder='SOCKS5 密码已保存，留空不修改';}catch{statusEl.textContent='尚未读取到本机配置';}}
form.addEventListener('submit',async event=>{event.preventDefault();save.disabled=true;statusEl.className='status';statusEl.textContent='正在检测 AdsPower 和 SOCKS5 出口…';const payload={};for(const [id,key] of Object.entries(fields))payload[key]=value(id);try{const response=await fetch('/api/setup',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});const data=await response.json();if(!response.ok)throw new Error(data.error||'保存失败');statusEl.className='status ok';statusEl.textContent='已保存，出口 IP：'+data.exit_ip;document.getElementById('key').value='';document.getElementById('proxyPassword').value='';}catch(error){statusEl.className='status error';statusEl.textContent=error.message||'保存失败';}finally{save.disabled=false;}});load();
</script></body></html>`))
