#!/usr/bin/env bash
# Initialize or repair the local XIASS execution-node runtime.
# The script is run by a short-lived host updater container and never removes
# application data, PostgreSQL, Redis, or Docker volumes.

set -Eeuo pipefail
umask 077

: "${INSTALL_DIR:?INSTALL_DIR is required}"
: "${RUNTIME_NODE_ID:?RUNTIME_NODE_ID is required}"
: "${RUNTIME_TUNNEL_TOKEN:?RUNTIME_TUNNEL_TOKEN is required}"
: "${RUNTIME_DEFAULT_PROXY_ID:?RUNTIME_DEFAULT_PROXY_ID is required}"
: "${RUNTIME_LEGACY_NODE_ID:?RUNTIME_LEGACY_NODE_ID is required}"
: "${RUNTIME_LEGACY_PROXY_ID:?RUNTIME_LEGACY_PROXY_ID is required}"

DEPLOY_DIR="$INSTALL_DIR/deploy"
ENV_FILE="$DEPLOY_DIR/.env"
BACKUP_ROOT="${BACKUP_DIR:-/root/xiass-backups}/cluster-runtime"
RUNTIME_BACKUP_DIR=""
COMPOSE=()
COMPOSE_FILE=""
COMPOSE_BUILD_FILE=""
COMPOSE_FILES=()
COMPOSE_PROJECT_NAME=""
OLD_ENV_FILE=""

log() { printf '[XIASS cluster] %s\n' "$*"; }
warn() { printf '[XIASS cluster] 警告：%s\n' "$*" >&2; }
die() { printf '[XIASS cluster] 错误：%s\n' "$*" >&2; exit 1; }

cleanup() {
    local status=$?
    trap - EXIT INT TERM
    if [ "$status" -ne 0 ] && [ -n "$OLD_ENV_FILE" ] && [ -f "$OLD_ENV_FILE" ]; then
        warn "本机节点运行时未通过健康检查，恢复原配置。"
        cp "$OLD_ENV_FILE" "$ENV_FILE"
        if [ -n "$COMPOSE_FILE" ] && [ -f "$COMPOSE_FILE" ]; then
            compose up -d --no-deps --no-build --force-recreate xiass-api >/dev/null 2>&1 || warn "原应用容器未能自动恢复，请检查 Docker 日志。"
        fi
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

[ -d "$DEPLOY_DIR" ] || die "未找到 XIASS 部署目录：$DEPLOY_DIR"
[ -f "$ENV_FILE" ] || die "未找到 XIASS 环境配置：$ENV_FILE"
command -v docker >/dev/null 2>&1 || die "缺少 Docker"

read_env_value() {
    local key="$1"
    awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); gsub(/^\047|\047$/, ""); gsub(/^"|"$/, ""); print; exit}' "$ENV_FILE"
}

resolve_compose() {
    if docker compose version >/dev/null 2>&1; then
        COMPOSE=(docker compose)
    elif command -v docker-compose >/dev/null 2>&1; then
        COMPOSE=(docker-compose)
    else
        die "缺少 Docker Compose"
    fi

    local project_dir label_files label_file
    project_dir=$(docker inspect --type container --format '{{ index .Config.Labels "com.docker.compose.project.working_dir" }}' xiass-api 2>/dev/null || true)
    label_files=$(docker inspect --type container --format '{{ index .Config.Labels "com.docker.compose.project.config_files" }}' xiass-api 2>/dev/null || true)
    COMPOSE_PROJECT_NAME=$(docker inspect --type container --format '{{ index .Config.Labels "com.docker.compose.project" }}' xiass-api 2>/dev/null || true)
    [ "$COMPOSE_PROJECT_NAME" != '<no value>' ] || COMPOSE_PROJECT_NAME=""
    [ "$project_dir" != '<no value>' ] || project_dir=""
    project_dir="${project_dir:-$DEPLOY_DIR}"
    IFS=',' read -r -a label_file_list <<< "$label_files"
    for label_file in "${label_file_list[@]}"; do
        [ -n "$label_file" ] && [ "$label_file" != '<no value>' ] || continue
        if [ "${label_file#/}" = "$label_file" ]; then
            label_file="$project_dir/$label_file"
        fi
        [ -f "$label_file" ] && [ -r "$label_file" ] || die "当前容器的 Compose 文件不可读：${label_file}；未修改配置。"
        COMPOSE_FILES+=("$label_file")
    done
    if [ "${#COMPOSE_FILES[@]}" -eq 0 ]; then
        if [ -f "$DEPLOY_DIR/docker-compose.local.yml" ] && [ -d "$DEPLOY_DIR/postgres_data" ]; then
            COMPOSE_FILE="$DEPLOY_DIR/docker-compose.local.yml"
        elif [ -f "$DEPLOY_DIR/docker-compose.yml" ]; then
            COMPOSE_FILE="$DEPLOY_DIR/docker-compose.yml"
        else
            die "未找到当前 XIASS Compose 文件"
        fi
        COMPOSE_FILES=("$COMPOSE_FILE")
        if [ "$(read_env_value XIASS_BUILD_MODE)" = "source" ] && [ -f "$DEPLOY_DIR/docker-compose.build.yml" ]; then
            COMPOSE_BUILD_FILE="$DEPLOY_DIR/docker-compose.build.yml"
            COMPOSE_FILES+=("$COMPOSE_BUILD_FILE")
        fi
    fi
    COMPOSE_FILE="${COMPOSE_FILES[0]}"
}

