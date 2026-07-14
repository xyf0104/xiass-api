#!/usr/bin/env bash
# XIASS API Docker 一键安装脚本
# 用法：curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/install.sh | sudo bash

set -Eeuo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

REPO_URL="https://github.com/xyf0104/xiass-api.git"
RAW_BASE_URL="https://raw.githubusercontent.com/xyf0104/xiass-api/main"
INSTALL_DIR="${INSTALL_DIR:-}"
BRANCH="${BRANCH:-main}"
SERVER_PORT="${SERVER_PORT:-8080}"
RAW_PORT_RANGE="${SOFT_ROUTER_PROXY_RAW_PORT_RANGE:-12083-12150}"
PUBLIC_PORT_RANGE="${SOFT_ROUTER_PROXY_PUBLIC_PORT_RANGE:-1101-1120}"
BACKUP_DIR="${BACKUP_DIR:-/root/xiass-backups}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@xiass.local}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-}"
BUILD_MODE="${BUILD_MODE:-image}"
EXISTING_INSTALL=false
ADMIN_PASSWORD_GENERATED=false
ARCH=""
COMPOSE=()
PERSISTENCE_MODE="${PERSISTENCE_MODE:-}"
COMPOSE_FILE=""
APP_CONTAINER=""
POSTGRES_CONTAINER=""

resolve_install_dir() {
    local candidate working_dir install_candidate
    local -A discovered=()
    [ -n "$INSTALL_DIR" ] && return

    for candidate in xiass-api nowind-api sub2api; do
        if container_exists "$candidate"; then
            working_dir=$(docker inspect --type container \
                --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}' \
                "$candidate" 2>/dev/null || true)
            if [ -n "$working_dir" ]; then
                if [ "$(basename "$working_dir")" = "deploy" ]; then
                    install_candidate=$(dirname "$working_dir")
                else
                    install_candidate="$working_dir"
                fi
                discovered["$install_candidate"]=1
            fi
        fi
    done
    if [ "${#discovered[@]}" -gt 1 ]; then
        die "检测到多个运行中的 XIASS/legacy 安装目录；请显式设置 INSTALL_DIR 后重试。"
    fi
    if [ "${#discovered[@]}" -eq 1 ]; then
        for install_candidate in "${!discovered[@]}"; do
            INSTALL_DIR="$install_candidate"
        done
        return
    fi

    local existing=()
    for candidate in /opt/xiass-api /opt/nowind-api /opt/sub2api; do
        if [ -d "$candidate/.git" ] || [ -f "$candidate/deploy/.env" ]; then
            existing+=("$candidate")
        fi
    done
    if [ "${#existing[@]}" -gt 1 ]; then
        die "检测到多个安装目录但没有可用的运行容器标签；请显式设置 INSTALL_DIR 后重试。"
    fi
    if [ "${#existing[@]}" -eq 1 ]; then
        INSTALL_DIR="${existing[0]}"
        return
    fi
    INSTALL_DIR="/opt/xiass-api"
}

log()  { echo -e "${CYAN}[XIASS]${NC} $*"; }
ok()   { echo -e "${GREEN}[完成]${NC} $*"; }
warn() { echo -e "${YELLOW}[提醒]${NC} $*"; }
err()  { echo -e "${RED}[错误]${NC} $*" >&2; }
die()  { err "$*"; exit 1; }

gen_secret() {
    openssl rand -hex 32
}

ask() {
    local prompt="$1" default="$2" var_name="$3" input=""
    if [ -r /dev/tty ]; then
        read -r -p "$(echo -e "${BLUE}${prompt}${NC} [${default}]: ")" input < /dev/tty || true
    fi
    printf -v "$var_name" '%s' "${input:-$default}"
}

ask_password() {
    local prompt="$1" var_name="$2" input=""
    if [ -r /dev/tty ]; then
        read -r -s -p "$(echo -e "${BLUE}${prompt}${NC}: ")" input < /dev/tty || true
        echo ""
    fi
    printf -v "$var_name" '%s' "$input"
}

confirm() {
    local prompt="$1" default_yes="${2:-true}" answer=""
    if [ ! -r /dev/tty ]; then
        "$default_yes"
        return
    fi
    if "$default_yes"; then
        read -r -p "$(echo -e "${BLUE}${prompt}${NC} [Y/n]: ")" answer < /dev/tty || true
        [[ ! "$answer" =~ ^[nN]$ ]]
    else
        read -r -p "$(echo -e "${BLUE}${prompt}${NC} [y/N]: ")" answer < /dev/tty || true
        [[ "$answer" =~ ^[yY]$ ]]
    fi
}

