#!/bin/sh
set -eu

ROOT="${XIASS_ROOT:-}"
UCI_BIN="${UCI_BIN:-uci}"
SKIP_UCI="${XIASS_SKIP_UCI:-0}"
NO_BACKUP=0

while [ "$#" -gt 0 ]; do
	case "$1" in
		--no-backup) NO_BACKUP=1 ;;
		*) printf '[XIASS] 未知迁移参数：%s\n' "$1" >&2; exit 2 ;;
	esac
	shift
done

target() {
	printf '%s%s\n' "$ROOT" "$1"
}

CANONICAL_PACKAGE="xiass_soft_router_agent"
LEGACY_PACKAGE="nowind_soft_router_agent"
CANONICAL_CONFIG="$(target /etc/config/xiass_soft_router_agent)"
LEGACY_CONFIG="$(target /etc/config/nowind_soft_router_agent)"
CANONICAL_FRPC="$(target /etc/frp/xiass-soft-router-frpc.ini)"
LEGACY_FRPC="$(target /etc/frp/nowind-soft-router-frpc.ini)"
CANONICAL_LOG="$(target /var/log/xiass-soft-router-agent.log)"
LEGACY_LOG="$(target /var/log/nowind-soft-router-agent.log)"
BACKUP_DIR="$(target /root/xiass-soft-router-agent-migration-$(date +%Y%m%d%H%M%S))"
SCHEMA_VERSION=3

log() {
	printf '[XIASS] %s\n' "$*"
}

backup_path() {
	logical="$1"
	source_path="$(target "$logical")"
	[ -e "$source_path" ] || return 0
	destination="$BACKUP_DIR$logical"
	mkdir -p "$(dirname "$destination")"
	cp -a "$source_path" "$destination"
}

copy_if_missing() {
	source_path="$1"
	destination="$2"
	[ -e "$source_path" ] || return 0
	[ ! -e "$destination" ] || return 0
	mkdir -p "$(dirname "$destination")"
	cp -a "$source_path" "$destination"
}

uci_cmd() {
	if [ -n "$ROOT" ]; then
		"$UCI_BIN" -c "$(target /etc/config)" "$@"
	else
		"$UCI_BIN" "$@"
	fi
}

uci_get_any() {
	package="$1"
	shift
	for option in "$@"; do
		value="$(uci_cmd -q get "$package.main.$option" 2>/dev/null || true)"
		if [ -n "$value" ]; then
			printf '%s\n' "$value"
			return 0
		fi
	done
	return 1
}

migrate_option() {
	target_option="$1"
	shift
	current="$(uci_get_any "$CANONICAL_PACKAGE" "$target_option" 2>/dev/null || true)"
	[ -z "$current" ] || return 0
	for package in "$CANONICAL_PACKAGE" "$LEGACY_PACKAGE"; do
		value="$(uci_get_any "$package" "$@" 2>/dev/null || true)"
		if [ -n "$value" ]; then
			uci_cmd set "$CANONICAL_PACKAGE.main.$target_option=$value"
			return 0
		fi
	done
}

force_legacy_option() {
	target_option="$1"
	shift
	value="$(uci_get_any "$LEGACY_PACKAGE" "$@" 2>/dev/null || true)"
	[ -z "$value" ] || uci_cmd set "$CANONICAL_PACKAGE.main.$target_option=$value"
}

set_default() {
	option="$1"
	value="$2"
	current="$(uci_get_any "$CANONICAL_PACKAGE" "$option" 2>/dev/null || true)"
	[ -n "$current" ] || uci_cmd set "$CANONICAL_PACKAGE.main.$option=$value"
}

normalize_default_path() {
	option="$1"
	old_path="$2"
	new_path="$3"
	current="$(uci_get_any "$CANONICAL_PACKAGE" "$option" 2>/dev/null || true)"
	[ "$current" = "$old_path" ] || return 0
	uci_cmd set "$CANONICAL_PACKAGE.main.$option=$new_path"
}

option_matches() {
	package="$1"
	expected="$2"
	shift 2
	value="$(uci_get_any "$package" "$@" 2>/dev/null || true)"
	[ -z "$value" ] && return 0
	[ "$value" = "$expected" ]
}

