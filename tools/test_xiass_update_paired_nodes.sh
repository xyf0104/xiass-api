#!/usr/bin/env bash
# Real Git, Compose rendering and updater/runtime scripts; no Docker daemon or network.
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
export REAL_GIT="$(command -v git)" REAL_DOCKER="$(command -v docker)"
"$REAL_DOCKER" compose version >/dev/null || fail 'Docker Compose CLI is required (no daemon needed)'
command -v python3 >/dev/null || fail 'python3 is required for JSON assertions'
mkdir -p "$ROOT_DIR/../artifacts"
TEST_DIR=$(mktemp -d "$ROOT_DIR/../artifacts/xiass-paired-update.XXXXXX")
trap 'rm -rf "$TEST_DIR"' EXIT
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0
export GIT_AUTHOR_NAME=Fixture GIT_AUTHOR_EMAIL=fixture@example.invalid
export GIT_COMMITTER_NAME=Fixture GIT_COMMITTER_EMAIL=fixture@example.invalid
mkdir -p "$TEST_DIR/bin" "$TEST_DIR/home"

cat > "$TEST_DIR/bin/git" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
if [ "${3:-}" = fetch ]; then
    [ "$*" = "-C $INSTALL_DIR fetch --no-tags origin refs/tags/v9.8.7" ] || exit 91
    exec "$REAL_GIT" -C "$INSTALL_DIR" fetch --no-tags "$FIXTURE_ORIGIN" refs/tags/v9.8.7
fi
exec "$REAL_GIT" "$@"
SH
cat > "$TEST_DIR/bin/id" <<'SH'
#!/usr/bin/env bash
[ "$*" = -u ] || exit 91
printf '0\n'
SH
cat > "$TEST_DIR/bin/sleep" <<'SH'
#!/usr/bin/env bash
exit 0
SH
cat > "$TEST_DIR/bin/curl" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
case "${!#}" in
    https://api.github.com/repos/xyf0104/xiass-api/releases/latest)
        printf '{"tag_name":"v9.8.7","draft":false,"prerelease":false}\n'; exit 0 ;;
    http://127.0.0.1:18080/api/v1/settings/public)
        if [ "$SCENARIO" = runtime-version-failure ]; then
            printf '{"code":0,"data":{"version":"9.8.6"}}\n'
        else
            printf '{"code":0,"data":{"version":"9.8.7"}}\n'
        fi
        exit 0 ;;
    http://127.0.0.1:18080/readyz)
        printf '{"status":"ok","checks":{"postgres":"ok","redis":"ok","execution_node":"ok"}}\n'
        exit 0 ;;
esac
[ "${!#}" = http://127.0.0.1:18080/health ] || exit 91
printf 'health\n' >> "$FIXTURE_NODE/calls"
if [ "$SCENARIO" = health-failure ] && [ "$(cat "$FIXTURE_NODE/switch-count")" = 1 ]; then exit 22; fi
SH
cat > "$TEST_DIR/bin/docker" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
reject() { printf 'UNEXPECTED: %s\n' "$*" >> "$FIXTURE_NODE/calls"; exit 91; }
image_id="sha256:$(printf '%064d' 1)"
case "$*" in
    'compose version') exec "$REAL_DOCKER" compose version ;;
    'container inspect '*)
        case "$3" in xiass-api|xiass-api-postgres|xiass-api-redis) exit 0 ;; *) exit 1 ;; esac ;;
    'image tag sha256:fixture-old fixture/app:latest') printf 'restore-image\n' >> "$FIXTURE_NODE/calls"; exit 0 ;;
    'image inspect --format {{.Id}} '*)
        printf '%s\n' "${!#}" > "$FIXTURE_NODE/inspected-image"
        printf '%s\n' "$image_id"; exit 0 ;;
    'run -d --name xiass-update-version-'*)
        [ "$*" = "run -d --name $4 --pull never --network none --read-only --cap-drop ALL --security-opt no-new-privileges --entrypoint /app/xiass-api $image_id --version" ] || reject 'unsafe probe'
        printf 'probe-image\n' >> "$FIXTURE_NODE/calls"
        exit 0 ;;
    'rm -f xiass-update-version-'*) printf 'remove-probe\n' >> "$FIXTURE_NODE/calls"; exit 0 ;;
    'logs xiass-update-version-'*)
        if [ "$(cat "$FIXTURE_NODE/inspected-image")" = ghcr.io/xyf0104/xiass-api:9.8.6 ]; then
            printf 'XIASS API 9.8.6 (commit: old, built: fixture)\n'
        else
            printf 'XIASS API 9.8.7 (commit: stable, built: fixture)\n'
        fi
        exit 0 ;;
    'inspect --type container --format '*)
        case "$5" in
            *project.config_files*) cat "$FIXTURE_NODE/compose-labels" ;;
            *project.working_dir*) printf '%s/deploy\n' "$INSTALL_DIR" ;;
            *'"com.docker.compose.project"'*) printf '%s\n' "$FIXTURE_PROJECT" ;;
            *'.Image}} {{.Config.Image}'*) printf 'sha256:fixture-old fixture/app:latest\n' ;;
            '{{.Image}} {{.State.Running}}') printf '%s true\n' "$image_id" ;;
            '{{.State.Status}} {{.State.ExitCode}}') printf 'exited 0\n' ;;
            *'/var/lib/postgresql/data'*) [ "$LAYOUT" = named ] && printf 'volume\n' || printf 'bind\n' ;;
            *) reject "$@" ;;
        esac
        exit 0 ;;