read_env_value() {
    local key="$1" env_file="$2"
    awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$env_file" 2>/dev/null
}

container_exists() {
    docker container inspect "$1" >/dev/null 2>&1
}

detect_runtime_layout() {
    local candidate mount_type

    if [ -n "$PERSISTENCE_MODE" ] && [ "$PERSISTENCE_MODE" != "local" ] && [ "$PERSISTENCE_MODE" != "named" ]; then
        die "PERSISTENCE_MODE 只能是 local 或 named。"
    fi

    for candidate in xiass-api nowind-api sub2api; do
        if container_exists "$candidate"; then
            APP_CONTAINER="$candidate"
            break
        fi
    done

    case "$APP_CONTAINER" in
        xiass-api) candidate="xiass-api-postgres" ;;
        nowind-api) candidate="nowind-api-postgres" ;;
        sub2api) candidate="sub2api-postgres" ;;
        *) candidate="" ;;
    esac
    if [ -n "$candidate" ] && container_exists "$candidate"; then
        POSTGRES_CONTAINER="$candidate"
    else
        for candidate in xiass-api-postgres nowind-api-postgres sub2api-postgres; do
            if container_exists "$candidate"; then
                POSTGRES_CONTAINER="$candidate"
                break
            fi
        done
    fi

    if [ -z "$PERSISTENCE_MODE" ] && [ -n "$POSTGRES_CONTAINER" ]; then
        mount_type=$(docker inspect --type container \
            --format '{{range .Mounts}}{{if eq .Destination "/var/lib/postgresql/data"}}{{.Type}}{{end}}{{end}}' \
            "$POSTGRES_CONTAINER" 2>/dev/null || true)
        case "$mount_type" in
            volume) PERSISTENCE_MODE="named" ;;
            bind) PERSISTENCE_MODE="local" ;;
        esac
    fi

    if [ -z "$PERSISTENCE_MODE" ]; then
        if [ -d "$INSTALL_DIR/deploy/postgres_data" ] || [ -d "$INSTALL_DIR/deploy/redis_data" ]; then
            PERSISTENCE_MODE="local"
        else
            PERSISTENCE_MODE="local"
        fi
    fi

    if [ -n "$APP_CONTAINER" ]; then
        log "检测到当前应用容器：$APP_CONTAINER；PostgreSQL 容器：${POSTGRES_CONTAINER:-未运行}；持久化模式：$PERSISTENCE_MODE"
    fi
}

select_compose_file() {
    case "$PERSISTENCE_MODE" in
        named) COMPOSE_FILE="$INSTALL_DIR/deploy/docker-compose.yml" ;;
        local) COMPOSE_FILE="$INSTALL_DIR/deploy/docker-compose.local.yml" ;;
        *) die "未知持久化模式：$PERSISTENCE_MODE" ;;
    esac
    [ -f "$COMPOSE_FILE" ] || die "部署文件缺失：$COMPOSE_FILE"
}

check_root() {
    [ "$(id -u)" -eq 0 ] || die "请使用 root 权限运行，例如：curl -fsSL ${RAW_BASE_URL}/install.sh | sudo bash"
}

check_system() {
    [ "$(uname -s)" = "Linux" ] || die "一键安装仅支持 Linux。"

    case "$(uname -m)" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) die "暂不支持当前 CPU 架构：$(uname -m)" ;;
    esac

    local distro="Linux" mem_mb=0 disk_mb=0
    if [ -r /etc/os-release ]; then
        # shellcheck disable=SC1091
        . /etc/os-release
        distro="${PRETTY_NAME:-${ID:-Linux}}"
    fi
    if command -v free >/dev/null 2>&1; then
        mem_mb=$(free -m | awk '/^Mem:/{print $2}')
    elif [ -r /proc/meminfo ]; then
        mem_mb=$(awk '/^MemTotal:/{print int($2/1024)}' /proc/meminfo)
    fi
    disk_mb=$(df -Pm / | awk 'NR==2{print $4}')

    log "系统：${distro}，架构：${ARCH}，内存：${mem_mb:-未知}MB，可用磁盘：${disk_mb:-未知}MB"
    if [ "${mem_mb:-0}" -gt 0 ] && [ "$mem_mb" -lt 1500 ]; then
        warn "内存低于 1.5GB，建议先添加 swap；源码构建至少建议 3GB 内存。"
    fi
    if [ "${disk_mb:-0}" -gt 0 ] && [ "$disk_mb" -lt 4096 ]; then
        die "可用磁盘不足 4GB。请先清理磁盘或扩容。"
    fi
}

