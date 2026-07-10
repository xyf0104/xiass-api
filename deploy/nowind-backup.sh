#!/usr/bin/env bash
# NoWind API 完整离线一致性备份：.env、应用数据、PostgreSQL、Redis。

set -Eeuo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/nowind-api}"
DEPLOY_DIR="$INSTALL_DIR/deploy"
BACKUP_DIR="${BACKUP_DIR:-/root/nowind-backups}"
KEEP_BACKUPS="${KEEP_BACKUPS:-10}"
LOCK_DIR="/tmp/nowind-maintenance.lock"
STACK_STOPPED=false
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

cleanup() {
    local status=$?
    if "$STACK_STOPPED"; then
        log "备份流程中断，正在恢复原服务..."
        compose up -d >/dev/null 2>&1 || true
    fi
    rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
    exit "$status"
}

wait_for_health() {
    local port attempt
    port=$(read_env_value SERVER_PORT)
    port="${port:-8080}"
    for attempt in $(seq 1 90); do
        if curl -fsS --max-time 3 "http://127.0.0.1:${port}/health" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    return 1
}

prune_old_backups() {
    local index=0 file
    while IFS= read -r file; do
        index=$((index + 1))
        if [ "$index" -gt "$KEEP_BACKUPS" ]; then
            rm -f -- "$file" "$file.sha256"
        fi
    done < <(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'nowind-runtime-*.tar.gz' -printf '%T@ %p\n' | sort -nr | cut -d' ' -f2-)
}

main() {
    [ "$(id -u)" -eq 0 ] || die "请使用 sudo 或 root 运行。"
    command -v docker >/dev/null 2>&1 || die "缺少 Docker。"
    command -v curl >/dev/null 2>&1 || die "缺少 curl。"
    [ -f "$DEPLOY_DIR/.env" ] || die "未找到 $DEPLOY_DIR/.env，请先安装 NoWind。"
    [ -f "$DEPLOY_DIR/docker-compose.local.yml" ] || die "未找到 Docker Compose 配置。"
    for path in data postgres_data redis_data; do
        [ -d "$DEPLOY_DIR/$path" ] || die "持久化目录缺失：$DEPLOY_DIR/$path"
    done
    [[ "$KEEP_BACKUPS" =~ ^[0-9]+$ ]] && [ "$KEEP_BACKUPS" -ge 1 ] || die "KEEP_BACKUPS 必须是正整数。"
    mkdir "$LOCK_DIR" 2>/dev/null || die "已有安装、更新、备份或恢复任务正在运行。"
    trap cleanup EXIT INT TERM

    init_compose
    mkdir -p "$BACKUP_DIR"
    chmod 700 "$BACKUP_DIR"

    local timestamp archive temp_archive
    timestamp=$(date +%Y%m%d-%H%M%S)
    archive="$BACKUP_DIR/nowind-runtime-${timestamp}.tar.gz"
    temp_archive="$archive.partial"

    log "停止容器以创建一致性备份（不会删除卷或数据）..."
    compose down
    STACK_STOPPED=true

    log "归档 .env、应用数据、PostgreSQL 和 Redis..."
    tar --numeric-owner -czf "$temp_archive" \
        -C "$DEPLOY_DIR" .env data postgres_data redis_data
    mv "$temp_archive" "$archive"
    chmod 600 "$archive"

    if command -v sha256sum >/dev/null 2>&1; then
        (cd "$BACKUP_DIR" && sha256sum "$(basename "$archive")" > "$(basename "$archive").sha256")
        chmod 600 "$archive.sha256"
    fi

    log "重新启动 NoWind..."
    compose up -d
    STACK_STOPPED=false
    wait_for_health || die "备份已完成，但服务未及时恢复，请运行 docker compose logs 检查。"

    prune_old_backups
    trap - EXIT INT TERM
    rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
    printf '\n备份完成：%s\n' "$archive"
    printf '备份包含 .env 密钥，请只保存在可信位置。默认保留最近 %s 份。\n' "$KEEP_BACKUPS"
}

main "$@"
