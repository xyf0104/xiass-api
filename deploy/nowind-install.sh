#!/usr/bin/env bash
# =============================================================================
# NoWind API 交互式一键安装脚本
# =============================================================================
# 用法（在 VPS 上执行）：
#   curl -fsSL https://raw.githubusercontent.com/xyf0104/nowind-api/main/deploy/nowind-install.sh | sudo bash
#
# 功能：
#   1. 检测系统环境、安装 Docker（若缺失）
#   2. 交互式收集：端口、管理员邮箱、管理员密码
#   3. 自动生成安全密钥（JWT / TOTP / PostgreSQL）
#   4. 从源码构建（包含你的定制前端）或拉取官方镜像
#   5. 启动 PostgreSQL + Redis + NoWind API
#   6. 输出访问地址、管理员信息、Google 登录配置指引
# =============================================================================
set -euo pipefail

# ===== 颜色 =====
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# ===== 默认值 =====
REPO_URL="https://github.com/xyf0104/nowind-api.git"
INSTALL_DIR="${INSTALL_DIR:-/opt/nowind-api}"
BRANCH="${BRANCH:-main}"
SERVER_PORT="8080"
ADMIN_EMAIL="admin@nowind.local"
ADMIN_PASSWORD=""
BUILD_MODE="image"  # image = 拉取官方镜像; source = 从源码构建（含定制前端）

# ===== 工具函数 =====
log()  { echo -e "${CYAN}[NoWind]${NC} $*"; }
ok()   { echo -e "${GREEN}[✓]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[✗]${NC} $*" >&2; }
gen_secret() { openssl rand -hex 32; }

# 交互式读取（兼容 curl | bash 管道模式）
ask() {
    local prompt="$1" default="$2" var_name="$3"
    local input
    if [ -e /dev/tty ] && [ -r /dev/tty ]; then
        read -p "$(echo -e "${BLUE}${prompt}${NC} [${default}]: ")" input < /dev/tty
    else
        input=""
    fi
    eval "${var_name}='${input:-$default}'"
}

ask_password() {
    local prompt="$1" var_name="$2"
    local input
    if [ -e /dev/tty ] && [ -r /dev/tty ]; then
        read -sp "$(echo -e "${BLUE}${prompt}${NC}: ")" input < /dev/tty
        echo ""
    else
        input=""
    fi
    eval "${var_name}='${input}'"
}

# ===== 前置检查 =====
check_root() {
    [ "$(id -u)" -eq 0 ] || { err "请使用 root 运行: curl ... | sudo bash"; exit 1; }
}

check_system() {
    log "检测系统环境..."
    local os arch mem_mb
    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)
    [ "$os" = "linux" ] || { err "仅支持 Linux 系统，当前: $os"; exit 1; }
    mem_mb=$(free -m 2>/dev/null | awk '/Mem:/{print $2}' || echo 0)
    ok "系统: ${os}/${arch}  内存: ${mem_mb}MB"
    if [ "$mem_mb" -gt 0 ] && [ "$mem_mb" -lt 1500 ]; then
        warn "内存不足 1.5GB，源码构建可能失败。建议使用官方镜像模式或添加 swap。"
    fi
}

# ===== 安装 Docker =====
install_docker() {
    if command -v docker >/dev/null 2>&1; then
        ok "Docker 已安装: $(docker --version 2>/dev/null | head -1)"
    else
        log "正在安装 Docker..."
        curl -fsSL https://get.docker.com | sh
        systemctl enable --now docker 2>/dev/null || true
        ok "Docker 安装完成"
    fi

    # 检测 docker compose
    if docker compose version >/dev/null 2>&1; then
        COMPOSE="docker compose"
    elif command -v docker-compose >/dev/null 2>&1; then
        COMPOSE="docker-compose"
    else
        err "未检测到 docker compose 插件，请手动安装"
        exit 1
    fi
    ok "Compose: $($COMPOSE version 2>/dev/null | head -1)"
}

# ===== 安装 git =====
install_git() {
    if command -v git >/dev/null 2>&1; then
        return
    fi
    log "正在安装 git..."
    (apt-get update -y && apt-get install -y git) 2>/dev/null || yum install -y git 2>/dev/null || true
}

