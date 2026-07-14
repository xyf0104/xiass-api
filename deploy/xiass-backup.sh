#!/usr/bin/env bash
# XIASS API 完整离线一致性备份：.env、应用数据、PostgreSQL、Redis。

set -Eeuo pipefail

INSTALL_DIR="${INSTALL_DIR:-}"
DEPLOY_DIR=""
BACKUP_DIR="${BACKUP_DIR:-/root/xiass-backups}"
KEEP_BACKUPS="${KEEP_BACKUPS:-10}"
LOCK_DIR="/tmp/nowind-maintenance.lock"
SKIP_MAINTENANCE_LOCK="${SKIP_MAINTENANCE_LOCK:-false}"
LOCK_HELD=false
STACK_STOPPED=false
COMPOSE=()
COMPOSE_ARGS=()
COMPOSE_FILES=()
PERSISTENCE_MODE="${PERSISTENCE_MODE:-}"
COMPOSE_FILE=""
ACTUAL_COMPOSE_FILE=""
ACTUAL_COMPOSE_FILES=()
APP_CONTAINER=""
POSTGRES_CONTAINER=""
REDIS_CONTAINER=""
APP_VOLUME=""
POSTGRES_VOLUME=""
REDIS_VOLUME=""

log() { printf '[XIASS] %s\n' "$*"; }
die() { printf '[XIASS] 错误：%s\n' "$*" >&2; exit 1; }

read_env_value() {
    local key="$1"
    awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$DEPLOY_DIR/.env" 2>/dev/null
}

read_env_compat() {
    local value
    value=$(read_env_value "$1")
    [ -n "$value" ] || value=$(read_env_value "$2")
    printf '%s\n' "$value"
}

container_exists() {
    docker container inspect "$1" >/dev/null 2>&1
}

resolve_install_dir() {
    local candidate working_dir install_candidate
    local -A discovered=()
    [ -n "$INSTALL_DIR" ] || {
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
            for install_candidate in "${!discovered[@]}"; do INSTALL_DIR="$install_candidate"; done
        fi
    }
    if [ -z "$INSTALL_DIR" ]; then
        local existing=()
        for candidate in /opt/xiass-api /opt/nowind-api /opt/sub2api; do
            if [ -f "$candidate/deploy/.env" ]; then existing+=("$candidate"); fi
        done
        if [ "${#existing[@]}" -gt 1 ]; then
            die "检测到多个安装目录但没有可用的运行容器标签；请显式设置 INSTALL_DIR 后重试。"
        fi
        INSTALL_DIR="${existing[0]:-/opt/xiass-api}"
    fi
    DEPLOY_DIR="$INSTALL_DIR/deploy"
}

mount_info() {
    local container="$1" destination="$2"
    docker inspect --type container \
        --format "{{range .Mounts}}{{if eq .Destination \"${destination}\"}}{{.Type}}|{{.Name}}{{end}}{{end}}" \
        "$container" 2>/dev/null || true
}

