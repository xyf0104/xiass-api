#!/usr/bin/env bash
# XIASS API 数据安全更新：先完整备份，再更新代码/镜像并重建应用。

set -Eeuo pipefail

INSTALL_DIR="${INSTALL_DIR:-}"
DEPLOY_DIR=""
RAW_BASE_URL="https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy"
CANONICAL_UPSTREAM_URL="https://github.com/xyf0104/xiass-api.git"
CANONICAL_UPSTREAM_REMOTE="xiass-upstream"
BACKUP_DIR="${BACKUP_DIR:-/root/xiass-backups}"
LOCK_DIR="/tmp/nowind-maintenance.lock"
COMPOSE=()
COMPOSE_ARGS=()
COMPOSE_FILES=()
PERSISTENCE_MODE="${PERSISTENCE_MODE:-}"
COMPOSE_FILE=""
ACTUAL_COMPOSE_FILE=""
ACTUAL_COMPOSE_LABELS=""
ACTUAL_COMPOSE_FILES=()
APP_CONTAINER=""
POSTGRES_CONTAINER=""
PREVIOUS_REF=""
PREVIOUS_IMAGE_ID=""
PREVIOUS_IMAGE_REF=""
PREVIOUS_COMPOSE_SNAPSHOT=""
PREVIOUS_BUILD_SNAPSHOT=""
PREVIOUS_COMPOSE_FILES=()
UPDATE_REMOTE=""
UPDATE_REF=""
UPDATE_STARTED=false
UPDATE_SUCCEEDED=false
LOCK_HELD=false
CREATED_UPDATE_REMOTE=false

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

is_canonical_origin() {
    local remote_url="${1%/}"
    case "$remote_url" in
        "https://github.com/xyf0104/xiass-api"|"https://github.com/xyf0104/xiass-api.git"|\
        "git@github.com:xyf0104/xiass-api"|"git@github.com:xyf0104/xiass-api.git"|\
        "ssh://git@github.com/xyf0104/xiass-api"|"ssh://git@github.com/xyf0104/xiass-api.git")
            return 0
            ;;
    esac
    return 1
}

is_known_legacy_origin() {
    local remote_url="${1%/}"
    case "$remote_url" in
        "https://github.com/xyf0104/nowind-api"|"https://github.com/xyf0104/nowind-api.git"|\
        "git@github.com:xyf0104/nowind-api"|"git@github.com:xyf0104/nowind-api.git"|\
        "ssh://git@github.com/xyf0104/nowind-api"|"ssh://git@github.com/xyf0104/nowind-api.git")
            return 0
            ;;
    esac
    return 1
}

ensure_xiass_update_remote() {
    local current_origin current_upstream allow_migration
    current_upstream=$(git -C "$INSTALL_DIR" remote get-url "$CANONICAL_UPSTREAM_REMOTE" 2>/dev/null || true)
    if [ -n "$current_upstream" ]; then
        is_canonical_origin "$current_upstream" \
            || die "现有 xiass-upstream 不是 XIASS API 官方来源；为保护自定义配置，未执行更新。"
        UPDATE_REMOTE="$CANONICAL_UPSTREAM_REMOTE"
        return 0
    fi

    current_origin=$(git -C "$INSTALL_DIR" remote get-url origin 2>/dev/null || true)
    if is_canonical_origin "$current_origin"; then
        UPDATE_REMOTE="origin"
        return 0
    fi

    allow_migration="${XIASS_ALLOW_ORIGIN_MIGRATION:-0}"
    if ! is_known_legacy_origin "$current_origin" && [ "$allow_migration" != "1" ]; then
        die "检测到非官方历史 Git origin；为保护自定义 fork，未自动切换更新来源。确认后可设置 XIASS_ALLOW_ORIGIN_MIGRATION=1 重试。"
    fi

    git -C "$INSTALL_DIR" remote add "$CANONICAL_UPSTREAM_REMOTE" "$CANONICAL_UPSTREAM_URL"
    CREATED_UPDATE_REMOTE=true
    UPDATE_REMOTE="$CANONICAL_UPSTREAM_REMOTE"
    log "已为历史安装添加 XIASS API 更新来源；原 Git origin 保持不变。"
}