# ===== 交互式配置 =====
interactive_config() {
    echo ""
    echo -e "${CYAN}══════════════════════════════════════════════${NC}"
    echo -e "${BOLD}  NoWind API 安装配置向导${NC}"
    echo -e "${CYAN}══════════════════════════════════════════════${NC}"
    echo ""

    # 部署模式
    echo -e "${YELLOW}部署模式：${NC}"
    echo "  1) 从源码构建（包含你的定制前端，首次约 3-8 分钟）${GREEN}← 推荐${NC}"
    echo "  2) 拉取官方镜像（更快，但使用官方原版界面）"
    echo ""
    local mode_choice=""
    if [ -e /dev/tty ] && [ -r /dev/tty ]; then
        read -p "$(echo -e "${BLUE}选择部署模式${NC} [1]: ")" mode_choice < /dev/tty
    fi
    case "$mode_choice" in
        2) BUILD_MODE="image" ;;
        *) BUILD_MODE="source" ;;
    esac
    echo ""

    # 端口
    ask "服务端口" "$SERVER_PORT" SERVER_PORT

    # 管理员邮箱
    ask "管理员邮箱" "$ADMIN_EMAIL" ADMIN_EMAIL

    # 管理员密码
    echo ""
    echo -e "${YELLOW}管理员密码（留空则自动生成，首次启动后从日志中查看）${NC}"
    ask_password "管理员密码" ADMIN_PASSWORD

    echo ""
    echo -e "${CYAN}──────────────────────────────────────────────${NC}"
    echo -e "  部署模式:     ${BOLD}$([ "$BUILD_MODE" = "source" ] && echo "从源码构建" || echo "拉取官方镜像")${NC}"
    echo -e "  服务端口:     ${BOLD}${SERVER_PORT}${NC}"
    echo -e "  管理员邮箱:   ${BOLD}${ADMIN_EMAIL}${NC}"
    echo -e "  管理员密码:   ${BOLD}$([ -n "$ADMIN_PASSWORD" ] && echo "***（已设置）" || echo "（自动生成）")${NC}"
    echo -e "${CYAN}──────────────────────────────────────────────${NC}"
    echo ""

    local confirm=""
    if [ -e /dev/tty ] && [ -r /dev/tty ]; then
        read -p "$(echo -e "${BLUE}确认以上配置并开始安装？${NC} [Y/n]: ")" confirm < /dev/tty
    fi
    case "$confirm" in
        [nN]*) echo "已取消安装。"; exit 0 ;;
    esac
    echo ""
}

# ===== 克隆 / 更新仓库 =====
setup_repo() {
    install_git
    if [ -d "$INSTALL_DIR/.git" ]; then
        log "更新已有仓库 $INSTALL_DIR ..."
        git -C "$INSTALL_DIR" fetch origin "$BRANCH"
        git -C "$INSTALL_DIR" reset --hard "origin/$BRANCH"
    else
        log "克隆仓库到 $INSTALL_DIR ..."
        git clone -b "$BRANCH" "$REPO_URL" "$INSTALL_DIR"
    fi
    ok "仓库准备就绪"
}

# ===== 生成 .env =====
generate_env() {
    local env_file="$INSTALL_DIR/deploy/.env"

    if [ -f "$env_file" ]; then
        log ".env 已存在，备份为 .env.bak 并重新生成"
        cp "$env_file" "${env_file}.bak"
    fi

    local jwt_secret pg_password totp_key
    jwt_secret=$(gen_secret)
    pg_password=$(gen_secret)
    totp_key=$(gen_secret)

    cat > "$env_file" <<ENVEOF
# ═══════════════════════════════════════════════
# NoWind API 环境配置（由安装脚本自动生成）
# ═══════════════════════════════════════════════

# 服务器
SERVER_PORT=${SERVER_PORT}
SERVER_MODE=release
RUN_MODE=standard
TZ=Asia/Shanghai

# PostgreSQL
POSTGRES_USER=sub2api
POSTGRES_PASSWORD=${pg_password}
POSTGRES_DB=sub2api

# Redis
REDIS_PASSWORD=

# 安全密钥
JWT_SECRET=${jwt_secret}
TOTP_ENCRYPTION_KEY=${totp_key}

# 管理员
ADMIN_EMAIL=${ADMIN_EMAIL}
ADMIN_PASSWORD=${ADMIN_PASSWORD}

# 日志
LOG_LEVEL=info
LOG_FORMAT=json
LOG_OUTPUT_TO_STDOUT=true
LOG_OUTPUT_TO_FILE=true

# 运维监控
OPS_ENABLED=true

# URL 安全（开发/内网环境可放宽）
SECURITY_URL_ALLOWLIST_ENABLED=false
SECURITY_URL_ALLOWLIST_ALLOW_INSECURE_HTTP=true
SECURITY_URL_ALLOWLIST_ALLOW_PRIVATE_HOSTS=true
ENVEOF

    chmod 600 "$env_file"
    ok ".env 生成完成"

    # 保存密钥信息用于后续输出
    GENERATED_PG_PASSWORD="$pg_password"
    GENERATED_JWT_SECRET="$jwt_secret"
    GENERATED_TOTP_KEY="$totp_key"
}

# ===== 数据目录 =====
create_data_dirs() {
    mkdir -p "$INSTALL_DIR/deploy/data" \
             "$INSTALL_DIR/deploy/postgres_data" \
             "$INSTALL_DIR/deploy/redis_data"
    ok "数据目录创建完成"
}

# ===== 启动服务 =====
start_services() {
    local compose_dir="$INSTALL_DIR/deploy"

    if [ "$BUILD_MODE" = "source" ]; then
        log "从源码构建并启动（首次需要较长时间，请耐心等待）..."
        $COMPOSE -f "$compose_dir/docker-compose.local.yml" \
                 -f "$compose_dir/docker-compose.build.yml" \
                 --project-directory "$compose_dir" \
                 up -d --build
    else
        log "拉取官方镜像并启动..."
        $COMPOSE -f "$compose_dir/docker-compose.local.yml" \
                 --project-directory "$compose_dir" \
                 up -d
    fi
    ok "容器启动完成"
}

