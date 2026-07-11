#!/usr/bin/env bash
# NoWind API 完整恢复脚本。恢复失败时自动把原数据移回。

set -Eeuo pipefail

INSTALL_DIR="${INSTALL_DIR:-/opt/nowind-api}"
DEPLOY_DIR="$INSTALL_DIR/deploy"
LOCK_DIR="/tmp/nowind-maintenance.lock"
ASSUME_YES=false
ROLLBACK_NEEDED=false
QUARANTINE=""
FAILED_RESTORE=""
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

application_service() {
    if compose config --services 2>/dev/null | grep -qx 'nowind-api'; then
        printf 'nowind-api\n'
    else
        printf 'sub2api\n'
    fi
}

validate_archive() {
    local archive="$1" entry
    if [ -f "$archive.sha256" ] && command -v sha256sum >/dev/null 2>&1; then
        (cd "$(dirname "$archive")" && sha256sum -c "$(basename "$archive").sha256") \
            || die "备份校验失败。"
    fi
    while IFS= read -r entry; do
        case "$entry" in
            .env|./.env|data|data/*|./data|./data/*|postgres_data|postgres_data/*|./postgres_data|./postgres_data/*|redis_data|redis_data/*|./redis_data|./redis_data/*) ;;
            *) die "备份包含不允许的路径：$entry" ;;
        esac
    done < <(tar -tzf "$archive")
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

move_runtime_to() {
    local destination="$1" item
    mkdir -p "$destination"
    for item in .env data postgres_data redis_data; do
        [ -e "$DEPLOY_DIR/$item" ] && mv "$DEPLOY_DIR/$item" "$destination/$item"
    done
}

cleanup() {
    local status=$?
    set +e
    if [ "$status" -ne 0 ] && "$ROLLBACK_NEEDED" && [ -d "$QUARANTINE" ]; then
        log "恢复流程异常，正在自动移回恢复前数据..."
        compose down >/dev/null 2>&1 || true
        move_runtime_to "$FAILED_RESTORE"
        local item
        for item in .env data postgres_data redis_data; do
            [ -e "$QUARANTINE/$item" ] && mv "$QUARANTINE/$item" "$DEPLOY_DIR/$item"
        done
        init_compose
        compose up -d >/dev/null 2>&1 || true
        log "恢复前数据已移回；失败的数据保留在 $FAILED_RESTORE"
    fi
    rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
    exit "$status"
}

main() {
    [ "$(id -u)" -eq 0 ] || die "请使用 sudo 或 root 运行。"
    local archive=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --yes|-y) ASSUME_YES=true ;;
            *) [ -z "$archive" ] || die "只能指定一个备份文件。"; archive="$1" ;;
        esac
        shift
    done
    [ -n "$archive" ] || die "用法：nowind-restore.sh /root/nowind-backups/nowind-runtime-时间.tar.gz"
    archive=$(readlink -f "$archive")
    [ -f "$archive" ] || die "备份文件不存在：$archive"
    [ -f "$DEPLOY_DIR/docker-compose.local.yml" ] || die "请先在新服务器运行 NoWind 一键安装。"
    command -v docker >/dev/null 2>&1 || die "缺少 Docker。"
    command -v tar >/dev/null 2>&1 || die "缺少 tar。"

    validate_archive "$archive"
    if ! "$ASSUME_YES"; then
        [ -r /dev/tty ] || die "非交互执行请加 --yes。"
        local answer=""
        read -r -p "恢复会替换当前实例数据，确认继续？[y/N]: " answer < /dev/tty || true
        [[ "$answer" =~ ^[yY]$ ]] || exit 0
    fi

    mkdir "$LOCK_DIR" 2>/dev/null || die "已有安装、更新、备份或恢复任务正在运行。"
    trap cleanup EXIT
    trap 'exit 130' INT TERM
    init_compose

    local timestamp
    timestamp=$(date +%Y%m%d-%H%M%S)
    QUARANTINE="$INSTALL_DIR/restore-quarantine-$timestamp"
    FAILED_RESTORE="$INSTALL_DIR/failed-restore-$timestamp"

    log "停止容器（不会删除卷）..."
    compose down
    move_runtime_to "$QUARANTINE"
    ROLLBACK_NEEDED=true

    log "恢复备份数据..."
    tar --same-owner -xzf "$archive" -C "$DEPLOY_DIR"
    chmod 600 "$DEPLOY_DIR/.env"
    init_compose
    compose up -d

    if wait_for_health; then
        ROLLBACK_NEEDED=false
        printf '\n恢复完成。原数据已保留在：%s\n' "$QUARANTINE"
        printf '确认运行稳定后再自行清理该隔离目录。\n'
        return
    fi

    compose ps || true
    compose logs --tail 160 "$(application_service)" || true
    die "恢复后的服务未通过健康检查。"
}

main "$@"
