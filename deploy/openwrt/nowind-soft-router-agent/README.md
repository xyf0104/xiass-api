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

1. Deploy or update Nowind to v1.0.54.
2. Make sure the Docker compose file exposes the public authenticated SOCKS
   range, for example `1081-1100`.
3. Run a US frps service for raw upstream ports, for example `12081-12150`.
   See `deploy/frps-soft-router.example.toml`.
4. In Nowind admin, open `代理管理 -> 代理节点`:
   - Enable the feature.
   - `公网域名/IP`: public host such as `api.example.com`.
   - `Nowind 内部访问地址`: `host.docker.internal` for Docker, or `127.0.0.1`
     for binary/systemd deployment.
   - `FRP 服务地址`: the US frps host, such as `api.example.com`.
   - `FRP 控制端口`: frps bind port, such as `7010`.
   - `Raw FRP 端口起止`: internal upstream range, such as `12081-12150`.
   - `公网 SOCKS 端口起止`: authenticated public range, such as `1081-1100`.
   - Set a default SOCKS username and password.
   - Set the FRP token.
5. Create an OpenWrt Agent in the panel and copy its token.

The raw FRP ports should not be opened to the public Internet. Public users
should use only Nowind's authenticated SOCKS ports, for example:

```txt
socks5://username:password@api.example.com:1081
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
Services -> NoWind Proxy Agent
```

Set:

- Enabled: on
- Panel URL: `https://api.example.com`
- Agent Token: token copied from Nowind admin
- frpc binary: usually `/usr/bin/frpc`
- frpc config: keep `/etc/frp/nowind-soft-router-frpc.ini`

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
