local m, s, o

m = Map("nowind_soft_router_agent", translate("XIASS 代理节点"),
	translate("将本机 PassWall SOCKS 监听上报到 XIASS，并按面板配置生成独立 FRP 映射。"))

s = m:section(NamedSection, "main", "agent", translate("Agent 设置"))
s.addremove = false
s.anonymous = false

o = s:option(Flag, "enabled", translate("启用"))
o.rmempty = false

o = s:option(Value, "panel_url", translate("XIASS 面板地址"))
o.placeholder = "https://api.example.com"
o.rmempty = false

o = s:option(Value, "agent_token", translate("Agent Token"))
o.password = true
o.rmempty = true

o = s:option(Value, "interval", translate("拉取间隔（秒）"))
o.datatype = "uinteger"
o.default = "20"

o = s:option(Value, "frpc_bin", translate("frpc 程序路径"))
o.default = "/usr/bin/frpc"

o = s:option(Value, "frpc_config", translate("生成的 frpc 配置"))
o.default = "/etc/frp/nowind-soft-router-frpc.ini"

o = s:option(Value, "frpc_pid_file", translate("frpc PID 文件"))
o.default = "/var/run/nowind-soft-router-agent/frpc.pid"

o = s:option(Value, "local_ip", translate("本机 SOCKS 监听 IP"))
o.default = "127.0.0.1"

o = s:option(Flag, "include_passwall", translate("扫描 PassWall"))
o.default = "1"

o = s:option(Flag, "include_passwall2", translate("扫描 PassWall2"))
o.default = "1"

o = s:option(Value, "extra_socks", translate("额外 SOCKS 端口"))
o.description = translate("可选，多个用英文逗号分隔，例如 1081:日本,1082:英国。")
o.rmempty = true

o = s:option(Value, "log_file", translate("日志文件"))
o.default = "/var/log/nowind-soft-router-agent.log"

o = s:option(Value, "log_max_size", translate("日志最大大小"))
o.default = "5M"
o.description = translate("支持 K/M/G，例如 5M；设为 0 表示不自动轮转。")

o = s:option(Value, "log_backups", translate("日志保留份数"))
o.datatype = "uinteger"
o.default = "5"
o.description = translate("日志达到最大大小后会轮转为 .1、.2 等旧文件。")

return m
