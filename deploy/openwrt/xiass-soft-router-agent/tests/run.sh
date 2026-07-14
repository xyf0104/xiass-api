#!/bin/sh
set -eu

BASE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

sh "$BASE_DIR/tests/static_check.sh"
python3 "$BASE_DIR/tests/test_agent.py"
sh "$BASE_DIR/tests/test_migration.sh"
sh "$BASE_DIR/tests/test_install.sh"

printf '全部 OpenWrt Agent 校验通过。\n'