compose() {
    local args=() compose_file
    for compose_file in "${COMPOSE_FILES[@]}"; do args+=(-f "$compose_file"); done
    if [ -n "$COMPOSE_PROJECT_NAME" ]; then args+=(--project-name "$COMPOSE_PROJECT_NAME"); fi
    "${COMPOSE[@]}" "${args[@]}" --project-directory "$DEPLOY_DIR" "$@"
}

set_env_value() {
    local key="$1" value="$2" temp escaped
    case "$key$value" in *$'\n'*|*$'\r'*) die "配置值包含非法换行：$key" ;; esac
    escaped=${value//\\/\\\\}
    escaped=${escaped//\'/\\\'}
    value="'$escaped'"
    temp="${ENV_FILE}.runtime.$$"
    XIASS_ENV_VALUE="$value" awk -v key="$key" '
        BEGIN { value = ENVIRON["XIASS_ENV_VALUE"]; replaced = 0 }
        $0 ~ "^" key "=" { if (!replaced) { print key "=" value; replaced = 1 }; next }
        { print }
        END { if (!replaced) print key "=" value }
    ' "$ENV_FILE" > "$temp"
    chmod 600 "$temp"
    mv -f "$temp" "$ENV_FILE"
}

wait_for_health() {
    local port attempt
    port=$(read_env_value SERVER_PORT)
    port="${port:-8080}"
    for attempt in $(seq 1 120); do
		if curl -fsS --max-time 3 "http://127.0.0.1:${port}/readyz" >/dev/null 2>&1; then return 0; fi
        sleep 2
    done
    return 1
}

main() {
    command -v curl >/dev/null 2>&1 || die "缺少 curl"
    resolve_compose
    local emergency_egress
    emergency_egress=$(read_env_value GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS)
    case "$emergency_egress" in
        ''|true|false) ;;
        *) die "现有应急本地出口配置必须为 true 或 false；未修改配置。" ;;
    esac
    mkdir -p "$BACKUP_ROOT"
    RUNTIME_BACKUP_DIR="$BACKUP_ROOT/$(date -u +%Y%m%dT%H%M%SZ)-$RUNTIME_NODE_ID"
    mkdir -p "$RUNTIME_BACKUP_DIR"
    OLD_ENV_FILE="$RUNTIME_BACKUP_DIR/.env.before-runtime"
    cp "$ENV_FILE" "$OLD_ENV_FILE"
    chmod 600 "$OLD_ENV_FILE"

    set_env_value GATEWAY_EXECUTION_NODE_ENABLED true
    set_env_value GATEWAY_EXECUTION_NODE_ID "$RUNTIME_NODE_ID"
    set_env_value GATEWAY_EXECUTION_NODE_DEFAULT_PROXY_ID "$RUNTIME_DEFAULT_PROXY_ID"
    if [ -z "$emergency_egress" ]; then
        set_env_value GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS false
    fi
    set_env_value GATEWAY_EXECUTION_NODE_CONTROL_PLANE true
    set_env_value GATEWAY_EXECUTION_NODE_LEGACY_UNASSIGNED_NODE_ID "$RUNTIME_LEGACY_NODE_ID"
    set_env_value GATEWAY_EXECUTION_NODE_LEGACY_UNASSIGNED_PROXY_ID "$RUNTIME_LEGACY_PROXY_ID"
    set_env_value XIASS_CLUSTER_TUNNEL_TOKEN "$RUNTIME_TUNNEL_TOKEN"

    log "已备份 .env，开始仅重建 XIASS 应用容器。"
    compose up -d --no-deps --no-build --force-recreate xiass-api >/dev/null
    wait_for_health || die "本机 XIASS 未通过健康检查，开始自动回滚"
    log "本机节点运行时已初始化，负载均衡仍保持关闭，请在面板中确认后再启用。"
}

main "$@"
