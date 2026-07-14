package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	softRouterFRPServiceName     = "xiass-frps-soft-router"
	softRouterFRPConfigPath      = "/etc/xiass-frps-soft-router/frps.toml"
	softRouterFRPBinaryPath      = "/usr/local/bin/xiass-frps-soft-router"
	softRouterDockerSocketPath   = "/var/run/docker.sock"
	softRouterFRPHelperImage     = "alpine:3.20"
	softRouterFRPInstallMethod   = "docker_host_helper"
	softRouterFRPInstallTimeout  = 4 * time.Minute
	softRouterDockerAPIVersion   = "v1.40"
	softRouterDefaultDeployDir   = "/opt/xiass-api/deploy"
	softRouterFRPInstallLogLimit = 32 * 1024
)

type DockerSoftRouterFRPInstaller struct {
	socketPath string
	image      string
}

func NewDockerSoftRouterFRPInstaller() *DockerSoftRouterFRPInstaller {
	return &DockerSoftRouterFRPInstaller{
		socketPath: softRouterDockerSocketPath,
		image:      softRouterFRPHelperImage,
	}
}

func (i *DockerSoftRouterFRPInstaller) Status(ctx context.Context, cfg SoftRouterProxyConfig) SoftRouterFRPStatus {
	normalized, _ := normalizeSoftRouterConfig(&cfg)
	if normalized != nil {
		cfg = *normalized
	}
	status := SoftRouterFRPStatus{
		ServiceName:         softRouterFRPServiceName,
		ConfigPath:          softRouterFRPConfigPath,
		InstallMethod:       softRouterFRPInstallMethod,
		ControlHost:         firstNonEmptyString(cfg.UpstreamHost, cfg.FRPServerHost, cfg.PublicHost, "host.docker.internal"),
		ControlPort:         cfg.FRPServerPort,
		RawPortRange:        formatPortRange(cfg.RawPortStart, cfg.RawPortEnd),
		PublicPortRange:     formatPortRange(cfg.PublicPortStart, cfg.PublicPortEnd),
		DeployedRawRange:    strings.TrimSpace(os.Getenv(softRouterRawPortRangeEnv)),
		DeployedPublicRange: strings.TrimSpace(os.Getenv(softRouterPublicPortRangeEnv)),
	}
	if i == nil {
		status.Reason = "installer unavailable"
		return status
	}
	status.ControlPortOpen = tcpPortOpen(status.ControlHost, status.ControlPort)
	status.RawRangeDeployed = deploymentRangeCoversSilently(softRouterRawPortRangeEnv, cfg.RawPortStart, cfg.RawPortEnd)
	status.PublicRangeDeployed = deploymentRangeCoversSilently(softRouterPublicPortRangeEnv, cfg.PublicPortStart, cfg.PublicPortEnd)
	status.NeedsRestart = !status.RawRangeDeployed || !status.PublicRangeDeployed
	status.Installed = status.ControlPortOpen && status.RawRangeDeployed
	if _, err := os.Stat(i.socketPath); err == nil {
		status.DockerSocketAvailable = true
	} else {
		status.Reason = "docker socket is not mounted"
		return status
	}
	if err := i.pingDocker(ctx); err == nil {
		status.DockerAvailable = true
		status.InstallSupported = true
	} else {
		status.Reason = "docker daemon is unavailable"
	}
	if status.Reason == "" {
		switch {
		case !status.ControlPortOpen:
			status.Reason = "frps control port is not reachable"
		case status.NeedsRestart:
			status.Reason = "container restart is required for updated port ranges"
		}
	}
	return status
}