detect_runtime_layout() {
    local candidate mount_type mount label_file labels project_dir
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
        xiass-api) POSTGRES_CONTAINER="xiass-api-postgres"; REDIS_CONTAINER="xiass-api-redis" ;;
        nowind-api) POSTGRES_CONTAINER="nowind-api-postgres"; REDIS_CONTAINER="nowind-api-redis" ;;
        sub2api) POSTGRES_CONTAINER="sub2api-postgres"; REDIS_CONTAINER="sub2api-redis" ;;
    esac
    container_exists "$POSTGRES_CONTAINER" || POSTGRES_CONTAINER=""
    container_exists "$REDIS_CONTAINER" || REDIS_CONTAINER=""
    if [ -z "$PERSISTENCE_MODE" ] && [ -n "$POSTGRES_CONTAINER" ]; then
        mount=$(mount_info "$POSTGRES_CONTAINER" /var/lib/postgresql/data)
        mount_type=${mount%%|*}
        case "$mount_type" in
            volume) PERSISTENCE_MODE="named" ;;
            bind) PERSISTENCE_MODE="local" ;;
        esac
    fi
    if [ -z "$PERSISTENCE_MODE" ]; then
        [ -d "$DEPLOY_DIR/postgres_data" ] && PERSISTENCE_MODE="local" || PERSISTENCE_MODE="local"
    fi
    if [ "$PERSISTENCE_MODE" = "named" ]; then
        APP_VOLUME="$(mount_info "$APP_CONTAINER" /app/data | cut -d'|' -f2)"
        POSTGRES_VOLUME="$(mount_info "$POSTGRES_CONTAINER" /var/lib/postgresql/data | cut -d'|' -f2)"
        REDIS_VOLUME="$(mount_info "$REDIS_CONTAINER" /data | cut -d'|' -f2)"
        [ -n "$APP_VOLUME" ] && [ -n "$POSTGRES_VOLUME" ] && [ -n "$REDIS_VOLUME" ] \
            || die "无法从当前容器读取命名卷；请在服务运行时执行，或显式设置 PERSISTENCE_MODE=local。"
    fi
    if [ -n "$APP_CONTAINER" ]; then
        labels=$(docker inspect --type container \
            --format '{{ index .Config.Labels "com.docker.compose.project.config_files" }}' \
            "$APP_CONTAINER" 2>/dev/null || true)
        project_dir=$(docker inspect --type container \
            --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}' \
            "$APP_CONTAINER" 2>/dev/null || true)
        IFS=',' read -r -a label_files <<< "$labels"
        for label_file in "${label_files[@]}"; do
            if [ -n "$label_file" ] && [ "${label_file#/}" = "$label_file" ] && [ -n "$project_dir" ]; then
                label_file="$project_dir/$label_file"
            fi
            if [ -f "$label_file" ]; then
                ACTUAL_COMPOSE_FILES+=("$label_file")
                [ -n "$ACTUAL_COMPOSE_FILE" ] || ACTUAL_COMPOSE_FILE="$label_file"
            fi
        done
    fi
    log "当前应用容器：${APP_CONTAINER:-未运行}；PostgreSQL 容器：${POSTGRES_CONTAINER:-未运行}；持久化模式：$PERSISTENCE_MODE"
}

select_compose_file() {
    local canonical_only="${1:-false}" canonical_file
    case "$PERSISTENCE_MODE" in
        named) canonical_file="$DEPLOY_DIR/docker-compose.yml" ;;
        local) canonical_file="$DEPLOY_DIR/docker-compose.local.yml" ;;
    esac
    if [ "$canonical_only" = "true" ] || [ -z "$ACTUAL_COMPOSE_FILE" ]; then
        COMPOSE_FILE="$canonical_file"
    else
        COMPOSE_FILE="$ACTUAL_COMPOSE_FILE"
    fi
    [ -f "$COMPOSE_FILE" ] || die "未找到 Docker Compose 配置：$COMPOSE_FILE"
}

init_compose() {
    if docker compose version >/dev/null 2>&1; then
        COMPOSE=(docker compose)
    elif command -v docker-compose >/dev/null 2>&1; then
        COMPOSE=(docker-compose)
    else
        die "缺少 Docker Compose。"
    fi

    if [ "${#COMPOSE_FILES[@]}" -eq 0 ]; then
        COMPOSE_FILES=("$COMPOSE_FILE")
        if [ "$(read_env_compat XIASS_BUILD_MODE NOWIND_BUILD_MODE)" = "source" ]; then
            COMPOSE_FILES+=("$DEPLOY_DIR/docker-compose.build.yml")
        fi
    fi
    COMPOSE_ARGS=()
    local compose_file
    for compose_file in "${COMPOSE_FILES[@]}"; do
        COMPOSE_ARGS+=(-f "$compose_file")
    done
    COMPOSE_ARGS+=(--project-directory "$DEPLOY_DIR")
}