remove_created_update_remote() {
    if [ "$CREATED_UPDATE_REMOTE" != true ]; then
        return 0
    fi
    if git -C "$INSTALL_DIR" remote remove "$CANONICAL_UPSTREAM_REMOTE"; then
        CREATED_UPDATE_REMOTE=false
        log "已移除本次更新新增的 XIASS API 更新来源。"
        return 0
    fi
    log "错误：无法自动移除本次更新新增的 XIASS API 更新来源。"
    return 1
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
            if [ -d "$candidate/.git" ] || [ -f "$candidate/deploy/.env" ]; then
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

detect_runtime_layout() {
    local candidate mount_type label_file project_dir
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
        if [ -d "$DEPLOY_DIR/postgres_data" ] || [ -d "$DEPLOY_DIR/redis_data" ]; then
            PERSISTENCE_MODE="local"
        else
            PERSISTENCE_MODE="local"
        fi
    fi
    if [ -n "$APP_CONTAINER" ]; then
        ACTUAL_COMPOSE_LABELS=$(docker inspect --type container \
            --format '{{ index .Config.Labels "com.docker.compose.project.config_files" }}' \
            "$APP_CONTAINER" 2>/dev/null || true)
        project_dir=$(docker inspect --type container \
            --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}' \
            "$APP_CONTAINER" 2>/dev/null || true)
        IFS=',' read -r -a label_files <<< "$ACTUAL_COMPOSE_LABELS"
        for label_file in "${label_files[@]}"; do
            if [ -n "$label_file" ] && [ "${label_file#/}" = "$label_file" ] && [ -n "$project_dir" ]; then
                label_file="$project_dir/$label_file"
            fi
            if [ -f "$label_file" ]; then
                ACTUAL_COMPOSE_FILES+=("$label_file")
                if [ -z "$ACTUAL_COMPOSE_FILE" ]; then
                    ACTUAL_COMPOSE_FILE="$label_file"
                fi
            fi
        done
    fi
    log "当前应用容器：${APP_CONTAINER:-未运行}；PostgreSQL 容器：${POSTGRES_CONTAINER:-未运行}；持久化模式：$PERSISTENCE_MODE；实际 Compose：${ACTUAL_COMPOSE_FILE:-由挂载类型选择}"
}

select_compose_file() {
    local canonical_only="${1:-false}" canonical_file
    case "$PERSISTENCE_MODE" in
        named) canonical_file="$DEPLOY_DIR/docker-compose.yml" ;;
        local) canonical_file="$DEPLOY_DIR/docker-compose.local.yml" ;;
        *) die "未知持久化模式：$PERSISTENCE_MODE" ;;
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
        [ -n "$COMPOSE_FILE" ] || select_compose_file
        COMPOSE_FILES=("$COMPOSE_FILE")
        if [ "$(read_env_compat XIASS_BUILD_MODE NOWIND_BUILD_MODE)" = "source" ] && [ -f "$DEPLOY_DIR/docker-compose.build.yml" ]; then
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

snapshot_previous_compose() {
    local snapshot_dir compose_file index=0 snapshot_file
    snapshot_dir=$(mktemp -d /tmp/xiass-api-update-compose.XXXXXX)
    PREVIOUS_COMPOSE_SNAPSHOT="$snapshot_dir"
    PREVIOUS_COMPOSE_FILES=()
    for compose_file in "${COMPOSE_FILES[@]}"; do
        index=$((index + 1))
        snapshot_file="$snapshot_dir/${index}-$(basename "$compose_file")"
        cp "$compose_file" "$snapshot_file"
        PREVIOUS_COMPOSE_FILES+=("$snapshot_file")
    done
}

