#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)

test_dependency_bootstrap() (
    local test_dir manager scenario name case_dir status expected_calls calls count=0
    mkdir -p "$repo_root/../artifacts"
    test_dir=$(mktemp -d "$repo_root/../artifacts/xiass-update-dependencies.XXXXXX")
    trap 'rm -rf "$test_dir"' EXIT

    for name in \
        apt-get:success dnf:success yum:success apk:success \
        apt-get:install-failure dnf:install-failure yum:install-failure apk:install-failure \
        apt-get:missing-after-install dnf:missing-after-install yum:missing-after-install apk:missing-after-install \
        apt-get:update-failure apt-get:existing none:unsupported \
        apt-get:missing-git apt-get:missing-env; do
        manager=${name%%:*}
        scenario=${name#*:}
        case_dir="$test_dir/$name"
        mkdir -p "$case_dir/install/deploy" "$case_dir/empty-path"
        if [ "$scenario" != missing-git ]; then mkdir -p "$case_dir/install/.git"; fi
        printf 'JWT_SECRET=fixture-unchanged\n' > "$case_dir/env.before"
        if [ "$scenario" != missing-env ]; then cp "$case_dir/env.before" "$case_dir/install/deploy/.env"; fi
        status=0
        # A separate Bash preserves errexit even though the parent captures failure.
        # The empty PATH and mocks prevent real package, network or Docker calls.
        PATH="$case_dir/empty-path" INSTALL_DIR="$case_dir/install" XIASS_UPDATE_LIB_ONLY=1 \
            "$BASH" -s -- "$repo_root/deploy/xiass-update.sh" "$case_dir" "$manager" "$scenario" \
            > "$case_dir/output" 2>&1 <<'BASH' || status=$?
set -Eeuo pipefail
source "$1"
fixture=$2 manager=$3 scenario=$4
jq_ready=false
[ "$scenario" != existing ] || jq_ready=true
command() {
    [ "$#" -eq 2 ] && [ "$1" = -v ] || { printf 'unexpected command\n' >> "$fixture/calls"; exit 97; }
    case "$2" in
        jq) [ "$jq_ready" = true ] ;;
        apt-get|dnf|yum|apk) [ "$2" = "$manager" ] ;;
        curl|git|docker|mktemp) return 0 ;;
        *) printf 'unexpected lookup %s\n' "$2" >> "$fixture/calls"; exit 97 ;;
    esac
}
package_manager() {
    printf '%s\n' "$*" >> "$fixture/calls"
    [ "$1" = "$manager" ] || exit 97
    case "$*" in
        'apt-get update')
            [ "$scenario" != update-failure ] || return 23
            return 0 ;;
        'apt-get install -y jq') [ "${DEBIAN_FRONTEND:-}" = noninteractive ] || exit 97 ;;
        'dnf install -y jq'|'yum install -y jq'|'apk add --no-cache jq') ;;
        *) exit 97 ;;
    esac
    [ "$scenario" != install-failure ] || return 23
    [ "$scenario" = missing-after-install ] || jq_ready=true
}
apt-get() { package_manager apt-get "$@"; }
dnf() { package_manager dnf "$@"; }
yum() { package_manager yum "$@"; }
apk() { package_manager apk "$@"; }
id() { [ "$*" = -u ] || exit 97; printf '0\n'; }
resolve_install_dir() { DEPLOY_DIR="$INSTALL_DIR/deploy"; }
git() {
    [ "$*" = "-C $INSTALL_DIR rev-parse HEAD" ] && [ "$jq_ready" = true ] \
        || { printf 'unexpected git\n' >> "$fixture/calls"; exit 97; }
    printf 'bootstrap-complete\n' >> "$fixture/calls"
    exit 73
}
docker() { printf 'unexpected docker\n' >> "$fixture/calls"; exit 97; }
curl() { printf 'unexpected curl\n' >> "$fixture/calls"; exit 97; }
mkdir() { printf 'unexpected mutation\n' >> "$fixture/calls"; exit 97; }
mktemp() { printf 'unexpected mutation\n' >> "$fixture/calls"; exit 97; }
main
BASH
        expected_calls=""
        case "$scenario" in
            existing|unsupported|missing-git|missing-env) ;;
            update-failure) expected_calls='apt-get update' ;;
            *)
                case "$manager" in
                    apt-get) expected_calls=$'apt-get update\napt-get install -y jq' ;;
                    dnf|yum) expected_calls="$manager install -y jq" ;;
                    apk) expected_calls='apk add --no-cache jq' ;;
                esac ;;
        esac
        case "$scenario" in
            success|existing)
                [ -z "$expected_calls" ] || expected_calls+=$'\n'
                expected_calls+='bootstrap-complete'
                [ "$status" -eq 73 ] || { printf 'FAIL: %s did not complete bootstrap (status %s)\n' "$name" "$status" >&2; exit 1; } ;;
            install-failure|update-failure)
                [ "$status" -eq 23 ] || { printf 'FAIL: %s did not stop on package failure (status %s)\n' "$name" "$status" >&2; exit 1; } ;;
            *)
                [ "$status" -eq 1 ] || { printf 'FAIL: %s did not fail closed (status %s)\n' "$name" "$status" >&2; exit 1; } ;;
        esac
        calls=""
        [ ! -f "$case_dir/calls" ] || calls=$(< "$case_dir/calls")
        [ "$calls" = "$expected_calls" ] || {
            printf 'FAIL: %s unexpected bootstrap or mutation calls:\n%s\n' "$name" "$calls" >&2
            exit 1
        }
        if [ "$scenario" = missing-env ]; then
            [ ! -e "$case_dir/install/deploy/.env" ] || { echo 'FAIL: missing .env was created' >&2; exit 1; }
        else
            cmp -s "$case_dir/env.before" "$case_dir/install/deploy/.env" \
                || { echo 'FAIL: bootstrap changed .env' >&2; exit 1; }
        fi
        [ ! -e "$case_dir/install/deploy/data" ] || { echo 'FAIL: bootstrap created app data' >&2; exit 1; }
        printf 'PASS: dependency bootstrap %s\n' "$name"
        count=$((count + 1))
    done
    printf 'Updater mocked dependency bootstrap tests passed (%s cases; no Docker or package installation).\n' "$count"
)