install_base_dependencies() {
    local missing=() cmd
    for cmd in curl git openssl tar gzip awk sed ss; do
        command -v "$cmd" >/dev/null 2>&1 || missing+=("$cmd")
    done
    [ "${#missing[@]}" -eq 0 ] && return

    log "缺少基础依赖：${missing[*]}，正在安装..."
    if command -v apt-get >/dev/null 2>&1; then
        apt-get update -y
        DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl git openssl tar gzip iproute2 procps
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y ca-certificates curl git openssl tar gzip iproute procps-ng
    elif command -v yum >/dev/null 2>&1; then
        yum install -y ca-certificates curl git openssl tar gzip iproute procps-ng
    elif command -v apk >/dev/null 2>&1; then
        apk add --no-cache ca-certificates curl git openssl tar gzip iproute2 procps bash
    else
        die "无法识别系统包管理器。请手动安装：curl git openssl tar gzip iproute2。"
    fi

    for cmd in curl git openssl tar gzip awk sed ss; do
        command -v "$cmd" >/dev/null 2>&1 || die "依赖安装后仍找不到命令：$cmd"
    done
    ok "基础依赖已就绪"
}

install_compose_plugin() {
    log "未检测到 Docker Compose，正在安装 Compose 插件..."
    if command -v apt-get >/dev/null 2>&1; then
        DEBIAN_FRONTEND=noninteractive apt-get install -y docker-compose-plugin
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y docker-compose-plugin
    elif command -v yum >/dev/null 2>&1; then
        yum install -y docker-compose-plugin
    elif command -v apk >/dev/null 2>&1; then
        apk add --no-cache docker-cli-compose
    fi
}

install_docker() {
    if ! command -v docker >/dev/null 2>&1; then
        log "未检测到 Docker，正在安装官方 Docker Engine..."
        curl -fsSL https://get.docker.com | sh
    fi

    if command -v systemctl >/dev/null 2>&1; then
        systemctl enable --now docker >/dev/null 2>&1 || die "Docker 服务启动失败，请运行 systemctl status docker 查看原因。"
    elif command -v service >/dev/null 2>&1; then
        service docker start >/dev/null 2>&1 || die "Docker 服务启动失败。"
    fi

    docker info >/dev/null 2>&1 || die "Docker 守护进程不可用，请先修复 Docker。"

    if docker compose version >/dev/null 2>&1; then
        COMPOSE=(docker compose)
    else
        install_compose_plugin
        if docker compose version >/dev/null 2>&1; then
            COMPOSE=(docker compose)
        elif command -v docker-compose >/dev/null 2>&1; then
            COMPOSE=(docker-compose)
        else
            die "Docker 已安装，但缺少 Compose 插件。请安装 docker-compose-plugin 后重试。"
        fi
    fi

    ok "$(docker --version)；$("${COMPOSE[@]}" version | head -n 1)"
}

load_existing_config() {
    local env_file="$INSTALL_DIR/deploy/.env" value=""
    if [ ! -f "$env_file" ]; then
        return
    fi

    EXISTING_INSTALL=true
    value=$(read_env_value SERVER_PORT "$env_file")
    [ -n "$value" ] && SERVER_PORT="$value"
    value=$(read_env_value SOFT_ROUTER_PROXY_RAW_PORT_RANGE "$env_file")
    [ -n "$value" ] && RAW_PORT_RANGE="$value"
    value=$(read_env_value SOFT_ROUTER_PROXY_PUBLIC_PORT_RANGE "$env_file")
    [ -n "$value" ] && PUBLIC_PORT_RANGE="$value"
    value=$(read_env_value ADMIN_EMAIL "$env_file")
    [ -n "$value" ] && ADMIN_EMAIL="$value"
    value=$(read_env_value XIASS_BUILD_MODE "$env_file")
    [ -n "$value" ] || value=$(read_env_value NOWIND_BUILD_MODE "$env_file")
    if [ "$value" = "source" ] || [ "$value" = "image" ]; then
        BUILD_MODE="$value"
    elif [ "$ARCH" = "arm64" ]; then
        BUILD_MODE="source"
    fi
}

