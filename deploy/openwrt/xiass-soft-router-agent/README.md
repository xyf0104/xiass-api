# XIASS API 软路由 Agent（OpenWrt）

`xiass-soft-router-agent` 会发现本机 PassWall/PassWall2 SOCKS 监听，上报到 XIASS API，并根据面板配置运行一套独立的 `frpc`。它不会修改其他 FRP 服务的配置，也不会使用 `killall`、`pkill` 等方式停止系统中的其他 `frpc`。

## 默认路径

- UCI：`/etc/config/xiass_soft_router_agent`
- 命令：`/usr/bin/xiass-soft-router-agent`
- 服务：`/etc/init.d/xiass-soft-router-agent`
- FRP 配置：`/etc/frp/xiass-soft-router-frpc.ini`
- 运行状态：`/var/run/xiass-soft-router-agent/status.json`
- 日志：`/var/log/xiass-soft-router-agent.log`

旧的 `nowind_*` UCI 包、旧命令、旧服务名、旧 LuCI 地址和旧默认 FRP 文件均受兼容或迁移处理。新安装和升级后的主入口只显示 XIASS API 名称。

## 安装或原地升级

将本目录复制到 OpenWrt 后执行：

```sh
cd /path/to/xiass-soft-router-agent
sh install.sh
```

依赖缺失时安装：

```sh
opkg update
opkg install python3 python3-openssl python3-urllib frpc luci-compat
```

`python3-openssl` 在部分软件源中名为 `python3-ssl`。安装脚本会预检 `import ssl, urllib.request`；缺失时不会切换正在运行的旧节点程序，也不会启动新服务。

安装脚本会先备份新旧配置和程序，再使用带进程归属校验的新 Agent 停止旧 Agent 自己的 `frpc`，最后迁移配置并启动新服务。现有 HK FRP 或其他 FRP 实例不会被按进程名停止，也不会被改写配置。

LuCI 入口：`服务 -> XIASS API 软路由节点`。

## 实时状态

LuCI 同时显示：

- XIASS API 面板是否可达；
- Agent 最近一次上报是否成功；
- Agent 最近一次配置拉取是否成功；
- Agent 所属 `frpc` 进程是否正常；
- FRP 控制端口、登录和 Raw 映射隧道是否正常；
- 每个 PassWall SOCKS 监听能否通过外部上游建立连接；
- `frpc` 最新一条日志和最近一次上报成功时间。

FRP 隧道与 PassWall 出口分别显示，因此可以出现“隧道绿色、出口红色”。出口探测使用 Agent 内的 Python SOCKS5 与 `socket.create_connection`，再在该连接中完成 HTTP/HTTPS 请求并读取响应；不依赖 BusyBox `nc -z`。绿色表示当前正常，红色表示失败或无法实时确认。状态 JSON、LuCI 页面和日志不会输出节点令牌或 FRP 令牌。

## 兼容策略

- 首次升级时复制 `/etc/config/nowind_soft_router_agent`，并将旧字段别名迁移到新字段。
- 若已有 canonical UCI 仍为出厂默认值，而旧 UCI 含真实值，会优先迁移旧值；已自定义的 canonical UCI 不会被覆盖。
- 只有旧默认路径会切换到新默认路径；自定义日志和 PID 路径保持不变，非 Agent 管理的自定义 FRP 配置不会被新 Agent 接管。
- 新 Agent 只接受 `/etc/frp/xiass-soft-router-frpc.ini` 与 `/etc/frp/nowind-soft-router-frpc.ini`；指向 HK 或其他 FRP 配置的值会被拒绝，旧 UCI 文件保持原样供回退审计。
- 旧 FRP 配置会复制而不是移动，便于回退；后续面板同步会生成 `xiass-*` 代理名称。
- `/usr/bin/nowind-soft-router-agent` 与 `/etc/init.d/nowind-soft-router-agent` 保留为兼容转发入口。
- 旧 LuCI URL 会隐藏跳转到新页面，不再生成旧名称菜单项。
- Agent 同时解析新旧 UCI 包、字段别名和面板 JSON 字段别名。
- 已运行的旧 Agent 会保持服务，直到候选 XIASS Agent 的面板、上报、拉取及必要的 FRPS control socket 检查全部通过。

可单独重新执行迁移：

```sh
sh migrate.sh
```

## 卸载

默认卸载会保留配置、日志和 FRP 配置：

```sh
sh uninstall.sh
```

删除新名称数据：

```sh
sh uninstall.sh --purge
```

只有明确需要同时删除旧名称配置时才执行：

```sh
sh uninstall.sh --purge-legacy
```

## 检查命令

```sh
/usr/bin/xiass-soft-router-agent --version
/usr/bin/xiass-soft-router-agent --once
/usr/bin/xiass-soft-router-agent --status-json
/usr/bin/xiass-soft-router-agent --health-check
/etc/init.d/xiass-soft-router-agent restart
logread -e xiass-soft-router-agent
ps | grep '[f]rpc'
```

源码目录内静态检查与测试：

```sh
sh tests/run.sh
```
