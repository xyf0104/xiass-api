#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/deploy/xiass-runtime-start.sh"
CALLS="$(mktemp)"

cleanup() {
    rm -f "$CALLS"
}
trap cleanup EXIT

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

XIASS_RUNTIME_START_LIB_ONLY=1 source "$SCRIPT"

TEAM_CHILD_BROWSER_ENABLED=true
BUILD_MODE=image
SKIP_CORE_START=true
CORE_READY_DELAY_SECONDS=5
HASH=$(printf '%064d' 1)
IMAGE="sha256:$(printf '%064d' 2)"
HEALTH=healthy
STATE=running
PROTOCOL=4
CONFIG_HASH="$HASH"
LOCAL_IMAGE="$IMAGE"
PULL_FAIL=false
PROBE_FAIL=false
XIASS_RUNTIME_TEAM_SOURCE_UNCHANGED=false

service_exists() {
    case "$1" in
        team-child-browser|team-child-automation) return 0 ;;
        *) return 1 ;;
    esac
}

profile_compose() {
    printf '%s\n' "$*" >> "$CALLS"
    if [ "$1" = "ps" ]; then
        printf 'automation-container\n'
    elif [ "$1" = config ]; then
        printf 'team-child-automation %s\n' "$CONFIG_HASH"
    elif [ "$1" = pull ] && [ "$PULL_FAIL" = true ]; then
        return 1
    fi
    return 0
}

team_compose_probe() {
    [ "$PROBE_FAIL" = false ] || return 1
    profile_compose "$@"
}

team_probe() {
    [ "$PROBE_FAIL" = false ] || return 1
    "$@"
}

docker() {
    printf 'docker %s\n' "$*" >> "$CALLS"
    case "$*" in
        *com.docker.compose.config-hash*) printf '%s\n' "$HASH" ;;
        *'{{.Config.Image}}'*) printf 'fixture/team:latest\n' ;;
        *'{{.Image}}'*) printf '%s\n' "$IMAGE" ;;
        'image inspect '*) printf '%s\n' "$LOCAL_IMAGE" ;;
        *'.State.Health'*) printf '%s\n' "$HEALTH" ;;
        *'{{.State.Status}}'*) printf '%s\n' "$STATE" ;;
        'exec '*) printf '%s' "$PROTOCOL" ;;
        *) fail "unexpected Docker call: $*" ;;
    esac
}

sleep() {
    printf 'sleep %s\n' "$1" >> "$CALLS"
}

wait_for_automation_health() {
    [ "$1" = "automation-container" ] || fail 'unexpected Team automation container id'
}

start_browser_stack

grep -Fqx 'pull team-child-automation' "$CALLS" || fail 'fast update must pull the Team automation image'
grep -Fqx 'sleep 0' "$CALLS" || fail 'fast update must not delay an already healthy browser stack'
grep -Fqx 'up -d --no-deps --no-build --pull never --no-recreate team-child-browser' "$CALLS" || fail 'fast update must preserve the browser and never start its dependencies'
grep -Fqx 'up -d --no-deps --no-build --pull never team-child-automation' "$CALLS" || fail 'healthy sidecar must use Compose change detection after pull'
if grep -Fqx 'pull team-child-browser team-child-automation' "$CALLS"; then
    fail 'fast update must not pull or restart Chromium'
fi

printf 'xiass runtime Team sidecar update test passed.\n'

run_case() {
    : > "$CALLS"
    start_browser_stack
    grep -Fqx 'up -d --no-deps --no-build --pull never --no-recreate team-child-browser' "$CALLS" || fail 'Chromium must be preserved'
    if grep -E '^(pull|build|up).* (xiass-api|postgres|redis|watchtower)( |$)' "$CALLS"; then
        fail 'Team update must not change the application or database'
    fi
}

expect_pull() {
    grep -Fqx 'pull team-child-automation' "$CALLS" || fail 'image updates must always check the registry'
}

XIASS_RUNTIME_TEAM_SOURCE_UNCHANGED=true
run_case
expect_pull
if grep -q -- '--force-recreate' "$CALLS"; then fail 'verified healthy sidecar must not force recreation'; fi

CONFIG_HASH=$(printf '%064d' 3)
run_case
expect_pull
CONFIG_HASH=unknown
run_case
expect_pull
CONFIG_HASH="$HASH"
LOCAL_IMAGE="sha256:$(printf '%064d' 4)"
run_case
expect_pull
LOCAL_IMAGE="$IMAGE"
PROBE_FAIL=true
run_case
expect_pull
PROBE_FAIL=false
STATE=exited
run_case
expect_pull
STATE=running

for HEALTH in unhealthy starting exited; do
    run_case
    expect_pull
    grep -Fqx 'up -d --no-deps --no-build --pull never --force-recreate team-child-automation' "$CALLS" || fail 'unready sidecar must be recreated'
