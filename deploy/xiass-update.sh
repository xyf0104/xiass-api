#!/usr/bin/env bash
# XIASS API 数据安全更新：预拉镜像并仅替换应用容器，失败时恢复旧版本。

set -Eeuo pipefail

INSTALL_DIR="${INSTALL_DIR:-}"
DEPLOY_DIR=""
RELEASE_API_URL="https://api.github.com/repos/xyf0104/xiass-api/releases/latest"
RAW_BASE_URL=""
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
ACTUAL_COMPOSE_PROJECT_NAME=""
ACTUAL_COMPOSE_FILES=()
APP_CONTAINER=""
POSTGRES_CONTAINER=""
PREVIOUS_REF=""
PREVIOUS_IMAGE_ID=""
PREVIOUS_IMAGE_REF=""
PREVIOUS_COMPOSE_SNAPSHOT=""
PREVIOUS_BUILD_SNAPSHOT=""
PREVIOUS_COMPOSE_FILES=()
ORIGINAL_COMPOSE_FILES=()
LOCAL_COMPOSE_FILES=()
PREVIOUS_ENV_FILE=""
UPDATE_REMOTE=""
UPDATE_REF=""
UPDATE_TAG=""
TARGET_VERSION=""
TARGET_APP_IMAGE=""
TARGET_APP_IMAGE_ID=""
RUNNING_APP_VERSION=""
VERSION_PROBE_CONTAINER=""
UPDATE_STARTED=false
APP_REPLACEMENT_STARTED=false
UPDATE_SUCCEEDED=false
LOCK_HELD=false
CREATED_UPDATE_REMOTE=false
RUNTIME_START_SCRIPT=""
TEAM_CHILD_BROWSER_PROFILE_SOURCE=""
TARGET_APP_IMAGE_PREFETCHED=false
UPDATE_BACKUP_CREATED=false

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

update_full_backup_enabled() {
    local value="${XIASS_UPDATE_FULL_BACKUP:-}"
    if [ -z "$value" ]; then
        value=$(read_env_value XIASS_UPDATE_FULL_BACKUP)
    fi
    value=$(printf '%s' "$value" | tr '[:upper:]' '[:lower:]')
    case "$value" in
        1|true|yes|on) return 0 ;;
        *) return 1 ;;
    esac
}

generate_runtime_token() {
    local token=""
    if command -v openssl >/dev/null 2>&1; then
        token=$(openssl rand -hex 32 2>/dev/null || true)
    fi
    if [ -z "$token" ] && command -v od >/dev/null 2>&1; then
        token=$(head -c 32 /dev/urandom 2>/dev/null | od -An -tx1 | tr -d ' \n' || true)
    fi
    [ -n "$token" ] || die "无法生成 Team 自动化服务令牌。"
    printf '%s\n' "$token"
}

append_env_default() {
    local key="$1" value="$2"
    if ! grep -qE "^${key}=" "$DEPLOY_DIR/.env"; then
        printf '\n%s=%s\n' "$key" "$value" >> "$DEPLOY_DIR/.env"
    fi
}

ensure_team_child_runtime_config() {
    local token
    # Existing explicit values remain authoritative. These defaults only make
    # the newly added Team workflow usable on older XIASS installations.
    append_env_default TEAM_CHILD_BROWSER_ENABLED true
    append_env_default TEAM_CHILD_BROWSER_START_DELAY_SECONDS 5
    append_env_default TEAM_CHILD_BROWSER_CONTROL_TTL_SECONDS 120
    append_env_default TEAM_CHILD_AUTOMATION_IMAGE ghcr.io/xyf0104/xiass-team-child-automation:latest
    append_env_default XIASS_UPDATER_IMAGE ghcr.io/xyf0104/xiass-updater:latest

    token=$(read_env_value TEAM_CHILD_AUTOMATION_TOKEN)
    if [ -z "$token" ]; then
        token=$(generate_runtime_token)
        if grep -qE '^TEAM_CHILD_AUTOMATION_TOKEN=' "$DEPLOY_DIR/.env"; then
            if sed --version >/dev/null 2>&1; then
                sed -i "s/^TEAM_CHILD_AUTOMATION_TOKEN=.*/TEAM_CHILD_AUTOMATION_TOKEN=${token}/" "$DEPLOY_DIR/.env"
            else
                sed -i '' "s/^TEAM_CHILD_AUTOMATION_TOKEN=.*/TEAM_CHILD_AUTOMATION_TOKEN=${token}/" "$DEPLOY_DIR/.env"
            fi
        else
            printf '\nTEAM_CHILD_AUTOMATION_TOKEN=%s\n' "$token" >> "$DEPLOY_DIR/.env"
        fi
    fi
    chmod 600 "$DEPLOY_DIR/.env"
}

