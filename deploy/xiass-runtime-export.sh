#!/usr/bin/env bash
# Build an online XIASS migration package from a running Docker deployment.
# This script runs only inside the short-lived XIASS updater sidecar. The
# sidecar gets a read-only view of the host installation plus Docker-socket
# access. It publishes only the finished archive into protected application
# data through Docker's scoped file-copy operation.

set -Eeuo pipefail
umask 077

: "${INSTALL_DIR:?INSTALL_DIR is required}"

OUTPUT=""
PUBLISH_TO_CONTAINER=""
PUBLISH_CONTAINER=""
PUBLISH_PATH=""
PUBLISH_TEMP_PATH=""
PUBLISH_COMPLETED=false
SOURCE_ORIGIN="${XIASS_SOURCE_ORIGIN:-}"
APP_CONTAINER=""
POSTGRES_FROM_CONTAINER=""
RUNTIME_CONTEXT_FROM_CONTAINER=""
REDIS_CONTAINER=""
TEAM_BROWSER_CONTAINER=""
TEAM_AUTOMATION_CONTAINER=""
WORK_DIR=""
REDIS_SNAPSHOT_CLIENT=""

log() { printf '[XIASS] %s\n' "$*"; }
die() { printf '[XIASS] 错误：%s\n' "$*" >&2; exit 1; }

cleanup() {
    local status=$?
    if [ -n "$REDIS_SNAPSHOT_CLIENT" ]; then
        docker rm -fv "$REDIS_SNAPSHOT_CLIENT" >/dev/null 2>&1 || true
    fi
    if [ -n "$PUBLISH_CONTAINER" ] && [ -n "$PUBLISH_TEMP_PATH" ] && [ "$PUBLISH_COMPLETED" != "true" ] && command -v docker >/dev/null 2>&1; then
        docker exec "$PUBLISH_CONTAINER" rm -f "$PUBLISH_TEMP_PATH" >/dev/null 2>&1 || true
    fi
    [ -z "$WORK_DIR" ] || rm -rf "$WORK_DIR"
    exit "$status"
}

usage() {
    cat <<'EOF'
Usage: xiass-runtime-export.sh --output /tmp/xiass-migration.tar.gz --postgres-from-container container:/app/data/runtime-exports/.postgres-xxx.sql.gz
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --output)
            [ "$#" -ge 2 ] || die "--output requires a path"
            OUTPUT="$2"
            shift 2
            ;;
        --publish-to-container)
            [ "$#" -ge 2 ] || die "--publish-to-container requires a target"
            PUBLISH_TO_CONTAINER="$2"
            shift 2
            ;;
        --postgres-from-container)
            [ "$#" -ge 2 ] || die "--postgres-from-container requires a target"
            POSTGRES_FROM_CONTAINER="$2"
            shift 2
            ;;
        --runtime-context-from-container)
            [ "$#" -ge 2 ] || die "--runtime-context-from-container requires a target"
            RUNTIME_CONTEXT_FROM_CONTAINER="$2"
            shift 2
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            die "unknown argument: $1"
            ;;
    esac
done

