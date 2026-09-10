#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/deploy/xiass-update.sh"

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
event() { printf '%s\n' "$*" >> "$CALLS"; }

run_fixture() {
    local scenario="$1" fixture="$2"
    local preparation="${scenario#legacy-}" legacy=false
    case "$scenario" in legacy|legacy-*) legacy=true ;; esac
    XIASS_UPDATE_LIB_ONLY=1 source "$SCRIPT"
    INSTALL_DIR="$fixture/install"
    DEPLOY_DIR="$INSTALL_DIR/deploy"
    BACKUP_DIR="$fixture/backups"
    LOCK_DIR="$fixture/maintenance.lock"
    export CALLS="$fixture/calls"
    mkdir -p "$INSTALL_DIR/.git" "$DEPLOY_DIR/data" "$DEPLOY_DIR/postgres_data" "$DEPLOY_DIR/redis_data"
    printf 'GATEWAY_EXECUTION_NODE_ID=node-two\nXIASS_CLUSTER_TUNNEL_TOKEN=fixture-pairing-token\nJWT_SECRET=fixture-jwt\nTOTP_ENCRYPTION_KEY=fixture-totp\nDATABASE_HOST=control-plane\nTEAM_CHILD_AUTOMATION_TOKEN=fixture-team\nTEAM_CHILD_BROWSER_ENABLED=false\n' > "$DEPLOY_DIR/.env"
    if [ "$preparation" = source ] || [ "$preparation" = build-failure ]; then
        printf 'XIASS_BUILD_MODE=source\n' >> "$DEPLOY_DIR/.env"
        printf 'original-build\n' > "$DEPLOY_DIR/docker-compose.build.yml"
    fi
    cp "$DEPLOY_DIR/.env" "$fixture/env.original"
    printf 'original-compose\n' > "$DEPLOY_DIR/docker-compose.local.yml"
    printf 'node-override\n' > "$DEPLOY_DIR/node.yml"
    printf 'proxy-override\n' > "$DEPLOY_DIR/proxy.yml"
    printf 'app-session-state\n' > "$DEPLOY_DIR/data/sentinel"
    printf 'postgres-state\n' > "$DEPLOY_DIR/postgres_data/sentinel"
    printf 'redis-state\n' > "$DEPLOY_DIR/redis_data/sentinel"
    local browser_profile="$fixture/browser-profile"
    local browser_target="$DEPLOY_DIR/team_child_browser_data"
    local browser_container=xiass-api-team-child-browser browser_running=true
    if "$legacy"; then browser_container=nowind-api-team-child-browser; fi
    mkdir -p "$browser_profile"
    printf 'live-login-state\n' > "$browser_profile/.profile-state"
    if [ "$scenario" = legacy-existing-profile ]; then
        mkdir -p "$browser_target"
        printf 'existing-canonical-login-state\n' > "$browser_target/.profile-state"
    fi
    : > "$CALLS"
    local switch_count=0
    local fixture_image_id="sha256:$(printf '%064d' 1)"
    unset XIASS_UPDATE_FULL_BACKUP
    if [ "$scenario" = full-backup ]; then
        XIASS_UPDATE_FULL_BACKUP=true
    fi

    id() { printf '0\n'; }
    sleep() { :; }
    resolve_install_dir() { :; }
    detect_runtime_layout() {
        APP_CONTAINER=xiass-api
        if "$legacy"; then APP_CONTAINER=nowind-api; fi
        PERSISTENCE_MODE=local
        ACTUAL_COMPOSE_FILE="$DEPLOY_DIR/docker-compose.local.yml"
        ACTUAL_COMPOSE_FILES=("$ACTUAL_COMPOSE_FILE" "$DEPLOY_DIR/node.yml" "$DEPLOY_DIR/proxy.yml")
        if [ "$(deployment_build_mode)" = source ]; then
            ACTUAL_COMPOSE_FILES+=("$DEPLOY_DIR/docker-compose.build.yml")
        fi
    }
    git() {
        [ "$1" = -C ] || return 90
        shift 2
        case "$*" in
            'rev-parse HEAD') printf 'previous-ref\n' ;;
            'rev-parse --verify FETCH_HEAD^{commit}') printf 'target-ref\n' ;;
            'show target-ref:backend/cmd/server/VERSION') printf '9.8.7\n' ;;
            'remote get-url xiass-upstream') return 2 ;;
            'remote get-url origin') printf 'https://github.com/xyf0104/xiass-api.git\n' ;;
            'fetch --no-tags origin refs/tags/v9.8.7') event fetch ;;
            'ls-files --error-unmatch -- '*) return 0 ;;
            'diff HEAD --quiet -- '*node.yml) [ "$scenario" = clean-config ] ;;
            'diff --quiet previous-ref target-ref -- backend/migrations')
                [ "$scenario" != migration ] ;;
            diff*) return 0 ;;
            'reset --hard target-ref')
                event sync-target
                printf 'target-compose\n' > "$DEPLOY_DIR/docker-compose.local.yml"
                if [ "$scenario" != clean-config ]; then
                    printf 'target-node-overwrite\n' > "$DEPLOY_DIR/node.yml"
                fi
                ;;
            'reset --hard previous-ref') event restore-git ;;
            *) fail "unexpected git call: $*" ;;
        esac
    }
    docker() {
        case "$*" in
            'compose version') return 0 ;;
            'image tag old-image old-image:latest') event restore-image ;;
            'image inspect --format {{.Id}} fixture/app:latest')
                event inspect-image
                [ "$preparation" != image-inspect-failure ] || return 1
                if [ "$preparation" = image-id-drift-failure ] && [ "$(grep -c '^inspect-image$' "$CALLS")" -gt 1 ]; then
                    printf 'changed-image\n'
                    return 0
                fi
                printf '%s\n' "$fixture_image_id" ;;
            'run -d --name xiass-update-version-'*)
                [ "$*" = "run -d --name $4 --pull never --network none --read-only --cap-drop ALL --security-opt no-new-privileges --entrypoint /app/xiass-api $fixture_image_id --version" ] \
                    || fail 'probe must be offline without business mounts or env'
                event probe-image
                [ "$APP_REPLACEMENT_STARTED" = false ] || fail 'image probe occurred after app replacement'
                [ "$preparation" != image-probe-failure ] || return 1
                ;;
            'rm -f xiass-update-version-'*) event remove-probe ;;
            'logs xiass-update-version-'*)
                if [ "$preparation" = image-version-failure ]; then
                    printf 'XIASS API 9.8.6 (commit: old, built: fixture)\n'
                else
                    printf 'XIASS API v9.8.7 (commit: target, built: fixture)\n'
                fi ;;
            compose*down) event legacy-down ;;
            'inspect --type container --format '*)
                if [ "$5" = '{{.State.Status}} {{.State.ExitCode}}' ]; then
                    case "$preparation" in
                        image-probe-timeout-failure) event probe-poll; printf 'running 0\n' ;;
                        image-probe-exit-failure) printf 'exited 2\n' ;;
                        *) printf 'exited 0\n' ;;
                    esac
                    return 0
                fi
                if [ "$5" = '{{.Image}} {{.State.Running}}' ]; then
                    [ "${!#}" = updated-app ] || fail 'unexpected running container inspection'
                    if [ "$preparation" = runtime-image-failure ]; then
                        printf 'wrong-image true\n'
                    else
                        printf '%s true\n' "$fixture_image_id"
                    fi
                    return 0
                fi
                [ "${!#}" = "$browser_container" ] || fail 'unexpected browser inspection'
                event capture-profile
                printf 'bind|%s\n' "$browser_profile"
                ;;
            *) fail "unexpected Docker call: $*" ;;
        esac
    }
    curl() {
        case "${!#}" in
            "$RELEASE_API_URL")
                printf '{"tag_name":"v9.8.7","draft":false,"prerelease":false}\n'
                return 0 ;;
            http://127.0.0.1:8080/api/v1/settings/public)
                event runtime-version
                case "$preparation" in
                    runtime-http-failure) return 22 ;;
                    runtime-json-failure) printf '{"code":0,"data":{}}\n' ;;
                    runtime-version-failure) printf '{"code":0,"data":{"version":"9.8.6"}}\n' ;;
                    *) printf '{"code":0,"data":{"version":"9.8.7"}}\n' ;;
                esac
                return 0 ;;
            http://127.0.0.1:8080/readyz)
                event runtime-ready
                case "$preparation" in
                    runtime-readiness-failure) return 22 ;;
                    runtime-readiness-json-failure) printf '{"status":"ok","checks":{"redis":"unavailable"}}\n' ;;
                    *) printf '{"status":"ok","checks":{"postgres":"ok","redis":"ok","execution_node":"ok"}}\n' ;;
                esac
                return 0 ;;
        esac
        [ "$scenario" = full-backup ] || fail 'ordinary update attempted a full backup or network request'
        event full-backup
        printf ':\n'
    }
    capture_previous_image() {
        event capture-image
        PREVIOUS_IMAGE_ID=old-image
        PREVIOUS_IMAGE_REF=old-image:latest
    }
    container_exists() { [ "$1" = "$browser_container" ]; }
    cp() {
        if [ "${1:-}" = -a ] && [ "${2:-}" = "$browser_profile/." ]; then
            event profile-copy
            if "$browser_running"; then event profile-copy-live; fi
        fi
        command cp "$@"
    }
    stop_known_runtime_containers() {
        event legacy-stop
        # Model the browser's final write before it is safe to copy its profile.
        printf 'stopped-login-state\n' > "$browser_profile/.profile-state"
        browser_running=false
    }
    compose() {
        event "compose $*"
        case "$*" in
            'config --quiet') [ "$preparation" != config-failure ] ;;
            'config --format json')
                [ "$preparation" != image-json-failure ] || return 1
                printf '{"services":{"xiass-api":{"image":"fixture/app:latest"}}}\n' ;;
            'up --help')
                if [ "$preparation" = compose-unsupported ]; then
                    printf 'up options: --no-build\n'
                else
                    printf 'up options: --pull policy\n'
                fi
                ;;
            'config --services') printf 'xiass-api\npostgres\nredis\nwatchtower\n' ;;
            'pull xiass-api')
                [ "$APP_REPLACEMENT_STARTED" = false ] || fail 'pull occurred during application replacement'
                [ "$preparation" != pull-failure ] ;;
            'build xiass-api')
                [ "$APP_REPLACEMENT_STARTED" = false ] || fail 'build occurred during application replacement'
                [ "$preparation" != build-failure ] ;;
            'pull postgres'|'pull redis'|'pull watchtower')
                [ "$APP_REPLACEMENT_STARTED" = false ] || fail 'dependency pull occurred after legacy shutdown'
                [ "$preparation" != dependency-pull-failure ] ;;
            'up -d --no-deps --no-build --pull never --force-recreate xiass-api')
                switch_count=$((switch_count + 1))
                if [ "$switch_count" = 1 ]; then
                    [ "$(cat "$DEPLOY_DIR/node.yml")" = node-override ] || fail 'node-specific Compose modification was lost'
                    [ "${#COMPOSE_FILES[@]}" -ge 3 ] || fail 'Compose overlays were discarded'
                fi
                ;;
            'ps -q xiass-api') printf 'updated-app\n' ;;
            down) event legacy-down ;;
            ps|'logs --tail 160 xiass-api') return 0 ;;
            *) fail "unexpected Compose call: $*" ;;
        esac
    }
    wait_for_health() {
        event health
        if [ "$scenario" = health-failure ] && [ "$switch_count" = 1 ]; then return 1; fi
        return 0
    }
    start_runtime_stack() {
        event "runtime $*"
        if "$legacy"; then
            [ "$1" = true ] || fail 'legacy startup would pull or build while offline'
            if [ "$scenario" != legacy-existing-profile ]; then
                grep -Fqx 'stopped-login-state' "$browser_target/.profile-state" \
                    || fail 'legacy startup must receive the browser profile after its final write'
            fi
        else
            [ "$*" = 'true true' ] || fail 'canonical update attempted to restart core services'
            [ "${#COMPOSE_FILES[@]}" -ge 3 ] || fail 'runtime overlays were discarded'
        fi
    }
    main
}

