#!/usr/bin/env bash
# =============================================================================
# NoWind API 一键安装脚本（VPS）
# -----------------------------------------------------------------------------
# 从源码构建并以 Docker Compose 启动（自带 PostgreSQL + Redis）。
# 部署的是本 fork 修改后的前端界面（Dockerfile 会编译并嵌入前端）。
#
# 用法（在 VPS 上以 root 或具备 sudo 的用户执行）：
#   curl -fsSL https://raw.githubusercontent.com/xyf0104/sub2api/main/deploy/nowind-install.sh | bash
# 可选环境变量：
#   APP_DIR=/opt/nowind-api   安装目录
#   PORT=8080                 对外端口
# =============================================================================
set -euo pipefail

REPO_URL="https://github.com/xyf0104/sub2api.git"
APP_DIR="${APP_DIR:-/opt/nowind-api}"
PORT="${PORT:-8080}"

log() { printf "\033[1;36m[NoWind]\033[0m %s\n" "$*"; }
err() { printf "\033[1;31m[NoWind][错误]\033[0m %s\n" "$*" >&2; }

# 0. 基础依赖
command -v git >/dev/null 2>&1 || { err "未找到 git，请先安装 git"; exit 1; }
command -v openssl >/dev/null 2>&1 || { err "未找到 openssl，请先安装 openssl"; exit 1; }

# 1. 安装 Docker（若缺失）
if ! command -v docker >/dev/null 2>&1; then
  log "未检测到 Docker，开始安装..."
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker 2>/dev/null || true
fi
if ! docker compose version >/dev/null 2>&1; then
  err "需要 Docker Compose v2（docker compose）。请升级 Docker 后重试。"
  exit 1
fi

# 2. 拉取/更新源码
if [ -d "$APP_DIR/.git" ]; then
  log "更新已有代码：$APP_DIR"
  git -C "$APP_DIR" pull --ff-only
else
  log "克隆仓库到：$APP_DIR"
  git clone --depth 1 "$REPO_URL" "$APP_DIR"
fi

cd "$APP_DIR/deploy"

# 3. 生成 .env（含随机密钥），仅首次生成
if [ ! -f .env ]; then
  log "生成 .env 与随机密钥..."
  cp .env.example .env
  setkv() {
    if grep -q "^$1=" .env; then
      sed -i.bak "s|^$1=.*|$1=$2|" .env && rm -f .env.bak
    else
      printf '%s=%s\n' "$1" "$2" >> .env
    fi
  }
  setkv POSTGRES_PASSWORD "$(openssl rand -hex 32)"
  setkv JWT_SECRET "$(openssl rand -hex 32)"
  setkv TOTP_ENCRYPTION_KEY "$(openssl rand -hex 32)"
  setkv SERVER_PORT "$PORT"
  setkv ADMIN_EMAIL "admin@nowind.local"
  ADMIN_PW="$(openssl rand -hex 8)"
  setkv ADMIN_PASSWORD "$ADMIN_PW"
  printf '%s\n' "$ADMIN_PW" > .admin_password.txt
  chmod 600 .env .admin_password.txt
  log "已生成管理员密码并保存到：$APP_DIR/deploy/.admin_password.txt"
else
  log ".env 已存在，跳过生成（保留原有密钥）"
fi

# 4. 数据目录
mkdir -p data postgres_data redis_data

# 5. 构建并启动（首次会编译前端+后端，耗时数分钟）
log "开始构建并启动（首次较慢，请耐心等待）..."
docker compose -f docker-compose.local.yml -f docker-compose.build.yml up -d --build

# 6. 提示
IP="$(curl -fsS4 https://ifconfig.me 2>/dev/null || echo '<服务器IP>')"
log "部署完成 ✅"
log "访问地址： http://${IP}:${PORT}"
log "管理员邮箱：admin@nowind.local"
log "管理员密码：见 $APP_DIR/deploy/.admin_password.txt"
log "查看日志： docker compose -f $APP_DIR/deploy/docker-compose.local.yml -f $APP_DIR/deploy/docker-compose.build.yml logs -f sub2api"
