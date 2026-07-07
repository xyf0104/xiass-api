local m, s, o

m = Map("nowind_soft_router_agent", translate("NoWind Proxy Agent"),
	translate("Reports local PassWall SOCKS listeners to Nowind and applies independent FRP mappings."))

s = m:section(NamedSection, "main", "agent", translate("Agent Settings"))
s.addremove = false
s.anonymous = false

o = s:option(Flag, "enabled", translate("Enable"))
o.rmempty = false

o = s:option(Value, "panel_url", translate("Panel URL"))
o.placeholder = "https://api.example.com"
o.rmempty = false

o = s:option(Value, "agent_token", translate("Agent Token"))
o.password = true
o.rmempty = true

o = s:option(Value, "interval", translate("Poll Interval Seconds"))
o.datatype = "uinteger"
o.default = "20"

o = s:option(Value, "frpc_bin", translate("frpc Binary"))
o.default = "/usr/bin/frpc"

o = s:option(Value, "frpc_config", translate("Generated frpc Config"))
o.default = "/etc/frp/nowind-soft-router-frpc.ini"

o = s:option(Value, "frpc_pid_file", translate("frpc PID File"))
o.default = "/var/run/nowind-soft-router-agent/frpc.pid"

o = s:option(Value, "local_ip", translate("Local SOCKS Listen IP"))
o.default = "127.0.0.1"

o = s:option(Flag, "include_passwall", translate("Scan PassWall"))
o.default = "1"

o = s:option(Flag, "include_passwall2", translate("Scan PassWall2"))
o.default = "1"

o = s:option(Value, "extra_socks", translate("Extra SOCKS Ports"))
o.description = translate("Optional comma-separated entries like 1081:Japan,1082:UK.")
o.rmempty = true

o = s:option(Value, "log_file", translate("Log File"))
o.default = "/var/log/nowind-soft-router-agent.log"

return m