validate_port() {
    local port="$1"
    [[ "$port" =~ ^[0-9]+$ ]] && [ "$port" -ge 1 ] && [ "$port" -le 65535 ]
}

parse_range() {
    local range="$1" start_var="$2" end_var="$3" start end
    if [[ "$range" =~ ^([0-9]+)-([0-9]+)$ ]]; then
        start="${BASH_REMATCH[1]}"
        end="${BASH_REMATCH[2]}"
    elif validate_port "$range"; then
        start="$range"
        end="$range"
    else
        return 1
    fi
    validate_port "$start" && validate_port "$end" && [ "$start" -le "$end" ] || return 1
    [ $((end - start + 1)) -le 200 ] || return 1
    printf -v "$start_var" '%s' "$start"
    printf -v "$end_var" '%s' "$end"
}

ranges_overlap() {
    local a_start="$1" a_end="$2" b_start="$3" b_end="$4"
    [ "$a_start" -le "$b_end" ] && [ "$b_start" -le "$a_end" ]
}

validate_configuration() {
    local raw_start raw_end public_start public_end
    validate_port "$SERVER_PORT" || die "服务端口必须是 1-65535 之间的整数。"
    parse_range "$RAW_PORT_RANGE" raw_start raw_end || die "Raw FRP 端口范围格式错误，例如 12083-12150，最多 200 个端口。"
    parse_range "$PUBLIC_PORT_RANGE" public_start public_end || die "公网 SOCKS 端口范围格式错误，例如 1101-1120，最多 200 个端口。"

    if [ "$SERVER_PORT" -ge "$raw_start" ] && [ "$SERVER_PORT" -le "$raw_end" ]; then
        die "服务端口与 Raw FRP 端口范围冲突。"
    fi
    if [ "$SERVER_PORT" -ge "$public_start" ] && [ "$SERVER_PORT" -le "$public_end" ]; then
        die "服务端口与公网 SOCKS 端口范围冲突。"
    fi
    ranges_overlap "$raw_start" "$raw_end" "$public_start" "$public_end" && die "Raw FRP 与公网 SOCKS 端口范围不能重叠。"

    [[ "$ADMIN_EMAIL" =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]] || die "管理员邮箱格式不正确。"
    if [ -n "$ADMIN_PASSWORD" ]; then
        [ "${#ADMIN_PASSWORD}" -ge 12 ] || die "管理员密码至少需要 12 个字符。"
        [[ "$ADMIN_PASSWORD" =~ ^[A-Za-z0-9._~!@%+=:,/-]+$ ]] || die "管理员密码只能包含字母、数字和 ._~!@%+=:,/-。"
    fi
}

port_in_use() {
    local port="$1"
    ss -H -lnt 2>/dev/null | awk -v port="$port" '$4 ~ (":" port "$" ) {found=1} END {exit !found}'
}

check_ports_available() {
    "$EXISTING_INSTALL" && return

    local raw_start raw_end public_start public_end port occupied=()
    parse_range "$RAW_PORT_RANGE" raw_start raw_end
    parse_range "$PUBLIC_PORT_RANGE" public_start public_end

    port_in_use "$SERVER_PORT" && occupied+=("$SERVER_PORT")
    for ((port=raw_start; port<=raw_end; port++)); do
        port_in_use "$port" && occupied+=("$port")
    done
    for ((port=public_start; port<=public_end; port++)); do
        port_in_use "$port" && occupied+=("$port")
    done

    if [ "${#occupied[@]}" -gt 0 ]; then
        die "以下端口已被占用：${occupied[*]}。请释放端口或重新运行并选择其他范围。"
    fi
    if port_in_use 7010; then
        warn "端口 7010 已被占用；以后安装软路由 FRP 时需要在后台改用其他控制端口。"
    fi
    ok "服务端口和代理端口范围均可用"
}

