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
	"time"
)

type helperPairingResponse struct {
	DeviceSecret   string `json:"device_secret"`
	EnvironmentKey string `json:"environment_key"`
}

type helperCommandResponse struct {
	Ticket string `json:"ticket"`
}

type helperPairView struct {
	Success        bool
	Title          string
	Message        string
	ServerOrigin   string
	EnvironmentKey string
}

func (s *helperServer) pair(w http.ResponseWriter, r *http.Request) {
	serverOrigin, err := normalizeServerOrigin(r.URL.Query().Get("server"))
	if err != nil {
		s.renderPair(w, http.StatusBadRequest, helperPairView{Title: "XIASS 地址无效", Message: err.Error()})
		return
	}
	environmentKey := strings.TrimSpace(r.URL.Query().Get("environment"))
	ticket := strings.TrimSpace(r.URL.Query().Get("ticket"))
	if !validOpaqueID(environmentKey) || !validOpaqueID(ticket) {
		s.renderPair(w, http.StatusBadRequest, helperPairView{Title: "配对票据无效", Message: "请从 XIASS 工作台重新发起助手配对。"})
		return
	}
	cfg, _ := s.runtimeSnapshot()
	server, ok := cfg.Servers[serverOrigin]
	if !ok || server.EnvironmentKey != environmentKey {
		s.renderPair(w, http.StatusConflict, helperPairView{Title: "节点尚未配置", Message: "请先在本机设置页保存这个 XIASS 节点和 AdsPower 代理。", ServerOrigin: serverOrigin, EnvironmentKey: environmentKey})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var result apiEnvelope[helperPairingResponse]
	if err := s.serverRequest(ctx, serverOrigin, "/api/v1/tools/adspower/helpers/pairings/redeem", map[string]string{
		"ticket": ticket, "device_id": cfg.DeviceID,
	}, &result); err != nil {
		s.renderPair(w, http.StatusBadGateway, helperPairView{Title: "常驻助手配对失败", Message: err.Error(), ServerOrigin: serverOrigin, EnvironmentKey: environmentKey})
		return
	}
	if result.Data.DeviceSecret == "" || result.Data.EnvironmentKey != environmentKey {
		s.renderPair(w, http.StatusBadGateway, helperPairView{Title: "常驻助手配对失败", Message: "XIASS 没有返回有效的设备凭据。", ServerOrigin: serverOrigin, EnvironmentKey: environmentKey})
		return
	}
	next := cloneConfig(cfg)
	server.DeviceSecret = result.Data.DeviceSecret
	next.Servers[serverOrigin] = server
	if err := saveConfig(next); err != nil {
		s.renderPair(w, http.StatusInternalServerError, helperPairView{Title: "设备凭据保存失败", Message: err.Error(), ServerOrigin: serverOrigin, EnvironmentKey: environmentKey})
		return
	}
	s.replaceRuntime(next)
	s.renderPair(w, http.StatusOK, helperPairView{
		Success: true, Title: "XIASS 常驻助手已配对", Message: "以后可在手机或其他电脑打开 XIASS 工作台，一键把 Ads 授权任务发送到本机。",
		ServerOrigin: serverOrigin, EnvironmentKey: environmentKey,
	})
}

func (s *helperServer) renderPair(w http.ResponseWriter, status int, view helperPairView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = helperPairTemplate.Execute(w, view)
}

func (s *helperServer) runRemoteWorker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
pollLoop:
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg, _ := s.runtimeSnapshot()
			for origin, server := range cfg.Servers {
				if strings.TrimSpace(server.DeviceSecret) == "" {
					continue
				}
				select {
				case s.remoteSlots <- struct{}{}:
					go s.pollRemoteCommand(ctx, cfg, origin, server)
				default:
					continue pollLoop
				}
			}
		}
	}
}

func (s *helperServer) pollRemoteCommand(parent context.Context, cfg *config, origin string, server serverConfig) {
	defer func() { <-s.remoteSlots }()
	pollCtx, cancelPoll := context.WithTimeout(parent, 12*time.Second)
	requestBody := map[string]string{"device_id": cfg.DeviceID, "environment_key": server.EnvironmentKey}
	var result apiEnvelope[helperCommandResponse]
	err := s.serverRequestWithBearer(pollCtx, origin, "/api/v1/tools/adspower/helpers/commands/next", server.DeviceSecret, requestBody, &result)
	cancelPoll()
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.Printf("AdsPower helper poll %s failed: %v", server.EnvironmentKey, err)
		}
		return
	}
	ticket := strings.TrimSpace(result.Data.Ticket)
	if ticket == "" {
		return
	}
	localURL, err := remoteLaunchURL(cfg.ListenAddress, origin, ticket)
	if err != nil {
		log.Printf("AdsPower helper command %s has an invalid local address: %v", server.EnvironmentKey, err)
		return
	}
	launchCtx, cancelLaunch := context.WithTimeout(parent, 20*time.Minute)
	defer cancelLaunch()
	request, err := http.NewRequestWithContext(launchCtx, http.MethodGet, localURL, nil)
	if err != nil {
		log.Printf("AdsPower helper command %s could not start: %v", server.EnvironmentKey, err)
		return
	}
	response, err := s.client.Do(request)
	if err != nil {
		log.Printf("AdsPower helper command %s failed: %v", server.EnvironmentKey, err)
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		log.Printf("AdsPower helper command %s returned HTTP %d", server.EnvironmentKey, response.StatusCode)
	}
}

func (s *helperServer) serverRequestWithBearer(ctx context.Context, origin, path, secret string, body any, target any) error {
	payload, err := jsonBody(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(origin, "/")+path, payload)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return decodeServerResponse(response, target)
}

func jsonBody(body any) (io.Reader, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return strings.NewReader(string(payload)), nil
}

func decodeServerResponse(response *http.Response, target any) error {
	limited := io.LimitReader(response.Body, 2<<20)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(limited)
		return fmt.Errorf("XIASS returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	if err := json.NewDecoder(limited).Decode(target); err != nil {
		return fmt.Errorf("decode XIASS response: %w", err)
	}
	return nil
}

var helperPairTemplate = template.Must(template.New("pair").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title><style>body{margin:0;background:#071b26;color:#e9f7ff;font:15px system-ui,sans-serif;display:grid;min-height:100vh;place-items:center}.panel{width:min(620px,calc(100% - 32px));border:1px solid #1d5369;background:#082330;padding:28px;border-radius:8px;box-sizing:border-box}h1{font-size:22px;margin:0 0 12px;color:{{if .Success}}#4ade80{{else}}#fb7185{{end}}}p{color:#a8c3cf;line-height:1.7}.meta{margin-top:18px;border:1px solid #173e50;background:#061923;padding:12px;border-radius:6px;word-break:break-all}.meta b{color:#67d9ff;margin-right:8px}</style></head><body><main class="panel"><h1>{{.Title}}</h1><p>{{.Message}}</p>{{if .ServerOrigin}}<div class="meta"><div><b>服务器</b>{{.ServerOrigin}}</div><div><b>节点</b>{{.EnvironmentKey}}</div></div>{{end}}</main></body></html>`))

func remoteLaunchURL(listenAddress, origin, ticket string) (string, error) {
	base, err := url.Parse("http://" + listenAddress)
	if err != nil {
		return "", err
	}
	base.Path = "/launch"
	base.RawQuery = strings.TrimPrefix(launchURLFor(origin, ticket), "/launch?")
	return base.String(), nil
}
