#!/usr/bin/env bash
# Historical filename compatibility wrapper.

set -Eeuo pipefail
exec "$(dirname "$0")/enable-xiass-features.sh" "$@"