interactive_config() {
    echo ""
    echo -e "${CYAN}============================================================${NC}"
    echo -e "${BOLD}  XIASS API 一键安装${NC}"
    echo -e "${CYAN}============================================================${NC}"

    if "$EXISTING_INSTALL"; then
        warn "检测到已有部署，将保留 .env、PostgreSQL、Redis 和应用数据。"
        echo "  安装目录：$INSTALL_DIR"
        echo "  服务端口：$SERVER_PORT"
        echo "  Raw FRP： $RAW_PORT_RANGE"
        echo "  公网 SOCKS：$PUBLIC_PORT_RANGE"
        confirm "继续修复/更新部署文件并启动服务？" true || exit 0
        return
    fi

    if [ "$ARCH" = "amd64" ]; then
        echo ""
        echo "部署方式："
        echo "  1) 拉取 GHCR 正式镜像（推荐，速度快，支持后台在线更新）"
        echo "  2) 本机从源码构建（开发用途，耗时更长）"
        local mode_choice=""
        if [ -r /dev/tty ]; then
            read -r -p "$(echo -e "${BLUE}请选择${NC} [1]: ")" mode_choice < /dev/tty || true
        fi
        [ "$mode_choice" = "2" ] && BUILD_MODE="source" || BUILD_MODE="image"
    else
        BUILD_MODE="source"
        warn "当前为 arm64；正式 GHCR 镜像目前只发布 amd64，将自动使用源码构建。"
    fi

    ask "Web 服务端口" "$SERVER_PORT" SERVER_PORT
    ask "Raw FRP 端口范围" "$RAW_PORT_RANGE" RAW_PORT_RANGE
    ask "公网 SOCKS 端口范围" "$PUBLIC_PORT_RANGE" PUBLIC_PORT_RANGE
    ask "管理员邮箱" "$ADMIN_EMAIL" ADMIN_EMAIL

    local generated_password
    generated_password=$(openssl rand -hex 12)
    echo ""
    echo "管理员密码留空时会自动生成；自定义密码至少 12 位，且不能包含空格或引号。"
    ask_password "管理员密码（留空自动生成）" ADMIN_PASSWORD
    if [ -z "$ADMIN_PASSWORD" ]; then
        ADMIN_PASSWORD="$generated_password"
        ADMIN_PASSWORD_GENERATED=true
    fi

    validate_configuration
    echo ""
    echo "  安装目录：      $INSTALL_DIR"
    echo "  部署方式：      $BUILD_MODE"
    echo "  Web 端口：      $SERVER_PORT"
    echo "  Raw FRP：       $RAW_PORT_RANGE"
    echo "  公网 SOCKS：    $PUBLIC_PORT_RANGE"
    echo "  管理员邮箱：    $ADMIN_EMAIL"
    confirm "确认安装？" true || exit 0
}