[ -n "$OUTPUT" ] || die "--output is required"
case "$OUTPUT" in
    /*) ;;
    *) die "output path must be absolute" ;;
esac
if [ -n "$PUBLISH_TO_CONTAINER" ]; then
    case "$PUBLISH_TO_CONTAINER" in
        *:* )
            PUBLISH_CONTAINER="${PUBLISH_TO_CONTAINER%%:*}"
            PUBLISH_PATH="${PUBLISH_TO_CONTAINER#*:}"
            [ -n "$PUBLISH_CONTAINER" ] && [ -n "$PUBLISH_PATH" ] || die "publish target must include a container and absolute path"
            case "$PUBLISH_PATH" in
                /*) ;;
                *) die "publish target path must be absolute" ;;
            esac
            PUBLISH_TEMP_PATH="${PUBLISH_PATH}.partial"
            ;;
        * ) die "publish target must use container:path format" ;;
    esac
fi

command -v docker >/dev/null 2>&1 || die "Docker CLI is unavailable"
command -v tar >/dev/null 2>&1 || die "tar is unavailable"
command -v gzip >/dev/null 2>&1 || die "gzip is unavailable"
command -v jq >/dev/null 2>&1 || die "jq is unavailable"
[ -S /var/run/docker.sock ] || die "Docker socket is unavailable"
[ -f "$INSTALL_DIR/deploy/.env" ] || die "missing deployment .env"
[ -f "$INSTALL_DIR/deploy/docker-compose.local.yml" ] || die "missing canonical docker-compose.local.yml"

container_is_running() {
    [ "$(docker inspect --type container --format '{{.State.Running}}' "$1" 2>/dev/null || true)" = "true" ]
}

container_exists() {
    docker inspect --type container "$1" >/dev/null 2>&1
}

find_container() {
    local candidate
    for candidate in "$@"; do
        if container_is_running "$candidate"; then
            printf '%s\n' "$candidate"
            return 0
        fi
    done
    return 1
}

APP_CONTAINER="${PUBLISH_CONTAINER:-}"
if [ -z "$APP_CONTAINER" ]; then
    APP_CONTAINER=$(find_container xiass-api nowind-api sub2api) || die "XIASS application container is not running"
fi
container_is_running "$APP_CONTAINER" || die "XIASS application container is not running"
snapshot_container="${POSTGRES_FROM_CONTAINER%%:*}"
snapshot_path="${POSTGRES_FROM_CONTAINER#*:}"
case "$snapshot_path" in
    /app/data/runtime-exports/.postgres-*.sql.gz) ;;
    *) die "missing current application database snapshot; refusing to export a local fallback database" ;;
esac
case "${snapshot_path#/app/data/runtime-exports/}" in
    */*) die "invalid database snapshot path" ;;
esac
[ "$(docker inspect --format '{{.Id}}' "$snapshot_container")" = "$(docker inspect --format '{{.Id}}' "$APP_CONTAINER")" ] || die "database snapshot does not belong to the current application"
context_container="${RUNTIME_CONTEXT_FROM_CONTAINER%%:*}"
context_path="${RUNTIME_CONTEXT_FROM_CONTAINER#*:}"
case "$context_path" in
    /app/data/runtime-exports/.runtime-context-*.json) ;;
    *) die "missing effective application configuration" ;;
