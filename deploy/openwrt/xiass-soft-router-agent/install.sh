#!/bin/sh
set -eu

BASE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
ROOT="${XIASS_ROOT:-}"
SKIP_SERVICE="${XIASS_SKIP_SERVICE:-0}"
UCI_BIN="${UCI_BIN:-uci}"
[ -z "$ROOT" ] || SKIP_SERVICE=1
BACKUP_DIR="${ROOT}/root/xiass-soft-router-agent-backup-$(date +%Y%m%d%H%M%S)"

target() {
	printf '%s%s\n' "$ROOT" "$1"
}

log() {
	printf '[XIASS] %s\n' "$*"
}

uci_value() {
	package="$1"
	option="$2"
	if [ -n "$ROOT" ]; then
		"$UCI_BIN" -c "$(target /etc/config)" -q get "$package.main.$option" 2>/dev/null || true
	else
		"$UCI_BIN" -q get "$package.main.$option" 2>/dev/null || true
	fi
}

service_or_config_active() {
	service_script="$1"
	package="$2"
	if [ -x "$service_script" ] && "$service_script" status >/dev/null 2>&1; then
		return 0
	fi
	case "$(uci_value "$package" enabled)" in
		1|true|yes|on|enabled) return 0 ;;
	esac
	return 1
}

check_python_runtime() {
	if ! command -v python3 >/dev/null 2>&1; then
		log "缺少 python3，无法运行 XIASS 节点程序。"
		return 1
	fi
	if ! python3 -c 'import ssl, urllib.request' >/dev/null 2>&1; then
		log "当前 Python 缺少 ssl 或 urllib.request，无法进行 HTTPS、面板同步和出口传输检查。"
		return 1
	fi
	return 0
}

backup_path() {
	logical="$1"
	source_path="$(target "$logical")"
	[ -e "$source_path" ] || return 0
	destination="$BACKUP_DIR$logical"
	mkdir -p "$(dirname "$destination")"
	cp -a "$source_path" "$destination"
}

install_file() {
	source_relative="$1"
	destination_logical="$2"
	mode="$3"
	destination="$(target "$destination_logical")"
	mkdir -p "$(dirname "$destination")"
	backup_path "$destination_logical"
	cp "$BASE_DIR/files/$source_relative" "$destination"
	chmod "$mode" "$destination"
}

remove_legacy_rc_links() {
	rc_dir="$(target /etc/rc.d)"
	[ -d "$rc_dir" ] || return 0
	for link in "$rc_dir"/S??nowind-soft-router-agent "$rc_dir"/K??nowind-soft-router-agent; do
		[ -L "$link" ] || continue
		rm -f "$link"
	done
}

for logical in \
	/etc/config/xiass_soft_router_agent \
	/etc/config/nowind_soft_router_agent \
	/etc/frp/xiass-soft-router-frpc.ini \
	/etc/frp/nowind-soft-router-frpc.ini \
	/usr/bin/xiass-soft-router-agent \
	/usr/bin/nowind-soft-router-agent \
	/etc/init.d/xiass-soft-router-agent \
	/etc/init.d/nowind-soft-router-agent \
	/usr/lib/lua/luci/controller/xiass_soft_router_agent.lua \
	/usr/lib/lua/luci/controller/nowind_soft_router_agent.lua \
	/usr/lib/lua/luci/model/cbi/xiass_soft_router_agent.lua \
	/usr/lib/lua/luci/model/cbi/nowind_soft_router_agent.lua; do
	backup_path "$logical"
done

# 先安装候选命令并准备新配置。此时旧节点程序和旧 frpc 保持运行。
install_file usr/bin/xiass-soft-router-agent /usr/bin/xiass-soft-router-agent 0755

canonical_config="$(target /etc/config/xiass_soft_router_agent)"
legacy_config="$(target /etc/config/nowind_soft_router_agent)"
canonical_init="$(target /etc/init.d/xiass-soft-router-agent)"
legacy_init="$(target /etc/init.d/nowind-soft-router-agent)"
legacy_was_active=0
canonical_was_active=0
service_or_config_active "$legacy_init" nowind_soft_router_agent && legacy_was_active=1 || true
service_or_config_active "$canonical_init" xiass_soft_router_agent && canonical_was_active=1 || true
runtime_ready=1
check_python_runtime || runtime_ready=0
mkdir -p "$(target /etc/config)"
if [ ! -f "$canonical_config" ]; then
	if [ -f "$legacy_config" ]; then
		cp -a "$legacy_config" "$canonical_config"
		log "已保留并复制旧 UCI 配置。"
	else
		cp "$BASE_DIR/files/etc/config/xiass_soft_router_agent" "$canonical_config"
	fi
	chmod 0600 "$canonical_config"
