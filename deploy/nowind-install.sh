#!/usr/bin/env bash
# Compatibility entrypoint for installations created before XIASS API v1.0.68.
set -Eeuo pipefail

if [ -f "$(dirname "$0")/xiass-install.sh" ]; then
    exec bash "$(dirname "$0")/xiass-install.sh" "$@"
fi

exec bash <(curl -fsSL https://raw.githubusercontent.com/xyf0104/xiass-api/main/deploy/xiass-install.sh) "$@"
