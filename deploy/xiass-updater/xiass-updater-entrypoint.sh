#!/usr/bin/env bash
set -Eeuo pipefail

: "${INSTALL_DIR:?INSTALL_DIR is required}"

export BACKUP_DIR="${BACKUP_DIR:-/root/xiass-backups}"
if [ "${1:-}" = "runtime-export" ]; then
    shift
    exec bash /usr/local/lib/xiass/xiass-runtime-export.sh "$@"
fi
exec bash /usr/local/lib/xiass/xiass-update.sh "$@"
