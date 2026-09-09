#!/usr/bin/env bash
# Restore a XIASS logical migration package onto a Linux Docker host.

set -Eeuo pipefail
umask 077

PACKAGE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
INSTALL_DIR="/opt/xiass-api"
NEW_DOMAIN=""
ASSUME_YES=false
PREVIOUS_INSTALL=""
PREVIOUS_COMPOSE_FILE=""
DEPLOY_DIR=""
COMPOSE=()
RESTORE_CONFIG=""
RESTORE_REDIS_USER=""
RESTORE_REDIS_PASSWORD=""
ORIGINAL_ARGS=("$@")

log() { printf '[XIASS] %s\n' "$*"; }
warn() { printf '[XIASS] 警告：%s\n' "$*" >&2; }
die() { printf '[XIASS] 错误：%s\n' "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
Usage: sudo ./restore-xiass.sh [--domain new.example.com] [--install-dir /opt/xiass-api] [--yes]
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --domain)
            [ "$#" -ge 2 ] || die "--domain requires a value"
            NEW_DOMAIN="$2"
            shift 2
            ;;
        --install-dir)
            [ "$#" -ge 2 ] || die "--install-dir requires a path"
            INSTALL_DIR="$2"
            shift 2
            ;;
        --yes|-y)
            ASSUME_YES=true
            shift
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