func (i *DockerSoftRouterFRPInstaller) Install(ctx context.Context, cfg SoftRouterProxyConfig) (*SoftRouterFRPInstallResult, error) {
	if i == nil {
		return nil, infraerrors.ServiceUnavailable("SOFT_ROUTER_FRP_INSTALL_UNSUPPORTED", "当前部署不支持从面板安装 FRP")
	}
	if _, err := os.Stat(i.socketPath); err != nil {
		return nil, infraerrors.ServiceUnavailable(
			"SOFT_ROUTER_DOCKER_SOCKET_MISSING",
			"当前 XIASS API 容器没有挂载 /var/run/docker.sock，无法从面板安装 FRP。请使用新版一键安装脚本或更新 compose 后重建容器。",
		).WithCause(err)
	}
	if err := i.pingDocker(ctx); err != nil {
		return nil, infraerrors.ServiceUnavailable("SOFT_ROUTER_DOCKER_UNAVAILABLE", "无法连接 Docker Daemon，无法从面板安装 FRP").WithCause(err)
	}

	helperCtx, cancel := context.WithTimeout(ctx, softRouterFRPInstallTimeout)
	defer cancel()

	current, _ := i.inspectCurrentContainer(helperCtx)
	deployDir := current.ComposeWorkingDir
	if strings.TrimSpace(deployDir) == "" {
		deployDir = softRouterDefaultDeployDir
	}
	proxyBindAddr := current.FirstGateway()
	if strings.TrimSpace(proxyBindAddr) == "" {
		proxyBindAddr = "127.0.0.1"
	}
	ownedPublicPorts := ""
	if ranges, constrained, err := deploymentPortRanges(softRouterPublicPortRangeEnv); err == nil && constrained {
		ownedPublicPorts = current.HostTCPPortsCSV(ranges)
	}

	if err := i.pullImage(helperCtx); err != nil {
		return nil, err
	}
	containerID, err := i.createHelperContainer(helperCtx, cfg, deployDir, proxyBindAddr, ownedPublicPorts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = i.removeContainer(context.Background(), containerID) }()
	if err := i.startContainer(helperCtx, containerID); err != nil {
		return nil, err
	}
	exitCode, err := i.waitContainer(helperCtx, containerID)
	logText := i.containerLogs(context.Background(), containerID)
	logText = redactSoftRouterInstallLog(logText, cfg.FRPToken, cfg.DefaultPassword)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		message := fmt.Sprintf("FRP 安装失败，helper 退出码 %d", exitCode)
		if hint := firstInstallErrorLine(logText); hint != "" {
			message = "FRP 安装失败：" + hint
		}
		return nil, infraerrors.InternalServer(
			"SOFT_ROUTER_FRP_INSTALL_FAILED",
			message,
		).WithMetadata(map[string]string{"log": trimInstallLog(logText)})
	}

	status := i.Status(ctx, cfg)
	return &SoftRouterFRPInstallResult{
		Status:          status,
		RestartRequired: true,
		Message:         "FRP 已安装，宿主机 .env 已写入端口范围。请重启或重建当前 XIASS API 容器让公网 SOCKS 端口映射生效。",
		Log:             trimInstallLog(logText),
		Metadata: map[string]string{
			"deploy_dir":       deployDir,
			"proxy_bind_addr":  proxyBindAddr,
			"frp_service_name": softRouterFRPServiceName,
			"owned_ports":      ownedPublicPorts,
		},
	}, nil
}

type dockerCurrentContainerInfo struct {
	ComposeWorkingDir string
	Gateways          []string
	HostTCPPorts      map[int]bool
}

func (c dockerCurrentContainerInfo) FirstGateway() string {
	for _, gateway := range c.Gateways {
		gateway = strings.TrimSpace(gateway)
		if gateway != "" {
			return gateway
		}
	}
	return ""
}

func (c dockerCurrentContainerInfo) HostTCPPortsCSV(ranges []softRouterPortRange) string {
	ports := make([]int, 0, len(c.HostTCPPorts))
	for port := range c.HostTCPPorts {
		if !portInDeploymentRanges(port, ranges) {
			continue
		}
		ports = append(ports, port)
	}
	sort.Ints(ports)
	text := make([]string, 0, len(ports))
	for _, port := range ports {
		text = append(text, strconv.Itoa(port))
	}
	return strings.Join(text, ",")
}

func (i *DockerSoftRouterFRPInstaller) dockerClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", i.socketPath)
			},
		},
		Timeout: 0,
	}
}

func (i *DockerSoftRouterFRPInstaller) dockerRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/"+softRouterDockerAPIVersion+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return i.dockerClient().Do(req)
}