esac
[ "${1:-}" = compose ] || reject "$@"
shift
args=()
while [ "$#" -gt 0 ]; do
    case "$1" in
        -f|--project-directory|--project-name|--env-file|--profile) args+=("$1" "$2"); shift 2 ;;
        *) break ;;
    esac
done
printf 'compose %s\n' "$*" >> "$FIXTURE_NODE/calls"
case "$*" in
    'config --quiet')
        [ "$SCENARIO" != config-failure ] || exit 1
        exec "$REAL_DOCKER" compose "${args[@]}" config --quiet ;;
    'config --services') exec "$REAL_DOCKER" compose "${args[@]}" config --services ;;
    'config --format json') exec "$REAL_DOCKER" compose "${args[@]}" config --format json ;;
    'up --help') printf 'up options: --pull policy\n' ;;
    'pull xiass-api')
        [ "$(cat "$FIXTURE_NODE/switch-count")" = 0 ] || reject 'pull after app replacement'
        [ "$SCENARIO" != pull-failure ] || exit 1
        touch "$FIXTURE_NODE/prepared" ;;
    'up -d --no-deps --no-build --pull never --force-recreate xiass-api')
        [ -f "$FIXTURE_NODE/prepared" ] || reject 'app replacement before image preparation'
        count=$(( $(cat "$FIXTURE_NODE/switch-count") + 1 ))
        printf '%s\n' "$count" > "$FIXTURE_NODE/switch-count"
        "$REAL_DOCKER" compose "${args[@]}" config --format json > "$FIXTURE_NODE/model-$count.json" ;;
    ps|'logs --tail 160 xiass-api') : ;;
    'ps -q xiass-api') printf 'updated-app\n' ;;
    *) reject "$@" ;;
esac
SH
chmod +x "$TEST_DIR/bin/"*

check_model() {
    python3 - "$1" "$2" <<'PY'
import json
import sys

before, after = [json.load(open(path)) for path in sys.argv[1:]]
assert before['name'] == after['name'], 'running Compose project identity changed'
for key in ('environment', 'volumes', 'networks', 'network_mode', 'extra_hosts'):
    old = before['services']['xiass-api'].get(key)
    new = after['services']['xiass-api'].get(key)
    assert old == new, key + ' changed: ' + repr(old) + ' != ' + repr(new)
for key in ('volumes', 'networks'):
    assert before.get(key) == after.get(key), key + ' identity changed'
assert set(before['services']) == set(after['services']), 'unexpected resident service added'
PY
}

