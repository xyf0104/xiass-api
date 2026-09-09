#!/usr/bin/env bash
# Apply a source-authoritative XIASS cluster join on the target host.
# This script is launched by the short-lived updater container and runs with a
# host view of the deployment directory. It never removes data volumes.

set -Eeuo pipefail
umask 077

: "${INSTALL_DIR:?INSTALL_DIR is required}"
: "${JOIN_BUNDLE_B64:?JOIN_BUNDLE_B64 is required}"
: "${JOIN_SOURCE_URL:?JOIN_SOURCE_URL is required}"
: "${JOIN_TARGET_NODE_ID:?JOIN_TARGET_NODE_ID is required}"
: "${JOIN_TUNNEL_PROOF:?JOIN_TUNNEL_PROOF is required}"

DEPLOY_DIR="$INSTALL_DIR/deploy"
ENV_FILE="$DEPLOY_DIR/.env"
BACKUP_ROOT="${BACKUP_DIR:-/root/xiass-backups}/cluster-join"
JOIN_BACKUP_DIR=""
COMPOSE=()
COMPOSE_FILE=""
COMPOSE_BUILD_FILE=""
COMPOSE_FILES=()
COMPOSE_PROJECT_NAME=""
OLD_ENV_FILE=""
APPLIED=false

log() { printf '[XIASS cluster] %s\n' "$*"; }
warn() { printf '[XIASS cluster] 警告：%s\n' "$*" >&2; }
die() { printf '[XIASS cluster] 错误：%s\n' "$*" >&2; exit 1; }