func (i *DockerSoftRouterFRPInstaller) dockerRequestOK(ctx context.Context, method, path string, body any, okCodes ...int) ([]byte, error) {
	resp, err := i.dockerRequest(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if readErr != nil {
		return nil, readErr
	}
	for _, code := range okCodes {
		if resp.StatusCode == code {
			return data, nil
		}
	}
	if len(okCodes) == 0 && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return data, nil
	}
	return nil, fmt.Errorf("docker %s %s returned %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
}

func (i *DockerSoftRouterFRPInstaller) pingDocker(ctx context.Context) error {
	resp, err := i.dockerRequest(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker ping returned %d", resp.StatusCode)
	}
	return nil
}

func (i *DockerSoftRouterFRPInstaller) inspectCurrentContainer(ctx context.Context) (dockerCurrentContainerInfo, error) {
	hostname, _ := os.Hostname()
	candidates := []string{strings.TrimSpace(hostname), "xiass-api", "nowind-api", "sub2api"}
	var lastErr error
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		data, err := i.dockerRequestOK(ctx, http.MethodGet, "/containers/"+url.PathEscape(candidate)+"/json", nil, http.StatusOK)
		if err != nil {
			lastErr = err
			continue
		}
		var payload struct {
			Config struct {
				Labels map[string]string `json:"Labels"`
			} `json:"Config"`
			NetworkSettings struct {
				Ports map[string][]struct {
					HostIP   string `json:"HostIp"`
					HostPort string `json:"HostPort"`
				} `json:"Ports"`
				Networks map[string]struct {
					Gateway string `json:"Gateway"`
				} `json:"Networks"`
			} `json:"NetworkSettings"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return dockerCurrentContainerInfo{}, err
		}
		info := dockerCurrentContainerInfo{}
		if payload.Config.Labels != nil {
			info.ComposeWorkingDir = payload.Config.Labels["com.docker.compose.project.working_dir"]
		}
		info.HostTCPPorts = map[int]bool{}
		for containerPort, bindings := range payload.NetworkSettings.Ports {
			_, proto, ok := strings.Cut(containerPort, "/")
			if !ok || proto != "tcp" {
				continue
			}
			for _, binding := range bindings {
				hostPort, err := strconv.Atoi(strings.TrimSpace(binding.HostPort))
				if err == nil && validPort(hostPort) {
					info.HostTCPPorts[hostPort] = true
				}
			}
		}
		for _, netInfo := range payload.NetworkSettings.Networks {
			if strings.TrimSpace(netInfo.Gateway) != "" {
				info.Gateways = append(info.Gateways, netInfo.Gateway)
			}
		}
		return info, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("container not found")
	}
	return dockerCurrentContainerInfo{}, lastErr
}

func (i *DockerSoftRouterFRPInstaller) pullImage(ctx context.Context) error {
	image, tag, ok := strings.Cut(i.image, ":")
	if !ok {
		tag = "latest"
	}
	path := "/images/create?fromImage=" + url.QueryEscape(image) + "&tag=" + url.QueryEscape(tag)
	data, err := i.dockerRequestOK(ctx, http.MethodPost, path, nil, http.StatusOK)
	if err != nil {
		return infraerrors.ServiceUnavailable("SOFT_ROUTER_HELPER_IMAGE_PULL_FAILED", "无法拉取 FRP 安装 helper 镜像").WithCause(err)
	}
	_ = data
	return nil
}

func (i *DockerSoftRouterFRPInstaller) createHelperContainer(ctx context.Context, cfg SoftRouterProxyConfig, deployDir, proxyBindAddr, ownedPublicPorts string) (string, error) {
	name := "xiass-frp-installer-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	cmd := "cat <<'XIASS_FRP_INSTALL_SCRIPT' | chroot /host /bin/sh\n" + softRouterFRPHostInstallScript + "\nXIASS_FRP_INSTALL_SCRIPT\n"
	body := map[string]any{
		"Image": i.image,
		"Cmd":   []string{"/bin/sh", "-c", cmd},
		"Tty":   true,
		"Env": []string{
			"SERVICE_NAME=" + softRouterFRPServiceName,
			"CONFIG_DIR=/etc/xiass-frps-soft-router",
			"CONFIG_FILE=" + softRouterFRPConfigPath,
			"FRPS_BIN=" + softRouterFRPBinaryPath,
			"FRP_TOKEN=" + cfg.FRPToken,
			"BIND_PORT=" + strconv.Itoa(cfg.FRPServerPort),
			"RAW_PORT_START=" + strconv.Itoa(cfg.RawPortStart),
			"RAW_PORT_END=" + strconv.Itoa(cfg.RawPortEnd),
			"PUBLIC_PORT_START=" + strconv.Itoa(cfg.PublicPortStart),
			"PUBLIC_PORT_END=" + strconv.Itoa(cfg.PublicPortEnd),
			"PUBLIC_PORTS_OWNED_BY_XIASS=" + ownedPublicPorts,
			"PROXY_BIND_ADDR=" + proxyBindAddr,
			"XIASS_DEPLOY_DIR=" + deployDir,
		},
		"HostConfig": map[string]any{
			"Binds":       []string{"/:/host"},
			"Privileged":  true,
			"PidMode":     "host",
			"NetworkMode": "host",
		},
	}
	data, err := i.dockerRequestOK(ctx, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), body, http.StatusCreated)
	if err != nil {
		return "", infraerrors.InternalServer("SOFT_ROUTER_HELPER_CREATE_FAILED", "创建 FRP 安装 helper 容器失败").WithCause(err)
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.ID) == "" {
		return "", fmt.Errorf("docker create did not return container id")
	}
	return out.ID, nil
}

func (i *DockerSoftRouterFRPInstaller) startContainer(ctx context.Context, id string) error {
	_, err := i.dockerRequestOK(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/start", nil, http.StatusNoContent, http.StatusNotModified)
	if err != nil {
		return infraerrors.InternalServer("SOFT_ROUTER_HELPER_START_FAILED", "启动 FRP 安装 helper 容器失败").WithCause(err)
	}
	return nil
}

func (i *DockerSoftRouterFRPInstaller) waitContainer(ctx context.Context, id string) (int, error) {
	data, err := i.dockerRequestOK(ctx, http.MethodPost, "/containers/"+url.PathEscape(id)+"/wait", nil, http.StatusOK)
	if err != nil {
		return -1, infraerrors.InternalServer("SOFT_ROUTER_HELPER_WAIT_FAILED", "等待 FRP 安装 helper 容器失败").WithCause(err)
	}
	var out struct {
		StatusCode int `json:"StatusCode"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return -1, err
	}
	return out.StatusCode, nil
}

func (i *DockerSoftRouterFRPInstaller) containerLogs(ctx context.Context, id string) string {
	data, err := i.dockerRequestOK(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs?stdout=1&stderr=1", nil, http.StatusOK)
	if err != nil {
		return ""
	}
	if len(data) > softRouterFRPInstallLogLimit {
		data = data[len(data)-softRouterFRPInstallLogLimit:]
	}
	return string(data)
}

func (i *DockerSoftRouterFRPInstaller) removeContainer(ctx context.Context, id string) error {
	_, err := i.dockerRequestOK(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id)+"?force=1&v=1", nil, http.StatusNoContent, http.StatusNotFound)
	return err
}

func formatPortRange(start, end int) string {
	if start == 0 && end == 0 {
		return ""
	}
	if start == end {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

func deploymentRangeCoversSilently(envName string, start, end int) bool {
	ranges, constrained, err := deploymentPortRanges(envName)
	if err != nil || !constrained {
		return false
	}
	return rangeCovered(start, end, ranges)
}

func trimInstallLog(logText string) string {
	logText = strings.TrimSpace(logText)
	if len(logText) <= softRouterFRPInstallLogLimit {
		return logText
	}
	return logText[len(logText)-softRouterFRPInstallLogLimit:]
}

func redactSoftRouterInstallLog(logText string, secrets ...string) string {
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		logText = strings.ReplaceAll(logText, secret, "[redacted]")
	}
	return logText
}

func firstInstallErrorLine(logText string) string {
	for _, line := range strings.Split(logText, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\r"))
		line = strings.TrimPrefix(line, "\x01")
		line = strings.TrimPrefix(line, "\x02")
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Error: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Error: "))
		}
	}
	return ""
}

const softRouterFRPHostInstallScript = `#!/bin/sh
set -eu

SERVICE_NAME="${SERVICE_NAME:-xiass-frps-soft-router}"
CONFIG_DIR="${CONFIG_DIR:-/etc/xiass-frps-soft-router}"
CONFIG_FILE="${CONFIG_FILE:-$CONFIG_DIR/frps.toml}"
FRPS_BIN="${FRPS_BIN:-/usr/local/bin/xiass-frps-soft-router}"
FRP_TOKEN="${FRP_TOKEN:-}"
BIND_PORT="${BIND_PORT:-7010}"
RAW_PORT_START="${RAW_PORT_START:-12083}"
RAW_PORT_END="${RAW_PORT_END:-12150}"
PUBLIC_PORT_START="${PUBLIC_PORT_START:-1101}"
PUBLIC_PORT_END="${PUBLIC_PORT_END:-1120}"
PUBLIC_PORTS_OWNED_BY_XIASS="${PUBLIC_PORTS_OWNED_BY_XIASS:-${PUBLIC_PORTS_OWNED_BY_NOWIND:-}}"
PROXY_BIND_ADDR="${PROXY_BIND_ADDR:-127.0.0.1}"
XIASS_DEPLOY_DIR="${XIASS_DEPLOY_DIR:-${NOWIND_DEPLOY_DIR:-/opt/xiass-api/deploy}}"
LEGACY_SERVICE_NAMES="${LEGACY_SERVICE_NAMES:-frps-nowind-soft-router frps-us}"
RUN_USER="${RUN_USER:-root}"
ROLLBACK_DIR=""
PREVIOUS_CONFIG_BACKUP=""
PREVIOUS_BINARY_BACKUP=""
PREVIOUS_UNIT_BACKUP=""
PREVIOUS_UNIT_PATH=""
PREVIOUS_ENV_FILE=""
PREVIOUS_ENV_BACKUP=""
PREVIOUS_SERVICE_EXISTS=0
PREVIOUS_SERVICE_ACTIVE=0
PREVIOUS_SERVICE_ENABLED=""
CURRENT_SERVICE_STOPPED=0
NEW_SERVICE_STARTED=0
MIGRATION_COMPLETE=0
TEMP_DIR=""

info() { printf '%s\n' "$*"; }
rollback_migration() {
    status=$?
    trap - EXIT INT TERM
    if [ "$status" -ne 0 ] && [ "$MIGRATION_COMPLETE" != "1" ]; then
        info "FRP installation failed; restoring the previous XIASS service state"
        if command -v systemctl >/dev/null 2>&1; then
            if [ "$CURRENT_SERVICE_STOPPED" = "1" ] || [ "$NEW_SERVICE_STARTED" = "1" ]; then
                systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
            fi
            if [ "$PREVIOUS_SERVICE_EXISTS" = "1" ]; then
                [ -n "$PREVIOUS_CONFIG_BACKUP" ] && cp -a "$PREVIOUS_CONFIG_BACKUP" "$CONFIG_FILE" >/dev/null 2>&1 || true
                [ -n "$PREVIOUS_BINARY_BACKUP" ] && cp -a "$PREVIOUS_BINARY_BACKUP" "$FRPS_BIN" >/dev/null 2>&1 || true
                if [ -n "$PREVIOUS_UNIT_BACKUP" ] && [ -n "$PREVIOUS_UNIT_PATH" ]; then
                    rm -f "/etc/systemd/system/$SERVICE_NAME.service" >/dev/null 2>&1 || true
                    cp -a "$PREVIOUS_UNIT_BACKUP" "$PREVIOUS_UNIT_PATH" >/dev/null 2>&1 || true
                fi
                systemctl daemon-reload >/dev/null 2>&1 || true
                case "$PREVIOUS_SERVICE_ENABLED" in
                    enabled|enabled-runtime|linked|linked-runtime)
                        systemctl enable "$SERVICE_NAME" >/dev/null 2>&1 || true
                        ;;
                    *)
                        systemctl disable "$SERVICE_NAME" >/dev/null 2>&1 || true
                        ;;
                esac
                if [ "$PREVIOUS_SERVICE_ACTIVE" = "1" ]; then
                    systemctl start "$SERVICE_NAME" >/dev/null 2>&1 || true
                    systemctl is-active --quiet "$SERVICE_NAME" || info "Warning: previous XIASS FRP service did not recover automatically"
                fi
            else
                systemctl disable "$SERVICE_NAME" >/dev/null 2>&1 || true
            fi
        fi
        if [ -n "$PREVIOUS_ENV_BACKUP" ] && [ -n "$PREVIOUS_ENV_FILE" ]; then
            cp -a "$PREVIOUS_ENV_BACKUP" "$PREVIOUS_ENV_FILE" >/dev/null 2>&1 || true
        fi
    fi
    [ -n "$TEMP_DIR" ] && rm -rf "$TEMP_DIR" >/dev/null 2>&1 || true
    [ -n "$ROLLBACK_DIR" ] && rm -rf "$ROLLBACK_DIR" >/dev/null 2>&1 || true
    exit "$status"
}
fail() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}
need_cmd() { command -v "$1" >/dev/null 2>&1 || fail "missing command: $1"; }

valid_port() {
    case "$1" in ''|*[!0-9]*) return 1 ;; esac
    [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

validate_config() {
    valid_port "$BIND_PORT" || fail "BIND_PORT must be between 1 and 65535"
    valid_port "$RAW_PORT_START" || fail "RAW_PORT_START must be between 1 and 65535"
    valid_port "$RAW_PORT_END" || fail "RAW_PORT_END must be between 1 and 65535"
    valid_port "$PUBLIC_PORT_START" || fail "PUBLIC_PORT_START must be between 1 and 65535"
    valid_port "$PUBLIC_PORT_END" || fail "PUBLIC_PORT_END must be between 1 and 65535"
    [ "$RAW_PORT_START" -le "$RAW_PORT_END" ] || fail "RAW_PORT_START must be <= RAW_PORT_END"
    [ "$PUBLIC_PORT_START" -le "$PUBLIC_PORT_END" ] || fail "PUBLIC_PORT_START must be <= PUBLIC_PORT_END"
    [ -n "$FRP_TOKEN" ] || fail "FRP_TOKEN is required"
}

port_listening() {
    port="$1"
    if command -v ss >/dev/null 2>&1; then
        ss -ltn 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]$port$" && return 0
    fi
    if command -v netstat >/dev/null 2>&1; then
        netstat -ltn 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]$port$" && return 0
    fi
    return 1
}

port_owned_by_xiass() {
    case ",$PUBLIC_PORTS_OWNED_BY_XIASS," in
        *",$1,"*) return 0 ;;
        *) return 1 ;;
    esac
}

assert_no_active_legacy_service() {
    command -v systemctl >/dev/null 2>&1 || return 0
    for service in $LEGACY_SERVICE_NAMES; do
        [ "$service" = "$SERVICE_NAME" ] && continue
        if systemctl is-active --quiet "$service" 2>/dev/null; then
            fail "legacy FRP service $service is running; use xiass-frps-migrate.sh to preserve its current token and configuration"
        fi
    done
}

prepare_xiass_rollback() {
    command -v systemctl >/dev/null 2>&1 || return 0
    if ! systemctl cat "$SERVICE_NAME" >/dev/null 2>&1; then
        return 0
    fi
    PREVIOUS_SERVICE_EXISTS=1
    ROLLBACK_DIR=$(mktemp -d /tmp/xiass-frp-rollback.XXXXXX)
    PREVIOUS_UNIT_PATH=$(systemctl show --property FragmentPath --value "$SERVICE_NAME" 2>/dev/null || true)
    if [ -n "$PREVIOUS_UNIT_PATH" ] && [ -f "$PREVIOUS_UNIT_PATH" ]; then
        PREVIOUS_UNIT_BACKUP="$ROLLBACK_DIR/unit.service"
        cp -a "$PREVIOUS_UNIT_PATH" "$PREVIOUS_UNIT_BACKUP"
    fi
    if [ -f "$CONFIG_FILE" ]; then
        PREVIOUS_CONFIG_BACKUP="$ROLLBACK_DIR/$(basename "$CONFIG_FILE")"
        cp -a "$CONFIG_FILE" "$PREVIOUS_CONFIG_BACKUP"
    fi
    if [ -f "$FRPS_BIN" ]; then
        PREVIOUS_BINARY_BACKUP="$ROLLBACK_DIR/$(basename "$FRPS_BIN")"
        cp -a "$FRPS_BIN" "$PREVIOUS_BINARY_BACKUP"
    fi
    PREVIOUS_SERVICE_ENABLED=$(systemctl is-enabled "$SERVICE_NAME" 2>/dev/null || true)
    if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
        PREVIOUS_SERVICE_ACTIVE=1
    fi
}

stop_current_xiass_service() {
    command -v systemctl >/dev/null 2>&1 || return 0
    if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
        info "Stopping existing XIASS FRP service for update"
        systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || fail "could not stop existing XIASS FRP service"
        CURRENT_SERVICE_STOPPED=1
    fi
}

assert_ports_free() {
    stop_current_xiass_service
    if port_listening "$BIND_PORT"; then
        fail "FRP control port $BIND_PORT is already in use"
    fi
    p="$RAW_PORT_START"
    while [ "$p" -le "$RAW_PORT_END" ]; do
        if port_listening "$p"; then
            fail "Raw FRP port $p is already in use"
        fi
        p=$((p + 1))
    done
    p="$PUBLIC_PORT_START"
    while [ "$p" -le "$PUBLIC_PORT_END" ]; do
        if port_listening "$p" && ! port_owned_by_xiass "$p"; then
            fail "Public SOCKS port $p is already in use"
        fi
        p=$((p + 1))
    done
}

detect_arch() {
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        armv7l|armv7|armv6l|armv6) echo "arm" ;;
        *) fail "unsupported architecture: $arch" ;;
    esac
}

fetch_url() {
    url="$1"
    out="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$out"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$out" "$url"
    else
        fail "missing curl or wget"
    fi
}

fetch_stdout() {
    url="$1"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "$url"
    else
        fail "missing curl or wget"
    fi
}

latest_frp_version() {
    fetch_stdout https://api.github.com/repos/fatedier/frp/releases/latest \
        | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
        | head -n 1
}

install_frps() {
    need_cmd tar
    need_cmd find
    need_cmd sed
    version="${FRP_VERSION:-$(latest_frp_version || true)}"
    [ -n "$version" ] || fail "could not detect latest frp version"
    version_no_v="${version#v}"
    arch="$(detect_arch)"
    archive="frp_${version_no_v}_linux_${arch}.tar.gz"
    url="https://github.com/fatedier/frp/releases/download/v${version_no_v}/${archive}"
    TEMP_DIR="$(mktemp -d)"
    info "Downloading frp ${version}"
    fetch_url "$url" "$TEMP_DIR/$archive"
    tar -xzf "$TEMP_DIR/$archive" -C "$TEMP_DIR"
    src="$(find "$TEMP_DIR" -type f -name frps | head -n 1)"
    [ -n "$src" ] || fail "frps binary not found"
    install -m 0755 "$src" "$FRPS_BIN"
    rm -rf "$TEMP_DIR"
    TEMP_DIR=""
}

write_config() {
    mkdir -p "$CONFIG_DIR"
    cat > "$CONFIG_FILE" <<EOF
bindPort = $BIND_PORT
proxyBindAddr = "$PROXY_BIND_ADDR"

auth.method = "token"
auth.token = "$FRP_TOKEN"

allowPorts = [
  { start = $RAW_PORT_START, end = $RAW_PORT_END }
]

log.to = "/var/log/$SERVICE_NAME.log"
log.level = "info"
log.maxDays = 7
EOF
    chmod 600 "$CONFIG_FILE"
}

write_service() {
    unit="/etc/systemd/system/$SERVICE_NAME.service"
    cat > "$unit" <<EOF
[Unit]
Description=XIASS API FRP server for soft-router proxy nodes
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$RUN_USER
ExecStart=$FRPS_BIN -c $CONFIG_FILE
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
}

upsert_env_value() {
    file="$1"
    key="$2"
    value="$3"
    [ -f "$file" ] || return 0
    if grep -q "^$key=" "$file"; then
        tmp="${file}.tmp.$$"
        awk -v k="$key" -v v="$value" 'BEGIN{done=0} $0 ~ "^" k "=" { print k "=" v; done=1; next } { print } END{ if (!done) print k "=" v }' "$file" > "$tmp"
        cat "$tmp" > "$file"
        rm -f "$tmp"
    else
        printf '\n%s=%s\n' "$key" "$value" >> "$file"
    fi
}

update_xiass_env() {
    env_file="$XIASS_DEPLOY_DIR/.env"
    if [ ! -f "$env_file" ]; then
        for candidate in /opt/xiass-api/deploy/.env /opt/nowind-api/deploy/.env /opt/sub2api/deploy/.env; do
            if [ -f "$candidate" ]; then
                env_file="$candidate"
                break
            fi
        done
    fi
    if [ ! -f "$env_file" ]; then
        info "XIASS API .env not found, skip port-range update"
        return
    fi
    if [ -z "$ROLLBACK_DIR" ]; then
        ROLLBACK_DIR=$(mktemp -d /tmp/xiass-frp-rollback.XXXXXX)
    fi
    PREVIOUS_ENV_FILE="$env_file"
    PREVIOUS_ENV_BACKUP="$ROLLBACK_DIR/env"
    cp -a "$env_file" "$PREVIOUS_ENV_BACKUP"
    cp "$env_file" "${env_file}.bak.$(date +%Y%m%d%H%M%S)"
    upsert_env_value "$env_file" "SOFT_ROUTER_PROXY_RAW_PORT_RANGE" "$RAW_PORT_START-$RAW_PORT_END"
    upsert_env_value "$env_file" "SOFT_ROUTER_PROXY_PUBLIC_PORT_RANGE" "$PUBLIC_PORT_START-$PUBLIC_PORT_END"
    info "Updated XIASS API env: $env_file"
}

open_firewall_ports() {
    if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi active; then
        ufw allow "$BIND_PORT/tcp" >/dev/null 2>&1 || true
        ufw allow "$PUBLIC_PORT_START:$PUBLIC_PORT_END/tcp" >/dev/null 2>&1 || true
    fi
    if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
        firewall-cmd --permanent --add-port="$BIND_PORT/tcp" >/dev/null 2>&1 || true
        firewall-cmd --permanent --add-port="$PUBLIC_PORT_START-$PUBLIC_PORT_END/tcp" >/dev/null 2>&1 || true
        firewall-cmd --reload >/dev/null 2>&1 || true
    fi
}

start_service() {
    if command -v systemctl >/dev/null 2>&1; then
        systemctl daemon-reload
        systemctl enable "$SERVICE_NAME" >/dev/null 2>&1
        systemctl restart "$SERVICE_NAME"
        systemctl is-active --quiet "$SERVICE_NAME" || fail "$SERVICE_NAME did not become active"
        NEW_SERVICE_STARTED=1
    else
        info "systemctl not found, service file written but not started"
    fi
}

validate_config
assert_no_active_legacy_service
prepare_xiass_rollback
trap rollback_migration EXIT INT TERM
install_frps
assert_ports_free
write_config
write_service
open_firewall_ports
update_xiass_env
start_service
MIGRATION_COMPLETE=1
trap - EXIT INT TERM
[ -n "$ROLLBACK_DIR" ] && rm -rf "$ROLLBACK_DIR"

info "Installed $SERVICE_NAME"
info "frps config: $CONFIG_FILE"
info "frps bind port: $BIND_PORT"
info "raw FRP range: $RAW_PORT_START-$RAW_PORT_END"
info "public SOCKS range: $PUBLIC_PORT_START-$PUBLIC_PORT_END"
info "proxy bind address: $PROXY_BIND_ADDR"
info "Restart or recreate the XIASS API container for updated public port publishing."
`
