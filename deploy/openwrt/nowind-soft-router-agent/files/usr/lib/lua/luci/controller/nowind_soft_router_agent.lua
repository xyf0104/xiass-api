module("luci.controller.nowind_soft_router_agent", package.seeall)

function index()
	entry({"admin", "services", "nowind-soft-router-agent"}, cbi("nowind_soft_router_agent"), _("XIASS 代理节点"), 60).dependent = false
end
