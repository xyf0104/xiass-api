#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/deploy/xiass-update.sh"
CANONICAL="https://github.com/xyf0104/xiass-api.git"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

run_script_case() {
    local source_origin="$1" upstream_origin="$2" allow_migration="$3" assertions="$4"
    XIASS_UPDATE_LIB_ONLY=1 SOURCE_ORIGIN="$source_origin" UPSTREAM_ORIGIN="$upstream_origin" \
        XIASS_ALLOW_ORIGIN_MIGRATION="$allow_migration" CANONICAL="$CANONICAL" bash -s "$SCRIPT" <<EOF
set -Eeuo pipefail
script="\$1"
calls="\$(mktemp)"
trap 'rm -f "\$calls"' EXIT
git() {
    if [ "\$1" = "-C" ] && [ "\$3" = "remote" ] && [ "\$4" = "get-url" ]; then
        case "\$5" in
            origin)
                [ -n "\$SOURCE_ORIGIN" ] || return 2
                printf '%s\\n' "\$SOURCE_ORIGIN"
                return 0
                ;;
            xiass-upstream)
                [ -n "\$UPSTREAM_ORIGIN" ] || return 2
                printf '%s\\n' "\$UPSTREAM_ORIGIN"
                return 0
                ;;
        esac
    fi
    if [ "\$1" = "-C" ] && [ "\$3" = "remote" ] && [ "\$4" = "add" ]; then
        printf 'add:%s:%s\\n' "\$5" "\$6" >> "\$calls"
        return 0
    fi
    if [ "\$1" = "-C" ] && [ "\$3" = "remote" ] && [ "\$4" = "remove" ]; then
        printf 'remove:%s\\n' "\$5" >> "\$calls"
        return 0
    fi
    return 1
}
source "\$script"
INSTALL_DIR=/fixture
ensure_xiass_update_remote
$assertions
EOF
}

run_known_legacy_case() {
    run_script_case "$1" "" "0" '
[ "$UPDATE_REMOTE" = "xiass-upstream" ]
[ "$CREATED_UPDATE_REMOTE" = true ]
[ "$(sed -n "1p" "$calls")" = "add:xiass-upstream:$CANONICAL" ]
remove_created_update_remote
[ "$CREATED_UPDATE_REMOTE" = false ]
[ "$(sed -n "2p" "$calls")" = "remove:xiass-upstream" ]'
}

run_canonical_case() {
    run_script_case "$CANONICAL" "" "0" '
[ "$UPDATE_REMOTE" = "origin" ]
[ "$CREATED_UPDATE_REMOTE" = false ]
[ ! -s "$calls" ]'
}

run_existing_upstream_case() {
    run_script_case "https://github.com/example/custom-xiass.git" "$CANONICAL" "0" '
[ "$UPDATE_REMOTE" = "xiass-upstream" ]
[ "$CREATED_UPDATE_REMOTE" = false ]
[ ! -s "$calls" ]'
}

run_opt_in_case() {
    run_script_case "https://github.com/example/custom-xiass.git" "" "1" '
[ "$UPDATE_REMOTE" = "xiass-upstream" ]
[ "$CREATED_UPDATE_REMOTE" = true ]
[ "$(sed -n "1p" "$calls")" = "add:xiass-upstream:$CANONICAL" ]'
}

run_rejected_custom_case() {
    if XIASS_UPDATE_LIB_ONLY=1 SOURCE_ORIGIN="https://github.com/example/custom-xiass.git" bash -s "$SCRIPT" <<'EOF'
set -Eeuo pipefail
script="$1"
git() {
    if [ "$1" = "-C" ] && [ "$3" = "remote" ] && [ "$4" = "get-url" ] && [ "$5" = "origin" ]; then
        printf '%s\n' "$SOURCE_ORIGIN"
        return 0
    fi
    return 2
}
source "$script"
INSTALL_DIR=/fixture
ensure_xiass_update_remote
EOF
    then
        fail "custom fork must require explicit migration opt-in"
    fi
}

for origin in \
    "https://github.com/xyf0104/nowind-api.git" \
    "git@github.com:xyf0104/nowind-api.git" \
    "ssh://git@github.com/xyf0104/nowind-api.git"; do
    run_known_legacy_case "$origin"
done
run_canonical_case
run_existing_upstream_case
run_opt_in_case
run_rejected_custom_case

printf 'xiass update remote migration tests passed.\n'