# ===== 获取公网 IP =====
get_public_ip() {
    curl -fsSL --connect-timeout 5 --max-time 10 https://api.ipify.org 2>/dev/null \
    || curl -fsSL --connect-timeout 5 --max-time 10 https://ifconfig.me 2>/dev/null \
    || echo "YOUR_SERVER_IP"
}

# ===== 输出完成信息 =====
print_completion() {
    local ip
    ip=$(get_public_ip)
    local compose_dir="$INSTALL_DIR/deploy"
    local compose_files="-f $compose_dir/docker-compose.local.yml"
    if [ "$BUILD_MODE" = "source" ]; then
        compose_files="$compose_files -f $compose_dir/docker-compose.build.yml"
    fi

    echo ""
    echo -e "${CYAN}══════════════════════════════════════════════════════════════${NC}"
    echo -e "${GREEN}${BOLD}  ✅ NoWind API 部署成功！${NC}"
    echo -e "${CYAN}══════════════════════════════════════════════════════════════${NC}"
    echo ""
    echo -e "  ${BOLD}访问地址:${NC}       http://${ip}:${SERVER_PORT}"
    echo -e "  ${BOLD}管理员邮箱:${NC}     ${ADMIN_EMAIL}"
    if [ -n "$ADMIN_PASSWORD" ]; then
        echo -e "  ${BOLD}管理员密码:${NC}     ***（你设置的密码）"
    else
        echo -e "  ${BOLD}管理员密码:${NC}     ${YELLOW}自动生成，请从日志中查看：${NC}"
        echo -e "                  cd $compose_dir && $COMPOSE $compose_files logs sub2api | grep -i 'admin password'"
    fi
    echo ""
    echo -e "${CYAN}──────── 生成的安全密钥（请妥善保管）────────${NC}"
    echo -e "  POSTGRES_PASSWORD:   ${GENERATED_PG_PASSWORD}"
    echo -e "  JWT_SECRET:          ${GENERATED_JWT_SECRET}"
    echo -e "  TOTP_KEY:            ${GENERATED_TOTP_KEY}"
    echo ""
    echo -e "${CYAN}──────── 常用命令 ────────${NC}"
    echo -e "  查看日志:  cd $compose_dir && $COMPOSE $compose_files logs -f sub2api"
    echo -e "  重启服务:  cd $compose_dir && $COMPOSE $compose_files restart sub2api"
    echo -e "  停止服务:  cd $compose_dir && $COMPOSE $compose_files down"
    echo -e "  更新重建:  cd $compose_dir && git -C $INSTALL_DIR pull && $COMPOSE $compose_files up -d --build"
    echo ""
    echo -e "${CYAN}══════════════════════════════════════════════════════════════${NC}"
    echo -e "${BOLD}  📌 部署后必做：设置 Google 登录${NC}"
    echo -e "${CYAN}══════════════════════════════════════════════════════════════${NC}"
    echo ""
    echo -e "  NoWind API 支持通过 ${BOLD}通用 OIDC${NC} 接入 Google 登录。"
    echo -e "  登录管理后台后，进入 ${BOLD}系统设置 → 安全与认证 → OIDC 登录${NC}，填写："
    echo ""
    echo -e "  ${BOLD}提供商名称:${NC}      Google"
    echo -e "  ${BOLD}Client ID:${NC}        （从 Google Cloud Console 获取）"
    echo -e "  ${BOLD}Client Secret:${NC}    （从 Google Cloud Console 获取）"
    echo -e "  ${BOLD}Issuer URL:${NC}       https://accounts.google.com"
    echo -e "  ${BOLD}Scopes:${NC}           openid email profile"
    echo -e "  ${BOLD}Redirect URI:${NC}     http://${ip}:${SERVER_PORT}/api/v1/auth/oauth/oidc/callback"
    echo ""
    echo -e "  ${YELLOW}获取 Google OAuth 凭据步骤：${NC}"
    echo -e "  1. 访问 https://console.cloud.google.com/apis/credentials"
    echo -e "  2. 创建 OAuth 2.0 客户端 ID（应用类型选「Web 应用」）"
    echo -e "  3. 在「已获授权的重定向 URI」中添加："
    echo -e "     ${BOLD}http://${ip}:${SERVER_PORT}/api/v1/auth/oauth/oidc/callback${NC}"
    echo -e "  4. 如果你配了域名和 HTTPS，把上面的地址换成 https://你的域名/api/v1/auth/oauth/oidc/callback"
    echo -e "  5. 复制 Client ID 和 Client Secret 填入后台"
    echo ""
    echo -e "${CYAN}══════════════════════════════════════════════════════════════${NC}"
    echo ""
}

# ===== 主流程 =====
main() {
    check_root
    check_system
    install_docker
    interactive_config
    setup_repo
    generate_env
    create_data_dirs
    start_services

    # 等待服务启动
    log "等待服务启动..."
    sleep 8

    print_completion
}

main "$@"
