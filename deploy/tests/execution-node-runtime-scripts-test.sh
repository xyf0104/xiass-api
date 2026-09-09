#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
JOIN_SCRIPT="$ROOT/xiass-cluster-join.sh"
RUNTIME_SCRIPT="$ROOT/xiass-cluster-runtime.sh"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

run_fixture() {
    # Each fixture has its own process. Keep mock state alive for EXIT rollback:
    # Bash 5 unwinds function-local variables before running that trap.
    flow="$1" scenario="$2" fixture="$3"
    script="$RUNTIME_SCRIPT" fixture_switches=0
    INSTALL_DIR="$fixture/install" BACKUP_DIR="$fixture/backups"
    deploy="$INSTALL_DIR/deploy" expected_files=() expected_project=paired-existing
    mkdir -p "$deploy/data" "$deploy/postgres_data" "$deploy/redis_data" "$fixture/external"
    printf 'services: {xiass-api: {image: fixture/app}}\n' > "$deploy/docker-compose.local.yml"
    cp "$deploy/docker-compose.local.yml" "$deploy/docker-compose.yml"
    printf 'services: {xiass-api: {build: ..}}\n' > "$deploy/docker-compose.build.yml"
    printf 'services: {xiass-api: {environment: {NODE_ROUTE: paired}}}\n' > "$deploy/node runtime.yml"
    printf 'services: {xiass-api: {environment: {PROXY_ROUTE: loopback}}}\n' > "$fixture/external/proxy.yml"
    cat > "$deploy/.env" <<'ENV'
SERVER_PORT=18080
XIASS_BUILD_MODE=source
JWT_REFRESH_TOKEN_STORE='postgres'
JWT_SECRET='target-jwt-$#=literal'
TOTP_ENCRYPTION_KEY='target-totp-$#=literal'
DATABASE_PASSWORD='target-pg-$#=literal'
UNOWNED_SECRET='keep-$#=literal'
ENV
    case "$scenario" in
        true) printf "GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS='true'\n" >> "$deploy/.env" ;;
        false) printf 'GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS="false"\n' >> "$deploy/.env" ;;
        plain-false) printf 'GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS=false\n' >> "$deploy/.env" ;;
        invalid-egress) printf 'GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS=yes\n' >> "$deploy/.env" ;;
    esac
    if [ "$scenario" = source-postgres ]; then
        sed 's/JWT_REFRESH_TOKEN_STORE=.*/JWT_REFRESH_TOKEN_STORE=redis/' "$deploy/.env" > "$fixture/env-adjusted"
        mv "$fixture/env-adjusted" "$deploy/.env"
    fi
    compose_labels="docker-compose.local.yml,docker-compose.build.yml,node runtime.yml,$fixture/external/proxy.yml"
    expected_files=("$deploy/docker-compose.local.yml" "$deploy/docker-compose.build.yml" "$deploy/node runtime.yml" "$fixture/external/proxy.yml")
    case "$scenario" in
        missing-base) rm "$deploy/docker-compose.local.yml" ;;
        missing-overlay) rm "$deploy/node runtime.yml" ;;
        fallback|fallback-source)
            compose_labels='<no value>'
            expected_project=""
            expected_files=("$deploy/docker-compose.local.yml")
            if [ "$scenario" = fallback-source ]; then
                expected_files+=("$deploy/docker-compose.build.yml")
            else
                sed 's/XIASS_BUILD_MODE=source/XIASS_BUILD_MODE=image/' "$deploy/.env" > "$fixture/env-adjusted"
                mv "$fixture/env-adjusted" "$deploy/.env"
            fi ;;
    esac
    cp "$deploy/.env" "$fixture/env.before"
    for dir in data postgres_data redis_data; do printf 'original-state\n' > "$deploy/$dir/sentinel"; done
    : > "$fixture/calls"
    expected_args=()
    for file in "${expected_files[@]}"; do expected_args+=(-f "$file"); done
    [ -z "$expected_project" ] || expected_args+=(--project-name "$expected_project")
    expected_args+=(--project-directory "$deploy" up -d --no-deps --no-build --force-recreate xiass-api)
    printf '%s\n' "${expected_args[@]}" > "$fixture/compose.expected"

    RUNTIME_NODE_ID=primary RUNTIME_TUNNEL_TOKEN='runtime-$#=literal'
    RUNTIME_DEFAULT_PROXY_ID=9 RUNTIME_LEGACY_NODE_ID=primary RUNTIME_LEGACY_PROXY_ID=9
    JOIN_SOURCE_URL=https://source.example.invalid JOIN_TARGET_NODE_ID=worker JOIN_TUNNEL_PROOF=fixture-proof
    if [ "$flow" = join ]; then
        script="$JOIN_SCRIPT"
        jq -n '{version:1,target_node_id:"worker",source_node_id:"primary",tunnel_proof:"fixture-proof",target_proxy_id:17,legacy_node_id:"primary",legacy_proxy_id:9,database_user:"source-user",database_pass:"source-pg-$#=literal",database_name:"source-db",database_sslmode:"disable",redis_username:"source-redis",redis_password:"source-redis-$#=literal",redis_db:3,redis_enable_tls:false,jwt_secret:"source-jwt-$#=literal",totp_key:"source-totp-$#=literal"}' > "$fixture/source.json"
        store=""
        case "$scenario" in
            source-redis) store='"redis"' ;;
            source-postgres) store='"postgres"' ;;
            invalid-store-empty) store='""' ;;
            invalid-store-null) store=null ;;
            invalid-store-bool) store=true ;;
            invalid-store-case) store='"POSTGRES"' ;;
            invalid-store-other) store='"disk"' ;;
        esac
        if [ -n "$store" ]; then
            jq --argjson store "$store" '.jwt_refresh_token_store=$store | if $store == "postgres" then .version=2 else . end' "$fixture/source.json" > "$fixture/source-adjusted.json"
            mv "$fixture/source-adjusted.json" "$fixture/source.json"
        fi
        JOIN_BUNDLE_B64=$(base64 < "$fixture/source.json" | tr -d '\n')
    fi
    docker() {
        case "$*" in
            'compose version') return 0 ;;
            'inspect --type container --format '*)
                case "$5" in
                    *project.working_dir*) printf '%s\n' "$deploy" ;;
                    *project.config_files*) printf '%s\n' "$compose_labels" ;;
                    *'"com.docker.compose.project"'*) printf '%s\n' "${expected_project:-<no value>}" ;;
                    *) fail 'unexpected container inspection' ;;
                esac ;;
            compose*)
                shift
                fixture_switches=$((fixture_switches + 1))
                printf '%s\n' "$@" > "$fixture/compose-$fixture_switches"
                cmp -s "$fixture/compose.expected" "$fixture/compose-$fixture_switches" || fail 'Compose lost overlays/project or attempted a non-app operation'
                printf 'app-recreate\n' >> "$fixture/calls"
                cp "$deploy/.env" "$fixture/env-at-switch-$fixture_switches"
                if [ "$scenario" = compose-failure ] && [ "$fixture_switches" = 1 ]; then return 1; fi ;;
            *) fail "unexpected Docker command: $*" ;;
        esac
    }
    curl() {
        case "$*" in
            *'http://127.0.0.1:18080/readyz'*)
                if [ "$scenario" = health-failure ] && [ "$fixture_switches" = 1 ]; then return 22; fi
                printf '{"status":"ok"}\n' ;;
            *'https://source.example.invalid/api/v1/internal/execution-nodes/pairing/finalize'*)
                printf 'finalize\n' >> "$fixture/calls"
                [ "$scenario" != finalize-failure ] ;;
            *) fail 'unexpected network request' ;;
        esac
    }
    sleep() { :; }
    # Redirect only the existing fixed bundle scratch path; execute all real
    # script functions while keeping fixtures and secrets inside the workspace.
    sed "s|/tmp/xiass-cluster-join-bundle.json|\"$fixture/bundle.json\"|g" "$script" > "$fixture/script.sh"
    source "$fixture/script.sh"
}