container_exists() {
    docker container inspect "$1" >/dev/null 2>&1
}

directory_has_entries() {
    local directory="$1"
    [ -d "$directory" ] || return 1
    [ -n "$(find "$directory" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]
}

capture_team_child_browser_profile() {
    local container="" candidate mount type source
    for candidate in \
        xiass-api-team-child-browser \
        nowind-api-team-child-browser \
        sub2api-team-child-browser; do
        if container_exists "$candidate"; then
            container="$candidate"
            break
        fi
    done
    [ -n "$container" ] || return 0

    mount=$(docker inspect --type container \
        --format '{{range .Mounts}}{{if eq .Destination "/config"}}{{.Type}}|{{.Source}}{{end}}{{end}}' \
        "$container" 2>/dev/null || true)
    [ -n "$mount" ] || {
        log "未识别到 Team 浏览器配置挂载，跳过浏览器登录资料迁移。"
        return 0
    }
    IFS='|' read -r type source <<< "$mount"
    case "$type" in
        bind|volume) ;;
        *)
            log "Team 浏览器配置挂载类型 ${type:-未知} 不支持自动迁移，保留原挂载不变。"
            return 0
            ;;
    esac
    if [ -z "$source" ] || [ ! -d "$source" ]; then
        log "Team 浏览器配置源目录不可读，跳过登录资料迁移。"
        return 0
    fi

    TEAM_CHILD_BROWSER_PROFILE_SOURCE="$source"
    log "已记录现有 Team 浏览器配置，用于升级后的无覆盖迁移。"
}

