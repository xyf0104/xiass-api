#!/bin/sh
set -eu

BASE_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

install_file() {
  src="$1"
  dst="$2"
  mode="$3"
  mkdir -p "$(dirname "$dst")"
  cp "$BASE_DIR/files/$src" "$dst"
  chmod "$mode" "$dst"
}

install_file "usr/bin/nowind-soft-router-agent" "/usr/bin/nowind-soft-router-agent" 0755
install_file "etc/init.d/nowind-soft-router-agent" "/etc/init.d/nowind-soft-router-agent" 0755
install_file "usr/lib/lua/luci/controller/nowind_soft_router_agent.lua" "/usr/lib/lua/luci/controller/nowind_soft_router_agent.lua" 0644
install_file "usr/lib/lua/luci/model/cbi/nowind_soft_router_agent.lua" "/usr/lib/lua/luci/model/cbi/nowind_soft_router_agent.lua" 0644

if [ ! -f /etc/config/nowind_soft_router_agent ]; then
  install_file "etc/config/nowind_soft_router_agent" "/etc/config/nowind_soft_router_agent" 0600
fi

mkdir -p /etc/frp /var/run/nowind-soft-router-agent
chmod 700 /var/run/nowind-soft-router-agent

/etc/init.d/nowind-soft-router-agent enable >/dev/null 2>&1 || true

echo "Installed nowind-soft-router-agent."
echo "Requires python3 and frpc. Install with: opkg update && opkg install python3 frpc luci-compat"
echo "Configure it in LuCI: Services -> NoWind Proxy Agent"
echo "Or edit /etc/config/nowind_soft_router_agent, then run:"
echo "  /etc/init.d/nowind-soft-router-agent restart"
