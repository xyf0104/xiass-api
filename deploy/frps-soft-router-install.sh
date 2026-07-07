#!/bin/sh
#
# Install an independent frps service for Nowind soft-router proxy nodes.
#
# Examples:
#   FRP_TOKEN="$(openssl rand -hex 24)" sh frps-soft-router-install.sh
#   PROXY_BIND_ADDR=172.18.0.1 RAW_PORT_START=12083 RAW_PORT_END=12150 sh frps-soft-router-install.sh

set -eu

SERVICE_NAME="${SERVICE_NAME:-frps-nowind-soft-router}"
CONFIG_DIR="${CONFIG_DIR:-/etc/frp-nowind-soft-router}"
CONFIG_FILE="${CONFIG_FILE:-$CONFIG_DIR/frps.toml}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
FRPS_BIN="${FRPS_BIN:-$INSTALL_DIR/frps-nowind-soft-router}"
FRP_VERSION="${FRP_VERSION:-}"
FRP_TOKEN="${FRP_TOKEN:-}"
BIND_PORT="${BIND_PORT:-7010}"
RAW_PORT_START="${RAW_PORT_START:-12083}"
RAW_PORT_END="${RAW_PORT_END:-12150}"
PROXY_BIND_ADDR="${PROXY_BIND_ADDR:-}"
RUN_USER="${RUN_USER:-root}"

info() {
    printf '%s\n' "$*"
}

fail() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

require_root() {
    if [ "$(id -u)" != "0" ]; then
        fail "please run as root"
    fi
}

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || fail "missing command: $1"
}

valid_port() {
    case "$1" in
        ''|*[!0-9]*) return 1 ;;
    esac
    [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

validate_config() {
    valid_port "$BIND_PORT" || fail "BIND_PORT must be between 1 and 65535"
    valid_port "$RAW_PORT_START" || fail "RAW_PORT_START must be between 1 and 65535"
    valid_port "$RAW_PORT_END" || fail "RAW_PORT_END must be between 1 and 65535"
    [ "$RAW_PORT_START" -le "$RAW_PORT_END" ] || fail "RAW_PORT_START must be <= RAW_PORT_END"
    case "$FRP_TOKEN" in
        *[!A-Za-z0-9_.=-]*)
            fail "FRP_TOKEN can only contain letters, numbers, dot, underscore, equals and hyphen"
            ;;
    esac
}

detect_arch() {
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        armv7l|armv7) echo "arm" ;;
        armv6l|armv6) echo "arm" ;;
        *) fail "unsupported architecture: $arch" ;;
    esac
}

latest_frp_version() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL https://api.github.com/repos/fatedier/frp/releases/latest \
            | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
            | head -n 1
        return
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -qO- https://api.github.com/repos/fatedier/frp/releases/latest \
            | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
            | head -n 1
        return
    fi
    return 1
}

download_file() {
    url="$1"
    out="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fL "$url" -o "$out"
        return
    fi
    if command -v wget >/dev/null 2>&1; then
        wget -O "$out" "$url"
        return
    fi
    fail "missing curl or wget"
}

detect_proxy_bind_addr() {
    if command -v docker >/dev/null 2>&1; then
        container_id="$(docker ps -q --filter 'name=^/sub2api$' | head -n 1 || true)"
        if [ -n "$container_id" ]; then
            gateway="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.Gateway}}{{"\n"}}{{end}}' "$container_id" 2>/dev/null | sed '/^$/d' | head -n 1 || true)"
            if [ -n "$gateway" ]; then
                echo "$gateway"
                return
            fi
        fi
    fi
    if command -v ip >/dev/null 2>&1; then
        gateway="$(ip -4 addr show docker0 2>/dev/null | awk '/inet / { split($2, a, "/"); print a[1]; exit }' || true)"
        if [ -n "$gateway" ]; then
            echo "$gateway"
            return
        fi
    fi
    echo "127.0.0.1"
}

ensure_token() {
    if [ -n "$FRP_TOKEN" ]; then
        return
    fi
    if [ -t 0 ]; then
        printf 'FRP token (leave empty to generate): '
        read -r FRP_TOKEN
    fi
    if [ -n "$FRP_TOKEN" ]; then
        return
    fi
    if command -v openssl >/dev/null 2>&1; then
        FRP_TOKEN="$(openssl rand -hex 24)"
        return
    fi
    FRP_TOKEN="$(date +%s | sha256sum | awk '{print $1}')"
}

install_frps() {
    arch="$(detect_arch)"
    if [ -z "$FRP_VERSION" ]; then
        FRP_VERSION="$(latest_frp_version || true)"
    fi
    [ -n "$FRP_VERSION" ] || fail "could not detect latest frp version; set FRP_VERSION=vX.Y.Z"
    version_no_v="${FRP_VERSION#v}"
    archive="frp_${version_no_v}_linux_${arch}.tar.gz"
    url="https://github.com/fatedier/frp/releases/download/v${version_no_v}/${archive}"
    tmp_dir="$(mktemp -d)"
    trap 'rm -rf "$tmp_dir"' EXIT INT TERM

    info "Downloading $url"
    download_file "$url" "$tmp_dir/$archive"
    tar -xzf "$tmp_dir/$archive" -C "$tmp_dir"
    src="$(find "$tmp_dir" -type f -name frps | head -n 1)"
    [ -n "$src" ] || fail "frps binary not found in archive"
    install -m 0755 "$src" "$FRPS_BIN"
}

write_config() {
    if [ -z "$PROXY_BIND_ADDR" ]; then
        PROXY_BIND_ADDR="$(detect_proxy_bind_addr)"
    fi
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
Description=FRP server for Nowind soft-router proxy nodes
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

main() {
    require_root
    require_cmd tar
    require_cmd find
    require_cmd sed
    require_cmd awk
    ensure_token
    validate_config
    install_frps
    write_config
    write_service

    if command -v systemctl >/dev/null 2>&1; then
        systemctl daemon-reload
        systemctl enable "$SERVICE_NAME"
        systemctl restart "$SERVICE_NAME"
        systemctl --no-pager --full status "$SERVICE_NAME" || true
    fi

    info ""
    info "Installed $SERVICE_NAME"
    info "frps binary: $FRPS_BIN"
    info "config: $CONFIG_FILE"
    info "frps bind port: $BIND_PORT"
    info "raw FRP range: $RAW_PORT_START-$RAW_PORT_END"
    info "proxy bind address: $PROXY_BIND_ADDR"
    info "FRP token: $FRP_TOKEN"
    info ""
    info "In Nowind admin -> proxies -> proxy nodes, use the same FRP host, bind port, token and raw range."
    info "If Nowind runs in Docker, set the panel upstream host to host.docker.internal."
}

main "$@"
