module("luci.controller.nowind_soft_router_agent", package.seeall)

-- XIASS_COMPAT_WRAPPER=1
-- 保留旧书签地址，但不再创建旧名称菜单项。
function index()
	entry({"admin", "services", "nowind-soft-router-agent"}, call("legacy_redirect"), nil).leaf = true
end

function legacy_redirect()
	local dispatcher = require "luci.dispatcher"
	local http = require "luci.http"
	http.redirect(dispatcher.build_url("admin", "services", "xiass-soft-router-agent"))
end