canonical_is_factory_defaults() {
	version="$(uci_get_any "$CANONICAL_PACKAGE" config_version 2>/dev/null || true)"
	case "$version" in
		""|2|3) ;;
		*) return 1 ;;
	esac
	option_matches "$CANONICAL_PACKAGE" 0 enabled xiass_enabled nowind_enabled || return 1
	option_matches "$CANONICAL_PACKAGE" https://api.example.com api_url xiass_api_url panel_url nowind_api_url nowind_panel_url || return 1
	option_matches "$CANONICAL_PACKAGE" "" agent_token xiass_agent_token nowind_agent_token || return 1
	option_matches "$CANONICAL_PACKAGE" 20 interval poll_interval xiass_interval nowind_interval || return 1
	option_matches "$CANONICAL_PACKAGE" /usr/bin/frpc frpc_bin frpc_binary || return 1
	option_matches "$CANONICAL_PACKAGE" /etc/frp/xiass-soft-router-frpc.ini frpc_config frpc_config_file || return 1
	option_matches "$CANONICAL_PACKAGE" /var/run/xiass-soft-router-agent/frpc.pid frpc_pid_file frpc_pid || return 1
	option_matches "$CANONICAL_PACKAGE" 127.0.0.1 local_ip socks_listen_ip || return 1
	option_matches "$CANONICAL_PACKAGE" 1 include_passwall || return 1
	option_matches "$CANONICAL_PACKAGE" 1 include_passwall2 || return 1
	option_matches "$CANONICAL_PACKAGE" "" extra_socks extra_socks_ports || return 1
	option_matches "$CANONICAL_PACKAGE" "" exit_probe_host socks_probe_host || return 1
	option_matches "$CANONICAL_PACKAGE" 0 exit_probe_port socks_probe_port || return 1
	option_matches "$CANONICAL_PACKAGE" 5 exit_probe_timeout socks_probe_timeout || return 1
	option_matches "$CANONICAL_PACKAGE" auto exit_probe_protocol socks_probe_protocol || return 1
	option_matches "$CANONICAL_PACKAGE" /var/log/xiass-soft-router-agent.log log_file || return 1
	option_matches "$CANONICAL_PACKAGE" 5M log_max_size || return 1
	option_matches "$CANONICAL_PACKAGE" 5 log_backups || return 1
}

legacy_has_real_values() {
	[ -f "$LEGACY_CONFIG" ] || return 1
	option_matches "$LEGACY_PACKAGE" 0 enabled xiass_enabled nowind_enabled || return 0
	option_matches "$LEGACY_PACKAGE" https://api.example.com api_url xiass_api_url panel_url nowind_api_url nowind_panel_url || return 0
	option_matches "$LEGACY_PACKAGE" "" agent_token xiass_agent_token nowind_agent_token || return 0
	option_matches "$LEGACY_PACKAGE" 20 interval poll_interval xiass_interval nowind_interval || return 0
	option_matches "$LEGACY_PACKAGE" /usr/bin/frpc frpc_bin frpc_binary || return 0
	option_matches "$LEGACY_PACKAGE" /etc/frp/nowind-soft-router-frpc.ini frpc_config frpc_config_file || return 0
	option_matches "$LEGACY_PACKAGE" /var/run/nowind-soft-router-agent/frpc.pid frpc_pid_file frpc_pid || return 0
	option_matches "$LEGACY_PACKAGE" 127.0.0.1 local_ip socks_listen_ip || return 0
	option_matches "$LEGACY_PACKAGE" 1 include_passwall || return 0
	option_matches "$LEGACY_PACKAGE" 1 include_passwall2 || return 0
	option_matches "$LEGACY_PACKAGE" "" extra_socks extra_socks_ports || return 0
	option_matches "$LEGACY_PACKAGE" "" exit_probe_host socks_probe_host || return 0
	option_matches "$LEGACY_PACKAGE" 0 exit_probe_port socks_probe_port || return 0
	option_matches "$LEGACY_PACKAGE" 5 exit_probe_timeout socks_probe_timeout || return 0
	option_matches "$LEGACY_PACKAGE" auto exit_probe_protocol socks_probe_protocol || return 0
	option_matches "$LEGACY_PACKAGE" /var/log/nowind-soft-router-agent.log log_file || return 0
	option_matches "$LEGACY_PACKAGE" 5M log_max_size || return 0
	option_matches "$LEGACY_PACKAGE" 5 log_backups || return 0
	return 1
}

ensure_managed_frpc_config() {
	current="$(uci_get_any "$CANONICAL_PACKAGE" frpc_config frpc_config_file 2>/dev/null || true)"
	case "$current" in
		/etc/frp/xiass-soft-router-frpc.ini|/etc/frp/nowind-soft-router-frpc.ini) return 0 ;;
	esac
	log "拒绝接管非本节点程序管理的 frpc 配置：${current:-（空）}；旧 UCI 保持不变。"
	uci_cmd set "$CANONICAL_PACKAGE.main.frpc_config=/etc/frp/xiass-soft-router-frpc.ini"
}

if [ "$NO_BACKUP" -eq 0 ]; then
	backup_path /etc/config/xiass_soft_router_agent
	backup_path /etc/config/nowind_soft_router_agent
	backup_path /etc/frp/xiass-soft-router-frpc.ini
	backup_path /etc/frp/nowind-soft-router-frpc.ini
fi

if [ ! -f "$CANONICAL_CONFIG" ] && [ -f "$LEGACY_CONFIG" ]; then
	mkdir -p "$(dirname "$CANONICAL_CONFIG")"
	cp -a "$LEGACY_CONFIG" "$CANONICAL_CONFIG"
	chmod 0600 "$CANONICAL_CONFIG"
	log "已复制旧 UCI 配置到新配置包。"
fi

if [ ! -f "$CANONICAL_CONFIG" ]; then
	printf '[XIASS] 未找到 %s，请先运行 install.sh。\n' "$CANONICAL_CONFIG" >&2
	exit 1
fi