if [ "${1:-}" = --fixture ]; then
    run_fixture "$2" "$3" "$4"
    exit 0
fi

bash -n "$JOIN_SCRIPT"
bash -n "$RUNTIME_SCRIPT"

# Both host-side flows must preserve the existing deployment and persistent
# state. The assertions intentionally guard against destructive shortcuts.
for script in "$JOIN_SCRIPT" "$RUNTIME_SCRIPT"; do
    grep -Fq 'compose up -d --no-deps --no-build --force-recreate xiass-api' "$script"
    if grep -Fq 'compose down -v' "$script"; then
        echo "cluster script must never remove persistent volumes: $script" >&2
        exit 1
    fi
    grep -Fq 'cp "$ENV_FILE" "$OLD_ENV_FILE"' "$script"
    grep -Fq 'cp "$OLD_ENV_FILE" "$ENV_FILE"' "$script"
done

grep -Fq 'set_env_value GATEWAY_EXECUTION_NODE_ID "$JOIN_TARGET_NODE_ID"' "$JOIN_SCRIPT"
grep -Fq 'set_env_value GATEWAY_EXECUTION_NODE_DEFAULT_PROXY_ID "$target_proxy_id"' "$JOIN_SCRIPT"
grep -Fq 'set_env_value XIASS_CLUSTER_TUNNEL_TOKEN "$JOIN_TUNNEL_PROOF"' "$JOIN_SCRIPT"
grep -Fq 'set_env_value DATABASE_PASSWORD "$database_pass"' "$JOIN_SCRIPT"
grep -Fq 'set_env_value POSTGRES_USER "$database_user"' "$JOIN_SCRIPT"
grep -Fq 'set_env_value POSTGRES_PASSWORD "$database_pass"' "$JOIN_SCRIPT"
grep -Fq 'set_env_value POSTGRES_DB "$database_name"' "$JOIN_SCRIPT"
grep -Fq 'set_env_value GATEWAY_EXECUTION_NODE_ID "$RUNTIME_NODE_ID"' "$RUNTIME_SCRIPT"
grep -Fq 'set_env_value XIASS_CLUSTER_TUNNEL_TOKEN "$RUNTIME_TUNNEL_TOKEN"' "$RUNTIME_SCRIPT"
grep -Fq 'compose up -d --no-deps --no-build --force-recreate xiass-api' "$RUNTIME_SCRIPT"

