local m, s, o

m = Map("xiass_soft_router_agent", translate("XIASS API 软路由节点"),
	translate("上报本机 PassWall SOCKS 监听，并按 XIASS API 下发配置运行独立的 FRP 客户端。"))

s = m:section(SimpleSection)
s.template = "xiass_soft_router_agent/status"

s = m:section(NamedSection, "main", "agent", translate("节点设置"))
s.addremove = false
s.anonymous = false

o = s:option(Flag, "enabled", translate("启用节点"))
o.rmempty = false

o = s:option(Value, "api_url", translate("XIASS API 地址"))
o.placeholder = "https://api.example.com"
o.rmempty = false

o = s:option(Value, "agent_token", translate("节点令牌"))
o.password = true
o.rmempty = true

o = s:option(Value, "interval", translate("同步间隔（秒）"))
o.datatype = "uinteger"
o.default = "20"

o = s:option(Value, "frpc_bin", translate("frpc 程序路径"))
o.default = "/usr/bin/frpc"

o = s:option(Value, "frpc_config", translate("frpc 配置路径"))
o.default = "/etc/frp/xiass-soft-router-frpc.ini"

o = s:option(Value, "frpc_pid_file", translate("frpc 进程号文件"))
o.default = "/var/run/xiass-soft-router-agent/frpc.pid"

o = s:option(Value, "local_ip", translate("本机 SOCKS 监听地址"))
o.default = "127.0.0.1"

o = s:option(Flag, "include_passwall", translate("扫描 PassWall"))
o.default = "1"

o = s:option(Flag, "include_passwall2", translate("扫描 PassWall2"))
o.default = "1"

o = s:option(Value, "extra_socks", translate("额外 SOCKS 端口"))
o.description = translate("多个条目用英文逗号分隔，例如 1081:日本,1082:英国。")
o.rmempty = true

o = s:option(Value, "exit_probe_host", translate("出口探测主机"))
o.description = translate("留空时使用 XIASS API 地址中的主机名。")
o.rmempty = true

o = s:option(Value, "exit_probe_port", translate("出口探测端口"))
o.datatype = "port"
o.placeholder = "443"
o.rmempty = true

o = s:option(Value, "exit_probe_timeout", translate("出口探测超时（秒）"))
o.datatype = "uinteger"
o.default = "5"

o = s:option(ListValue, "exit_probe_protocol", translate("出口探测协议"))
o.default = "auto"
o:value("auto", translate("自动"))
o:value("https", "HTTPS")
o:value("http", "HTTP")
o:value("tcp", "TCP")

o = s:option(Value, "log_file", translate("日志文件"))
o.default = "/var/log/xiass-soft-router-agent.log"

o = s:option(Value, "log_max_size", translate("日志最大大小"))
o.default = "5M"
o.description = translate("支持 K、M、G；设为 0 时不自动轮转。")

o = s:option(Value, "log_backups", translate("日志保留份数"))
o.datatype = "uinteger"
o.default = "5"

function m.on_after_commit(self)
	require("luci.sys").call("/etc/init.d/xiass-soft-router-agent restart >/dev/null 2>&1 &")
end

return m