if [ "$SKIP_UCI" != "1" ]; then
	uci_cmd -q get "$CANONICAL_PACKAGE.main" >/dev/null 2>&1 || uci_cmd set "$CANONICAL_PACKAGE.main=agent"
	force_legacy=0
	if canonical_is_factory_defaults && legacy_has_real_values; then
		force_legacy=1
		log "检测到出厂默认 XIASS 配置，正在迁移旧配置中的实际值。"
	fi

	if [ "$force_legacy" = "1" ]; then
		force_legacy_option enabled enabled xiass_enabled nowind_enabled
		force_legacy_option api_url api_url xiass_api_url panel_url nowind_api_url nowind_panel_url
		force_legacy_option agent_token agent_token xiass_agent_token nowind_agent_token
		force_legacy_option interval interval poll_interval xiass_interval nowind_interval
		force_legacy_option frpc_bin frpc_bin frpc_binary
		force_legacy_option frpc_config frpc_config frpc_config_file
		force_legacy_option frpc_pid_file frpc_pid_file frpc_pid
		force_legacy_option local_ip local_ip socks_listen_ip
		force_legacy_option include_passwall include_passwall
		force_legacy_option include_passwall2 include_passwall2
		force_legacy_option extra_socks extra_socks_ports
		force_legacy_option exit_probe_host exit_probe_host socks_probe_host
		force_legacy_option exit_probe_port exit_probe_port socks_probe_port
		force_legacy_option exit_probe_timeout exit_probe_timeout socks_probe_timeout
		force_legacy_option exit_probe_protocol exit_probe_protocol socks_probe_protocol
		force_legacy_option log_file log_file
		force_legacy_option log_max_size log_max_size
		force_legacy_option log_backups log_backups
	fi

	migrate_option enabled enabled xiass_enabled nowind_enabled
	migrate_option api_url api_url xiass_api_url panel_url nowind_api_url nowind_panel_url
	migrate_option agent_token agent_token xiass_agent_token nowind_agent_token
	migrate_option interval interval poll_interval xiass_interval nowind_interval
	migrate_option frpc_bin frpc_bin frpc_binary
	migrate_option frpc_config frpc_config frpc_config_file
	migrate_option frpc_pid_file frpc_pid_file frpc_pid
	migrate_option local_ip local_ip socks_listen_ip
	migrate_option include_passwall include_passwall
	migrate_option include_passwall2 include_passwall2
	migrate_option extra_socks extra_socks extra_socks_ports
	migrate_option exit_probe_host exit_probe_host socks_probe_host
	migrate_option exit_probe_port exit_probe_port socks_probe_port
	migrate_option exit_probe_timeout exit_probe_timeout socks_probe_timeout
	migrate_option exit_probe_protocol exit_probe_protocol socks_probe_protocol
	migrate_option log_file log_file
	migrate_option log_max_size log_max_size
	migrate_option log_backups log_backups

	set_default enabled 0
	set_default api_url https://api.example.com
	set_default interval 20
	set_default frpc_bin /usr/bin/frpc
	set_default frpc_config /etc/frp/xiass-soft-router-frpc.ini
	set_default frpc_pid_file /var/run/xiass-soft-router-agent/frpc.pid
	set_default local_ip 127.0.0.1
	set_default include_passwall 1
	set_default include_passwall2 1
	set_default exit_probe_port 0
	set_default exit_probe_timeout 5
	set_default exit_probe_protocol auto
	set_default log_file /var/log/xiass-soft-router-agent.log
	set_default log_max_size 5M
	set_default log_backups 5
	set_default config_version "$SCHEMA_VERSION"
	set_default migration_state fresh

	normalize_default_path frpc_config /etc/frp/nowind-soft-router-frpc.ini /etc/frp/xiass-soft-router-frpc.ini
	normalize_default_path frpc_pid_file /var/run/nowind-soft-router-agent/frpc.pid /var/run/xiass-soft-router-agent/frpc.pid
	normalize_default_path log_file /var/log/nowind-soft-router-agent.log /var/log/xiass-soft-router-agent.log
	ensure_managed_frpc_config
	uci_cmd set "$CANONICAL_PACKAGE.main.config_version=$SCHEMA_VERSION"
	uci_cmd set "$CANONICAL_PACKAGE.main.migration_state=complete"

	uci_cmd commit "$CANONICAL_PACKAGE"
	chmod 0600 "$CANONICAL_CONFIG"
fi

copy_if_missing "$LEGACY_FRPC" "$CANONICAL_FRPC"
copy_if_missing "$LEGACY_LOG" "$CANONICAL_LOG"
mkdir -p "$(target /etc/frp)" "$(target /var/run/xiass-soft-router-agent)"
chmod 0700 "$(target /var/run/xiass-soft-router-agent)"
[ ! -f "$CANONICAL_FRPC" ] || chmod 0600 "$CANONICAL_FRPC"

log "迁移完成；旧配置和旧 FRP 文件仍保留用于兼容与回退。"
if [ "$NO_BACKUP" -eq 0 ] && [ -d "$BACKUP_DIR" ]; then
	log "迁移备份：$BACKUP_DIR"
fi