case "$INSTALL_DIR" in
    /*) ;;
    *) die "install directory must be absolute" ;;
esac

if [ -n "$NEW_DOMAIN" ]; then
    NEW_DOMAIN="${NEW_DOMAIN#http://}"
    NEW_DOMAIN="${NEW_DOMAIN#https://}"
    NEW_DOMAIN="${NEW_DOMAIN%%/*}"
    [[ "$NEW_DOMAIN" =~ ^[A-Za-z0-9.-]+(:[0-9]{1,5})?$ ]] || die "invalid replacement domain"
fi

if [ "$(id -u)" -ne 0 ]; then
    command -v sudo >/dev/null 2>&1 || die "run this script as root or install sudo"
    exec sudo -E bash "$0" "${ORIGINAL_ARGS[@]}"
fi

install_packages() {
    local manager=""
    if command -v apt-get >/dev/null 2>&1; then
        manager="apt"
    elif command -v dnf >/dev/null 2>&1; then
        manager="dnf"
    elif command -v yum >/dev/null 2>&1; then
        manager="yum"
    elif command -v apk >/dev/null 2>&1; then
        manager="apk"
    else
        die "unsupported package manager; install Docker, Docker Compose, curl, wget, tar, gzip, jq, and coreutils manually"
    fi

    case "$manager" in
        apt)
            apt-get update
            DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl wget tar gzip jq coreutils docker.io docker-compose-plugin || \
                DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl wget tar gzip jq coreutils docker.io docker-compose
            ;;
        dnf)
            dnf install -y ca-certificates curl wget tar gzip jq coreutils docker docker-compose-plugin || \
                dnf install -y ca-certificates curl wget tar gzip jq coreutils docker docker-compose
            ;;
        yum)
            yum install -y ca-certificates curl wget tar gzip jq coreutils docker docker-compose-plugin || \
                yum install -y ca-certificates curl wget tar gzip jq coreutils docker docker-compose
            ;;
        apk)
            apk add --no-cache ca-certificates curl wget tar gzip jq coreutils docker docker-cli-compose
            ;;
    esac
}

ensure_runtime() {
    local required_command
    for required_command in docker curl wget tar gzip jq sha256sum; do
        if ! command -v "$required_command" >/dev/null 2>&1; then
            log "installing required system packages"
            install_packages
            break
        fi
    done
    command -v docker >/dev/null 2>&1 || die "Docker installation failed"
    for required_command in curl wget tar gzip jq sha256sum; do
        command -v "$required_command" >/dev/null 2>&1 || die "required command is unavailable after installation: $required_command"
    done
    if command -v systemctl >/dev/null 2>&1; then
        systemctl enable --now docker >/dev/null 2>&1 || true
    fi
    docker info >/dev/null 2>&1 || die "Docker daemon is not ready"
    if docker compose version >/dev/null 2>&1; then
        COMPOSE=(docker compose)
    elif command -v docker-compose >/dev/null 2>&1; then
        COMPOSE=(docker-compose)
    else
        install_packages
        if docker compose version >/dev/null 2>&1; then
            COMPOSE=(docker compose)
        elif command -v docker-compose >/dev/null 2>&1; then
            COMPOSE=(docker-compose)
        else
            die "Docker Compose installation failed"
        fi
    fi
}

manifest_value() {
    local key="$1"
    jq -r --arg key "$key" '.[$key] // empty' "$PACKAGE_DIR/manifest.json"
}

verify_package() {
    [ -f "$PACKAGE_DIR/manifest.json" ] || die "missing manifest.json"
    [ -f "$PACKAGE_DIR/checksums.sha256" ] || die "missing checksums.sha256"
    [ -x "$PACKAGE_DIR/restore-xiass.sh" ] || die "restore script permissions are invalid"
    [ -f "$PACKAGE_DIR/payload/postgres.sql.gz" ] || die "missing PostgreSQL export"
    [ -f "$PACKAGE_DIR/payload/redis.rdb" ] || die "missing Redis export"
    [ -d "$PACKAGE_DIR/payload/app-data" ] || die "missing application data"
    [ -f "$PACKAGE_DIR/payload/.env" ] || die "missing deployment .env"
    [ -f "$PACKAGE_DIR/deploy/docker-compose.local.yml" ] || die "missing canonical docker-compose.local.yml"
    (cd "$PACKAGE_DIR" && sha256sum -c checksums.sha256)
    case "$(manifest_value format_version)" in
        1) ;;
        2)
            [ -s "$PACKAGE_DIR/payload/runtime-context.json" ] || die "missing effective runtime configuration"
            [ -s "$PACKAGE_DIR/payload/redis.acl" ] || die "missing Redis ACL configuration"
            jq -e '.version == 2 and (.config | type == "object") and (.environment | type == "object")' "$PACKAGE_DIR/payload/runtime-context.json" >/dev/null || die "invalid runtime configuration"
            ;;
        *) die "unsupported migration package format" ;;
    esac
}

compose() {
    local overrides=()
    [ -z "$RESTORE_CONFIG" ] || overrides=(-f "$RESTORE_CONFIG")
    "${COMPOSE[@]}" -f "$DEPLOY_DIR/docker-compose.local.yml" "${overrides[@]}" --project-directory "$DEPLOY_DIR" "$@"
}

compose_at() {
    local deploy_dir="$1" compose_file="$2"
    shift 2
    "${COMPOSE[@]}" -f "$deploy_dir/$compose_file" --project-directory "$deploy_dir" "$@"
}

stop_existing_install() {
    local existing_install="$1" existing_deploy compose_file
    existing_deploy="$existing_install/deploy"
    [ -d "$existing_deploy" ] || return 0
    for compose_file in docker-compose.local.yml docker-compose.yml; do
        if [ -f "$existing_deploy/$compose_file" ]; then
            PREVIOUS_COMPOSE_FILE="$compose_file"
            log "stopping existing XIASS containers before restore"
            compose_at "$existing_deploy" "$compose_file" down --remove-orphans
            return 0
        fi
    done
    warn "existing install has no recognized Compose file; refusing an unsafe overwrite"
    return 1
}

read_env_value() {
    local key="$1"
    awk -F= -v key="$key" '$1 == key {sub(/^[^=]*=/, ""); print; exit}' "$DEPLOY_DIR/.env" 2>/dev/null
}

replace_domain_in_payload() {
    local old_domain="$1" new_domain="$2" file escaped_old escaped_new
    [ -n "$old_domain" ] && [ -n "$new_domain" ] || return 0
    [ "$old_domain" != "$new_domain" ] || return 0
    escaped_old=$(printf '%s' "$old_domain" | sed 's/[.[\\*^$\\/]/\\\\&/g')
    escaped_new=$(printf '%s' "$new_domain" | sed 's/[&\\/]/\\\\&/g')
    while IFS= read -r -d '' file; do
        grep -Iq . "$file" || continue
        if grep -q -- "$old_domain" "$file"; then
            sed -i "s/${escaped_old}/${escaped_new}/g" "$file"
        fi
    done < <(find "$PACKAGE_DIR/payload" "$PACKAGE_DIR/deploy" -type f -print0)
}

wait_for_postgres() {
    local attempt
    for attempt in $(seq 1 90); do
        if compose exec -T postgres sh -c 'exec pg_isready -U "${POSTGRES_USER:-sub2api}" -d "${POSTGRES_DB:-sub2api}"' >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    return 1
}

wait_for_health() {
    local endpoint host port attempt
    endpoint=$(compose port xiass-api 8080) || return 1
    endpoint="${endpoint%%$'\n'*}"
    host="${endpoint%:*}"
    port="${endpoint##*:}"
    case "$host" in
        0.0.0.0) host=127.0.0.1 ;;
        '::'|'[::]') host='[::1]' ;;
    esac
    [ -n "$host" ] && [[ "$port" =~ ^[0-9]+$ ]] || return 1
    for attempt in $(seq 1 120); do
        if curl -fsS --max-time 3 "http://${host}:${port}/readyz" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    return 1
}

prepare_restored_runtime() {
    [ "$(manifest_value format_version)" = "2" ] || return 0
    local source="$PACKAGE_DIR/payload/runtime-context.json" password app_image redis_image
    password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
    app_image=$(manifest_value app_image)
    redis_image=$(manifest_value redis_image)
    [ -n "$app_image" ] && [ -n "$redis_image" ] || die "missing source image identity"
    if [ "$(jq -r '.config.gateway.execution_node.enabled' "$source")" = "true" ]; then
        warn "此压缩包恢复为独立实例；业务数据与账号归属记录保留，恢复后需重新配对其他节点，不会自动连接旧集群。"
    fi
    XIASS_RESTORE_PG_PASSWORD="$password" jq '
        .config | .database.host="postgres" | .database.port=5432 | .database.sslmode="disable"
        | .database.password=env.XIASS_RESTORE_PG_PASSWORD
        | .redis.host="redis" | .redis.port=6379 | .redis.enable_tls=false
        | .redis.sentinel_addrs=[] | .redis.sentinel_master_name="" | .redis.sentinel_username="" | .redis.sentinel_password=""
        | .server.host="0.0.0.0" | .server.port=8080
        | .gateway.execution_node.enabled=false
        | .gateway.execution_node.witness.enabled=false
        | .gateway.execution_node.witness.url="" | .gateway.execution_node.witness.token=""
        | .gateway.execution_node.witness.cluster_id=""
        | .gateway.forced_codex_instructions_template_file=""
    ' "$source" > "$DEPLOY_DIR/data/xiass-restored-config.json"
    # Config values come from the effective snapshot. Empty optional env values
    # leave that snapshot authoritative, while later .env edits/pairing still work.
    jq '[.config | (paths(scalars), paths(type=="array")) | map(tostring|ascii_upcase) | join("_")]
        + [.environment | keys[]] | unique
        | map(. as $key | select(test("^[A-Z_][A-Z0-9_]*$") and
          (["HOSTNAME","HOME","PATH","PWD","SHLVL","USER","LOGNAME","SHELL","_"] | index($key) | not)))' \
        "$source" > "$DEPLOY_DIR/.restore-bindings.json"
    jq --slurpfile effective "$DEPLOY_DIR/data/xiass-restored-config.json" --slurpfile keys "$DEPLOY_DIR/.restore-bindings.json" --arg install "$INSTALL_DIR" '
        . as $source | $effective[0] as $cfg | (.config|keys|map(ascii_upcase)) as $prefixes
        | reduce $keys[0][] as $key ({};
            .[$key] = (if any($prefixes[]; . as $p | $key==$p or ($key|startswith($p+"_"))) or ($key|startswith("XIASS_CLUSTER_"))
                       then "" else ($source.environment[$key] // "") end))
        | del(.SERVER_PORT)
        | . + {CONFIG_FILE:"/app/data/xiass-restored-config.json", DATA_DIR:"/app/data",
            POSTGRES_USER:$cfg.database.user, POSTGRES_PASSWORD:$cfg.database.password, POSTGRES_DB:$cfg.database.dbname,
            DATABASE_HOST:"postgres", DATABASE_PORT:"5432", DATABASE_SSLMODE:"disable",
            DATABASE_USER:$cfg.database.user, DATABASE_PASSWORD:$cfg.database.password, DATABASE_DBNAME:$cfg.database.dbname,
            REDIS_HOST:"redis", REDIS_PORT:"6379", REDIS_USERNAME:$cfg.redis.username, REDIS_PASSWORD:$cfg.redis.password,
            REDIS_DB:($cfg.redis.db|tostring), REDIS_ENABLE_TLS:"false", JWT_SECRET:$cfg.jwt.secret,
            JWT_REFRESH_TOKEN_STORE:$cfg.jwt.refresh_token_store, TOTP_ENCRYPTION_KEY:$cfg.totp.encryption_key,
            GATEWAY_EXECUTION_NODE_ENABLED:"false", XIASS_CLUSTER_STATE_SOURCE_URL:"", XIASS_CLUSTER_TUNNEL_TOKEN:"",
            XIASS_CLUSTER_NODE_URLS_JSON:"{}", XIASS_REDIS_BACKUP_CREDENTIALS_FILE:($install+"/deploy/ha/secrets/redis-backup.json")}
    ' "$source" > "$DEPLOY_DIR/.restore-environment.json"
    local name value escaped
    while IFS= read -r -d '' name && IFS= read -r -d '' value; do
        escaped=${value//\\/\\\\}
        escaped=${escaped//\'/\\\'}
        printf "%s='%s'\n" "$name" "$escaped" >> "$DEPLOY_DIR/.env"
    done < <(jq -j 'to_entries[] | .key,"\u0000",(.value|tostring),"\u0000"' "$DEPLOY_DIR/.restore-environment.json")
    RESTORE_CONFIG="$DEPLOY_DIR/docker-compose.restore.json"
    jq --slurpfile effective "$DEPLOY_DIR/data/xiass-restored-config.json" --slurpfile keys "$DEPLOY_DIR/.restore-bindings.json" --slurpfile managed "$DEPLOY_DIR/.restore-environment.json" \
        --arg image "$app_image" --arg redis_image "$redis_image" --arg install "$INSTALL_DIR" '
        $effective[0] as $cfg
        | (reduce (($keys[0] + ($managed[0]|keys))|unique)[] as $key ({}; .[$key]="${"+$key+"-}")) as $environment
        | {services:{
            "xiass-api": {image:$image, environment:($environment + {SERVER_HOST:"0.0.0.0", SERVER_PORT:"8080"})},
            postgres: {environment:{POSTGRES_USER:$cfg.database.user, POSTGRES_PASSWORD:$cfg.database.password, POSTGRES_DB:$cfg.database.dbname}},
            redis: {image:$redis_image, command:["redis-server","/data/redis.conf"],
                environment:{REDIS_USERNAME:($cfg.redis.username // ""),REDISCLI_AUTH:$cfg.redis.password},
                healthcheck:{test:["CMD-SHELL","redis-cli --user \"${REDIS_USERNAME:-default}\" ping"]}}
        }} | .services.postgres.environment |= with_entries(.value |= gsub("\\$";"$$"))
           | .services.redis.environment |= with_entries(.value |= gsub("\\$";"$$"))
           | .services.redis.healthcheck.test[1] |= gsub("\\$";"$$")
    ' "$source" > "$RESTORE_CONFIG"
    rm -f "$DEPLOY_DIR/.restore-environment.json" "$DEPLOY_DIR/.restore-bindings.json"
    chmod 600 "$RESTORE_CONFIG" "$DEPLOY_DIR/data/xiass-restored-config.json"
    if [ -f "$PACKAGE_DIR/payload/redis-backup.json" ]; then
        mkdir -p "$DEPLOY_DIR/ha/secrets"
        chmod 700 "$DEPLOY_DIR/ha/secrets"
        cp "$PACKAGE_DIR/payload/redis-backup.json" "$DEPLOY_DIR/ha/secrets/redis-backup.json"
        chmod 600 "$DEPLOY_DIR/ha/secrets/redis-backup.json"
    fi
    cp "$PACKAGE_DIR/payload/redis.rdb" "$DEPLOY_DIR/redis_data/dump.rdb"
    cp "$PACKAGE_DIR/payload/redis.acl" "$DEPLOY_DIR/redis_data/users.acl"
    RESTORE_REDIS_USER="xiass-restore-$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')"
    RESTORE_REDIS_PASSWORD=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
    printf '\nuser %s on >%s ~* &* +@all\n' "$RESTORE_REDIS_USER" "$RESTORE_REDIS_PASSWORD" >> "$DEPLOY_DIR/redis_data/users.acl"
    printf '%s\n' 'dir /data' 'aclfile /data/users.acl' 'dbfilename dump.rdb' 'save 60 1' 'appendonly no' 'appendfsync everysec' > "$DEPLOY_DIR/redis_data/redis.conf"
    chmod 600 "$DEPLOY_DIR/redis_data/users.acl" "$DEPLOY_DIR/redis_data/redis.conf"
    docker run --rm --user 0 --entrypoint sh -v "$DEPLOY_DIR/redis_data:/data" "$redis_image" -c 'chown -R redis:redis /data'
}

finish_redis_restore() {
    local attempt persistence auth_reply auth_status=0
    for attempt in $(seq 1 90); do
        if compose exec -T -e REDISCLI_AUTH="$RESTORE_REDIS_PASSWORD" redis redis-cli -e --user "$RESTORE_REDIS_USER" PING >/dev/null 2>&1; then break; fi
        sleep 1
    done
    compose exec -T redis redis-check-rdb /data/dump.rdb >/dev/null
    compose exec -T -e REDISCLI_AUTH="$RESTORE_REDIS_PASSWORD" redis redis-cli -e --user "$RESTORE_REDIS_USER" CONFIG SET appendonly yes >/dev/null
    for attempt in $(seq 1 120); do
        persistence=$(compose exec -T -e REDISCLI_AUTH="$RESTORE_REDIS_PASSWORD" redis redis-cli -e --user "$RESTORE_REDIS_USER" --raw INFO persistence | tr -d '\r')
        if printf '%s\n' "$persistence" | grep -qx 'aof_rewrite_in_progress:0' && printf '%s\n' "$persistence" | grep -qx 'aof_last_bgrewrite_status:ok'; then break; fi
        sleep 1
    done
    printf '%s\n' "$persistence" | grep -qx 'aof_enabled:1' || die "Redis AOF did not become active"
    printf '%s\n' "$persistence" | grep -qx 'aof_rewrite_in_progress:0' || die "Redis AOF rewrite did not finish"
    printf '%s\n' "$persistence" | grep -qx 'aof_last_bgrewrite_status:ok' || die "Redis AOF rewrite failed"
    # This generated file owns only persistence settings. CONFIG REWRITE also
    # copies image-injected modules, which would load twice on the next boot.
    sed -i 's/^appendonly no$/appendonly yes/' "$DEPLOY_DIR/redis_data/redis.conf"
    chown --reference="$DEPLOY_DIR/redis_data/users.acl" "$DEPLOY_DIR/redis_data/redis.conf"
    # Restore the exact original ACL, removing the temporary local installer.
    cp "$PACKAGE_DIR/payload/redis.acl" "$DEPLOY_DIR/redis_data/users.acl"
    chmod 600 "$DEPLOY_DIR/redis_data/users.acl"
    compose exec -T -e REDISCLI_AUTH="$RESTORE_REDIS_PASSWORD" redis redis-cli -e --user "$RESTORE_REDIS_USER" ACL LOAD >/dev/null
    compose restart redis
    for attempt in $(seq 1 90); do
        if compose exec -T redis sh -c 'exec redis-cli -e --user "${REDIS_USERNAME:-default}" PING' >/dev/null 2>&1; then break; fi
        sleep 1
    done
    compose exec -T redis sh -c 'exec redis-cli -e --user "${REDIS_USERNAME:-default}" PING' >/dev/null
    # PING can succeed as the default nopass user even after automatic AUTH fails.
    auth_reply=$(printf '%s' "$RESTORE_REDIS_PASSWORD" | compose exec -T redis sh -c \
        'unset REDISCLI_AUTH; exec redis-cli -e --raw -x AUTH "$1"' sh "$RESTORE_REDIS_USER" 2>&1) || auth_status=$?
    [ "$auth_status" -eq 1 ] && [[ "$auth_reply" == WRONGPASS\ * ]] || \
        die "temporary Redis installer identity removal could not be verified"
    RESTORE_REDIS_PASSWORD=""
}

rollback() {
    local status=$?
    if [ "$status" -ne 0 ] && [ -n "$PREVIOUS_INSTALL" ] && [ -d "$PREVIOUS_INSTALL" ]; then
        warn "restore failed; restoring the previous installation directory"
        if [ -n "$DEPLOY_DIR" ] && [ -f "$DEPLOY_DIR/docker-compose.local.yml" ]; then
            compose down >/dev/null 2>&1 || true
        fi
        rm -rf "$INSTALL_DIR"
        mv "$PREVIOUS_INSTALL" "$INSTALL_DIR"
        if [ -n "$PREVIOUS_COMPOSE_FILE" ] && [ -f "$INSTALL_DIR/deploy/$PREVIOUS_COMPOSE_FILE" ]; then
            compose_at "$INSTALL_DIR/deploy" "$PREVIOUS_COMPOSE_FILE" up -d >/dev/null 2>&1 || \
                warn "previous XIASS containers could not be restarted automatically"
        fi
    fi
    exit "$status"
}

ensure_runtime
verify_package

SOURCE_DOMAIN=$(manifest_value source_origin)
SOURCE_DOMAIN="${SOURCE_DOMAIN#http://}"
SOURCE_DOMAIN="${SOURCE_DOMAIN#https://}"
SOURCE_DOMAIN="${SOURCE_DOMAIN%%/*}"

if ! "$ASSUME_YES"; then
    [ -r /dev/tty ] || die "non-interactive execution requires --yes"
    read -r -p "This will restore XIASS data into ${INSTALL_DIR}. Continue? [y/N]: " answer < /dev/tty || true
    [[ "$answer" =~ ^[yY]$ ]] || exit 0
fi

if [ -n "$NEW_DOMAIN" ]; then
    if [ -n "$SOURCE_DOMAIN" ]; then
        log "replacing source domain $SOURCE_DOMAIN with $NEW_DOMAIN"
        replace_domain_in_payload "$SOURCE_DOMAIN" "$NEW_DOMAIN"
    else
        warn "the source domain was not recorded in this package; no blind replacement was performed"
    fi
fi

timestamp=$(date +%Y%m%d-%H%M%S)
if [ -e "$INSTALL_DIR" ]; then
    PREVIOUS_INSTALL="${INSTALL_DIR}.before-xiass-restore-${timestamp}"
    log "preserving any existing install at $PREVIOUS_INSTALL"
    stop_existing_install "$INSTALL_DIR" || die "could not stop the existing XIASS deployment"
    mv "$INSTALL_DIR" "$PREVIOUS_INSTALL"
fi
trap rollback EXIT INT TERM

DEPLOY_DIR="$INSTALL_DIR/deploy"
mkdir -p "$DEPLOY_DIR"
cp -a "$PACKAGE_DIR/deploy/." "$DEPLOY_DIR/"
cp "$PACKAGE_DIR/payload/.env" "$DEPLOY_DIR/.env"
chmod 600 "$DEPLOY_DIR/.env"

rm -rf "$DEPLOY_DIR/data" "$DEPLOY_DIR/redis_data" "$DEPLOY_DIR/team_child_browser_data" "$DEPLOY_DIR/team_child_automation_data"
mkdir -p "$DEPLOY_DIR/data" "$DEPLOY_DIR/redis_data"
cp -a "$PACKAGE_DIR/payload/app-data/." "$DEPLOY_DIR/data/"
if [ -d "$PACKAGE_DIR/payload/team-child-browser-data" ]; then
    mkdir -p "$DEPLOY_DIR/team_child_browser_data"
    cp -a "$PACKAGE_DIR/payload/team-child-browser-data/." "$DEPLOY_DIR/team_child_browser_data/"
fi
if [ -d "$PACKAGE_DIR/payload/team-child-automation-data" ]; then
    mkdir -p "$DEPLOY_DIR/team_child_automation_data"
    cp -a "$PACKAGE_DIR/payload/team-child-automation-data/." "$DEPLOY_DIR/team_child_automation_data/"
fi
prepare_restored_runtime

log "pulling XIASS images and starting PostgreSQL/Redis"
compose pull postgres redis xiass-api watchtower
if [ "$(read_env_value TEAM_CHILD_BROWSER_ENABLED)" = "true" ]; then
    compose --profile team-browser pull team-child-browser team-child-automation
fi
compose up -d postgres redis
wait_for_postgres || die "PostgreSQL did not become ready"

postgres_user=$(read_env_value POSTGRES_USER)
postgres_user="${postgres_user:-sub2api}"
postgres_db=$(read_env_value POSTGRES_DB)
postgres_db="${postgres_db:-sub2api}"
log "restoring PostgreSQL logical backup"
gzip -dc "$PACKAGE_DIR/payload/postgres.sql.gz" | compose exec -T postgres sh -c '
    export PGPASSWORD="${POSTGRES_PASSWORD:-}"
    exec psql -v ON_ERROR_STOP=1 -h 127.0.0.1 -U "${POSTGRES_USER:-sub2api}" -d "${POSTGRES_DB:-sub2api}"
'

log "restoring Redis snapshot"
if [ -n "$RESTORE_CONFIG" ]; then
    finish_redis_restore
else
    compose stop redis
    rm -rf "$DEPLOY_DIR/redis_data/appendonlydir" "$DEPLOY_DIR/redis_data/dump.rdb"
    cp "$PACKAGE_DIR/payload/redis.rdb" "$DEPLOY_DIR/redis_data/dump.rdb"
    compose up -d redis
fi

log "starting XIASS API"
compose up -d xiass-api watchtower
if [ "$(read_env_value TEAM_CHILD_BROWSER_ENABLED)" = "true" ]; then
    compose --profile team-browser up -d team-child-browser team-child-automation
fi
wait_for_health || die "XIASS did not pass the health check"

trap - EXIT INT TERM
log "restore complete"
if [ -n "$PREVIOUS_INSTALL" ]; then
    log "previous installation preserved at: $PREVIOUS_INSTALL"
fi
