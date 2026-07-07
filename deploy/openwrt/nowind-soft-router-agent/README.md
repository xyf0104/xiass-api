# Nowind Soft Router Agent for OpenWrt

This package lets an OpenWrt router report PassWall SOCKS listeners to the
Nowind admin panel and pull desired FRP mappings from it.

It uses the same Python-agent approach as the previously validated prototype:
OpenWrt discovers PassWall SOCKS listeners locally, writes an independent frpc
config, and actively connects back to the US frps service.

It is intentionally independent from any existing FRP/LuCI setup. It writes only
these paths by default:

- `/etc/config/nowind_soft_router_agent`
- `/usr/bin/nowind-soft-router-agent`
- `/etc/init.d/nowind-soft-router-agent`
- `/etc/frp/nowind-soft-router-frpc.ini`
- `/var/run/nowind-soft-router-agent/`

## Server-Side Flow

1. Deploy or update Nowind to v1.0.56 or newer.
2. In Nowind admin, open `代理管理 -> 代理节点`:
   - `公网域名/IP`: public host such as `api.example.com`.
   - `Nowind 内部访问地址`: `host.docker.internal` for Docker, or `127.0.0.1`
     for binary/systemd deployment.
   - `FRP 服务地址`: the US frps host, such as `api.example.com`.
   - `FRP 控制端口`: frps bind port, such as `7010`.
   - `Raw FRP 端口起止`: internal upstream range, such as `12083-12150`.
   - `公网 SOCKS 端口起止`: authenticated public range, such as `1101-1120`.
   - Set a default SOCKS username and password.
   - Set the FRP token.
3. Click `安装 FRP`. The panel installs an independent server-side frps service,
   opens the relevant firewall ports when ufw/firewalld is active, and updates
   the deployment `.env` ranges.
4. Recreate the Nowind container when prompted so Docker publishes the selected
   public SOCKS range.
5. Create an OpenWrt Agent in the panel and copy its token.

The raw FRP ports should not be opened to the public Internet. Public users
should use only Nowind's authenticated SOCKS ports, for example:

```txt
socks5://username:password@api.example.com:1101
```

## OpenWrt Install

Copy this directory to OpenWrt, then run:

```sh
cd /path/to/nowind-soft-router-agent
sh install.sh
```

Install dependencies if they are missing:

```sh
opkg update
opkg install python3 frpc luci-compat
```

If `frpc` is already provided by another LuCI FRP package, you can reuse that
binary path. This agent starts its own frpc process with its own config file.

## Configure In LuCI

Open:

```txt
Services -> NoWind 代理节点
```

Set:

- Enabled: on
- Panel URL: `https://api.example.com`
- Agent Token: token copied from Nowind admin
- frpc binary: usually `/usr/bin/frpc`
- frpc config: keep `/etc/frp/nowind-soft-router-frpc.ini`
- Extra SOCKS ports: optional. Leave empty when PassWall/PassWall2 scanning
  already finds your SOCKS listeners. Use comma-separated values only for
  custom local listeners, for example `1081:Japan,1082:UK`.
- Log file: use a persistent disk path when available, for example
  `/mnt/sdb1/nowind-soft-router-agent/nowind-soft-router-agent.log`.
- Log max size: default `5M`. Set `0` to disable automatic rotation.
- Log backups: default `5`, keeping rotated files such as `.1`, `.2`, etc.

Save/apply, then start:

```sh
/etc/init.d/nowind-soft-router-agent enable
/etc/init.d/nowind-soft-router-agent restart
```

## CLI Check

```sh
logread -f | grep nowind-soft-router
netstat -lntp | grep -E '1081|1082'
ps | grep nowind-soft-router-agent
ps | grep frpc
/usr/bin/nowind-soft-router-agent --once
```

After the agent reports, the Nowind panel will show discovered PassWall SOCKS
listeners. Configure a mapping in the panel, then the agent will rewrite and
restart its independent frpc process automatically.
