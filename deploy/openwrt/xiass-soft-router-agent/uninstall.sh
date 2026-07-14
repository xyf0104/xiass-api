#!/bin/sh
set -eu

ROOT="${XIASS_ROOT:-}"
SKIP_SERVICE="${XIASS_SKIP_SERVICE:-0}"
[ -z "$ROOT" ] || SKIP_SERVICE=1
PURGE=0
PURGE_LEGACY=0

while [ "$#" -gt 0 ]; do
	case "$1" in
		--purge) PURGE=1 ;;
		--purge-legacy) PURGE=1; PURGE_LEGACY=1 ;;
		*) printf '[XIASS] 未知卸载参数：%s\n' "$1" >&2; exit 2 ;;
	esac
	shift
done

target() {
	printf '%s%s\n' "$ROOT" "$1"
}

log() {
	printf '[XIASS] %s\n' "$*"
}

remove_rc_links() {
	rc_dir="$(target /etc/rc.d)"
	[ -d "$rc_dir" ] || return 0
	for name in xiass-soft-router-agent nowind-soft-router-agent; do
		for link in "$rc_dir"/S??"$name" "$rc_dir"/K??"$name"; do
			[ -L "$link" ] || continue
			rm -f "$link"
		done
	done
}

remove_compat_file() {
	path="$1"
	[ -f "$path" ] || return 0
	grep -q 'XIASS_COMPAT_WRAPPER=1' "$path" 2>/dev/null || return 0
	rm -f "$path"
}

if [ "$SKIP_SERVICE" != "1" ]; then
	[ ! -x "$(target /usr/bin/xiass-soft-router-agent)" ] || "$(target /usr/bin/xiass-soft-router-agent)" --stop-frpc >/dev/null 2>&1 || true
	[ ! -x "$(target /etc/init.d/xiass-soft-router-agent)" ] || "$(target /etc/init.d/xiass-soft-router-agent)" disable >/dev/null 2>&1 || true
	[ ! -x "$(target /etc/init.d/xiass-soft-router-agent)" ] || "$(target /etc/init.d/xiass-soft-router-agent)" stop >/dev/null 2>&1 || true
fi

remove_rc_links
rm -f \
	"$(target /usr/bin/xiass-soft-router-agent)" \
	"$(target /etc/init.d/xiass-soft-router-agent)" \
	"$(target /usr/lib/lua/luci/controller/xiass_soft_router_agent.lua)" \
	"$(target /usr/lib/lua/luci/model/cbi/xiass_soft_router_agent.lua)" \
	"$(target /usr/lib/lua/luci/view/xiass_soft_router_agent/status.htm)"
remove_compat_file "$(target /usr/bin/nowind-soft-router-agent)"
remove_compat_file "$(target /etc/init.d/nowind-soft-router-agent)"
remove_compat_file "$(target /usr/lib/lua/luci/controller/nowind_soft_router_agent.lua)"
rm -rf "$(target /var/run/xiass-soft-router-agent)"

if [ "$PURGE" -eq 1 ]; then
	rm -f \
		"$(target /etc/config/xiass_soft_router_agent)" \
		"$(target /etc/frp/xiass-soft-router-frpc.ini)" \
		"$(target /var/log/xiass-soft-router-agent.log)"
	if [ "$PURGE_LEGACY" -eq 1 ]; then
		rm -f \
			"$(target /etc/config/nowind_soft_router_agent)" \
			"$(target /etc/frp/nowind-soft-router-frpc.ini)" \
			"$(target /var/log/nowind-soft-router-agent.log)"
		rm -rf "$(target /var/run/nowind-soft-router-agent)"
	fi
else
	log "配置、日志和 FRP 配置已保留；使用 --purge 可删除新名称数据。"
fi

rm -f "$(target /tmp/luci-indexcache)"
rm -rf "$(target /tmp/luci-modulecache)" 2>/dev/null || true
log "xiass-soft-router-agent 已卸载。"
[ "$PURGE_LEGACY" -eq 0 ] || log "旧名称配置也已按明确参数删除。"