stop_known_runtime_containers() {
    local container_name
    for container_name in \
        xiass-api xiass-api-watchtower xiass-api-postgres xiass-api-redis \
        nowind-api nowind-api-watchtower nowind-api-postgres nowind-api-redis \
        sub2api sub2api-watchtower sub2api-postgres sub2api-redis; do
        if container_exists "$container_name"; then
            log "停止旧运行容器 $container_name（不删除卷或数据目录）..."
            docker stop -t 60 "$container_name" >/dev/null 2>&1 || true
            docker rm "$container_name" >/dev/null 2>&1 || true
        fi
    done
}

compose() {
    "${COMPOSE[@]}" "${COMPOSE_ARGS[@]}" "$@"
}

capture_previous_image() {
    local image_snapshot="" container_name
    PREVIOUS_IMAGE_ID=""
    PREVIOUS_IMAGE_REF=""

    for container_name in xiass-api nowind-api sub2api; do
        if image_snapshot=$(docker inspect --type container \
            --format '{{.Image}} {{.Config.Image}}' "$container_name" 2>/dev/null); then
            break
        fi
        image_snapshot=""
    done
    if [ -z "$image_snapshot" ]; then
        log "未能记录当前应用镜像；更新失败时将使用原有 Git/Compose 恢复流程。"
        return 0
    fi

    read -r PREVIOUS_IMAGE_ID PREVIOUS_IMAGE_REF <<< "$image_snapshot"
    if [ -z "$PREVIOUS_IMAGE_ID" ] || [ -z "$PREVIOUS_IMAGE_REF" ]; then
        PREVIOUS_IMAGE_ID=""
        PREVIOUS_IMAGE_REF=""
        log "当前应用镜像信息不完整；更新失败时将使用原有 Git/Compose 恢复流程。"
        return 0
    fi

    log "已从 ${container_name} 记录当前应用镜像用于失败回滚：$PREVIOUS_IMAGE_REF"
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
    local image_tag_restored=false
    local rollback_started=false
    set +e
    log "更新失败，正在恢复原 Git 状态和旧容器栈..."

    if [ -f "$COMPOSE_FILE" ]; then
        init_compose
        compose down >/dev/null 2>&1 || true
    fi

    if ! git -C "$INSTALL_DIR" reset --hard "$PREVIOUS_REF" >/dev/null 2>&1; then
        remove_created_update_remote || true
        log "无法自动恢复 Git 到 ${PREVIOUS_REF}；更新前完整备份仍位于 ${BACKUP_DIR}。"
        return
    fi
    remove_created_update_remote || true

    if [ "${#PREVIOUS_COMPOSE_FILES[@]}" -gt 0 ]; then
        COMPOSE_FILES=("${PREVIOUS_COMPOSE_FILES[@]}")
        COMPOSE_FILE="${COMPOSE_FILES[0]}"
        init_compose
    else
        COMPOSE_FILES=()
        select_compose_file true
        init_compose
    fi
    if [ -z "$PREVIOUS_IMAGE_ID" ] || [ -z "$PREVIOUS_IMAGE_REF" ]; then
        log "更新前镜像快照不可用，将按原 compose 配置尝试恢复。"
    elif docker image tag "$PREVIOUS_IMAGE_ID" "$PREVIOUS_IMAGE_REF" >/dev/null 2>&1; then
        image_tag_restored=true
        log "旧应用镜像已重新标记为 ${PREVIOUS_IMAGE_REF}。"
    else
        log "无法重新标记旧应用镜像，将按原 compose 配置尝试恢复。"
    fi

    if "$image_tag_restored"; then
        if compose up -d --no-build >/dev/null 2>&1; then
            rollback_started=true
        else
            log "使用旧应用镜像启动失败，将按原 compose 配置再次尝试恢复。"
        fi
    fi
    if ! "$rollback_started" && compose up -d >/dev/null 2>&1; then
        rollback_started=true
    fi

    if "$rollback_started"; then
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
    elif [ "$status" -ne 0 ]; then
        remove_created_update_remote || true
    fi
    if "$LOCK_HELD"; then
        rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
    fi
    if [ -n "$PREVIOUS_COMPOSE_SNAPSHOT" ]; then
        rm -rf "$PREVIOUS_COMPOSE_SNAPSHOT"
    fi
    exit "$status"
}

