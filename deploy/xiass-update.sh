#!/usr/bin/env bash
# XIASS API 数据安全更新：先完整备份，再更新代码/镜像并重建应用。

set -Eeuo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/nowind-api}"
DEPLOY_DIR="$INSTALL_DIR/deploy"
RAW_BASE_URL="https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy"
BACKUP_DIR="${BACKUP_DIR:-/root/xiass-backups}"
LOCK_DIR="/tmp/nowind-maintenance.lock"
COMPOSE=()
COMPOSE_ARGS=()
PREVIOUS_REF=""
UPDATE_STARTED=false
UPDATE_SUCCEEDED=false
LOCK_HELD=false

log() { printf '[XIASS] %s\n' "$*"; }
die() { printf '[XIASS] 错误：%s\n' "$*" >&2; exit 1; }

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

rollback_update() {
    set +e
    log "更新失败，正在恢复原 Git 状态和旧容器栈..."

    if [ -f "$DEPLOY_DIR/docker-compose.local.yml" ]; then
        init_compose
        compose down >/dev/null 2>&1 || true
    fi

    if ! git -C "$INSTALL_DIR" reset --hard "$PREVIOUS_REF" >/dev/null 2>&1; then
        log "无法自动恢复 Git 到 $PREVIOUS_REF；更新前完整备份仍位于 $BACKUP_DIR。"
        return
    fi

    init_compose
    if compose up -d >/dev/null 2>&1; then
        if wait_for_health; then
            log "旧版本容器栈已恢复。"
        else
            log "旧栈已重新启动但健康检查未及时通过，请检查容器日志。"
        fi
    else
        log "旧栈自动启动失败，请在 $DEPLOY_DIR 使用原 compose 文件手动启动。"
    fi
}

cleanup() {
    local status=$?
    trap - EXIT INT TERM
    if [ "$status" -ne 0 ] && "$UPDATE_STARTED" && ! "$UPDATE_SUCCEEDED"; then
        rollback_update
    fi
    if "$LOCK_HELD"; then
        rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
    fi
    exit "$status"
}

main() {
    [ "$(id -u)" -eq 0 ] || die "请使用 sudo 或 root 运行。"
    for command_name in curl git docker; do
        command -v "$command_name" >/dev/null 2>&1 || die "缺少依赖：$command_name"
    done
    [ -d "$INSTALL_DIR/.git" ] || die "$INSTALL_DIR 不是 XIASS Git 安装目录。"
    [ -f "$DEPLOY_DIR/.env" ] || die "未找到 .env。"

    PREVIOUS_REF=$(git -C "$INSTALL_DIR" rev-parse HEAD)

    log "先创建更新前完整备份..."
    curl -fsSL "$RAW_BASE_URL/xiass-backup.sh" \
        | INSTALL_DIR="$INSTALL_DIR" BACKUP_DIR="$BACKUP_DIR" bash

    mkdir "$LOCK_DIR" 2>/dev/null || die "已有安装、更新、备份或恢复任务正在运行。"
    LOCK_HELD=true
    trap cleanup EXIT
    trap 'exit 130' INT TERM

    local patch_file=""
    if ! git -C "$INSTALL_DIR" diff --quiet || ! git -C "$INSTALL_DIR" diff --cached --quiet; then
        patch_file="/root/xiass-local-changes-$(date +%Y%m%d-%H%M%S).patch"
        git -C "$INSTALL_DIR" diff HEAD > "$patch_file"
        chmod 600 "$patch_file"
        log "本地源码修改已备份到 $patch_file"
    fi

    log "同步最新部署文件..."
    git -C "$INSTALL_DIR" fetch --prune origin main
    init_compose

    log "停止当前容器栈以迁移运行时名称（不会删除卷或数据）..."
    UPDATE_STARTED=true
    compose down

    git -C "$INSTALL_DIR" reset --hard origin/main
    init_compose

    if [ "$(read_env_value NOWIND_BUILD_MODE)" = "source" ]; then
        log "从最新源码重新构建并启动 XIASS 容器栈..."
        compose up -d --build
    else
        log "拉取最新正式镜像并启动 XIASS 容器栈..."
        compose pull nowind-api watchtower
        compose up -d
    fi

    if ! wait_for_health; then
        compose ps || true
        compose logs --tail 160 nowind-api || true
        die "更新后健康检查失败。数据没有删除，更新前备份位于 $BACKUP_DIR，可使用 xiass-restore.sh 恢复。"
    fi

    UPDATE_SUCCEEDED=true
    trap - EXIT INT TERM
    rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
    LOCK_HELD=false
    printf '\nXIASS 更新完成，PostgreSQL、Redis、应用数据和 .env 均沿用原目录。\n'
    [ -n "$patch_file" ] && printf '原本的本地修改补丁：%s\n' "$patch_file"
}

main "$@"