done
HEALTH=healthy
PROTOCOL=3
run_case
expect_pull
grep -Fqx 'up -d --no-deps --no-build --pull never --force-recreate team-child-automation' "$CALLS" || fail 'old protocol must be replaced'
PROTOCOL=4

XIASS_RUNTIME_TEAM_SOURCE_UNCHANGED=false
PULL_FAIL=true
run_case
expect_pull
if grep -E '^up .* team-child-automation$' "$CALLS"; then fail 'failed pull must preserve existing sidecar'; fi
PULL_FAIL=false
# A previous app update advanced the repository but failed to pull Team. The
# next release has identical Team source, config, and local/running image IDs.
# It must retry the registry, not mistake the healthy old image for freshness.
XIASS_RUNTIME_TEAM_SOURCE_UNCHANGED=true
CONFIG_HASH="$HASH"
LOCAL_IMAGE="$IMAGE"
PULL_FAIL=true
run_case
expect_pull
if grep -E '^up .* team-child-automation$' "$CALLS"; then fail 'repeated pull failure must preserve the stale but running sidecar'; fi
PULL_FAIL=false
run_case
expect_pull
grep -Fqx 'up -d --no-deps --no-build --pull never team-child-automation' "$CALLS" || fail 'retry must let Compose reconcile the freshly pulled image'
if grep -q -- '--force-recreate' "$CALLS"; then fail 'registry retry must not force-recreate a healthy sidecar'; fi
awk '/^pull team-child-automation$/ { pulled = 1 } /^up .* team-child-automation$/ { if (!pulled) exit 1; reconciled = 1 } END { if (!reconciled) exit 1 }' "$CALLS" \
    || fail 'registry pull must precede sidecar reconciliation'

BUILD_MODE=source
run_case
grep -Fqx 'build team-child-automation' "$CALLS" || fail 'source mode must still build'
grep -Fqx 'up -d --no-deps --no-build --pull never team-child-automation' "$CALLS" || fail 'source mode must use normal change detection'
BUILD_MODE=image

# Verify the actual readiness check, not the update test stub.
(
    XIASS_RUNTIME_START_LIB_ONLY=1 source "$SCRIPT"
    container_state() { printf '%s\n' "$STATE"; }
    container_health() { printf 'healthy\n'; }
    PROTOCOL=3
    if wait_for_automation_health automation-container; then fail 'readiness must reject protocol 3'; fi
    PROTOCOL=4
    wait_for_automation_health automation-container || fail 'readiness must accept protocol 4'
    STATE=exited
    if wait_for_automation_health automation-container; then fail 'stopped container with retained healthy status must not be ready'; fi
    grep -Fq 'workflow_schema_version === 4' "$CALLS" || fail 'protocol must validate workflow schema'
    grep -Fq "x-xiass-team-child-protocol') === '4'" "$CALLS" || fail 'protocol must validate response header'
    grep -Fq 'AbortSignal.timeout(5000)' "$CALLS" || fail 'protocol HTTP request must be bounded'
)
printf 'xiass runtime unchanged-image and failure-path tests passed.\n'

(
    RUNTIME_COMPOSE_FILES=("/fixture/base.yml" "/fixture/node.yml" "/fixture/proxy.yml")
    COMPOSE_FILE="${RUNTIME_COMPOSE_FILES[0]}"
    DEPLOY_DIR=/fixture
    COMPOSE=(capture_compose)
    capture_compose() { printf '%s\n' "$*" >> "$CALLS"; }
    compose config --quiet
    grep -Fqx -- '-f /fixture/base.yml -f /fixture/node.yml -f /fixture/proxy.yml --project-directory /fixture config --quiet' "$CALLS" \
        || fail 'runtime must retain all node and proxy Compose overlays'
    RUNTIME_PROJECT_NAME=paired-primary
    compose config --quiet
    grep -Fqx -- '-f /fixture/base.yml -f /fixture/node.yml -f /fixture/proxy.yml --project-directory /fixture --project-name paired-primary config --quiet' "$CALLS" \
        || fail 'runtime must retain the running Compose project and named-volume identity'
)

(
    service_exists() { return 0; }
    compose() { printf '%s\n' "$*" >> "$CALLS"; }
    wait_for_core_health() { return 0; }
    XIASS_RUNTIME_CORE_READY=true
    start_core
    grep -Fqx 'up -d --no-build --pull never postgres redis watchtower xiass-api' "$CALLS" \
        || fail 'prepared update must not pull while the old stack is stopped'
    XIASS_RUNTIME_CORE_READY=false
    start_core
    grep -Fqx 'up -d --no-build postgres redis watchtower xiass-api' "$CALLS" \
        || fail 'fresh installation must still allow missing dependency images'
)

printf 'xiass runtime overlay and prepared-image tests passed.\n'
