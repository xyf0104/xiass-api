module("luci.controller.xiass_soft_router_agent", package.seeall)

function index()
	entry({"admin", "services", "xiass-soft-router-agent"}, cbi("xiass_soft_router_agent"), _("XIASS API 软路由节点"), 60).dependent = false
	entry({"admin", "services", "xiass-soft-router-agent", "status"}, call("action_status")).leaf = true
end

function action_status()
	local http = require "luci.http"
	local sys = require "luci.sys"
	local raw = sys.exec("/usr/bin/xiass-soft-router-agent --status-json 2>/dev/null") or ""

	http.prepare_content("application/json")
	if raw:match("^%s*{") then
		http.write(raw)
	else
		http.write('{"schema_version":2,"service":"xiass-soft-router-agent","product":"XIASS API","enabled":false,"agent":{"running":false,"message":"状态命令不可用"},"xiass_api":{"ok":false,"message":"无法检测 XIASS API"},"report":{"ok":false,"message":"无法读取上报状态"},"pull":{"ok":false,"message":"无法读取拉取状态"},"exit":{"ok":false,"message":"无法读取 PassWall 出口状态"},"frpc":{"ok":false,"required":false,"process_ok":false,"control_ok":false,"process_message":"无法读取 frpc 进程状态","control_message":"无法读取 FRP 隧道状态","latest_log":"暂无 frpc 日志"}}')
	end
end
