#!/bin/sh
set -eu

BASE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
BACKUP_DIR="/root/nowind-soft-router-agent-backup-$(date +%Y%m%d%H%M%S)"

backup_path() {
  path="$1"
  [ -e "$path" ] || return 0
  mkdir -p "$BACKUP_DIR$(dirname "$path")"
  cp -a "$path" "$BACKUP_DIR$path"
}

install_file() {
  src="$1"
  dst="$2"
  mode="$3"
  mkdir -p "$(dirname "$dst")"
  backup_path "$dst"
  cp "$BASE_DIR/files/$src" "$dst"
  chmod "$mode" "$dst"
}

backup_path "/etc/config/nowind_soft_router_agent"
backup_path "/etc/frp/nowind-soft-router-frpc.ini"
backup_path "/var/log/nowind-soft-router-agent.log"
backup_path "/mnt/sdb1/nowind-soft-router-agent/nowind-soft-router-agent.log"

install_file "usr/bin/nowind-soft-router-agent" "/usr/bin/nowind-soft-router-agent" 0755
install_file "etc/init.d/nowind-soft-router-agent" "/etc/init.d/nowind-soft-router-agent" 0755
install_file "usr/lib/lua/luci/controller/nowind_soft_router_agent.lua" "/usr/lib/lua/luci/controller/nowind_soft_router_agent.lua" 0644
install_file "usr/lib/lua/luci/model/cbi/nowind_soft_router_agent.lua" "/usr/lib/lua/luci/model/cbi/nowind_soft_router_agent.lua" 0644

if [ ! -f /etc/config/nowind_soft_router_agent ]; then
  mkdir -p /etc/config
  cp "$BASE_DIR/files/etc/config/nowind_soft_router_agent" "/etc/config/nowind_soft_router_agent"
  chmod 0600 "/etc/config/nowind_soft_router_agent"
else
  echo "Keep existing config: /etc/config/nowind_soft_router_agent"
fi

mkdir -p /etc/frp /var/run/nowind-soft-router-agent
chmod 700 /var/run/nowind-soft-router-agent

/etc/init.d/nowind-soft-router-agent enable >/dev/null 2>&1 || true

echo "Installed nowind-soft-router-agent."
if [ -d "$BACKUP_DIR" ]; then
  echo "Backup saved to: $BACKUP_DIR"
fi
echo "Requires python3 and frpc. Install with: opkg update && opkg install python3 frpc luci-compat"
echo "Configure it in LuCI: Services -> XIASS 代理节点"
echo "Or edit /etc/config/nowind_soft_router_agent, then run:"
echo "  /etc/init.d/nowind-soft-router-agent restart"