setup_node() {
    local node="$1" role="$2" policy="$3" deploy base
    deploy="$node/install/deploy"
    mkdir -p "$node/install" "$node/state"
    "$REAL_GIT" -C "$node/install" init -q
    "$REAL_GIT" -C "$node/install" fetch -q "$FIXTURE_ORIGIN" main
    "$REAL_GIT" -C "$node/install" checkout -qb main "$OLD_REF"
    "$REAL_GIT" -C "$node/install" tag -f v9.8.7 "$OLD_REF" >/dev/null
    "$REAL_GIT" -C "$node/install" remote add origin https://github.com/xyf0104/xiass-api.git
    base=docker-compose.local.yml
    [ "$LAYOUT" != named ] || base=docker-compose.yml
    cat > "$deploy/.env" <<EOF
# Existing paired node configuration, including literal secret punctuation.
SERVER_PORT=18080
POSTGRES_USER=fixture
POSTGRES_PASSWORD='fixture-pg-\$#=literal'
POSTGRES_DB=fixture
DATABASE_USER=fixture
DATABASE_PASSWORD='fixture-pg-\$#=literal'
DATABASE_DBNAME=fixture
DATABASE_HOST='$([ "$role" = primary ] && printf postgres || printf 127.0.0.1)'
DATABASE_PORT='$([ "$role" = primary ] && printf 5432 || printf 15432)'
REDIS_HOST='$([ "$role" = primary ] && printf redis || printf 127.0.0.1)'
REDIS_PORT='$([ "$role" = primary ] && printf 6379 || printf 16379)'
REDIS_PASSWORD='fixture-redis-\$#=literal'
REDIS_DB=3
JWT_SECRET='fixture-shared-jwt'
TOTP_ENCRYPTION_KEY='fixture-shared-totp'
GATEWAY_EXECUTION_NODE_ENABLED='true'
GATEWAY_EXECUTION_NODE_ID='$role'
GATEWAY_EXECUTION_NODE_CONTROL_PLANE='$([ "$role" = primary ] && printf true || printf false)'
GATEWAY_EXECUTION_NODE_DEFAULT_PROXY_ID=17
XIASS_CLUSTER_STATE_SOURCE_URL='https://primary.example.invalid'
XIASS_CLUSTER_TUNNEL_TOKEN='fixture-pair-proof'
XIASS_CLUSTER_NODE_URLS_JSON='{"primary":"https://primary.example.invalid","worker":"https://worker.example.invalid"}'
TEAM_CHILD_BROWSER_ENABLED=false
TEAM_CHILD_BROWSER_START_DELAY_SECONDS=5
TEAM_CHILD_BROWSER_CONTROL_TTL_SECONDS=120
TEAM_CHILD_AUTOMATION_TOKEN=fixture-team
TEAM_CHILD_AUTOMATION_IMAGE=ghcr.io/xyf0104/xiass-team-child-automation:latest
XIASS_UPDATER_IMAGE=ghcr.io/xyf0104/xiass-updater:latest
EOF
    [ "$policy" = unset ] || printf "JWT_REFRESH_TOKEN_STORE='%s'\n" "$policy" >> "$deploy/.env"
    cat > "$deploy/node runtime.yml" <<'YAML'
services:
  xiass-api:
    environment:
      XIASS_FIXTURE_ROUTE: paired-loopback
      XIASS_CLUSTER_STATE_SOURCE_URL: ${XIASS_CLUSTER_STATE_SOURCE_URL}
      XIASS_CLUSTER_TUNNEL_TOKEN: ${XIASS_CLUSTER_TUNNEL_TOKEN}
      XIASS_CLUSTER_NODE_URLS_JSON: ${XIASS_CLUSTER_NODE_URLS_JSON}
    extra_hosts:
      - 'paired-loopback:127.0.0.1'
YAML
    cat > "$deploy/proxy.yml" <<'YAML'
services:
  xiass-api:
    environment:
      XIASS_FIXTURE_PROXY: socks5://127.0.0.1:11080
YAML
    if [ "$SCENARIO" = pinned-image-failure ]; then
        printf '    image: ghcr.io/xyf0104/xiass-api:9.8.6\n' >> "$deploy/proxy.yml"
    fi
    printf '%s,node runtime.yml,%s/proxy.yml\n' "$base" "$deploy" > "$node/compose-labels"
    printf '0\n' > "$node/switch-count"
    : > "$node/calls"
    # These files model persistent data, not a real HA database acceptance test.
    for dir in data postgres_data redis_data; do
        mkdir -p "$deploy/$dir"
        printf '%s-existing-data\n' "$role" > "$deploy/$dir/sentinel"
    done
    printf '{"primary":9,"worker":1}\n' > "$deploy/postgres_data/pairing-weights.json"
    cp "$deploy/.env" "$node/env.before"
    cp "$deploy/node runtime.yml" "$node/node.before"
    cp "$deploy/proxy.yml" "$node/proxy.before"
    env -i HOME="$TEST_DIR/home" PATH="$PATH" \
        "$REAL_DOCKER" compose -f "$deploy/$base" -f "$deploy/node runtime.yml" -f "$deploy/proxy.yml" \
        --project-directory "$deploy" --project-name "paired-$role" config --format json > "$node/model-before.json"
}

