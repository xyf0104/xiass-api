module("luci.controller.nowind_soft_router_agent", package.seeall)

function index()
	entry({"admin", "services", "nowind-soft-router-agent"}, cbi("nowind_soft_router_agent"), _("NoWind Proxy Agent"), 60).dependent = false
end