test_dependency_bootstrap
if [ "${1:-}" = --dependency-bootstrap-only ]; then exit 0; fi

docker run --rm \
    -v "$repo_root:/repo:ro" \
    alpine:3.20 \
    sh -eu -c '
        apk add --no-cache bash >/dev/null
        mkdir -p /tmp/backups
        touch /tmp/backups/xiass-runtime-1.tar.gz /tmp/backups/xiass-runtime-1.tar.gz.sha256
        sleep 1
        touch /tmp/backups/xiass-runtime-2.tar.gz /tmp/backups/xiass-runtime-2.tar.gz.sha256
        sleep 1
        touch /tmp/backups/xiass-runtime-3.tar.gz /tmp/backups/xiass-runtime-3.tar.gz.sha256
        BACKUP_DIR=/tmp/backups KEEP_BACKUPS=2 XIASS_BACKUP_LIB_ONLY=1 \
            bash -c '\''source /repo/deploy/xiass-backup.sh; prune_old_backups'\''
        test ! -e /tmp/backups/xiass-runtime-1.tar.gz
        test ! -e /tmp/backups/xiass-runtime-1.tar.gz.sha256
        test -e /tmp/backups/xiass-runtime-2.tar.gz
        test -e /tmp/backups/xiass-runtime-3.tar.gz
        test "$(find /tmp/backups -name "xiass-runtime-*.tar.gz" | wc -l)" -eq 2
    '

if grep -Fq '[ -n "$patch_file" ] && printf' "$repo_root/deploy/xiass-update.sh"; then
    echo "update success path can still return a false conditional status" >&2
    exit 1
fi

prefetch_line=$(grep -n '^[[:space:]]*prefetch_target_app_image$' "$repo_root/deploy/xiass-update.sh" | tail -n 1 | cut -d: -f1 || true)
legacy_stop_line=$(grep -n '^[[:space:]]*if ! stop_previous_runtime; then$' "$repo_root/deploy/xiass-update.sh" | tail -n 1 | cut -d: -f1 || true)
if [ -z "$prefetch_line" ] || [ -z "$legacy_stop_line" ] || [ "$prefetch_line" -ge "$legacy_stop_line" ]; then
    echo "main image must be prefetched before the legacy stack is stopped" >&2
    exit 1
fi

grep -Fq 'compose up -d --no-deps --no-build --pull never --force-recreate xiass-api' "$repo_root/deploy/xiass-update.sh" \
    || { echo "canonical app hot-swap path is missing" >&2; exit 1; }
grep -Fq 'if update_full_backup_enabled; then' "$repo_root/deploy/xiass-update.sh" \
    || { echo "offline full backup must be opt-in during an update" >&2; exit 1; }
grep -Fq 'XIASS_UPDATE_FULL_BACKUP=false' "$repo_root/deploy/.env.example" \
    || { echo "low-downtime update mode must be the documented default" >&2; exit 1; }
grep -Fq 'XIASS_RUNTIME_SKIP_CORE_START="$skip_core_start"' "$repo_root/deploy/xiass-update.sh" \
    || { echo "hot-swap must preserve the running database and cache" >&2; exit 1; }
grep -Fq 'local sidecar_up=(up -d --no-deps --no-build --pull never)' "$repo_root/deploy/xiass-runtime-start.sh" \
    || { echo "hot-swap must let Compose reuse an unchanged Team automation sidecar" >&2; exit 1; }

echo "Updater BusyBox compatibility and success-status test passed."