main() {
    [ "$(id -u)" -eq 0 ] || die "请使用 sudo 或 root 运行。"
    for command_name in curl git docker mktemp; do
        command -v "$command_name" >/dev/null 2>&1 || die "缺少依赖：$command_name"
    done
    resolve_install_dir
    [ -d "$INSTALL_DIR/.git" ] || die "$INSTALL_DIR 不是 XIASS Git 安装目录。"
    [ -f "$DEPLOY_DIR/.env" ] || die "未找到 .env。"

    PREVIOUS_REF=$(git -C "$INSTALL_DIR" rev-parse HEAD)

    mkdir "$LOCK_DIR" 2>/dev/null || die "已有安装、更新、备份或恢复任务正在运行。"
    LOCK_HELD=true
    trap cleanup EXIT
    trap 'exit 130' INT TERM

    detect_runtime_layout
    select_compose_file
    if [ "${#ACTUAL_COMPOSE_FILES[@]}" -gt 0 ]; then
        COMPOSE_FILES=("${ACTUAL_COMPOSE_FILES[@]}")
        COMPOSE_FILE="${COMPOSE_FILES[0]}"
    fi
    init_compose
    ensure_xiass_update_remote
    log "验证 XIASS API 更新来源..."
    git -C "$INSTALL_DIR" fetch --prune "$UPDATE_REMOTE" main
    UPDATE_REF=$(git -C "$INSTALL_DIR" rev-parse "$UPDATE_REMOTE/main")
    snapshot_previous_compose

    log "先创建更新前完整备份..."
    curl -fsSL "$RAW_BASE_URL/xiass-backup.sh" \
        | INSTALL_DIR="$INSTALL_DIR" BACKUP_DIR="$BACKUP_DIR" SKIP_MAINTENANCE_LOCK=true bash

    local patch_file=""
    if ! git -C "$INSTALL_DIR" diff --quiet || ! git -C "$INSTALL_DIR" diff --cached --quiet; then
        patch_file="/root/xiass-local-changes-$(date +%Y%m%d-%H%M%S).patch"
        git -C "$INSTALL_DIR" diff HEAD > "$patch_file"
        chmod 600 "$patch_file"
        log "本地源码修改已备份到 $patch_file"
    fi

    log "同步已验证的 XIASS API 部署文件..."
    capture_previous_image

    log "停止当前容器栈以迁移运行时名称（不会删除卷或数据）..."
    UPDATE_STARTED=true
    if ! compose down; then
        log "Compose 未能完整停止旧栈，改为按已知容器名安全停止。"
    fi
    stop_known_runtime_containers

    git -C "$INSTALL_DIR" reset --hard "$UPDATE_REF"
    COMPOSE_FILES=()
    ACTUAL_COMPOSE_FILES=()
    select_compose_file true
    init_compose

    if [ "$(read_env_compat XIASS_BUILD_MODE NOWIND_BUILD_MODE)" = "source" ]; then
        log "从最新源码重新构建并启动 XIASS 容器栈..."
        compose up -d --build
    else
        log "拉取最新正式镜像并启动 XIASS 容器栈..."
        compose pull xiass-api watchtower
        compose up -d
    fi

    if ! wait_for_health; then
        compose ps || true
        compose logs --tail 160 xiass-api || true
        die "更新后健康检查失败。数据没有删除，更新前备份位于 ${BACKUP_DIR}，可使用 xiass-restore.sh 恢复。"
    fi

    UPDATE_SUCCEEDED=true
    trap - EXIT INT TERM
    rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
    LOCK_HELD=false
    rm -rf "$PREVIOUS_COMPOSE_SNAPSHOT"
    PREVIOUS_COMPOSE_SNAPSHOT=""
    printf '\nXIASS 更新完成，PostgreSQL、Redis、应用数据和 .env 均沿用原目录。\n'
    [ -n "$patch_file" ] && printf '原本的本地修改补丁：%s\n' "$patch_file"
}

if [ "${XIASS_UPDATE_LIB_ONLY:-0}" != "1" ]; then
    main "$@"
fi