migrate_team_child_browser_profile() {
    local target
    [ -n "$TEAM_CHILD_BROWSER_PROFILE_SOURCE" ] || return 0

    target="$DEPLOY_DIR/team_child_browser_data"
    if [ "$TEAM_CHILD_BROWSER_PROFILE_SOURCE" = "$target" ]; then
        return 0
    fi
    if ! directory_has_entries "$TEAM_CHILD_BROWSER_PROFILE_SOURCE"; then
        log "现有 Team 浏览器配置为空，跳过登录资料迁移。"
        return 0
    fi
    if directory_has_entries "$target"; then
        log "新的 Team 浏览器配置目录已有内容，保留现有内容并跳过旧资料迁移。"
        return 0
    fi

    mkdir -p "$target"
    cp -a "$TEAM_CHILD_BROWSER_PROFILE_SOURCE"/. "$target"/ \
        || die "无法迁移 Team 浏览器登录资料；已停止更新以便自动回滚。"
    log "已将现有 Team 浏览器登录资料迁移到 XIASS 持久化目录。"
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

normalize_release_version() {
    local value="${1#v}"
    [[ "$value" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || return 1
    printf '%s\n' "$value"
}

resolve_stable_release() {
    local release_json source_version
    release_json=$(curl -fsSL --connect-timeout 10 --max-time 30 --retry 2 \
        -H 'Accept: application/vnd.github+json' "$RELEASE_API_URL") \
        || die "无法读取 XIASS API 最新稳定发行；不会回退到 main 或缓存镜像。"
    UPDATE_TAG=$(printf '%s' "$release_json" | jq -er \
        'select(.draft == false and .prerelease == false) | .tag_name | select(type == "string" and startswith("v"))') \
        || die "稳定发行元数据无效或指向预发布版本；未开始更新。"
    TARGET_VERSION=$(normalize_release_version "$UPDATE_TAG") \
        || die "稳定发行必须使用 vX.Y.Z 标签；未开始更新。"
    # Resolve only the fetched remote tag, never a possibly stale local tag or main.
    git -C "$INSTALL_DIR" fetch --no-tags "$UPDATE_REMOTE" "refs/tags/$UPDATE_TAG" \
        || die "无法获取稳定发行标签 ${UPDATE_TAG}；未开始更新。"
    UPDATE_REF=$(git -C "$INSTALL_DIR" rev-parse --verify 'FETCH_HEAD^{commit}') \
        || die "稳定发行标签无法解析为提交；未开始更新。"
    source_version=$(git -C "$INSTALL_DIR" show "$UPDATE_REF:backend/cmd/server/VERSION") \
        || die "稳定发行缺少服务版本文件；未开始更新。"
    source_version=$(normalize_release_version "$source_version") \
        || die "稳定发行的服务版本格式无效；未开始更新。"
    [ "$source_version" = "$TARGET_VERSION" ] \
        || die "稳定标签 $UPDATE_TAG 与源码版本 $source_version 不一致；未开始更新。"
    RAW_BASE_URL="https://raw.githubusercontent.com/xyf0104/xiass-api/$UPDATE_REF/deploy"
    log "已锁定稳定发行 ${UPDATE_TAG}，提交 ${UPDATE_REF}。"
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
        ACTUAL_COMPOSE_PROJECT_NAME=$(docker inspect --type container \
            --format '{{ index .Config.Labels "com.docker.compose.project" }}' \
            "$APP_CONTAINER" 2>/dev/null || true)
        [ "$ACTUAL_COMPOSE_PROJECT_NAME" != "<no value>" ] || ACTUAL_COMPOSE_PROJECT_NAME=""
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
            if [ -n "$label_file" ] && [ "$label_file" != "<no value>" ]; then
                [ -f "$label_file" ] || die "当前容器的 Compose 文件不可读：${label_file}；未开始切换。"
                ACTUAL_COMPOSE_FILES+=("$label_file")
                if [ -z "$ACTUAL_COMPOSE_FILE" ]; then
                    ACTUAL_COMPOSE_FILE="$label_file"
                fi
            fi
        done
    fi
    log "当前应用容器：${APP_CONTAINER:-未运行}；PostgreSQL 容器：${POSTGRES_CONTAINER:-未运行}；持久化模式：${PERSISTENCE_MODE}；实际 Compose：${ACTUAL_COMPOSE_FILE:-由挂载类型选择}"
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
    if [ -n "$ACTUAL_COMPOSE_PROJECT_NAME" ]; then
        COMPOSE_ARGS+=(--project-name "$ACTUAL_COMPOSE_PROJECT_NAME")
    fi
}

snapshot_previous_compose() {
    local snapshot_dir compose_file index=0 snapshot_file
    mkdir -p "$BACKUP_DIR/update-config"
    snapshot_dir=$(mktemp -d "$BACKUP_DIR/update-config/$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")
    PREVIOUS_COMPOSE_SNAPSHOT="$snapshot_dir"
    PREVIOUS_ENV_FILE="$snapshot_dir/env.before-update"
    local env_file="$DEPLOY_DIR/.env"
    cp -p "$env_file" "$PREVIOUS_ENV_FILE"
    chmod 600 "$PREVIOUS_ENV_FILE"
    printf '%s\n' "$PREVIOUS_REF" > "$snapshot_dir/git-ref"
    printf '%s %s\n' "$UPDATE_TAG" "$UPDATE_REF" > "$snapshot_dir/update-target"
    ORIGINAL_COMPOSE_FILES=("${COMPOSE_FILES[@]}")
    PREVIOUS_COMPOSE_FILES=()
    for compose_file in "${COMPOSE_FILES[@]}"; do
        index=$((index + 1))
        snapshot_file="$snapshot_dir/${index}-$(basename "$compose_file")"
        cp "$compose_file" "$snapshot_file"
        chmod 600 "$snapshot_file"
        PREVIOUS_COMPOSE_FILES+=("$snapshot_file")
        # Git diff omits untracked/ignored node overrides, but reset can still
        # overwrite them when the target release introduces the same path.
        if ! git -C "$INSTALL_DIR" ls-files --error-unmatch -- "$compose_file" >/dev/null 2>&1 \
            || ! git -C "$INSTALL_DIR" diff HEAD --quiet -- "$compose_file"; then
            LOCAL_COMPOSE_FILES+=("$compose_file")
        fi
    done
    log "已保存轻量配置回滚快照（含密钥，仅限本机管理员读取）：$snapshot_dir"
}

restore_local_compose() {
    local index compose_file
    [ "${#LOCAL_COMPOSE_FILES[@]}" -gt 0 ] || return 0
    for compose_file in "${LOCAL_COMPOSE_FILES[@]}"; do
        for index in "${!ORIGINAL_COMPOSE_FILES[@]}"; do
            if [ "${ORIGINAL_COMPOSE_FILES[$index]}" = "$compose_file" ]; then
                cp "${PREVIOUS_COMPOSE_FILES[$index]}" "$compose_file"
            fi
        done
    done
}

restore_previous_config() {
    local index env_file="$DEPLOY_DIR/.env"
    cp "$PREVIOUS_ENV_FILE" "$env_file" || return 1
    chmod 600 "$env_file" || return 1
    for index in "${!ORIGINAL_COMPOSE_FILES[@]}"; do
        cp "${PREVIOUS_COMPOSE_FILES[$index]}" "${ORIGINAL_COMPOSE_FILES[$index]}" || return 1
    done
}

stop_previous_runtime() {
    local args=() compose_file
    for compose_file in "${PREVIOUS_COMPOSE_FILES[@]}"; do
        args+=(-f "$compose_file")
    done
    args+=(--project-directory "$DEPLOY_DIR")
    if [ -n "$ACTUAL_COMPOSE_PROJECT_NAME" ]; then
        args+=(--project-name "$ACTUAL_COMPOSE_PROJECT_NAME")
    fi
    "${COMPOSE[@]}" "${args[@]}" down
}

stop_known_runtime_containers() {
    local container_name
    for container_name in \
        xiass-api xiass-api-watchtower xiass-api-postgres xiass-api-redis \
        nowind-api nowind-api-watchtower nowind-api-postgres nowind-api-redis \
        sub2api sub2api-watchtower sub2api-postgres sub2api-redis \
        xiass-api-team-child-browser xiass-api-team-child-automation \
        nowind-api-team-child-browser nowind-api-team-child-automation \
        sub2api-team-child-browser sub2api-team-child-automation; do
        if container_exists "$container_name"; then
            log "停止旧运行容器 ${container_name}（不删除卷或数据目录）..."
            docker stop -t 60 "$container_name" >/dev/null 2>&1 || true
            docker rm "$container_name" >/dev/null 2>&1 || true
        fi
    done
}

start_runtime_stack() {
    local core_ready="${1:-false}"
    local skip_core_start="${2:-false}"
    RUNTIME_START_SCRIPT="$DEPLOY_DIR/xiass-runtime-start.sh"
    if [ -x "$RUNTIME_START_SCRIPT" ]; then
        INSTALL_DIR="$INSTALL_DIR" \
        DEPLOY_DIR="$DEPLOY_DIR" \
        COMPOSE_FILE="$COMPOSE_FILE" \
        COMPOSE_BUILD_FILE="${COMPOSE_FILES[1]:-}" \
        XIASS_RUNTIME_COMPOSE_FILES="$(printf '%s\n' "${COMPOSE_FILES[@]}")" \
        XIASS_RUNTIME_PROJECT_NAME="$ACTUAL_COMPOSE_PROJECT_NAME" \
        PERSISTENCE_MODE="$PERSISTENCE_MODE" \
        BUILD_MODE="$(read_env_compat XIASS_BUILD_MODE NOWIND_BUILD_MODE)" \
        XIASS_RUNTIME_CORE_READY="$core_ready" \
        XIASS_RUNTIME_SKIP_CORE_START="$skip_core_start" \
        bash "$RUNTIME_START_SCRIPT"
        return $?
    fi

    # Older rollback targets do not contain the two-phase helper yet. Keep
    # their recovery path functional; the next successful update installs it.
    if [ "$skip_core_start" = true ]; then
        log "启动辅助脚本不可用；主应用已热切换，仅检查健康，不启动其他服务。"
        wait_for_health
        return $?
    fi
    log "旧版本没有两阶段启动脚本，使用兼容 Compose 恢复路径。"
    compose up -d --no-build --pull never
    wait_for_health
}

deployment_build_mode() {
    local mode
    mode=$(read_env_compat XIASS_BUILD_MODE NOWIND_BUILD_MODE)
    printf '%s\n' "${mode:-image}"
}

cleanup_version_probe() {
    [ -n "$VERSION_PROBE_CONTAINER" ] || return 0
    docker rm -f "$VERSION_PROBE_CONTAINER" >/dev/null 2>&1 \
        || { log "无法清理版本探测容器 ${VERSION_PROBE_CONTAINER}，请检查 Docker 状态。"; return 1; }
    VERSION_PROBE_CONTAINER=""
}

verify_prepared_app_image() {
    local config_json version_output image_version attempt probe_state
    config_json=$(compose config --format json) \
        || die "无法读取 Compose 的最终镜像配置，请使用支持 JSON 配置输出的 Docker Compose；旧应用保持运行。"
    TARGET_APP_IMAGE=$(printf '%s' "$config_json" | jq -er \
        '.services["xiass-api"].image | select(type == "string" and length > 0)') \
        || die "无法确定 xiass-api 的最终镜像；旧应用保持运行。"
    TARGET_APP_IMAGE_ID=$(docker image inspect --format '{{.Id}}' "$TARGET_APP_IMAGE") \
        || die "准备好的应用镜像不可读取；旧应用保持运行。"
    [[ "$TARGET_APP_IMAGE_ID" =~ ^sha256:[a-f0-9]{64}$ ]] \
        || die "应用镜像 ID 无效；旧应用保持运行。"
    # Bypass the deployment entrypoint and all Compose mounts/env. Only the
    # image binary runs, offline, without the production database or volumes.
    VERSION_PROBE_CONTAINER="xiass-update-version-$$-$RANDOM"
    docker run -d --name "$VERSION_PROBE_CONTAINER" --pull never --network none --read-only \
        --cap-drop ALL --security-opt no-new-privileges \
        --entrypoint /app/xiass-api "$TARGET_APP_IMAGE_ID" --version >/dev/null \
        || die "准备好的镜像无法执行版本校验；旧应用保持运行。"
    for attempt in $(seq 1 15); do
        probe_state=$(docker inspect --type container --format '{{.State.Status}} {{.State.ExitCode}}' "$VERSION_PROBE_CONTAINER") \
            || die "无法读取版本探测结果；旧应用保持运行。"
        case "$probe_state" in
            'exited 0')
                version_output=$(docker logs "$VERSION_PROBE_CONTAINER") \
                    || die "无法读取镜像版本输出；旧应用保持运行。"
                cleanup_version_probe || die "版本探测容器清理失败；未开始切换。"
                break ;;
            exited*|dead*) die "镜像版本探测失败；旧应用保持运行。" ;;
        esac
        sleep 1
    done
    [ -z "$VERSION_PROBE_CONTAINER" ] || die "镜像版本探测超过 15 秒，将清理探测容器；旧应用保持运行。"
    image_version=$(printf '%s\n' "$version_output" | sed -nE 's/^XIASS API (v?[0-9]+\.[0-9]+\.[0-9]+) \(commit: .*\)$/\1/p')
    image_version=$(normalize_release_version "$image_version") \
        || die "镜像没有返回有效的 XIASS 稳定版本；旧应用保持运行。"
    [ "$image_version" = "$TARGET_VERSION" ] \
        || die "镜像 $TARGET_APP_IMAGE 实际版本 ${image_version}，不是目标 ${TARGET_VERSION}；请检查固定镜像或镜像发布状态，旧应用保持运行。"
    printf '%s %s %s\n' "$TARGET_APP_IMAGE" "$TARGET_APP_IMAGE_ID" "$image_version" \
        > "$PREVIOUS_COMPOSE_SNAPSHOT/target-app-image"
    log "已验证准备好的镜像版本 ${image_version}，镜像 ID ${TARGET_APP_IMAGE_ID}。"
}

verify_target_image_id() {
    local image_id
    image_id=$(docker image inspect --format '{{.Id}}' "$TARGET_APP_IMAGE") || return 1
    [ "$image_id" = "$TARGET_APP_IMAGE_ID" ]
}

verify_running_target() {
    local container_id image_state port response version current_container_id
    container_id=$(compose ps -q xiass-api) || return 1
    [ -n "$container_id" ] || { log "未找到更新后的应用容器。"; return 1; }
    image_state=$(docker inspect --type container --format '{{.Image}} {{.State.Running}}' "$container_id") || return 1
    [ "$image_state" = "$TARGET_APP_IMAGE_ID true" ] \
        || { log "运行中的应用不是已校验的目标镜像，或容器已经停止。"; return 1; }
    port=$(read_env_value SERVER_PORT)
    port="${port:-8080}"
    response=$(curl -fsS --noproxy '*' --max-time 10 -H 'Cache-Control: no-cache' \
        "http://127.0.0.1:${port}/api/v1/settings/public") || return 1
    version=$(printf '%s' "$response" | jq -er 'select(.code == 0) | .data.version | select(type == "string")') || return 1
    version=$(normalize_release_version "$version") || return 1
    [ "$version" = "$TARGET_VERSION" ] \
        || { log "服务实际返回版本 ${version}，目标为 ${TARGET_VERSION}；不能仅凭健康检查判定更新成功。"; return 1; }
    response=$(curl -fsS --noproxy '*' --max-time 3 \
        "http://127.0.0.1:${port}/readyz") || {
        log "新版应用尚未满足数据库、Redis 或节点调度就绪条件；不能报告更新成功。"
        return 1
    }
    printf '%s' "$response" | jq -e '.status == "ok" and .checks.postgres == "ok" and .checks.redis == "ok" and .checks.execution_node == "ok"' >/dev/null \
        || { log "新版应用依赖就绪检查未通过。"; return 1; }
    current_container_id=$(compose ps -q xiass-api) || return 1
    [ "$current_container_id" = "$container_id" ] || return 1
    image_state=$(docker inspect --type container --format '{{.Image}} {{.State.Running}}' "$container_id") || return 1
    [ "$image_state" = "$TARGET_APP_IMAGE_ID true" ] || return 1
    RUNNING_APP_VERSION="$version"
    log "运行版本已核实：${RUNNING_APP_VERSION}（${container_id}）。"
}

prefetch_target_app_image() {
    local up_help
    compose config --quiet || die "更新后的 Compose 校验失败；旧容器保持运行。"
    up_help=$(compose up --help) || return 1
    if ! printf '%s\n' "$up_help" | grep -q -- '--pull'; then
        die "当前 Compose 不支持 --pull never，请先升级 Docker Compose；旧容器保持运行，未开始切换。"
    fi
    if [ "$(deployment_build_mode)" = "source" ]; then
        log "旧服务保持在线，构建 XIASS 主应用镜像..."
        compose build xiass-api \
            || die "主应用构建失败；旧容器保持运行，未开始切换。"
    else
        log "旧服务保持在线，预拉取 XIASS 主应用镜像..."
        compose pull xiass-api \
            || die "无法预拉取 XIASS 主应用镜像；旧容器保持运行，未开始切换。"
    fi
    verify_prepared_app_image
    if [ "$APP_CONTAINER" != "xiass-api" ]; then
        local service services
        services=$(compose config --services) || return 1
        for service in postgres redis watchtower; do
            if printf '%s\n' "$services" | awk -v service="$service" '$0 == service { found = 1 } END { exit !found }'; then
                compose pull "$service" || die "兼容迁移依赖镜像准备失败；旧容器保持运行。"
            fi
        done
    fi
    TARGET_APP_IMAGE_PREFETCHED=true
}

can_hot_swap_canonical_app() {
    [ "$APP_CONTAINER" = "xiass-api" ] \
        && [ "$TARGET_APP_IMAGE_PREFETCHED" = true ]
}

hot_swap_canonical_app() {
    local switch_started=$SECONDS
    log "仅热切换 XIASS 应用容器；PostgreSQL、Redis 和持久化数据保持在线..."
    APP_REPLACEMENT_STARTED=true
    compose up -d --no-deps --no-build --pull never --force-recreate xiass-api \
        || return 1
    wait_for_health || return 1
    verify_running_target || return 1
    log "应用替换和健康检查耗时 $((SECONDS - switch_started)) 秒；不含镜像准备时间。"
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
    if [ -n "$PREVIOUS_COMPOSE_SNAPSHOT" ]; then
        printf '%s %s\n' "$PREVIOUS_IMAGE_ID" "$PREVIOUS_IMAGE_REF" > "$PREVIOUS_COMPOSE_SNAPSHOT/app-image"
    fi
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
    log "更新失败，正在恢复原 Git 状态和应用配置..."
    if "$APP_REPLACEMENT_STARTED"; then
        log "注意：应用镜像回滚不会撤销已执行的数据库迁移，也不会恢复数据库备份；跨版本数据库兼容性需另行确认。"
    fi

    if "$APP_REPLACEMENT_STARTED" && [ "$APP_CONTAINER" != "xiass-api" ] && [ -f "$COMPOSE_FILE" ]; then
        init_compose
        compose down >/dev/null 2>&1 || true
        stop_known_runtime_containers
    fi

    if ! git -C "$INSTALL_DIR" reset --hard "$PREVIOUS_REF" >/dev/null 2>&1; then
        remove_created_update_remote || true
        if "$UPDATE_BACKUP_CREATED"; then
            log "无法自动恢复 Git 到 ${PREVIOUS_REF}；更新前完整备份仍位于 ${BACKUP_DIR}。"
        else
            log "无法自动恢复 Git 到 ${PREVIOUS_REF}；数据卷未被更新器删除或替换，请保留现场排查。"
        fi
        return
    fi
    remove_created_update_remote || true

    if ! restore_previous_config; then
        log "无法恢复原配置；轻量快照仍位于 ${PREVIOUS_COMPOSE_SNAPSHOT}，请保留现场排查。"
        return 1
    fi
    if [ "${#ORIGINAL_COMPOSE_FILES[@]}" -gt 0 ]; then
        COMPOSE_FILES=("${ORIGINAL_COMPOSE_FILES[@]}")
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

    if ! "$APP_REPLACEMENT_STARTED"; then
        log "准备阶段失败，旧应用容器未停止；已恢复部署配置。"
        return 0
    fi
    if [ "$APP_CONTAINER" = "xiass-api" ]; then
        if ! "$image_tag_restored"; then
            log "旧镜像不可用，停止自动回滚以免再次启动未经验证的镜像；数据库和缓存保持在线。"
            return 1
        fi
        if compose up -d --no-deps --no-build --pull never --force-recreate xiass-api && wait_for_health; then
            log "旧应用容器已恢复，PostgreSQL、Redis 和节点配对数据未重启或回退。"
        else
            log "旧应用未通过健康检查，请检查日志；数据库和缓存保持在线。"
        fi
        return
    fi

    if "$image_tag_restored"; then
        if start_runtime_stack true >/dev/null 2>&1; then
            rollback_started=true
        else
            log "使用旧应用镜像启动失败，将按原 compose 配置再次尝试恢复。"
        fi
    fi
    if ! "$rollback_started" && compose up -d --no-build --pull never >/dev/null 2>&1; then
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
    cleanup_version_probe || true
    if [ "$status" -ne 0 ] && "$UPDATE_STARTED" && ! "$UPDATE_SUCCEEDED"; then
        rollback_update
    elif [ "$status" -ne 0 ]; then
        remove_created_update_remote || true
    fi
    if "$LOCK_HELD"; then
        rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
    fi
    exit "$status"
}

ensure_jq() {
    command -v jq >/dev/null 2>&1 && return 0
    log "正在补齐更新所需的 jq；当前应用保持运行。"
    if command -v apt-get >/dev/null 2>&1; then
        apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y jq
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y jq
    elif command -v yum >/dev/null 2>&1; then
        yum install -y jq
    elif command -v apk >/dev/null 2>&1; then
        apk add --no-cache jq
    else
        die "请先安装 jq 后重新更新；当前应用未停止。"
    fi
    command -v jq >/dev/null 2>&1 || die "jq 安装未成功；当前应用未停止。"
}

main() {
    local prepare_started=$SECONDS
    [ "$(id -u)" -eq 0 ] || die "请使用 sudo 或 root 运行。"
    for command_name in curl git docker mktemp; do
        command -v "$command_name" >/dev/null 2>&1 || die "缺少依赖：$command_name"
    done
    resolve_install_dir
    [ -d "$INSTALL_DIR/.git" ] || die "$INSTALL_DIR 不是 XIASS Git 安装目录。"
    [ -f "$DEPLOY_DIR/.env" ] || die "未找到 .env。"
    ensure_jq

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
    resolve_stable_release
    snapshot_previous_compose

    if update_full_backup_enabled; then
        log "已启用更新前完整冷备；备份期间服务会短暂停止..."
        curl -fsSL "$RAW_BASE_URL/xiass-backup.sh" \
            | INSTALL_DIR="$INSTALL_DIR" BACKUP_DIR="$BACKUP_DIR" SKIP_MAINTENANCE_LOCK=true bash
        UPDATE_BACKUP_CREATED=true
    else
        log "使用低中断更新：保留现有数据卷，跳过会停止整套服务的完整冷备。"
    fi

    local patch_file=""
    if ! git -C "$INSTALL_DIR" diff --quiet || ! git -C "$INSTALL_DIR" diff --cached --quiet; then
        patch_file="$PREVIOUS_COMPOSE_SNAPSHOT/local-changes.patch"
        git -C "$INSTALL_DIR" diff HEAD > "$patch_file"
        chmod 600 "$patch_file"
        log "本地源码修改已备份到 $patch_file"
    fi

    log "同步已验证的 XIASS API 部署文件..."
    capture_previous_image
    capture_team_child_browser_profile

    UPDATE_STARTED=true
    git -C "$INSTALL_DIR" reset --hard "$UPDATE_REF"
    restore_local_compose
    ensure_team_child_runtime_config
    if [ "$APP_CONTAINER" != "xiass-api" ]; then
        COMPOSE_FILES=()
        ACTUAL_COMPOSE_FILES=()
        select_compose_file true
    fi
    init_compose
    if ! git -C "$INSTALL_DIR" diff --quiet "$PREVIOUS_REF" "$UPDATE_REF" -- backend/migrations; then
        log "此版本包含数据库迁移变更；镜像回滚不能撤销迁移，请确认已有独立数据库备份及升级兼容性。"
    fi
    prefetch_target_app_image
    log "更新准备耗时 $((SECONDS - prepare_started)) 秒；尚未开始应用切换（显式完整冷备除外）。"
    verify_target_image_id || die "切换前镜像 ID 已变化或不可读取；旧应用保持运行，请重新更新。"

    if can_hot_swap_canonical_app; then
        # Keep the existing browser mount; never copy its profile while running.
        if ! hot_swap_canonical_app; then
            compose ps || true
            compose logs --tail 160 xiass-api || true
            die "新版应用容器热切换失败。数据没有删除，将自动恢复旧版本。"
        fi
        # The page can recover as soon as the new application health check
        # passes. Converge unchanged core services and the Team browser stack
        # afterwards without another full-stack shutdown.
        if ! start_runtime_stack true true; then
            compose ps || true
            compose logs --tail 160 xiass-api || true
            die "更新后运行组件检查失败。数据没有删除，将自动恢复旧版本。"
        fi
    else
        log "历史部署布局使用完整兼容切换（不会删除卷或数据）..."
        APP_REPLACEMENT_STARTED=true
        if ! stop_previous_runtime; then
            log "Compose 未能完整停止旧栈，改为按已知容器名安全停止。"
        fi
        stop_known_runtime_containers

        # Prepare images first, then copy only after the legacy browser stops.
        migrate_team_child_browser_profile

        # A successfully prefetched image avoids placing registry download time
        # inside the legacy full-stack downtime window.
        if ! start_runtime_stack "$TARGET_APP_IMAGE_PREFETCHED"; then
            compose ps || true
            compose logs --tail 160 xiass-api || true
            die "更新后健康检查失败。更新器没有删除或替换现有数据卷，将自动恢复旧应用版本。"
        fi
    fi

    if ! wait_for_health; then
        compose ps || true
        compose logs --tail 160 xiass-api || true
        die "更新后健康检查失败。更新器没有删除或替换现有数据卷，将自动恢复旧应用版本。"
    fi
    if ! verify_running_target; then
        die "更新后运行版本或镜像身份校验失败，将自动恢复旧应用配置和镜像。"
    fi

    UPDATE_SUCCEEDED=true
    trap - EXIT INT TERM
    rmdir "$LOCK_DIR" >/dev/null 2>&1 || true
    LOCK_HELD=false
    printf '\nXIASS 更新完成，实际运行版本 %s；PostgreSQL、Redis、应用数据和 .env 均沿用原目录。\n' "$RUNNING_APP_VERSION"
    log "轻量配置快照保留在 ${PREVIOUS_COMPOSE_SNAPSHOT}；未复制或重置业务数据。"
    if [ -n "$patch_file" ]; then
        printf '原本的本地修改补丁：%s\n' "$patch_file"
    fi
}

if [ "${XIASS_UPDATE_LIB_ONLY:-0}" != "1" ]; then
    main "$@"
fi