esac
case "${context_path#/app/data/runtime-exports/}" in
    */*) die "invalid runtime context path" ;;
esac
[ "$(docker inspect --format '{{.Id}}' "$context_container")" = "$(docker inspect --format '{{.Id}}' "$APP_CONTAINER")" ] || die "runtime context does not belong to the current application"
REDIS_CONTAINER=$(find_container xiass-api-redis nowind-api-redis sub2api-redis || true)
TEAM_BROWSER_CONTAINER=$(find_container xiass-api-team-child-browser nowind-api-team-child-browser sub2api-team-child-browser || true)
TEAM_AUTOMATION_CONTAINER=$(find_container xiass-api-team-child-automation nowind-api-team-child-automation sub2api-team-child-automation || true)

# Browser and automation containers are intentionally optional. Their named or
# bind-mounted profile data is still readable through docker cp while stopped,
# so retain it whenever the containers exist rather than only when they happen
# to be running at the moment a migration package is requested.
if [ -z "$TEAM_BROWSER_CONTAINER" ]; then
    for candidate in xiass-api-team-child-browser nowind-api-team-child-browser sub2api-team-child-browser; do
        if container_exists "$candidate"; then
            TEAM_BROWSER_CONTAINER="$candidate"
            break
        fi
    done
fi
if [ -z "$TEAM_AUTOMATION_CONTAINER" ]; then
    for candidate in xiass-api-team-child-automation nowind-api-team-child-automation sub2api-team-child-automation; do
        if container_exists "$candidate"; then
            TEAM_AUTOMATION_CONTAINER="$candidate"
            break
        fi
    done
fi

WORK_DIR=$(mktemp -d)
trap cleanup EXIT INT TERM
mkdir -p "$WORK_DIR/payload/app-data" "$WORK_DIR/deploy"
docker cp "$RUNTIME_CONTEXT_FROM_CONTAINER" "$WORK_DIR/payload/runtime-context.json"
jq -e '.version == 2 and (.config | type == "object") and (.environment | type == "object")' "$WORK_DIR/payload/runtime-context.json" >/dev/null || die "invalid effective runtime context"

log "copying the current application's PostgreSQL logical snapshot"
docker cp "$POSTGRES_FROM_CONTAINER" "$WORK_DIR/payload/postgres.sql.gz"
gzip -t "$WORK_DIR/payload/postgres.sql.gz"

log "exporting Redis RDB snapshot"
runtime_context="$WORK_DIR/payload/runtime-context.json"
redis_image="redis:8-alpine"
if [ -n "$REDIS_CONTAINER" ]; then
    redis_image=$(docker inspect --format '{{.Config.Image}}' "$REDIS_CONTAINER")
fi
# The CLI shares the application's network namespace, so a paired instance's
# loopback tunnel is the same endpoint here, not the local Redis container.
redis_cli() {
    local host="$1" port="$2" username="$3" password="$4"
    shift 4
    local tls_args=() user_args=() auth_args=()
    if [ "$(jq -r '.config.redis.enable_tls' "$runtime_context")" = "true" ]; then
        tls_args=(--tls --sni "$host")
    fi
    [ -z "$username" ] || user_args=(--user "$username")
    if [ -n "$password" ] || [ -n "$username" ]; then
        auth_args=(--env REDISCLI_AUTH)
    fi
    REDISCLI_AUTH="$password" docker run --rm --network "container:$APP_CONTAINER" \
        "${auth_args[@]}" --entrypoint redis-cli "$redis_image" -e --no-auth-warning \
        -h "$host" -p "$port" "${tls_args[@]}" "${user_args[@]}" "$@"
}

read_secret() {
    # NUL cannot be represented in an environment variable. Preserve all other
    # bytes, including a trailing newline, without evaluating shell contents.
    jq -e "$2 | type == \"string\" and (index(\"\\u0000\") == null)" "$1" >/dev/null || die "invalid Redis credential"
    IFS= read -r -d '' SECRET_VALUE < <(jq -j "$2, \"\\u0000\"" "$1") || true
}

redis_host=$(jq -r '.config.redis.host' "$runtime_context")
redis_port=$(jq -r '.config.redis.port' "$runtime_context")
redis_username=$(jq -r '.config.redis.username // ""' "$runtime_context")
read_secret "$runtime_context" '.config.redis.password'
redis_password="$SECRET_VALUE"
backup_credentials=$(jq -r '.environment.XIASS_REDIS_BACKUP_CREDENTIALS_FILE // empty' "$runtime_context")
backup_credentials="${backup_credentials:-$INSTALL_DIR/deploy/ha/secrets/redis-backup.json}"
if [ -e "$backup_credentials" ]; then
    [ -f "$backup_credentials" ] && [ ! -L "$backup_credentials" ] || die "Redis backup credentials must be a regular private file"
    case "$(stat -c '%a' "$backup_credentials")" in 400|600) ;; *) die "Redis backup credentials must have mode 0400 or 0600" ;; esac
    case "$(realpath "$backup_credentials")" in "$(realpath "$INSTALL_DIR")"/*) ;; *) die "Redis backup credentials must remain inside the installation directory" ;; esac
    redis_username=$(jq -er '.username | select(type == "string" and length > 0)' "$backup_credentials")
    read_secret "$backup_credentials" '.password'
    redis_password="$SECRET_VALUE"
    cp "$backup_credentials" "$WORK_DIR/payload/redis-backup.json"
fi

[[ "$redis_port" =~ ^[0-9]+$ ]] && [ "$redis_port" -gt 0 ] && [ "$redis_port" -le 65535 ] && [ -n "$redis_host" ] || die "invalid effective Redis endpoint"

redis_info() {
    redis_cli "$redis_host" "$redis_port" "$redis_username" "$redis_password" --raw INFO server replication | \
        jq -eRs 'split("\r\n") | map(select(contains(":")) | split(":") | {(.[0]): (.[1:] | join(":"))}) | add'
}
redis_info > "$WORK_DIR/payload/redis-source.json"
jq -e '.role == "master" and (.run_id | length == 40)' "$WORK_DIR/payload/redis-source.json" >/dev/null || die "refusing a non-primary Redis snapshot"
redis_cli "$redis_host" "$redis_port" "$redis_username" "$redis_password" --json ACL LIST > "$WORK_DIR/redis-acl.json"
jq -er 'select(type == "array" and length > 0 and all(.[]; type == "string")) | .[]' "$WORK_DIR/redis-acl.json" > "$WORK_DIR/payload/redis.acl"
redis_cli "$redis_host" "$redis_port" "$redis_username" "$redis_password" --json MODULE LIST > "$WORK_DIR/payload/redis-modules.json"
tls_args=(); user_args=(); auth_args=()
[ "$(jq -r '.config.redis.enable_tls' "$runtime_context")" != "true" ] || tls_args=(--tls --sni "$redis_host")
[ -z "$redis_username" ] || user_args=(--user "$redis_username")
if [ -n "$redis_password" ] || [ -n "$redis_username" ]; then
    auth_args=(--env REDISCLI_AUTH)
fi
for snapshot_attempt in 1 2; do
    REDIS_SNAPSHOT_CLIENT=$(REDISCLI_AUTH="$redis_password" docker create --network "container:$APP_CONTAINER" \
        --label com.xiass.role=runtime-export-client --label "com.xiass.runtime_export_source=$APP_CONTAINER" \
        "${auth_args[@]}" --entrypoint redis-cli "$redis_image" \
        -e --no-auth-warning -h "$redis_host" -p "$redis_port" "${tls_args[@]}" "${user_args[@]}" --rdb /tmp/xiass.rdb)
    docker start -a "$REDIS_SNAPSHOT_CLIENT" > "$WORK_DIR/redis-rdb.log" 2>&1 || die "Redis snapshot transfer failed"
    [ "$(docker inspect --format '{{.State.ExitCode}}' "$REDIS_SNAPSHOT_CLIENT")" = "0" ] || die "Redis snapshot export failed"
    docker cp "$REDIS_SNAPSHOT_CLIENT:/tmp/xiass.rdb" "$WORK_DIR/payload/redis.rdb"
    [ -s "$WORK_DIR/payload/redis.rdb" ] || die "Redis returned an empty snapshot"
    docker rm -fv "$REDIS_SNAPSHOT_CLIENT" >/dev/null
    REDIS_SNAPSHOT_CLIENT=""
    redis_info > "$WORK_DIR/redis-source-after.json"
    if jq -e --slurpfile after "$WORK_DIR/redis-source-after.json" '.role == "master" and $after[0].role == "master" and .run_id == $after[0].run_id and .master_replid == $after[0].master_replid' "$WORK_DIR/payload/redis-source.json" >/dev/null; then
        break
    fi
    # A first SYNC creates Redis's backlog and changes its replication ID.
    # Discard that snapshot; the retry must pass the unchanged identity check.
    if [ "$snapshot_attempt" = "1" ] && jq -e --slurpfile after "$WORK_DIR/redis-source-after.json" '
        .role == "master" and $after[0].role == "master" and .run_id == $after[0].run_id
        and .repl_backlog_active == "0" and $after[0].repl_backlog_active == "1"
        and $after[0].master_replid2 == "0000000000000000000000000000000000000000"
    ' "$WORK_DIR/payload/redis-source.json" >/dev/null; then
        log "Redis replication backlog initialized; repeating snapshot with stable identity"
        rm -f "$WORK_DIR/payload/redis.rdb"
        cp "$WORK_DIR/redis-source-after.json" "$WORK_DIR/payload/redis-source.json"
    else
        die "Redis primary changed during export; retry on the stable primary"
    fi
done
redis_cli "$redis_host" "$redis_port" "$redis_username" "$redis_password" --json ACL LIST > "$WORK_DIR/redis-acl-after.json"
[ "$(jq -c 'sort' "$WORK_DIR/redis-acl.json")" = "$(jq -c 'sort' "$WORK_DIR/redis-acl-after.json")" ] || die "Redis permissions changed during export; retry after migration completes"

log "exporting XIASS application data"
docker cp "$APP_CONTAINER:/app/data/." "$WORK_DIR/payload/app-data"
# The migration packages themselves live in /app/data so that the API can
# serve them after the short-lived exporter exits. They must not recursively
# include older archives or the package currently being assembled.
rm -rf "$WORK_DIR/payload/app-data/runtime-exports"

if [ -n "$TEAM_BROWSER_CONTAINER" ]; then
    log "exporting Team browser profile"
    mkdir -p "$WORK_DIR/payload/team-child-browser-data"
    docker cp "$TEAM_BROWSER_CONTAINER:/config/." "$WORK_DIR/payload/team-child-browser-data"
fi

if [ -n "$TEAM_AUTOMATION_CONTAINER" ]; then
    log "exporting Team automation data"
    mkdir -p "$WORK_DIR/payload/team-child-automation-data"
    docker cp "$TEAM_AUTOMATION_CONTAINER:/app/data/." "$WORK_DIR/payload/team-child-automation-data"
fi

log "copying deployment configuration"
cp "$INSTALL_DIR/deploy/.env" "$WORK_DIR/payload/.env"
chmod 600 "$WORK_DIR/payload/.env"
for compose_file in docker-compose.local.yml docker-compose.yml docker-compose.xiass.yml docker-compose.build.yml; do
    if [ -f "$INSTALL_DIR/deploy/$compose_file" ]; then
        cp "$INSTALL_DIR/deploy/$compose_file" "$WORK_DIR/deploy/$compose_file"
    fi
done
cp /usr/local/lib/xiass/xiass-runtime-restore.sh "$WORK_DIR/restore-xiass.sh"
chmod 700 "$WORK_DIR/restore-xiass.sh"

APP_IMAGE=$(docker inspect --format '{{.Config.Image}}' "$APP_CONTAINER" 2>/dev/null || true)
CREATED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
jq -n \
    --arg created_at "$CREATED_AT" \
    --arg source_origin "$SOURCE_ORIGIN" \
    --arg app_image "$APP_IMAGE" \
    --arg redis_image "$redis_image" \
    '{format_version: 2, created_at: $created_at, source_origin: $source_origin, app_image: $app_image, redis_image: $redis_image, layout: "logical"}' \
    > "$WORK_DIR/manifest.json"

cat > "$WORK_DIR/README.txt" <<'EOF'
XIASS API migration package

1. Extract this archive on the target Linux server.
2. Run: sudo ./restore-xiass.sh
3. To replace the original domain during restore, run:
   sudo ./restore-xiass.sh --domain new.example.com

This package contains secrets, account credentials, database data, and browser
profiles. Store it only in a trusted location and delete it after the migration
has been verified.
EOF

(
    cd "$WORK_DIR"
    find . -type f ! -name checksums.sha256 -print0 | sort -z | xargs -0 sha256sum > checksums.sha256
)

mkdir -p "$(dirname "$OUTPUT")"
TEMP_OUTPUT="${OUTPUT}.partial"
rm -f "$TEMP_OUTPUT"
tar -C "$WORK_DIR" -czf "$TEMP_OUTPUT" .
mv "$TEMP_OUTPUT" "$OUTPUT"
chmod 600 "$OUTPUT"

if [ -n "$PUBLISH_TO_CONTAINER" ]; then
    log "publishing migration package to XIASS application data"
    # A restart can make a new API process reconcile the export record while
    # this sidecar is still copying. Publish through a private temporary name,
    # then rename inside the application container so a final archive is only
    # ever visible once its complete byte stream is in place.
    docker exec "$PUBLISH_CONTAINER" rm -f "$PUBLISH_TEMP_PATH" >/dev/null 2>&1 || true
    docker cp "$OUTPUT" "$PUBLISH_CONTAINER:$PUBLISH_TEMP_PATH"
    docker exec --user 0 "$PUBLISH_CONTAINER" sh -ceu '
        test -s "$1"
        chown "$(stat -c "%u:%g" "$3")" "$1"
        chmod 600 "$1"
        mv -f "$1" "$2"
    ' sh "$PUBLISH_TEMP_PATH" "$PUBLISH_PATH" "$context_path"
    PUBLISH_COMPLETED=true
fi

log "runtime migration package created: $OUTPUT"