if [ "${1:-}" = --fixture ]; then
    run_fixture "$2" "$3"
    exit 0
fi

if [ "${1:-}" = --release-fixture ]; then
    scenario="$2"
    XIASS_UPDATE_LIB_ONLY=1 source "$SCRIPT"
    INSTALL_DIR=/fixture
    UPDATE_REMOTE=origin
    curl() {
        case "$scenario" in
            api-failure) return 22 ;;
            json-failure) printf 'not-json\n' ;;
            draft-failure) printf '{"tag_name":"v9.8.7","draft":true,"prerelease":false}\n' ;;
            prerelease-failure) printf '{"tag_name":"v9.8.7","draft":false,"prerelease":true}\n' ;;
            tag-failure) printf '{"tag_name":"v9.8.7-rc1","draft":false,"prerelease":false}\n' ;;
            missing-failure) printf '{"draft":false,"prerelease":false}\n' ;;
            *) printf '{"tag_name":"v9.8.7","draft":false,"prerelease":false}\n' ;;
        esac
    }
    git() {
        case "$*" in
            '-C /fixture fetch --no-tags origin refs/tags/v9.8.7') [ "$scenario" != fetch-failure ] ;;
            '-C /fixture rev-parse --verify FETCH_HEAD^{commit}') printf 'stable-ref\n' ;;
            '-C /fixture show stable-ref:backend/cmd/server/VERSION')
                if [ "$scenario" = source-version-failure ]; then printf '9.8.6\n'; else printf '9.8.7\n'; fi ;;
            *) fail 'stable release resolution tried a branch or fallback source' ;;
        esac
    }
    resolve_stable_release
    [ "$UPDATE_REF" = stable-ref ] && [ "$TARGET_VERSION" = 9.8.7 ] || fail 'incorrect stable target'
    exit 0