run_update() {
    local node="$1" role="$2" status=0 expected=0
    case "$SCENARIO" in *failure|missing-overlay) expected=1 ;; esac
    env -i HOME="$TEST_DIR/home" PATH="$TEST_DIR/bin:$PATH" \
        REAL_GIT="$REAL_GIT" REAL_DOCKER="$REAL_DOCKER" \
        GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GIT_TERMINAL_PROMPT=0 \
        INSTALL_DIR="$node/install" BACKUP_DIR="$node/backups" \
        FIXTURE_NODE="$node" FIXTURE_PROJECT="paired-$role" FIXTURE_ORIGIN="$FIXTURE_ORIGIN" \
        SCENARIO="$SCENARIO" LAYOUT="$LAYOUT" XIASS_UPDATE_FULL_BACKUP=false \
        bash -c '
            XIASS_UPDATE_LIB_ONLY=1 source "$1"
            LOCK_DIR="$FIXTURE_NODE/maintenance.lock"
            # Explicit fixture directory, without Bash 4 automatic host discovery.
            resolve_install_dir() { DEPLOY_DIR="$INSTALL_DIR/deploy"; }
            main
        ' bash "$ROOT_DIR/deploy/xiass-update.sh" > "$node/output" 2>&1 || status=$?
    if [ "$status" != "$expected" ]; then
        sed -n '1,100p' "$node/output"
        fail "$SCENARIO/$role returned $status, expected $expected"
    fi
    if grep -Eq 'UNEXPECTED|compose (down|stop|restart)|volume rm' "$node/calls"; then
        cat "$node/calls"
        fail 'unexpected container or volume mutation'
    fi
    cmp -s "$node/env.before" "$node/install/deploy/.env" || fail 'env bytes or refresh-token policy changed'
    [ "$SCENARIO" = missing-overlay ] || cmp -s "$node/node.before" "$node/install/deploy/node runtime.yml" || fail 'untracked runtime override changed'
    cmp -s "$node/proxy.before" "$node/install/deploy/proxy.yml" || fail 'proxy override changed'
    for dir in data postgres_data redis_data; do
        grep -Fqx "$role-existing-data" "$node/install/deploy/$dir/sentinel" || fail 'persistent data changed'
    done
    grep -Fqx '{"primary":9,"worker":1}' "$node/install/deploy/postgres_data/pairing-weights.json" || fail 'pairing weights changed'
    [ ! -d "$node/maintenance.lock" ] || fail 'maintenance lock leaked'
    case "$SCENARIO" in
        pull-failure|config-failure|missing-overlay|pinned-image-failure)
            [ "$(cat "$node/switch-count")" = 0 ] || fail 'old app replaced before preparation succeeded' ;;
        health-failure|runtime-version-failure)
            [ "$(cat "$node/switch-count")" = 2 ] || fail 'rollback did not replace only the app'
            check_model "$node/model-before.json" "$node/model-2.json" ;;
        *)
            [ "$(cat "$node/switch-count")" = 1 ] || fail 'update must replace the app exactly once'
            check_model "$node/model-before.json" "$node/model-1.json" ;;
    esac
    local expected_ref="$NEW_REF"
    [ "$expected" = 0 ] || expected_ref="$OLD_REF"
    [ "$("$REAL_GIT" -C "$node/install" rev-parse HEAD)" = "$expected_ref" ] || fail 'Git update/rollback ref is wrong'
    if [ "$expected" = 0 ]; then
        grep -Fq '实际运行版本 9.8.7' "$node/output" || fail 'success did not report the verified version'
    elif grep -Fq 'XIASS 更新完成' "$node/output"; then
        fail 'version or preparation failure was reported as success'
    fi
}