setup_repo() {
    if [ -d "$INSTALL_DIR/.git" ]; then
        log "同步 XIASS 源码与部署文件..."
        if ! git -C "$INSTALL_DIR" diff --quiet || ! git -C "$INSTALL_DIR" diff --cached --quiet; then
            local patch_file="/root/xiass-local-changes-$(date +%Y%m%d-%H%M%S).patch"
            git -C "$INSTALL_DIR" diff HEAD > "$patch_file"
            chmod 600 "$patch_file"
            warn "检测到本地源码修改，已备份为 $patch_file"
        fi
        git -C "$INSTALL_DIR" fetch --prune origin "$BRANCH"
        git -C "$INSTALL_DIR" reset --hard "origin/$BRANCH"
    else
        if [ -d "$INSTALL_DIR" ] && [ -n "$(find "$INSTALL_DIR" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]; then
            die "$INSTALL_DIR 已存在且不是 XIASS Git 仓库。请更换 INSTALL_DIR 或先整理该目录。"
        fi
        log "克隆 XIASS 到 $INSTALL_DIR ..."
        mkdir -p "$(dirname "$INSTALL_DIR")"
        git clone --depth 1 --branch "$BRANCH" "$REPO_URL" "$INSTALL_DIR"
    fi
    [ -f "$INSTALL_DIR/deploy/docker-compose.local.yml" ] || die "部署文件缺失：deploy/docker-compose.local.yml"
    ok "源码与部署文件已就绪"
}

generate_env() {
    local env_file="$INSTALL_DIR/deploy/.env"
    if [ -f "$env_file" ]; then
        chmod 600 "$env_file"
        ok "保留已有 .env 配置"
        return
    fi

    local pg_password redis_password jwt_secret totp_key watchtower_token
    pg_password=$(gen_secret)
    redis_password=$(gen_secret)
    jwt_secret=$(gen_secret)
    totp_key=$(gen_secret)
    watchtower_token=$(gen_secret)

    umask 077
    cat > "$env_file" <<ENVEOF
# XIASS API 独立实例配置
# 此文件只保存在当前服务器，不会上传到 GitHub 或连接维护者数据库。
SERVER_PORT=${SERVER_PORT}
BIND_HOST=0.0.0.0
SERVER_MODE=release
RUN_MODE=standard
XIASS_BUILD_MODE=${BUILD_MODE}
XIASS_WATCHTOWER_TOKEN=${watchtower_token}
# Legacy aliases allow rollback to older images and maintenance scripts.
NOWIND_BUILD_MODE=${BUILD_MODE}
NOWIND_WATCHTOWER_TOKEN=${watchtower_token}
TZ=Asia/Shanghai

SOFT_ROUTER_PROXY_RAW_PORT_RANGE=${RAW_PORT_RANGE}
SOFT_ROUTER_PROXY_PUBLIC_PORT_RANGE=${PUBLIC_PORT_RANGE}

POSTGRES_USER=sub2api
POSTGRES_PASSWORD=${pg_password}
POSTGRES_DB=sub2api
REDIS_PASSWORD=${redis_password}

JWT_SECRET=${jwt_secret}
JWT_EXPIRE_HOUR=168
TOTP_ENCRYPTION_KEY=${totp_key}

ADMIN_EMAIL=${ADMIN_EMAIL}
ADMIN_PASSWORD=${ADMIN_PASSWORD}

LOG_LEVEL=info
LOG_FORMAT=json
LOG_OUTPUT_TO_STDOUT=true
LOG_OUTPUT_TO_FILE=true
OPS_ENABLED=true

# 软路由与内网代理功能需要访问私有地址，因此默认保留兼容配置。
SECURITY_URL_ALLOWLIST_ENABLED=false
SECURITY_URL_ALLOWLIST_ALLOW_INSECURE_HTTP=true
SECURITY_URL_ALLOWLIST_ALLOW_PRIVATE_HOSTS=true
ENVEOF
    chmod 600 "$env_file"
    ok "已生成当前服务器专属 .env 与随机安全密钥"
}

create_data_dirs() {
    if [ "$PERSISTENCE_MODE" = "named" ]; then
        ok "使用命名卷持久化，保留现有 Docker 卷"
        return
    fi
    mkdir -p "$INSTALL_DIR/deploy/data" \
             "$INSTALL_DIR/deploy/postgres_data" \
             "$INSTALL_DIR/deploy/redis_data"
    ok "持久化目录已创建"
}

configure_firewall() {
    local public_start public_end
    parse_range "$PUBLIC_PORT_RANGE" public_start public_end

    if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q '^Status: active'; then
        ufw allow "${SERVER_PORT}/tcp" >/dev/null
        ufw allow "${public_start}:${public_end}/tcp" >/dev/null
        ok "UFW 已放行 Web 与公网 SOCKS 端口"
    elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
        firewall-cmd --permanent --add-port="${SERVER_PORT}/tcp" >/dev/null
        firewall-cmd --permanent --add-port="${public_start}-${public_end}/tcp" >/dev/null
        firewall-cmd --reload >/dev/null
        ok "firewalld 已放行 Web 与公网 SOCKS 端口"
    else
        warn "未检测到启用中的 UFW/firewalld；如使用云服务器，请在安全组放行 TCP ${SERVER_PORT} 和 ${PUBLIC_PORT_RANGE}。"
    fi
}

compose() {
    local compose_dir="$INSTALL_DIR/deploy" args=(-f "$COMPOSE_FILE")
    if [ "$BUILD_MODE" = "source" ]; then
        args+=(-f "$compose_dir/docker-compose.build.yml")
    fi
    "${COMPOSE[@]}" "${args[@]}" --project-directory "$compose_dir" "$@"
}

remove_legacy_runtime_containers() {
    local container_name
    for container_name in \
        nowind-api nowind-api-watchtower nowind-api-postgres nowind-api-redis \
        sub2api sub2api-watchtower sub2api-postgres sub2api-redis; do
        if docker container inspect "$container_name" >/dev/null 2>&1; then
            log "优雅停止并移除旧版运行容器 $container_name（不会删除卷或数据）..."
            docker stop -t 60 "$container_name" >/dev/null 2>&1 || true
            docker rm "$container_name" >/dev/null
        fi
    done
}

start_services() {
    if [ "$BUILD_MODE" = "source" ]; then
        log "正在从源码构建并启动，首次通常需要 3-10 分钟..."
        compose build xiass-api
        remove_legacy_runtime_containers
        compose up -d
    else
        log "正在拉取 XIASS 正式镜像..."
        compose pull
        remove_legacy_runtime_containers
        compose up -d
    fi
    ok "容器已启动"
}

backup_existing_runtime() {
    if ! "$EXISTING_INSTALL" && [ -z "$APP_CONTAINER" ]; then
        return
    fi
    log "检测到已有安装；同步部署文件前先创建完整备份..."
    curl -fsSL "$RAW_BASE_URL/deploy/xiass-backup.sh" \
        | INSTALL_DIR="$INSTALL_DIR" BACKUP_DIR="$BACKUP_DIR" PERSISTENCE_MODE="$PERSISTENCE_MODE" bash
}

wait_for_service() {
    local attempt
    log "等待数据库迁移和服务健康检查完成..."
    for attempt in $(seq 1 120); do
        if curl -fsS --max-time 3 "http://127.0.0.1:${SERVER_PORT}/health" >/dev/null 2>&1; then
            ok "XIASS 健康检查通过"
            return
        fi
        sleep 2
    done

    compose ps || true
    compose logs --tail 120 xiass-api || true
    die "服务在 4 分钟内未通过健康检查。上方已输出容器状态和日志。"
}

get_public_ip() {
    curl -fsSL --connect-timeout 5 --max-time 10 https://api.ipify.org 2>/dev/null \
        || curl -fsSL --connect-timeout 5 --max-time 10 https://ifconfig.me 2>/dev/null \
        || echo "你的服务器IP"
}

print_completion() {
    local ip
    ip=$(get_public_ip)
    echo ""
    echo -e "${GREEN}${BOLD}XIASS API 已完整安装并通过健康检查。${NC}"
    echo ""
    echo "访问地址：      http://${ip}:${SERVER_PORT}"
    echo "管理员邮箱：    ${ADMIN_EMAIL}"
    if ! "$EXISTING_INSTALL"; then
        echo "管理员密码：    ${ADMIN_PASSWORD}"
        if "$ADMIN_PASSWORD_GENERATED"; then
            warn "这是自动生成的管理员密码，请立即保存并在登录后修改。"
        fi
    else
        echo "管理员密码：    保留原密码"
    fi
    echo ""
    echo "安装目录：      $INSTALL_DIR"
    echo "环境配置：      $INSTALL_DIR/deploy/.env（权限 600）"
    echo "应用数据：      $INSTALL_DIR/deploy/data"
    echo "PostgreSQL：    $INSTALL_DIR/deploy/postgres_data"
    echo "Redis：         $INSTALL_DIR/deploy/redis_data"
    echo ""
    echo "常用命令："
    echo "  查看状态：cd $INSTALL_DIR/deploy && ${COMPOSE[*]} -f $(basename "$COMPOSE_FILE") ps"
    echo "  查看日志：cd $INSTALL_DIR/deploy && ${COMPOSE[*]} -f $(basename "$COMPOSE_FILE") logs -f xiass-api"
    echo "  安全更新：curl -fsSL ${RAW_BASE_URL}/deploy/xiass-update.sh | sudo bash"
    echo "  完整备份：curl -fsSL ${RAW_BASE_URL}/deploy/xiass-backup.sh | sudo bash"
    echo ""
    warn "云厂商安全组还需放行 TCP ${SERVER_PORT}；使用代理节点时再放行 ${PUBLIC_PORT_RANGE}。"
    warn "任何维护操作都不要使用 docker compose down -v；-v 会删除命名卷。"
    echo ""
    echo "每台安装均使用本机独立 PostgreSQL、Redis、数据目录和随机密钥，不会连接或读取维护者的线上实例数据。"
}

main() {
    check_root
    check_system
    install_base_dependencies
    install_docker
    resolve_install_dir
    detect_runtime_layout
    load_existing_config
    if "$EXISTING_INSTALL"; then
        interactive_config
        log "已有实例将交给带完整备份和自动回滚的更新事务处理..."
        curl -fsSL "$RAW_BASE_URL/deploy/xiass-update.sh" \
            | INSTALL_DIR="$INSTALL_DIR" BACKUP_DIR="$BACKUP_DIR" PERSISTENCE_MODE="$PERSISTENCE_MODE" bash
        return
    fi
    interactive_config
    validate_configuration
    check_ports_available
    setup_repo
    select_compose_file
    generate_env
    create_data_dirs
    configure_firewall
    start_services
    wait_for_service
    print_completion
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