fi

mkdir -p "$ROOT_DIR/../artifacts"
TEST_DIR=$(mktemp -d "$ROOT_DIR/../artifacts/xiass-update-tests.XXXXXX")
trap 'rm -rf "$TEST_DIR"' EXIT

for scenario in valid api-failure json-failure draft-failure prerelease-failure tag-failure missing-failure fetch-failure source-version-failure; do
    status=0
    bash "$0" --release-fixture "$scenario" > "$TEST_DIR/release-output" 2>&1 || status=$?
    expected=1
    [ "$scenario" != valid ] || expected=0
    if [ "$status" != "$expected" ]; then
        cat "$TEST_DIR/release-output"
        fail "release $scenario returned $status, expected $expected"
    fi
    printf 'PASS: release %s\n' "$scenario"
done

assert_has() { grep -Fqx -- "$2" "$1" || fail "missing event: $2"; }
assert_absent() { if grep -Eq -- "$2" "$1"; then fail "unexpected event: $2"; fi; }
assert_before() {
    local first second
    first=$(grep -nFx -- "$2" "$1" | head -n 1 | cut -d: -f1)
    second=$(grep -nFx -- "$3" "$1" | head -n 1 | cut -d: -f1)
    [ "$first" -lt "$second" ] || fail "$2 must precede $3"
}

