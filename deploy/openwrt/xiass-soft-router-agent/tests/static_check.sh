#!/bin/sh
set -eu

BASE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

sh -n \
	"$BASE_DIR/install.sh" \
	"$BASE_DIR/migrate.sh" \
	"$BASE_DIR/uninstall.sh" \
	"$BASE_DIR/files/etc/init.d/xiass-soft-router-agent" \
	"$BASE_DIR/files/etc/init.d/nowind-soft-router-agent" \
	"$BASE_DIR/files/usr/bin/nowind-soft-router-agent"

python3 -c 'import ast, pathlib, sys; ast.parse(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))' \
	"$BASE_DIR/files/usr/bin/xiass-soft-router-agent"

if command -v luac >/dev/null 2>&1; then
	luac -p \
		"$BASE_DIR/files/usr/lib/lua/luci/controller/xiass_soft_router_agent.lua" \
		"$BASE_DIR/files/usr/lib/lua/luci/controller/nowind_soft_router_agent.lua" \
		"$BASE_DIR/files/usr/lib/lua/luci/model/cbi/xiass_soft_router_agent.lua"
fi

if rg -n '(killall|pkill)[[:space:]]+.*frpc|nc[[:space:]]+-z' \
	"$BASE_DIR/files" "$BASE_DIR/install.sh" "$BASE_DIR/migrate.sh" "$BASE_DIR/uninstall.sh" >/dev/null 2>&1; then
	printf '禁止按进程名批量终止 frpc，也禁止使用 nc -z 判断实时连通性。\n' >&2
	exit 1
fi

if rg -ni 'nowind|no[[:space:]]*wind' \
	"$BASE_DIR/files/usr/lib/lua/luci/controller/xiass_soft_router_agent.lua" \
	"$BASE_DIR/files/usr/lib/lua/luci/model/cbi/xiass_soft_router_agent.lua" \
	"$BASE_DIR/files/usr/lib/lua/luci/view/xiass_soft_router_agent/status.htm" >/dev/null 2>&1; then
	printf '新 LuCI 页面不得显示旧品牌。\n' >&2
	exit 1
fi

for required in xiass_api report pull exit process_ok control_ok latest_log setLog frpc_config_error; do
	rg -q "$required" "$BASE_DIR/files/usr/lib/lua/luci/view/xiass_soft_router_agent/status.htm" \
		"$BASE_DIR/files/usr/bin/xiass-soft-router-agent"
done

if rg -q "setStatus\('xiass-frpc-log-status'" "$BASE_DIR/files/usr/lib/lua/luci/view/xiass_soft_router_agent/status.htm"; then
	printf 'frpc 最新日志不得作为独立健康红绿状态。\n' >&2
	exit 1
fi

for required in canonical_is_factory_defaults legacy_has_real_values ensure_managed_frpc_config check_python_runtime; do
	rg -q "$required" "$BASE_DIR/migrate.sh" "$BASE_DIR/install.sh"
done

printf '静态检查通过。\n'
