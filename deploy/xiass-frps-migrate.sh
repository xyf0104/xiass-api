#!/usr/bin/env bash
# Migrate the historical XIASS soft-router frps systemd service without
# changing its token, port ranges, or existing OpenWrt clients.

set -Eeuo pipefail

SERVICE_NAME="${SERVICE_NAME:-xiass-frps-soft-router}"
CONFIG_DIR="${CONFIG_DIR:-/etc/xiass-frps-soft-router}"
FRPS_BIN="${FRPS_BIN:-/usr/local/bin/xiass-frps-soft-router}"
ALLOWED_SOURCE_SERVICE_NAMES="frps-nowind-soft-router frps-us"
BACKUP_ROOT="${BACKUP_ROOT:-/root/xiass-frps-backups}"
SOURCE_SERVICE="${SOURCE_SERVICE:-}"
SOURCE_CONFIG="${SOURCE_CONFIG:-}"
SOURCE_BIN="${SOURCE_BIN:-}"
RUN_USER="${RUN_USER:-}"
RUN_GROUP="${RUN_GROUP:-}"
BACKUP_DIR=""
MIGRATION_COMPLETE=false
SOURCE_WAS_ACTIVE=false
SOURCE_WAS_ENABLED=""

log() { printf '[XIASS FRP] %s\n' "$*"; }
die() { printf '[XIASS FRP] 错误：%s\n' "$*" >&2; exit 1; }

restore_legacy() {
    local status=$?
    trap - EXIT INT TERM
    if [ "$status" -ne 0 ] && [ -n "$SOURCE_SERVICE" ] && ! "$MIGRATION_COMPLETE"; then
        log "迁移未完成，正在恢复历史 FRP 服务 $SOURCE_SERVICE..."
        systemctl stop "$SERVICE_NAME" >/dev/null 2>&1 || true
        systemctl disable "$SERVICE_NAME" >/dev/null 2>&1 || true
        case "$SOURCE_WAS_ENABLED" in
            enabled|enabled-runtime|linked|linked-runtime)
                systemctl enable "$SOURCE_SERVICE" >/dev/null 2>&1 || true
                ;;
            *)
                systemctl disable "$SOURCE_SERVICE" >/dev/null 2>&1 || true
                ;;
        esac
        if "$SOURCE_WAS_ACTIVE"; then
            if ! systemctl start "$SOURCE_SERVICE" >/dev/null 2>&1; then
                log "错误：无法自动恢复历史 FRP 服务 $SOURCE_SERVICE，请立即检查 $BACKUP_DIR"
            elif ! systemctl is-active --quiet "$SOURCE_SERVICE"; then
                log "错误：历史 FRP 服务 $SOURCE_SERVICE 未恢复为运行状态，请立即检查 $BACKUP_DIR"
            fi
        fi
    fi
    exit "$status"
}

require_root_and_systemd() {
    [ "$(id -u)" -eq 0 ] || die "请使用 root 或 sudo 运行。"
    command -v systemctl >/dev/null 2>&1 || die "当前系统没有 systemd，无法自动迁移。"
    command -v install >/dev/null 2>&1 || die "缺少 install 命令。"
}

select_source_service() {
    local candidate active=() allowed=false
    if [ -n "$SOURCE_SERVICE" ]; then
		for candidate in $ALLOWED_SOURCE_SERVICE_NAMES; do
			[ "$SOURCE_SERVICE" = "$candidate" ] && allowed=true
		done
		"$allowed" || die "SOURCE_SERVICE 只能是 XIASS 历史服务：$ALLOWED_SOURCE_SERVICE_NAMES"
        systemctl is-active --quiet "$SOURCE_SERVICE" || die "指定的服务未运行：$SOURCE_SERVICE"
        return
    fi
    for candidate in $ALLOWED_SOURCE_SERVICE_NAMES; do
        if systemctl is-active --quiet "$candidate" 2>/dev/null; then
            active+=("$candidate")
        fi
    done
    if [ "${#active[@]}" -eq 0 ]; then
        die "未发现运行中的历史 FRP 服务；请设置 SOURCE_SERVICE 后重试。"
    fi
    if [ "${#active[@]}" -gt 1 ]; then
        die "发现多个运行中的历史 FRP 服务；请显式设置 SOURCE_SERVICE。"
    fi
    SOURCE_SERVICE="${active[0]}"
}