for scenario in image clean-config source pull-failure build-failure config-failure compose-unsupported health-failure legacy full-backup migration \
    legacy-source legacy-pull-failure legacy-build-failure legacy-config-failure legacy-compose-unsupported legacy-dependency-pull-failure legacy-existing-profile \
    image-json-failure image-inspect-failure image-probe-failure image-version-failure image-probe-timeout-failure image-probe-exit-failure image-id-drift-failure \
    runtime-version-failure runtime-image-failure runtime-json-failure runtime-http-failure runtime-readiness-failure runtime-readiness-json-failure \
    legacy-image-version-failure legacy-runtime-version-failure; do
    fixture="$TEST_DIR/$scenario"
    mkdir -p "$fixture"
    status=0
    bash "$0" --fixture "$scenario" "$fixture" > "$fixture/output" 2>&1 || status=$?
    expected=0
    case "$scenario" in *failure|*compose-unsupported) expected=1 ;; esac
    if [ "$status" -ne "$expected" ]; then
        sed -n '1,160p' "$fixture/output"
        fail "$scenario returned $status, expected $expected"
    fi
    calls="$fixture/calls"
    deploy="$fixture/install/deploy"
    assert_has "$calls" fetch
    assert_has "$calls" sync-target
    assert_has "$calls" capture-profile
    assert_absent "$calls" 'pg_dump|down -v|volume rm'
    assert_absent "$calls" '^profile-copy-live$'
    if grep -Fqx probe-image "$calls"; then assert_has "$calls" remove-probe; fi
    if [ "$scenario" = image-probe-timeout-failure ]; then
        [ "$(grep -c '^probe-poll$' "$calls")" = 15 ] || fail 'image probe wait is not bounded'
    fi
    [ "$(cat "$deploy/data/sentinel")" = app-session-state ] || fail 'application state changed'
    [ "$(cat "$deploy/postgres_data/sentinel")" = postgres-state ] || fail 'PostgreSQL data changed'
    [ "$(cat "$deploy/redis_data/sentinel")" = redis-state ] || fail 'Redis data changed'
    while IFS= read -r value; do
        grep -Fqx -- "$value" "$deploy/.env" || fail 'existing env or node identity changed'
    done < "$fixture/env.original"
    snapshot=$(find "$fixture/backups/update-config" -name env.before-update -print)
    [ -f "$snapshot" ] || fail 'configuration snapshot was not retained'
    cmp -s "$fixture/env.original" "$snapshot" || fail 'environment snapshot is not exact'
    mode=$(stat -c '%a' "$snapshot" 2>/dev/null || stat -f '%Lp' "$snapshot")
    [ "$mode" = 600 ] || fail 'environment snapshot must be private'
    if [ "$scenario" != full-backup ]; then assert_absent "$calls" '^full-backup$'; fi
    case "$scenario" in
        legacy|legacy-*) ;;
        *) assert_absent "$calls" 'down|legacy-stop|pull (postgres|redis|watchtower)' ;;
    esac
    case "$scenario" in
        legacy|legacy-source|legacy-runtime-version-failure)
            assert_before "$calls" legacy-down legacy-stop
            assert_before "$calls" legacy-stop profile-copy
            assert_before "$calls" profile-copy 'runtime true'
            cmp -s "$fixture/browser-profile/.profile-state" "$deploy/team_child_browser_data/.profile-state" \
                || fail 'migration lost the final stopped browser state'
            ;;
        legacy-existing-profile)
            assert_absent "$calls" '^profile-copy$'
            grep -Fqx 'existing-canonical-login-state' "$deploy/team_child_browser_data/.profile-state" \
                || fail 'existing browser profile was overwritten'
            ;;
        *)
            assert_absent "$calls" '^profile-copy$'
            [ ! -e "$deploy/team_child_browser_data" ] \
                || fail 'preparation failure or canonical hot update left a stale browser profile destination'
            grep -Fqx 'live-login-state' "$fixture/browser-profile/.profile-state" \
                || fail 'running browser profile was changed'
            ;;
    esac
    case "$scenario" in
        pull-failure|build-failure|config-failure|compose-unsupported|legacy-pull-failure|legacy-build-failure|legacy-config-failure|legacy-compose-unsupported|legacy-dependency-pull-failure|image-*-failure|legacy-image-version-failure)
            assert_absent "$calls" 'compose up -d|^health$|^runtime |legacy-down|legacy-stop'
            assert_has "$calls" restore-git
            cmp -s "$fixture/env.original" "$deploy/.env" || fail 'preparation failure did not restore env'
            [ "$(cat "$deploy/docker-compose.local.yml")" = original-compose ] || fail 'preparation failure did not restore Compose'
            ;;
        health-failure|runtime-*-failure)
            [ "$(grep -Fc 'compose up -d --no-deps --no-build --pull never --force-recreate xiass-api' "$calls")" = 2 ] || fail 'rollback must replace only the app'
            assert_before "$calls" restore-git restore-image
            cmp -s "$fixture/env.original" "$deploy/.env" || fail 'rollback changed original env'
            grep -Fq '不会撤销已执行的数据库迁移' "$fixture/output" || fail 'rollback must disclose schema limitation'
            ;;
        legacy-runtime-version-failure)
            assert_before "$calls" probe-image legacy-down
            assert_has "$calls" restore-git
            assert_has "$calls" restore-image
            cmp -s "$fixture/env.original" "$deploy/.env" || fail 'legacy version failure changed env'
            ;;
        legacy|legacy-source|legacy-existing-profile)
            prepare='compose pull xiass-api'
            [ "$scenario" != legacy-source ] || prepare='compose build xiass-api'
            assert_before "$calls" "$prepare" legacy-down
            assert_before "$calls" 'compose pull watchtower' legacy-down
            assert_has "$calls" 'runtime true'
            ;;
        *)
            prepare='compose pull xiass-api'
            [ "$scenario" != source ] || prepare='compose build xiass-api'
            assert_before "$calls" "$prepare" 'compose up -d --no-deps --no-build --pull never --force-recreate xiass-api'
            assert_before "$calls" health 'runtime true true'
            grep -Fq '应用替换和健康检查耗时' "$fixture/output" || fail 'missing switch duration'
            ;;
    esac
    if [ "$expected" = 0 ]; then
        assert_has "$calls" probe-image
        assert_has "$calls" runtime-version
        assert_has "$calls" runtime-ready
        grep -Fq '实际运行版本 9.8.7' "$fixture/output" || fail 'success must report the verified runtime version'
    else
        if grep -Fq 'XIASS 更新完成' "$fixture/output"; then fail 'failed verification reported update success'; fi
    fi
    if [ "$scenario" = migration ]; then
        grep -Fq '此版本包含数据库迁移变更' "$fixture/output" || fail 'migration change must be disclosed'
    fi
    printf 'PASS: %s\n' "$scenario"