cleanup() {
    local status=$?
    trap - EXIT INT TERM
    if [ "$status" -ne 0 ] && [ -n "$OLD_ENV_FILE" ] && [ -f "$OLD_ENV_FILE" ]; then
        warn "目标 XIASS 未通过加入验证，恢复原配置。"
        cp "$OLD_ENV_FILE" "$ENV_FILE"
        if [ -n "$COMPOSE_FILE" ] && [ -f "$COMPOSE_FILE" ]; then
            compose up -d --no-deps --no-build --force-recreate xiass-api >/dev/null 2>&1 || warn "原应用容器未能自动恢复，请检查 Docker 日志。"
        fi
    fi
    rm -f /tmp/xiass-cluster-join-bundle.json
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

    local label_files project_dir label_file
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

json_value() {
    local key="$1"
    jq -r --arg key "$key" '.[$key] // empty' /tmp/xiass-cluster-join-bundle.json
}

set_env_value() {
    local key="$1" value="$2" temp escaped
    case "$key$value" in *$'\n'*|*$'\r'*) die "配置值包含非法换行：$key" ;; esac
    # Compose dotenv single-quoted values are literal. Escape only the two
    # characters that have meaning in that form, while passing the finished
    # value through ENVIRON so awk cannot reinterpret backslash sequences.
    escaped=${value//\\/\\\\}
    escaped=${escaped//\'/\\\'}
    value="'$escaped'"
    temp="${ENV_FILE}.join.$$"
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

verify_local_state() {
    local port attempt body
    port=$(read_env_value SERVER_PORT)
    port="${port:-8080}"
    for attempt in $(seq 1 30); do
		body=$(curl -fsS --max-time 3 "http://127.0.0.1:${port}/readyz" 2>/dev/null || true)
        if [ -n "$body" ]; then return 0; fi
        sleep 1
    done
    return 1
}

finalize_source() {
    local url="${JOIN_SOURCE_URL%/}/api/v1/internal/execution-nodes/pairing/finalize"
    curl -fsS --max-time 15 -X POST "$url" \
        -H "Content-Type: application/json" \
        -H "X-XIASS-Execution-Node-ID: $JOIN_TARGET_NODE_ID" \
        --data "$(jq -cn --arg node "$JOIN_TARGET_NODE_ID" --arg proof "$JOIN_TUNNEL_PROOF" '{node_id:$node,proof:$proof}')" >/dev/null
}

main() {
    command -v jq >/dev/null 2>&1 || die "缺少 jq"
    command -v curl >/dev/null 2>&1 || die "缺少 curl"
    printf '%s' "$JOIN_BUNDLE_B64" | base64 -d > /tmp/xiass-cluster-join-bundle.json
    chmod 600 /tmp/xiass-cluster-join-bundle.json
    [ "$(json_value target_node_id)" = "$JOIN_TARGET_NODE_ID" ] || die "加入目标节点不匹配"
    [ "$(json_value tunnel_proof)" = "$JOIN_TUNNEL_PROOF" ] || die "加入证明不匹配"

    resolve_compose
    local emergency_egress refresh_token_store witness_enabled witness_url witness_token witness_cluster_id witness_ttl witness_timeout
    emergency_egress=$(read_env_value GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS)
    case "$emergency_egress" in
        ''|true|false) ;;
        *) die "现有应急本地出口配置必须为 true 或 false；未修改配置。" ;;
    esac
    refresh_token_store=$(jq -er 'if has("jwt_refresh_token_store") then .jwt_refresh_token_store else "redis" end | select(. == "redis" or . == "postgres")' /tmp/xiass-cluster-join-bundle.json) \
        || die "来源刷新令牌存储策略必须为 redis 或 postgres；未修改配置。"
    witness_enabled=$(json_value witness_enabled)
    [ -n "$witness_enabled" ] || witness_enabled=false
    case "$witness_enabled" in true|false) ;; *) die "来源容灾仲裁开关无效；未修改配置。" ;; esac
    witness_url=$(json_value witness_url)
    witness_token=$(json_value witness_token)
    witness_cluster_id=$(json_value witness_cluster_id)
    witness_ttl=$(json_value witness_lease_ttl_seconds)
    witness_timeout=$(json_value witness_request_timeout_seconds)
    if [ "$witness_enabled" = true ]; then
        case "$witness_url" in https://*|http://localhost:*|http://127.0.0.1:*|http://\[::1\]:*) ;; *) die "来源容灾仲裁地址必须使用 HTTPS；未修改配置。" ;; esac
        [ "${#witness_token}" -ge 32 ] || die "来源容灾仲裁密钥无效；未修改配置。"
        [ -n "$witness_cluster_id" ] || die "来源容灾集群身份缺失；未修改配置。"
        [ "$witness_ttl" -ge 10 ] && [ "$witness_ttl" -le 60 ] || die "来源容灾租约时间无效；未修改配置。"
        [ "$witness_timeout" -ge 1 ] && [ "$witness_timeout" -le 10 ] && [ "$witness_timeout" -lt "$witness_ttl" ] || die "来源容灾请求超时无效；未修改配置。"
    fi
    mkdir -p "$BACKUP_ROOT"
    JOIN_BACKUP_DIR="$BACKUP_ROOT/$(date -u +%Y%m%dT%H%M%SZ)-$JOIN_TARGET_NODE_ID"
    mkdir -p "$JOIN_BACKUP_DIR"
    OLD_ENV_FILE="$JOIN_BACKUP_DIR/.env.before-join"
    cp "$ENV_FILE" "$OLD_ENV_FILE"
    chmod 600 "$OLD_ENV_FILE"
    log "已备份目标配置到受保护目录。"

    target_proxy_id=$(json_value target_proxy_id)
    legacy_node_id=$(json_value legacy_node_id)
    legacy_proxy_id=$(json_value legacy_proxy_id)
    [ -n "$target_proxy_id" ] && [ "$target_proxy_id" -gt 0 ] || die "来源没有提供目标节点固定出口"
    [ -n "$legacy_node_id" ] || legacy_node_id="$(json_value source_node_id)"
    [ -n "$legacy_proxy_id" ] && [ "$legacy_proxy_id" -gt 0 ] || legacy_proxy_id="$target_proxy_id"

    # The target application keeps its own public ingress, but receives the
    # shared account runtime identity and its own fixed loopback egress.
    set_env_value GATEWAY_EXECUTION_NODE_ENABLED true
    set_env_value GATEWAY_EXECUTION_NODE_ID "$JOIN_TARGET_NODE_ID"
    set_env_value GATEWAY_EXECUTION_NODE_DEFAULT_PROXY_ID "$target_proxy_id"
    if [ -z "$emergency_egress" ]; then
        set_env_value GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS false
    fi
    set_env_value GATEWAY_EXECUTION_NODE_CONTROL_PLANE false
    set_env_value GATEWAY_EXECUTION_NODE_LEGACY_UNASSIGNED_NODE_ID "$legacy_node_id"
    set_env_value GATEWAY_EXECUTION_NODE_LEGACY_UNASSIGNED_PROXY_ID "$legacy_proxy_id"
    database_user=$(json_value database_user)
    database_pass=$(json_value database_pass)
    database_name=$(json_value database_name)
    set_env_value DATABASE_HOST 127.0.0.1
    set_env_value DATABASE_PORT 15432
    set_env_value DATABASE_USER "$database_user"
    set_env_value DATABASE_PASSWORD "$database_pass"
    set_env_value DATABASE_DBNAME "$database_name"
    # Older XIASS Compose files source the application's database identity
    # from POSTGRES_* instead of DATABASE_*. Keep both sets synchronized so a
    # source-authoritative join also works before the host Compose file itself
    # has been refreshed by a later installer/update.
    set_env_value POSTGRES_USER "$database_user"
    set_env_value POSTGRES_PASSWORD "$database_pass"
    set_env_value POSTGRES_DB "$database_name"
    set_env_value DATABASE_SSLMODE "$(json_value database_sslmode)"
    set_env_value REDIS_HOST 127.0.0.1
    set_env_value REDIS_PORT 16379
    set_env_value REDIS_USERNAME "$(json_value redis_username)"
    set_env_value REDIS_PASSWORD "$(json_value redis_password)"
    set_env_value REDIS_DB "$(json_value redis_db)"
    set_env_value REDIS_ENABLE_TLS "$(json_value redis_enable_tls)"
    set_env_value JWT_SECRET "$(json_value jwt_secret)"
    set_env_value JWT_REFRESH_TOKEN_STORE "$refresh_token_store"
    set_env_value TOTP_ENCRYPTION_KEY "$(json_value totp_key)"
    source_url=$(json_value source_url)
    [ -n "$source_url" ] || source_url="$JOIN_SOURCE_URL"
    [ -n "$source_url" ] || die "来源地址缺失"
    set_env_value XIASS_CLUSTER_STATE_SOURCE_URL "$source_url"
    set_env_value XIASS_CLUSTER_TUNNEL_TOKEN "$JOIN_TUNNEL_PROOF"
    set_env_value XIASS_CLUSTER_STATE_SOURCE_NODE_ID "$(json_value source_node_id)"
    set_env_value XIASS_CLUSTER_NODE_URLS_JSON "$(jq -cn --arg id "$(json_value source_node_id)" --arg url "$source_url" '{($id):$url}')"
    set_env_value GATEWAY_EXECUTION_NODE_WITNESS_ENABLED "$witness_enabled"
    if [ "$witness_enabled" = true ]; then
        set_env_value GATEWAY_EXECUTION_NODE_WITNESS_URL "$witness_url"
        set_env_value GATEWAY_EXECUTION_NODE_WITNESS_TOKEN "$witness_token"
        set_env_value GATEWAY_EXECUTION_NODE_WITNESS_CLUSTER_ID "$witness_cluster_id"
        set_env_value GATEWAY_EXECUTION_NODE_WITNESS_LEASE_TTL_SECONDS "$witness_ttl"
        set_env_value GATEWAY_EXECUTION_NODE_WITNESS_REQUEST_TIMEOUT_SECONDS "$witness_timeout"
    else
        set_env_value GATEWAY_EXECUTION_NODE_WITNESS_URL ""
        set_env_value GATEWAY_EXECUTION_NODE_WITNESS_TOKEN ""
        set_env_value GATEWAY_EXECUTION_NODE_WITNESS_CLUSTER_ID ""
        set_env_value GATEWAY_EXECUTION_NODE_WITNESS_LEASE_TTL_SECONDS 15
        set_env_value GATEWAY_EXECUTION_NODE_WITNESS_REQUEST_TIMEOUT_SECONDS 3
    fi

    log "已写入来源 PostgreSQL/Redis 和认证配置，开始仅重建目标应用容器。"
    compose up -d --no-deps --no-build --force-recreate xiass-api >/dev/null
    wait_for_health || die "目标 XIASS 未通过健康检查，开始自动回滚"
    verify_local_state || die "目标 XIASS 状态未稳定，开始自动回滚"
    finalize_source || die "目标已启动，但来源节点未确认配对完成；已自动回滚"
    APPLIED=true
    log "来源权威状态已接入，目标容器健康且配对已确认。"
}

main "$@"
