#!/bin/sh
set -eu

BASE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
ROOT="$TMP_DIR/root"
mkdir -p "$ROOT/etc/config" "$ROOT/etc/frp" "$ROOT/usr/lib/lua/luci/model/cbi"

awk '
	/--health-check/ { health = NR }
	/"\$legacy_init" stop/ { legacy_stop = NR }
	END { exit !(health && legacy_stop && health < legacy_stop) }
' "$BASE_DIR/install.sh"

cat >"$ROOT/etc/config/nowind_soft_router_agent" <<'EOF'
config agent 'main'
	option enabled '0'
	option panel_url 'https://xiass.example.com'
	option agent_token 'keep-this-secret'
	option frpc_config '/etc/frp/nowind-soft-router-frpc.ini'
	option frpc_pid_file '/var/run/nowind-soft-router-agent/frpc.pid'
EOF
printf 'legacy-agent-frp\n' >"$ROOT/etc/frp/nowind-soft-router-frpc.ini"
printf 'hk-frp-must-stay\n' >"$ROOT/etc/frp/hk-frpc.ini"
printf 'old-cbi\n' >"$ROOT/usr/lib/lua/luci/model/cbi/nowind_soft_router_agent.lua"

XIASS_ROOT="$ROOT" UCI_BIN="$BASE_DIR/tests/fake_uci.py" sh "$BASE_DIR/install.sh"

test -x "$ROOT/usr/bin/xiass-soft-router-agent"
test -x "$ROOT/etc/init.d/xiass-soft-router-agent"
grep -q 'XIASS_COMPAT_WRAPPER=1' "$ROOT/usr/bin/nowind-soft-router-agent"
grep -q 'XIASS_COMPAT_WRAPPER=1' "$ROOT/etc/init.d/nowind-soft-router-agent"
test ! -e "$ROOT/usr/lib/lua/luci/model/cbi/nowind_soft_router_agent.lua"
grep -q "option api_url 'https://xiass.example.com'" "$ROOT/etc/config/xiass_soft_router_agent"
grep -q '^hk-frp-must-stay$' "$ROOT/etc/frp/hk-frpc.ini"

XIASS_ROOT="$ROOT" sh "$BASE_DIR/uninstall.sh"
test ! -e "$ROOT/usr/bin/xiass-soft-router-agent"
test -f "$ROOT/etc/config/xiass_soft_router_agent"
test -f "$ROOT/etc/frp/xiass-soft-router-frpc.ini"
grep -q '^hk-frp-must-stay$' "$ROOT/etc/frp/hk-frpc.ini"

XIASS_ROOT="$ROOT" sh "$BASE_DIR/uninstall.sh" --purge
test ! -e "$ROOT/etc/config/xiass_soft_router_agent"
test -f "$ROOT/etc/config/nowind_soft_router_agent"
grep -q '^hk-frp-must-stay$' "$ROOT/etc/frp/hk-frpc.ini"

printf '安装与卸载测试通过。\n'