read_source_paths() {
    local unit exec_line
    unit=$(systemctl show --property FragmentPath --value "$SOURCE_SERVICE" 2>/dev/null || true)
    [ -n "$unit" ] && [ -f "$unit" ] || die "无法读取历史 systemd 单元：$SOURCE_SERVICE"
    exec_line=$(awk -F= '/^ExecStart=/{print $2; exit}' "$unit")
    [ -n "$SOURCE_CONFIG" ] || SOURCE_CONFIG=$(printf '%s\n' "$exec_line" | sed -n 's/.*[[:space:]]-c[[:space:]]\([^[:space:]]*\).*/\1/p')
    [ -n "$SOURCE_BIN" ] || SOURCE_BIN=$(printf '%s\n' "$exec_line" | awk '{print $1}')
    [ -n "$SOURCE_CONFIG" ] && [ -f "$SOURCE_CONFIG" ] || die "无法从 $unit 识别历史 FRP 配置；请设置 SOURCE_CONFIG。"
    [ -n "$SOURCE_BIN" ] && [ -x "$SOURCE_BIN" ] || die "无法从 $unit 识别历史 frps 二进制；请设置 SOURCE_BIN。"
    [ -n "$RUN_USER" ] || RUN_USER=$(systemctl show --property User --value "$SOURCE_SERVICE" 2>/dev/null || true)
    [ -n "$RUN_GROUP" ] || RUN_GROUP=$(systemctl show --property Group --value "$SOURCE_SERVICE" 2>/dev/null || true)
    RUN_USER="${RUN_USER:-root}"
    RUN_GROUP="${RUN_GROUP:-$RUN_USER}"
}

backup_source() {
    local unit
    BACKUP_DIR="$BACKUP_ROOT/$(date +%Y%m%d-%H%M%S)"
    mkdir -p "$BACKUP_DIR"
    chmod 700 "$BACKUP_ROOT" "$BACKUP_DIR"
    unit=$(systemctl show --property FragmentPath --value "$SOURCE_SERVICE")
    cp -a "$unit" "$BACKUP_DIR/$(basename "$unit")"
    cp -a "$SOURCE_CONFIG" "$BACKUP_DIR/$(basename "$SOURCE_CONFIG")"
    cp -a "$SOURCE_BIN" "$BACKUP_DIR/$(basename "$SOURCE_BIN")"
    SOURCE_WAS_ENABLED=$(systemctl is-enabled "$SOURCE_SERVICE" 2>/dev/null || true)
    if systemctl is-active --quiet "$SOURCE_SERVICE"; then
        SOURCE_WAS_ACTIVE=true
    fi
    printf 'source_service=%s\nsource_config=%s\nsource_bin=%s\n' \
        "$SOURCE_SERVICE" "$SOURCE_CONFIG" "$SOURCE_BIN" > "$BACKUP_DIR/manifest"
    chmod 600 "$BACKUP_DIR/manifest" "$BACKUP_DIR/$(basename "$SOURCE_CONFIG")"
}

write_xiass_unit() {
    local config_file="$CONFIG_DIR/$(basename "$SOURCE_CONFIG")"
    cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=XIASS API FRP server for soft-router proxy nodes
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${RUN_USER}
Group=${RUN_GROUP}
ExecStart=${FRPS_BIN} -c ${config_file}
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
EOF
}

main() {
    require_root_and_systemd
    if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
        log "$SERVICE_NAME 已在运行，不做重复迁移。"
        return
    fi
    select_source_service
    read_source_paths
    backup_source
    trap restore_legacy EXIT INT TERM

    log "已备份历史服务到 $BACKUP_DIR"
    log "停止历史服务 $SOURCE_SERVICE 并创建 XIASS API 服务..."
    systemctl stop "$SOURCE_SERVICE"
    mkdir -p "$CONFIG_DIR"
    cp -a "$SOURCE_CONFIG" "$CONFIG_DIR/$(basename "$SOURCE_CONFIG")"
    install -m 0755 "$SOURCE_BIN" "$FRPS_BIN"
    write_xiass_unit

    systemctl daemon-reload
    systemctl enable "$SERVICE_NAME" >/dev/null
    systemctl restart "$SERVICE_NAME"
    systemctl is-active --quiet "$SERVICE_NAME" || die "$SERVICE_NAME 未能启动"
    systemctl disable "$SOURCE_SERVICE" >/dev/null || die "无法禁用历史服务 $SOURCE_SERVICE"

    MIGRATION_COMPLETE=true
    trap - EXIT INT TERM
    log "迁移完成：$SOURCE_SERVICE -> $SERVICE_NAME"
    log "端口、令牌和原配置内容已保留；历史文件留在 $BACKUP_DIR。"
}

main "$@"