export FIXTURE_ORIGIN="$TEST_DIR/origin"
mkdir -p "$FIXTURE_ORIGIN/deploy"
mkdir -p "$FIXTURE_ORIGIN/backend/cmd/server"
"$REAL_GIT" -C "$FIXTURE_ORIGIN" init -q
"$REAL_GIT" -C "$FIXTURE_ORIGIN" checkout -qb main
cp "$ROOT_DIR/deploy/docker-compose.local.yml" "$ROOT_DIR/deploy/docker-compose.yml" \
    "$ROOT_DIR/deploy/xiass-runtime-start.sh" "$FIXTURE_ORIGIN/deploy/"
printf '.env\ndata/\npostgres_data/\nredis_data/\n' > "$FIXTURE_ORIGIN/deploy/.gitignore"
printf '9.8.6\n' > "$FIXTURE_ORIGIN/backend/cmd/server/VERSION"
"$REAL_GIT" -C "$FIXTURE_ORIGIN" add .
"$REAL_GIT" -C "$FIXTURE_ORIGIN" commit -qm 'fixture old release'
OLD_REF=$("$REAL_GIT" -C "$FIXTURE_ORIGIN" rev-parse HEAD)
# A new tracked file can clobber the same untracked path during reset --hard.
printf 'services: {xiass-api: {environment: {XIASS_FIXTURE_ROUTE: overwritten}}}\n' > "$FIXTURE_ORIGIN/deploy/node runtime.yml"
printf '9.8.7\n' > "$FIXTURE_ORIGIN/backend/cmd/server/VERSION"
"$REAL_GIT" -C "$FIXTURE_ORIGIN" add .
"$REAL_GIT" -C "$FIXTURE_ORIGIN" commit -qm 'fixture next release'
NEW_REF=$("$REAL_GIT" -C "$FIXTURE_ORIGIN" rev-parse HEAD)
"$REAL_GIT" -C "$FIXTURE_ORIGIN" tag -a v9.8.7 -m 'fixture stable release'
printf '9.9.0\n' > "$FIXTURE_ORIGIN/backend/cmd/server/VERSION"
"$REAL_GIT" -C "$FIXTURE_ORIGIN" commit -qam 'fixture main ahead of stable'

for SCENARIO in rolling-default rolling-redis rolling-postgres pull-failure config-failure health-failure missing-overlay pinned-image-failure runtime-version-failure; do
    LAYOUT=local
    policy=unset
    case "$SCENARIO" in rolling-redis) policy=redis ;; rolling-postgres|health-failure) policy=postgres; LAYOUT=named ;; esac
    pair="$TEST_DIR/$SCENARIO"
    setup_node "$pair/primary" primary "$policy"
    setup_node "$pair/worker" worker "$policy"
    if [ "$SCENARIO" = missing-overlay ]; then rm "$pair/primary/install/deploy/node runtime.yml"; fi
    run_update "$pair/primary" primary
    [ "$("$REAL_GIT" -C "$pair/worker/install" rev-parse HEAD)" = "$OLD_REF" ] || fail 'first update touched peer Git state'
    [ ! -s "$pair/worker/calls" ] || fail 'first update touched peer runtime'
    cmp -s "$pair/worker/env.before" "$pair/worker/install/deploy/.env" || fail 'first update changed peer env'
    case "$SCENARIO" in
        rolling-*) run_update "$pair/worker" worker ;;
    esac
    if [ "$SCENARIO" = rolling-default ]; then
        printf '0\n' > "$pair/primary/switch-count"
        : > "$pair/primary/calls"
        run_update "$pair/primary" primary
        patch=$(find "$pair/primary/backups/update-config" -name local-changes.patch -print)
        [ -s "$patch" ] || fail 'subsequent update did not back up the now-tracked local override'
        grep -Fq paired-loopback "$patch" || fail 'local override is missing from the retained patch'
    fi
    printf 'PASS: paired %s (%s, refresh-token store %s)\n' "$SCENARIO" "$LAYOUT" "$policy"
done
printf 'xiass paired-node isolated update tests passed (no live services contacted).\n'
