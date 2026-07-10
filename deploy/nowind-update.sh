#!/usr/bin/env bash
# NoWind API 数据安全更新：先完整备份，再更新代码/镜像并重建应用。

set -Eeuo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/nowind-api}"
DEPLOY_DIR="$INSTALL_DIR/deploy"
RAW_BASE_URL="https://raw.githubusercontent.com/xyf0104/nowind-api/main/deploy"
BACKUP_DIR="${BACKUP_DIR:-/root/nowind-backups}"
COMPOSE=()
COMPOSE_ARGS=()

log() { printf '[NoWind] %s\n' "$*"; }
die() { printf '[NoWind] 错误：%s\n' "$*" >&2; exit 1; }

read_env_value() {
    local key="$1"
    awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$DEPLOY_DIR/.env" 2>/dev/null
}

init_compose() {
    if docker compose version >/dev/null 2>&1; then
        COMPOSE=(docker compose)
    elif command -v docker-compose >/dev/null 2>&1; then
        COMPOSE=(docker-compose)
    else
        die "缺少 Docker Compose。"
    fi
    COMPOSE_ARGS=(-f "$DEPLOY_DIR/docker-compose.local.yml")
    if [ "$(read_env_value NOWIND_BUILD_MODE)" = "source" ]; then
        COMPOSE_ARGS+=(-f "$DEPLOY_DIR/docker-compose.build.yml")
    fi
    COMPOSE_ARGS+=(--project-directory "$DEPLOY_DIR")
}

compose() {
    "${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" "$@"
}

wait_for_health() {
    local port attempt
    port=$(read_env_value SERVER_PORT)
    port="${port:-8080}"
    for attempt in $(seq 1 120); do
        if curl -fsS --max-time 3 "http://127.0.0.1:${port}/health" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    return 1
}

main() {
    [ "$(id -u)" -eq 0 ] || die "请使用 sudo 或 root 运行。"
    for command_name in curl git docker; do
        command -v "$command_name" >/dev/null 2>&1 || die "缺少依赖：$command_name"
    done
    [ -d "$INSTALL_DIR/.git" ] || die "$INSTALL_DIR 不是 NoWind Git 安装目录。"
    [ -f "$DEPLOY_DIR/.env" ] || die "未找到 .env。"

    log "先创建更新前完整备份..."
    curl -fsSL "$RAW_BASE_URL/nowind-backup.sh" \
        | INSTALL_DIR="$INSTALL_DIR" BACKUP_DIR="$BACKUP_DIR" bash

    local patch_file=""
    if ! git -C "$INSTALL_DIR" diff --quiet || ! git -C "$INSTALL_DIR" diff --cached --quiet; then
        patch_file="/root/nowind-local-changes-$(date +%Y%m%d-%H%M%S).patch"
        git -C "$INSTALL_DIR" diff HEAD > "$patch_file"
        chmod 600 "$patch_file"
        log "本地源码修改已备份到 $patch_file"
    fi

    log "同步最新部署文件..."
    git -C "$INSTALL_DIR" fetch --prune origin main
    git -C "$INSTALL_DIR" reset --hard origin/main
    init_compose

    if [ "$(read_env_value NOWIND_BUILD_MODE)" = "source" ]; then
        log "从最新源码重新构建应用容器..."
        compose up -d --no-deps --build --force-recreate sub2api
    else
        log "拉取最新正式镜像并只重建应用容器..."
        compose pull sub2api watchtower
        compose up -d --no-deps --force-recreate sub2api
        compose up -d --no-deps watchtower
    fi

    if ! wait_for_health; then
        compose ps || true
        compose logs --tail 160 sub2api || true
        die "更新后健康检查失败。数据没有删除，更新前备份位于 $BACKUP_DIR，可使用 nowind-restore.sh 恢复。"
    fi

    printf '\nNoWind 更新完成，PostgreSQL、Redis、应用数据和 .env 均沿用原目录。\n'
    [ -n "$patch_file" ] && printf '原本的本地修改补丁：%s\n' "$patch_file"
}

main "$@"
