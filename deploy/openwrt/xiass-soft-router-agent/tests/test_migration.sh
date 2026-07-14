#!/bin/sh
set -eu

BASE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
ROOT="$TMP_DIR/root"
mkdir -p "$ROOT/etc/config" "$ROOT/etc/frp" "$ROOT/var/log"

cat >"$ROOT/etc/config/nowind_soft_router_agent" <<'EOF'
config agent 'main'
	option enabled '1'
	option panel_url 'https://xiass.example.com'
	option agent_token 'legacy-agent-secret'
	option interval '30'
	option frpc_bin '/usr/bin/frpc'
	option frpc_config '/etc/frp/nowind-soft-router-frpc.ini'
	option frpc_pid_file '/var/run/nowind-soft-router-agent/frpc.pid'
	option local_ip '127.0.0.1'
	option include_passwall '1'
	option include_passwall2 '1'
	option log_file '/var/log/nowind-soft-router-agent.log'
	option log_max_size '5M'
	option log_backups '5'
EOF
printf 'legacy-agent-frp\n' >"$ROOT/etc/frp/nowind-soft-router-frpc.ini"
printf 'hk-frp-must-stay\n' >"$ROOT/etc/frp/hk-frpc.ini"
printf 'legacy-log\n' >"$ROOT/var/log/nowind-soft-router-agent.log"

XIASS_ROOT="$ROOT" UCI_BIN="$BASE_DIR/tests/fake_uci.py" sh "$BASE_DIR/migrate.sh" --no-backup

CONFIG="$ROOT/etc/config/xiass_soft_router_agent"
test -f "$CONFIG"
grep -q "option api_url 'https://xiass.example.com'" "$CONFIG"
grep -q "option agent_token 'legacy-agent-secret'" "$CONFIG"
grep -q "option frpc_config '/etc/frp/xiass-soft-router-frpc.ini'" "$CONFIG"
grep -q "option frpc_pid_file '/var/run/xiass-soft-router-agent/frpc.pid'" "$CONFIG"
grep -q "option log_file '/var/log/xiass-soft-router-agent.log'" "$CONFIG"
grep -q "option exit_probe_timeout '5'" "$CONFIG"
cmp "$ROOT/etc/frp/nowind-soft-router-frpc.ini" "$ROOT/etc/frp/xiass-soft-router-frpc.ini"
cmp "$ROOT/var/log/nowind-soft-router-agent.log" "$ROOT/var/log/xiass-soft-router-agent.log"
grep -q '^hk-frp-must-stay$' "$ROOT/etc/frp/hk-frpc.ini"
test -f "$ROOT/etc/config/nowind_soft_router_agent"

FACTORY_ROOT="$TMP_DIR/factory-root"
mkdir -p "$FACTORY_ROOT/etc/config" "$FACTORY_ROOT/etc/frp"
cp "$BASE_DIR/files/etc/config/xiass_soft_router_agent" "$FACTORY_ROOT/etc/config/xiass_soft_router_agent"
cat >"$FACTORY_ROOT/etc/config/nowind_soft_router_agent" <<'EOF'
config agent 'main'
	option enabled '1'
	option panel_url 'https://legacy-real.example.com'
	option agent_token 'legacy-real-secret'
	option interval '45'
	option frpc_config '/etc/frp/hk-frpc.ini'
	option frpc_pid_file '/var/run/nowind-soft-router-agent/frpc.pid'
	option log_file '/var/log/nowind-soft-router-agent.log'
EOF
printf 'hk-frp-must-stay\n' >"$FACTORY_ROOT/etc/frp/hk-frpc.ini"

XIASS_ROOT="$FACTORY_ROOT" UCI_BIN="$BASE_DIR/tests/fake_uci.py" sh "$BASE_DIR/migrate.sh" --no-backup

FACTORY_CONFIG="$FACTORY_ROOT/etc/config/xiass_soft_router_agent"
grep -q "option enabled '1'" "$FACTORY_CONFIG"
grep -q "option api_url 'https://legacy-real.example.com'" "$FACTORY_CONFIG"
grep -q "option agent_token 'legacy-real-secret'" "$FACTORY_CONFIG"
grep -q "option interval '45'" "$FACTORY_CONFIG"
grep -q "option frpc_config '/etc/frp/xiass-soft-router-frpc.ini'" "$FACTORY_CONFIG"
grep -q "option migration_state 'complete'" "$FACTORY_CONFIG"
grep -q "option frpc_config '/etc/frp/hk-frpc.ini'" "$FACTORY_ROOT/etc/config/nowind_soft_router_agent"
grep -q '^hk-frp-must-stay$' "$FACTORY_ROOT/etc/frp/hk-frpc.ini"

printf '迁移测试通过。\n'