else
	log "保留现有配置：/etc/config/xiass_soft_router_agent"
fi

XIASS_ROOT="$ROOT" UCI_BIN="$UCI_BIN" sh "$BASE_DIR/migrate.sh" --no-backup

if [ "$SKIP_SERVICE" != "1" ] && { [ "$legacy_was_active" = "1" ] || [ "$canonical_was_active" = "1" ]; }; then
	log "正在检查候选 XIASS 节点程序；检查成功前不会停止旧节点程序。"
	if [ "$runtime_ready" != "1" ]; then
		log "候选节点程序缺少 Python SSL/urllib 运行依赖，旧节点程序保持运行。"
		exit 1
	fi
	if ! "$(target /usr/bin/xiass-soft-router-agent)" --health-check; then
		log "候选 XIASS 节点程序健康检查失败，旧节点程序保持运行，未执行服务切换。"
		exit 1
	fi
fi

# 健康检查成功后，先替换旧命令为带 PID/配置归属校验的兼容包装器。
# 保留旧 init 脚本直到它完成 stop，以便 procd 正确终止旧实例。
legacy_bin_installed=0
if [ "$SKIP_SERVICE" != "1" ]; then
	install_file usr/bin/nowind-soft-router-agent /usr/bin/nowind-soft-router-agent 0755
	legacy_bin_installed=1
	[ ! -x "$legacy_init" ] || "$legacy_init" stop >/dev/null 2>&1 || true
	[ ! -x "$canonical_init" ] || "$canonical_init" stop >/dev/null 2>&1 || true
	"$(target /usr/bin/xiass-soft-router-agent)" --stop-frpc >/dev/null 2>&1 || true
fi

install_file etc/init.d/xiass-soft-router-agent /etc/init.d/xiass-soft-router-agent 0755
install_file usr/lib/lua/luci/controller/xiass_soft_router_agent.lua /usr/lib/lua/luci/controller/xiass_soft_router_agent.lua 0644
install_file usr/lib/lua/luci/model/cbi/xiass_soft_router_agent.lua /usr/lib/lua/luci/model/cbi/xiass_soft_router_agent.lua 0644
install_file usr/lib/lua/luci/view/xiass_soft_router_agent/status.htm /usr/lib/lua/luci/view/xiass_soft_router_agent/status.htm 0644
[ "$legacy_bin_installed" = "1" ] || install_file usr/bin/nowind-soft-router-agent /usr/bin/nowind-soft-router-agent 0755
install_file etc/init.d/nowind-soft-router-agent /etc/init.d/nowind-soft-router-agent 0755
install_file usr/lib/lua/luci/controller/nowind_soft_router_agent.lua /usr/lib/lua/luci/controller/nowind_soft_router_agent.lua 0644

# 旧 CBI 页面会产生重复菜单，迁移后由隐藏重定向控制器接管。
rm -f "$(target /usr/lib/lua/luci/model/cbi/nowind_soft_router_agent.lua)"
mkdir -p "$(target /etc/frp)" "$(target /var/run/xiass-soft-router-agent)"
chmod 0700 "$(target /var/run/xiass-soft-router-agent)"
remove_legacy_rc_links

rm -f "$(target /tmp/luci-indexcache)"
rm -rf "$(target /tmp/luci-modulecache)" 2>/dev/null || true

if [ "$SKIP_SERVICE" != "1" ] && [ "$runtime_ready" = "1" ]; then
	"$(target /etc/init.d/xiass-soft-router-agent)" enable >/dev/null 2>&1 || true
	if "$(target /usr/bin/xiass-soft-router-agent)" --is-enabled >/dev/null 2>&1; then
		"$(target /etc/init.d/xiass-soft-router-agent)" restart >/dev/null 2>&1 || true
	else
		"$(target /etc/init.d/xiass-soft-router-agent)" stop >/dev/null 2>&1 || true
	fi
elif [ "$SKIP_SERVICE" != "1" ]; then
	log "未启动 XIASS 节点程序；请先安装 Python SSL/urllib 依赖后再重启服务。"
fi

log "xiass-soft-router-agent 安装完成。"
if [ -d "$BACKUP_DIR" ]; then
	log "升级前备份：$BACKUP_DIR"
fi
log "依赖：python3、python3-openssl（或 python3-ssl）、python3-urllib、frpc、luci-compat。"
log "LuCI 入口：服务 -> XIASS API 软路由节点"
log "命令：/etc/init.d/xiass-soft-router-agent restart"