archive_named_volume() {
    local role="$1" volume="$2" stage="$3"
    docker run --rm \
        -v "${volume}:/source:ro" \
        -v "${stage}/volumes:/backup" \
        alpine:3.20 \
        sh -c "tar -C /source -cf /backup/${role}.tar ."
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
    if "$LOCK_HELD"; then
        rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
    fi
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
    done < <(find "$BACKUP_DIR" -maxdepth 1 -type f -name 'xiass-runtime-*.tar.gz' -printf '%T@ %p\n' | sort -nr | cut -d' ' -f2-)
}

main() {
    [ "$(id -u)" -eq 0 ] || die "请使用 sudo 或 root 运行。"
    command -v docker >/dev/null 2>&1 || die "缺少 Docker。"
    command -v curl >/dev/null 2>&1 || die "缺少 curl。"
    resolve_install_dir
    [ -f "$DEPLOY_DIR/.env" ] || die "未找到 $DEPLOY_DIR/.env，请先安装 XIASS。"
    detect_runtime_layout
    select_compose_file
    if [ "${#ACTUAL_COMPOSE_FILES[@]}" -gt 0 ]; then
        COMPOSE_FILES=("${ACTUAL_COMPOSE_FILES[@]}")
        COMPOSE_FILE="${COMPOSE_FILES[0]}"
    fi
    if [ "$PERSISTENCE_MODE" = "local" ]; then
        for path in data postgres_data redis_data; do
            [ -d "$DEPLOY_DIR/$path" ] || die "持久化目录缺失：$DEPLOY_DIR/$path"
        done
    fi
    [[ "$KEEP_BACKUPS" =~ ^[0-9]+$ ]] && [ "$KEEP_BACKUPS" -ge 1 ] || die "KEEP_BACKUPS 必须是正整数。"
    if [ "$SKIP_MAINTENANCE_LOCK" = "true" ]; then
        log "使用父级更新事务锁。"
    else
        mkdir "$LOCK_DIR" 2>/dev/null || die "已有安装、更新、备份或恢复任务正在运行。"
        LOCK_HELD=true
    fi
    trap cleanup EXIT INT TERM

    init_compose
    mkdir -p "$BACKUP_DIR"
    chmod 700 "$BACKUP_DIR"

    local timestamp archive temp_archive stage
    timestamp=$(date +%Y%m%d-%H%M%S)
    archive="$BACKUP_DIR/xiass-runtime-${timestamp}.tar.gz"
    temp_archive="$archive.partial"

    log "停止容器以创建一致性备份（不会删除卷或数据）..."
    compose down
    STACK_STOPPED=true

    log "归档 .env、应用数据、PostgreSQL 和 Redis..."
    if [ "$PERSISTENCE_MODE" = "local" ]; then
        tar --numeric-owner -czf "$temp_archive" \
            -C "$DEPLOY_DIR" .env data postgres_data redis_data
    else
        stage=$(mktemp -d)
        mkdir -p "$stage/volumes"
        cp "$DEPLOY_DIR/.env" "$stage/.env"
        printf 'layout=named\n' > "$stage/layout"
        archive_named_volume app "$APP_VOLUME" "$stage"
        archive_named_volume postgres "$POSTGRES_VOLUME" "$stage"
        archive_named_volume redis "$REDIS_VOLUME" "$stage"
        tar --numeric-owner -czf "$temp_archive" -C "$stage" .env layout volumes
        rm -rf "$stage"
    fi
    mv "$temp_archive" "$archive"
    chmod 600 "$archive"

    if command -v sha256sum >/dev/null 2>&1; then
        (cd "$BACKUP_DIR" && sha256sum "$(basename "$archive")" > "$(basename "$archive").sha256")
        chmod 600 "$archive.sha256"
    fi

    log "重新启动 XIASS..."
    compose up -d
    STACK_STOPPED=false
    wait_for_health || die "备份已完成，但服务未及时恢复，请运行 docker compose logs 检查。"

    prune_old_backups
    trap - EXIT INT TERM
    if "$LOCK_HELD"; then
        rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
        LOCK_HELD=false
    fi
    printf '\n备份完成：%s\n' "$archive"
    printf '备份包含 .env 密钥，请只保存在可信位置。默认保留最近 %s 份。\n' "$KEEP_BACKUPS"
}

main "$@"
