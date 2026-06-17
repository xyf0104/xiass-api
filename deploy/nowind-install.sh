#!/usr/bin/env bash
# =============================================================================
# NoWind API 一键安装脚本（VPS）
# 从你的 fork 源码构建（含定制前端），自带 PostgreSQL + Redis，Docker 部署。
#
# 用法：
#   curl -fsSL https://raw.githubusercontent.com/xyf0104/nowind-api/main/deploy/nowind-install.sh | sudo bash
# 可选环境变量：
#   INSTALL_DIR=/opt/nowind-api  SERVER_PORT=8080  BRANCH=main
# =============================================================================
set -euo pipefail

REPO_URL="https://github.com/xyf0104/nowind-api.git"
INSTALL_DIR="${INSTALL_DIR:-/opt/nowind-api}"
BRANCH="${BRANCH:-main}"
SERVER_PORT="${SERVER_PORT:-8080}"

log() { echo -e "\033[1;36m[NoWind]\033[0m $*"; }
err() { echo -e "\033[1;31m[NoWind]\033[0m $*" >&2; }

[ "$(id -u)" -eq 0 ] || { err "请用 root 运行：curl ... | sudo bash"; exit 1; }

# 1) Docker
if ! command -v docker >/dev/null 2>&1; then
  log "安装 Docker ..."
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker 2>/dev/null || true
fi
if docker compose version >/dev/null 2>&1; then COMPOSE="docker compose";
elif command -v docker-compose >/dev/null 2>&1; then COMPOSE="docker-compose";
else err "未检测到 docker compose 插件，请先安装"; exit 1; fi

# 2) git
command -v git >/dev/null 2>&1 || { (apt-get update -y && apt-get install -y git) 2>/dev/null || yum install -y git 2>/dev/null || true; }

# 3) 克隆 / 更新仓库
if [ -d "$INSTALL_DIR/.git" ]; then
  log "更新已有仓库 $INSTALL_DIR ..."
  git -C "$INSTALL_DIR" fetch origin "$BRANCH"
  git -C "$INSTALL_DIR" reset --hard "origin/$BRANCH"
else
  log "克隆仓库到 $INSTALL_DIR ..."
  git clone -b "$BRANCH" "$REPO_URL" "$INSTALL_DIR"
fi

cd "$INSTALL_DIR/deploy"

# 4) 生成 .env（随机密钥，仅首次）
if [ ! -f .env ]; then
  log "生成 .env（随机密钥）..."
  gen() { openssl rand -hex 32; }
  cat > .env <<ENVEOF
SERVER_PORT=${SERVER_PORT}
SERVER_MODE=release
RUN_MODE=standard
TZ=Asia/Shanghai
POSTGRES_USER=sub2api
POSTGRES_PASSWORD=$(gen)
POSTGRES_DB=sub2api
JWT_SECRET=$(gen)
TOTP_ENCRYPTION_KEY=$(gen)
ADMIN_EMAIL=admin@nowind.local
ADMIN_PASSWORD=
ENVEOF
  chmod 600 .env
else
  log ".env 已存在，复用现有配置"
fi

# 5) 数据目录
mkdir -p data postgres_data redis_data

# 6) 构建并启动（首次会编译前端+后端，约 3-8 分钟）
log "开始构建并启动容器（首次较慢，请耐心等待）..."
$COMPOSE -f docker-compose.local.yml -f docker-compose.build.yml up -d --build

# 7) 输出访问信息
sleep 6
IP="$(curl -fsSL https://api.ipify.org 2>/dev/null || echo YOUR_SERVER_IP)"
log "================ 部署完成 ================"
log "访问地址 : http://${IP}:${SERVER_PORT}"
log "管理员邮箱 : $(grep -E '^ADMIN_EMAIL=' .env | cut -d= -f2-)"
PWD_LINE="$($COMPOSE -f docker-compose.local.yml -f docker-compose.build.yml logs sub2api 2>/dev/null | grep -i 'admin password' | tail -1 || true)"
if [ -n "$PWD_LINE" ]; then
  log "管理员密码 : ${PWD_LINE}"
else
  log "管理员密码 : 请查看日志获取（首次启动后）："
  log "  cd $INSTALL_DIR/deploy && $COMPOSE -f docker-compose.local.yml -f docker-compose.build.yml logs sub2api | grep -i 'admin password'"
fi
log "查看日志 : cd $INSTALL_DIR/deploy && $COMPOSE -f docker-compose.local.yml -f docker-compose.build.yml logs -f sub2api"
log "=========================================="