done

(
    XIASS_UPDATE_LIB_ONLY=1 source "$SCRIPT"
    DEPLOY_DIR="$TEST_DIR/runtime-helper"
    mkdir -p "$DEPLOY_DIR"
    printf 'TEAM_CHILD_AUTOMATION_TOKEN=fixture-team\n' > "$DEPLOY_DIR/.env"
    printf '%s\n' '#!/usr/bin/env bash' 'printf "%s\n" "$XIASS_RUNTIME_COMPOSE_FILES" > "$DEPLOY_DIR/received"' 'printf "%s\n" "$XIASS_RUNTIME_PROJECT_NAME" > "$DEPLOY_DIR/project"' 'exit 7' > "$DEPLOY_DIR/xiass-runtime-start.sh"
    chmod +x "$DEPLOY_DIR/xiass-runtime-start.sh"
    COMPOSE_FILES=("$DEPLOY_DIR/base.yml" "$DEPLOY_DIR/node.yml" "$DEPLOY_DIR/proxy.yml")
    COMPOSE_FILE="${COMPOSE_FILES[0]}"
    ACTUAL_COMPOSE_PROJECT_NAME=paired-primary
    status=0
    start_runtime_stack true true || status=$?
    [ "$status" = 7 ] || fail 'runtime helper failure was swallowed'
    [ "$(cat "$DEPLOY_DIR/received")" = "$(printf '%s\n' "${COMPOSE_FILES[@]}")" ] || fail 'runtime helper did not receive every overlay'
    [ "$(cat "$DEPLOY_DIR/project")" = paired-primary ] || fail 'runtime helper did not receive running project identity'
    rm "$DEPLOY_DIR/xiass-runtime-start.sh"
    compose() { fail 'missing helper must not restart any core service after a hot update'; }
    wait_for_health() { return 0; }
    start_runtime_stack true true || fail 'healthy app must stay online without a runtime helper'
    wait_for_health() { return 7; }
    status=0
    start_runtime_stack true true || status=$?
    [ "$status" = 7 ] || fail 'missing-helper fallback swallowed a health failure'
    printf 'XIASS_UPDATE_FULL_BACKUP=true\n' >> "$DEPLOY_DIR/.env"
    unset XIASS_UPDATE_FULL_BACKUP
    update_full_backup_enabled || fail 'explicit env-file full backup opt-in was ignored'
    XIASS_UPDATE_FULL_BACKUP=false
    if update_full_backup_enabled; then fail 'process-level full backup opt-out was ignored'; fi
)
printf 'PASS: runtime helper propagation and backup policy\n'

(
    XIASS_UPDATE_LIB_ONLY=1 source "$SCRIPT"
    DEPLOY_DIR="$TEST_DIR/legacy-project"
    PREVIOUS_COMPOSE_FILES=("/fixture/base.yml" "/fixture/node.yml")
    ACTUAL_COMPOSE_PROJECT_NAME=existing-legacy-project
    record_compose() { printf '%s\n' "$@"; }
    COMPOSE=(record_compose)
    expected=$(printf '%s\n' -f /fixture/base.yml -f /fixture/node.yml --project-directory "$DEPLOY_DIR" --project-name existing-legacy-project down)
    [ "$(stop_previous_runtime)" = "$expected" ] || fail 'legacy stop lost original Compose project identity'
)
printf 'PASS: legacy stop preserves project identity\n'

bash "$ROOT_DIR/tools/test_xiass_update_paired_nodes.sh"

printf 'xiass update pipeline tests passed.\n'