printf 'Execution-node runtime script contract test passed\n'

mkdir -p "$ROOT/../../artifacts"
TEST_DIR=$(mktemp -d "$ROOT/../../artifacts/cluster-runtime-tests.XXXXXX")
trap 'rm -rf "$TEST_DIR"' EXIT
for flow in runtime join; do
    scenarios=(default true false plain-false invalid-egress missing-base missing-overlay fallback fallback-source health-failure compose-failure)
    if [ "$flow" = join ]; then
        scenarios+=(source-redis source-postgres invalid-store-empty invalid-store-null invalid-store-bool invalid-store-case invalid-store-other finalize-failure)
    fi
    for scenario in "${scenarios[@]}"; do
        fixture="$TEST_DIR/$flow-$scenario"
        mkdir -p "$fixture"
        status=0 expected=0
        case "$scenario" in invalid-*|missing-*|*-failure) expected=1 ;; esac
        bash "$0" --fixture "$flow" "$scenario" "$fixture" > "$fixture/output" 2>&1 || status=$?
        if [ "$status" != "$expected" ]; then
            cat "$fixture/output"
            fail "$flow/$scenario returned $status, expected $expected"
        fi
        env_file="$fixture/install/deploy/.env"
        for dir in data postgres_data redis_data; do
            grep -Fqx original-state "$fixture/install/deploy/$dir/sentinel" || fail 'persistent data changed'
        done
        grep -Fqx "UNOWNED_SECRET='keep-\$#=literal'" "$env_file" || fail 'unowned env secret changed'
        case "$scenario" in
            invalid-*|missing-*)
                [ ! -s "$fixture/calls" ] || fail 'invalid config touched the running application'
                cmp -s "$fixture/env.before" "$env_file" || fail 'preflight failure modified env'
                [ ! -d "$fixture/backups" ] || fail 'invalid config reached env mutation/rollback setup' ;;
            *-failure)
                [ "$(grep -c '^app-recreate$' "$fixture/calls")" = 2 ] || fail 'failure did not recreate only the app for rollback'
                cmp -s "$fixture/env.before" "$env_file" || fail 'rollback did not restore the exact original env'
                cmp -s "$fixture/env.before" "$fixture/env-at-switch-2" || fail 'rollback recreated app before restoring secrets/store'
                ;;
            *)
                [ "$(grep -c '^app-recreate$' "$fixture/calls")" = 1 ] || fail 'success must recreate only the app once'
                case "$scenario" in
                    true|false|plain-false)
                        before=$(grep '^GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS=' "$fixture/env.before")
                        grep -Fqx "$before" "$env_file" || fail 'explicit emergency egress setting was rewritten' ;;
                    *) grep -Fqx "GATEWAY_EXECUTION_NODE_EMERGENCY_LOCAL_EGRESS='false'" "$env_file" || fail 'new runtime silently enabled emergency egress' ;;
                esac
                if [ "$flow" = join ]; then
                    store=redis
                    [ "$scenario" != source-postgres ] || store=postgres
                    grep -Fqx "JWT_REFRESH_TOKEN_STORE='$store'" "$env_file" || fail 'join did not adopt source refresh-token store'
                    grep -Fqx "JWT_SECRET='source-jwt-\$#=literal'" "$env_file" || fail 'join lost source auth secret'
                    grep -Fqx "DATABASE_PASSWORD='source-pg-\$#=literal'" "$env_file" || fail 'join corrupted source database secret'
                    grep -Fqx "DATABASE_HOST='127.0.0.1'" "$env_file" || fail 'join lost loopback connection path'
                    grep -Fqx "DATABASE_PORT='15432'" "$env_file" || fail 'join lost tunnel database port'
                    grep -Fqx finalize "$fixture/calls" || fail 'successful join was not finalized'
                else
                    grep -Fqx "JWT_REFRESH_TOKEN_STORE='postgres'" "$env_file" || fail 'runtime initialization changed token storage policy'
                    grep -Fqx "JWT_SECRET='target-jwt-\$#=literal'" "$env_file" || fail 'runtime initialization changed auth secret'
                fi
                ;;
        esac
        if [ -d "$fixture/backups" ]; then
            snapshot=$(find "$fixture/backups" -name '.env.before-*' -print)
            cmp -s "$fixture/env.before" "$snapshot" || fail 'backup did not preserve original env'
            mode=$(stat -c '%a' "$snapshot" 2>/dev/null || stat -f '%Lp' "$snapshot")
            [ "$mode" = 600 ] || fail 'env backup is not private'
        fi
        [ ! -e "$fixture/bundle.json" ] || fail 'join bundle secret was not cleaned up'
        printf 'PASS: %s/%s\n' "$flow" "$scenario"
    done
done
printf 'Execution-node runtime isolated script tests passed (no live Docker/network operations).\n'
