#!/usr/bin/env bash
# XIASS API 完整恢复脚本。恢复失败时自动把原数据移回。

set -Eeuo pipefail

INSTALL_DIR="${INSTALL_DIR:-}"
DEPLOY_DIR=""
LOCK_DIR="/tmp/nowind-maintenance.lock"
ASSUME_YES=false
ROLLBACK_NEEDED=false
QUARANTINE=""
FAILED_RESTORE=""
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
ARCHIVE_LAYOUT=""
RESTORE_STAGE=""

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
            if [ -f "$candidate/deploy/docker-compose.local.yml" ] || [ -f "$candidate/deploy/docker-compose.yml" ]; then
                existing+=("$candidate")
            fi
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
            || die "无法从当前容器读取命名卷；请先启动当前实例后再恢复。"
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

compose() {
    "${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" "$@"
}

application_service() {
    if compose config --services 2>/dev/null | grep -qx 'xiass-api'; then
        printf 'xiass-api\n'
    elif compose config --services 2>/dev/null | grep -qx 'nowind-api'; then
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
            .env|./.env|layout|./layout|data|data/*|./data|./data/*|postgres_data|postgres_data/*|./postgres_data|./postgres_data/*|redis_data|redis_data/*|./redis_data|./redis_data/*|volumes|volumes/*|./volumes|./volumes/*) ;;
            *) die "备份包含不允许的路径：$entry" ;;
        esac
    done < <(tar -tzf "$archive")
}

detect_archive_layout() {
    ARCHIVE_LAYOUT=$(tar -xOzf "$1" layout 2>/dev/null | awk -F= '$1 == "layout" {print $2; exit}')
    ARCHIVE_LAYOUT="${ARCHIVE_LAYOUT:-local}"
    case "$ARCHIVE_LAYOUT" in
        local|named) ;;
        *) die "备份布局无效：$ARCHIVE_LAYOUT" ;;
    esac
}

snapshot_named_volume() {
    local role="$1" volume="$2" destination="$3"
    mkdir -p "$destination"
    docker run --rm \
        -v "${volume}:/source:ro" \
        -v "${destination}:/backup" \
        alpine:3.20 \
        sh -c "tar -C /source -cf /backup/${role}.tar ."
}

restore_named_volume() {
    local role="$1" volume="$2" source="$3"
    docker run --rm \
        -v "${volume}:/target" \
        -v "${source}:/backup:ro" \
        alpine:3.20 \
        sh -c "find /target -mindepth 1 -exec rm -rf {} + && tar -C /target -xf /backup/${role}.tar"
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
        if [ "$PERSISTENCE_MODE" = "named" ]; then
            restore_named_volume app "$APP_VOLUME" "$QUARANTINE/named-volumes" || true
            restore_named_volume postgres "$POSTGRES_VOLUME" "$QUARANTINE/named-volumes" || true
            restore_named_volume redis "$REDIS_VOLUME" "$QUARANTINE/named-volumes" || true
            [ -f "$QUARANTINE/.env" ] && cp "$QUARANTINE/.env" "$DEPLOY_DIR/.env"
        else
            move_runtime_to "$FAILED_RESTORE"
            local item
            for item in .env data postgres_data redis_data; do
                [ -e "$QUARANTINE/$item" ] && mv "$QUARANTINE/$item" "$DEPLOY_DIR/$item"
            done
        fi
        init_compose
        compose up -d >/dev/null 2>&1 || true
        log "恢复前数据已移回；失败的数据保留在 $FAILED_RESTORE"
    fi
    rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
    [ -n "$RESTORE_STAGE" ] && rm -rf "$RESTORE_STAGE"
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
    [ -n "$archive" ] || die "用法：xiass-restore.sh /root/xiass-backups/xiass-runtime-时间.tar.gz"
    archive=$(readlink -f "$archive")
    [ -f "$archive" ] || die "备份文件不存在：$archive"
    command -v docker >/dev/null 2>&1 || die "缺少 Docker。"
    command -v tar >/dev/null 2>&1 || die "缺少 tar。"
    resolve_install_dir
    [ -f "$DEPLOY_DIR/docker-compose.local.yml" ] || die "请先在新服务器运行 XIASS 一键安装。"

    validate_archive "$archive"
    detect_archive_layout "$archive"
    if ! "$ASSUME_YES"; then
        [ -r /dev/tty ] || die "非交互执行请加 --yes。"
        local answer=""
        read -r -p "恢复会替换当前实例数据，确认继续？[y/N]: " answer < /dev/tty || true
        [[ "$answer" =~ ^[yY]$ ]] || exit 0
    fi

    detect_runtime_layout
    select_compose_file
    if [ "${#ACTUAL_COMPOSE_FILES[@]}" -gt 0 ]; then
        COMPOSE_FILES=("${ACTUAL_COMPOSE_FILES[@]}")
        COMPOSE_FILE="${COMPOSE_FILES[0]}"
    fi
    if [ "$ARCHIVE_LAYOUT" != "$PERSISTENCE_MODE" ]; then
        die "备份为 ${ARCHIVE_LAYOUT} 持久化布局，当前实例为 ${PERSISTENCE_MODE}；为避免误写数据，拒绝跨布局恢复。"
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
    ROLLBACK_NEEDED=true
    if [ "$PERSISTENCE_MODE" = "named" ]; then
        mkdir -p "$QUARANTINE/named-volumes"
        cp "$DEPLOY_DIR/.env" "$QUARANTINE/.env"
        snapshot_named_volume app "$APP_VOLUME" "$QUARANTINE/named-volumes"
        snapshot_named_volume postgres "$POSTGRES_VOLUME" "$QUARANTINE/named-volumes"
        snapshot_named_volume redis "$REDIS_VOLUME" "$QUARANTINE/named-volumes"
    else
        move_runtime_to "$QUARANTINE"
    fi

    log "恢复备份数据..."
    if [ "$PERSISTENCE_MODE" = "named" ]; then
        RESTORE_STAGE=$(mktemp -d)
        tar -xzf "$archive" -C "$RESTORE_STAGE"
        restore_named_volume app "$APP_VOLUME" "$RESTORE_STAGE/volumes"
        restore_named_volume postgres "$POSTGRES_VOLUME" "$RESTORE_STAGE/volumes"
        restore_named_volume redis "$REDIS_VOLUME" "$RESTORE_STAGE/volumes"
        cp "$RESTORE_STAGE/.env" "$DEPLOY_DIR/.env"
    else
        tar --same-owner -xzf "$archive" -C "$DEPLOY_DIR"
    fi
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
