package waf

import "ezpanel/internal/pkgmgr"

// 独立 OpenResty Lua WAF 引擎脚本（零外部 LuaRocks 依赖，仅使用 OpenResty 内建模块）。

func luaCoreScript(luaDir string) string {
	return `local cjson = require "cjson.safe"
local bit = require "bit"

local _M = {
    version = "",
    cfg = nil,
    base_dir = "` + luaDir + `",
    log_path = "` + pkgmgr.ContainerWAFEventLog() + `",
    challenge_secret = "ezwaf-default-secret",
}

local function read_file(path)
    local f = io.open(path, "r")
    if not f then
        return nil
    end
    local data = f:read("*a")
    f:close()
    return data
end

local function parse_ipv4(ip)
    if type(ip) ~= "string" then return nil end
    local a, b, c, d = ip:match("^(%d+)%.(%d+)%.(%d+)%.(%d+)$")
    if not a then return nil end
    a, b, c, d = tonumber(a), tonumber(b), tonumber(c), tonumber(d)
    if a > 255 or b > 255 or c > 255 or d > 255 then return nil end
    return bit.bor(bit.lshift(a, 24), bit.lshift(b, 16), bit.lshift(c, 8), d)
end

local function compile_ip_set(list)
    local exact = {}
    local cidrs = {}
    if type(list) ~= "table" then
        return { exact = exact, cidrs = cidrs }
    end
    for _, item in ipairs(list) do
        if type(item) == "string" and item ~= "" then
            local ip_part, prefix = item:match("^([^/]+)/(%d+)$")
            if ip_part and prefix then
                local num = parse_ipv4(ip_part)
                local bits = tonumber(prefix)
                if num and bits and bits >= 0 and bits <= 32 then
                    local mask = bits == 0 and 0 or bit.lshift(0xffffffff, 32 - bits)
                    cidrs[#cidrs + 1] = { net = bit.band(num, mask), mask = mask }
                end
            else
                exact[item] = true
            end
        end
    end
    return { exact = exact, cidrs = cidrs }
end

local function match_ip_set(set, ip, ip_num)
    if not set then return false end
    if set.exact[ip] then return true end
    if ip_num and #set.cidrs > 0 then
        for _, c in ipairs(set.cidrs) do
            if bit.band(ip_num, c.mask) == c.net then
                return true
            end
        end
    end
    return false
end

local function compile_bool_map(list)
    local m = {}
    if type(list) == "table" then
        for _, v in ipairs(list) do
            if type(v) == "string" and v ~= "" then
                m[v] = true
            end
        end
    end
    return m
end

local function compile_config(raw)
    if type(raw) ~= "table" then return nil end
    local cfg = {
        enabled = raw.enabled == true,
        mode = raw.mode or "block",
        cc = raw.cc or {},
        auto_ban = raw.autoBan or {},
        allow_ips = compile_ip_set(raw.allowIPs),
        deny_ips = compile_ip_set(raw.denyIPs),
        allow_paths = raw.allowPaths or {},
        disabled_groups = compile_bool_map(raw.disabledRuleGroups),
        block_html = raw.blockHtml or "",
        builtin_rules = raw.builtinRules or {},
        custom_rules = raw.customRules or {},
        exemptions = {},
        bans = {},
        sites = {},
        host_to_site = raw.hostToSite or {},
        secret = raw.challengeSecret or _M.challenge_secret,
    }
    if type(raw.exemptions) == "table" then
        for _, ex in ipairs(raw.exemptions) do
            local key = (ex.siteId or "") .. "|" .. (ex.ruleId or "") .. "|" .. (ex.uri or "")
            cfg.exemptions[key] = true
        end
    end
    if type(raw.bans) == "table" then
        for _, b in ipairs(raw.bans) do
            local key = (b.siteId or "") .. "|" .. (b.ip or "")
            cfg.bans[key] = b.expiresAt or 0
        end
    end
    if type(raw.sites) == "table" then
        for site_id, s in pairs(raw.sites) do
            cfg.sites[site_id] = {
                inherit = s.inheritGlobal ~= false,
                mode = s.mode or "block",
                cc_enabled = s.ccEnabled ~= false,
                cc_window = tonumber(s.ccWindowSeconds) or 60,
                cc_max = tonumber(s.ccMaxRequests) or 120,
                cc_action = s.ccAction or "block",
                allow_ips = compile_ip_set(s.allowIPs),
                deny_ips = compile_ip_set(s.denyIPs),
                allow_paths = s.allowPaths or {},
                deny_paths = s.denyPaths or {},
                disabled_groups = compile_bool_map(s.disabledGroups),
            }
        end
    end
    return cfg
end

function _M.load(force)
    local ver = read_file(_M.base_dir .. "/version.txt") or ""
    ver = ver:gsub("%s+$", "")
    if not force and _M.cfg ~= nil and ver == _M.version then
        return true
    end
    local text = read_file(_M.base_dir .. "/config.json")
    if not text or text == "" then
        return false
    end
    local parsed = cjson.decode(text)
    if not parsed then
        ngx.log(ngx.ERR, "ezwaf: failed to parse config.json")
        return false
    end
    _M.cfg = compile_config(parsed)
    _M.version = ver
    return true
end

local function match_prefix_list(list, uri)
    if type(list) ~= "table" then return false end
    for _, p in ipairs(list) do
        if type(p) == "string" and p ~= "" then
            local base = p:gsub("/+$", "")
            if base == "" or uri == base or uri:sub(1, #base + 1) == base .. "/" then
                return true
            end
        end
    end
    return false
end

local function is_exempt(cfg, site_id, rule_id, uri)
    if cfg.exemptions[site_id .. "|" .. rule_id .. "|" .. uri] then return true end
    if cfg.exemptions["|" .. rule_id .. "|" .. uri] then return true end
    return false
end

local function trunc(s, max_len)
    if type(s) ~= "string" then return "" end
    if #s > max_len then return s:sub(1, max_len) end
    return s
end

function _M.open_log()
    local file, err = io.open(_M.log_path, "a")
    if not file then
        ngx.log(ngx.ERR, "ezwaf: cannot open event log: ", err)
        return
    end
    file:setvbuf("no")
    _M.event_log = file
end

local function log_event(site_id, domain, ip, method, uri, qs, ua, group, rule_id, rule_name, action, matched, status)
    local stats = ngx.shared.ezwaf_stats
    if stats then
        stats:incr(action == "observe" and "observed" or "blocked", 1, 0)
    end
    local line = cjson.encode({
        ts = ngx.time(),
        siteId = site_id,
        domain = domain,
        ip = ip,
        method = method,
        uri = trunc(uri, 512),
        query = trunc(qs, 512),
        ua = trunc(ua, 256),
        ruleGroup = group,
        ruleId = rule_id,
        ruleName = rule_name,
        action = action,
        matched = trunc(matched, 256),
        status = status,
    })
    if line and _M.event_log then
        local ok, err = _M.event_log:write(line .. "\n")
        if not ok then
            ngx.log(ngx.ERR, "ezwaf: cannot write event log: ", err)
        end
    end
end

local default_block_html = [[<!DOCTYPE html>
<html lang="zh-CN">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>403 Forbidden - WAF</title>
<style>body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#f5f5f7;color:#1d1d1f;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0}
.card{background:#fff;border-radius:16px;padding:32px 36px;max-width:460px;box-shadow:0 8px 30px rgba(0,0,0,.08);text-align:center}
h1{margin:0 0 12px;font-size:22px}p{margin:0 0 16px;color:#6e6e73;font-size:14px;line-height:1.6}
code{background:#f2f2f7;padding:4px 8px;border-radius:6px;font-size:12px;color:#1d1d1f}</style>
</head><body><div class="card"><h1>请求已被安全防护拦截</h1><p>当前请求触发了 Web 应用防火墙安全策略，如有疑问请联系站点管理员。</p><code>%s</code></div></body></html>]]

local function respond_block(cfg, rule_id)
    ngx.status = ngx.HTTP_FORBIDDEN
    ngx.header["Content-Type"] = "text/html; charset=utf-8"
    ngx.header["X-EzWAF-Action"] = "block"
    local html = cfg.block_html
    if not html or html == "" then
        html = string.format(default_block_html, rule_id or "waf")
    end
    ngx.say(html)
    return ngx.exit(ngx.HTTP_FORBIDDEN)
end

local function verify_challenge(cfg, ip, ua)
    local cookie = ngx.var.cookie___ezwaf_pass
    if not cookie or cookie == "" then return false end
    local ts_str, sig = cookie:match("^(%d+)%.([0-9a-f]+)$")
    if not ts_str or not sig then return false end
    local ts = tonumber(ts_str)
    if not ts or ngx.time() - ts > 1800 or ts > ngx.time() + 60 then return false end
    local expected = ngx.md5(cfg.secret .. "|" .. ip .. "|" .. (ua or "") .. "|" .. ts_str)
    return sig == expected
end

local function respond_challenge(cfg, ip, ua)
    local ts_str = tostring(ngx.time())
    local sig = ngx.md5(cfg.secret .. "|" .. ip .. "|" .. (ua or "") .. "|" .. ts_str)
    local token = ts_str .. "." .. sig
    ngx.status = ngx.HTTP_SERVICE_UNAVAILABLE
    ngx.header["Content-Type"] = "text/html; charset=utf-8"
    ngx.header["Cache-Control"] = "no-store"
    ngx.say([[<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><title>安全校验中...</title></head><body><script>document.cookie="__ezwaf_pass=]] .. token .. [[; path=/; max-age=1800; SameSite=Lax";window.location.reload();</script><noscript>请启用浏览器 JavaScript 以继续访问。</noscript></body></html>]])
    return ngx.exit(ngx.HTTP_SERVICE_UNAVAILABLE)
end

local function check_custom_rule(r, ip, uri, qs, ua, method, headers, args_str)
    local val = ""
    if r.matchField == "uri" then val = uri
    elseif r.matchField == "query" then val = qs
    elseif r.matchField == "user_agent" then val = ua
    elseif r.matchField == "method" then val = method
    elseif r.matchField == "ip" then val = ip
    elseif r.matchField == "args" then val = args_str
    elseif r.matchField == "header" then
        val = headers[r.headerName or ""] or headers[string.lower(r.headerName or "")] or ""
        if type(val) == "table" then val = table.concat(val, ",") end
    end
    if type(val) ~= "string" then val = "" end
    local op = r.matchOperator
    local pat = r.matchValue or ""
    if op == "exact" then
        if val == pat then return val end
    elseif op == "prefix" then
        if val:sub(1, #pat) == pat then return val end
    elseif op == "contains" then
        if string.find(string.lower(val), string.lower(pat), 1, true) then return pat end
    elseif op == "regex" then
        local m = ngx.re.match(val, pat, "ijo")
        if m then return m[0] end
    end
    return nil
end

function _M.handle()
    local cfg = _M.cfg
    if not cfg then
        _M.load(true)
        cfg = _M.cfg
    end
    if not cfg then return end

    local host = string.lower(ngx.var.host or "")
    local site_id = ngx.var.ezwaf_site_id
    if not site_id or site_id == "" then
        site_id = cfg.host_to_site[host] or ""
    end
    local site = site_id ~= "" and cfg.sites[site_id] or nil

    -- 确定有效模式：站点若未继承全局，则按站点独立模式；否则遵循全局开关与模式
    local mode
    if site and not site.inherit then
        mode = site.mode
    else
        if not cfg.enabled then return end
        mode = cfg.mode
    end
    if mode ~= "block" and mode ~= "observe" then
        return
    end

    local ip = ngx.var.remote_addr or ""
    local ip_num = parse_ipv4(ip)
    local uri = ngx.var.uri or "/"
    local qs = ngx.var.args or ""
    local ua = ngx.var.http_user_agent or ""
    local method = ngx.req.get_method() or "GET"

    -- 1. 白名单优先放行（IP/CIDR & URL 前缀）
    if match_ip_set(cfg.allow_ips, ip, ip_num) or match_prefix_list(cfg.allow_paths, uri) then
        return
    end
    if site and (match_ip_set(site.allow_ips, ip, ip_num) or match_prefix_list(site.allow_paths, uri)) then
        return
    end

    local function trigger(group, rule_id, rule_name, rule_action, matched)
        if is_exempt(cfg, site_id, rule_id, uri) then
            return false
        end
        local effective = rule_action or mode
        if mode == "observe" then
            effective = "observe"
        end
        if effective == "challenge" then
            if verify_challenge(cfg, ip, ua) then
                return false
            end
            log_event(site_id, host, ip, method, uri, qs, ua, group, rule_id, rule_name, "challenge", matched, 503)
            respond_challenge(cfg, ip, ua)
            return true
        end
        local status = effective == "block" and 403 or 200
        log_event(site_id, host, ip, method, uri, qs, ua, group, rule_id, rule_name, effective, matched, status)
        if effective == "block" then
            respond_block(cfg, rule_id)
            return true
        end
        return false
    end

    -- 2. 静态 IP 黑名单 & 动态封禁池
    if match_ip_set(cfg.deny_ips, ip, ip_num) or (site and match_ip_set(site.deny_ips, ip, ip_num)) then
        if trigger("ip_deny", "ip-deny", "IP 黑名单", "block", ip) then return end
    end
    local now = ngx.time()
    local global_ban = cfg.bans["|" .. ip]
    local site_ban = cfg.bans[site_id .. "|" .. ip]
    if (global_ban and (global_ban == 0 or global_ban > now)) or
       (site_ban and (site_ban == 0 or site_ban > now)) then
        if trigger("ban", "dynamic-ban", "动态封禁名单", "block", ip) then return end
    end

    -- 3. 站点拦截路径
    if site and match_prefix_list(site.deny_paths, uri) then
        if trigger("path", "site-deny-path", "站点拦截路径", "block", uri) then return end
    end

    -- 4. CC 频率限制
    local cc_enabled = cfg.cc.enabled == true
    local cc_window = tonumber(cfg.cc.windowSeconds) or 60
    local cc_max = tonumber(cfg.cc.maxRequests) or 120
    local cc_action = cfg.cc.action or "block"
    if site and not site.inherit then
        cc_enabled = site.cc_enabled
        cc_window = site.cc_window
        cc_max = site.cc_max
        cc_action = site.cc_action
    end
    if cc_enabled and cc_window > 0 and cc_max > 0 then
        local limit_dict = ngx.shared.ezwaf_limit
        if limit_dict then
            local key = site_id .. ":" .. ip
            local count, err = limit_dict:incr(key, 1, 0, cc_window)
            if not err and count and count > cc_max then
                if trigger("cc", "cc-limit", "CC 频率限制", cc_action, tostring(count) .. "/" .. tostring(cc_window) .. "s") then
                    return
                end
            end
        end
    end

    local decoded_uri = ngx.unescape_uri(uri)
    local decoded_qs = qs ~= "" and ngx.unescape_uri(qs) or ""
    local args_str = decoded_qs ~= "" and (decoded_uri .. "?" .. decoded_qs) or decoded_uri
    local headers = ngx.req.get_headers(50)

    -- 5. 自定义规则（按优先级顺序）
    for _, r in ipairs(cfg.custom_rules) do
        if r.enabled and (r.siteId == "" or r.siteId == site_id) then
            local m = check_custom_rule(r, ip, decoded_uri, decoded_qs, ua, method, headers, args_str)
            if m then
                if r.action == "allow" then
                    return
                end
                if trigger("custom", r.id, r.name, r.action, m) then return end
            end
        end
    end

    -- 6. 内置安全规则库
    for _, r in ipairs(cfg.builtin_rules) do
        if r.enabled and not cfg.disabled_groups[r.group] and not (site and site.disabled_groups[r.group]) then
            local target = decoded_uri
            if r.target == "args" then target = args_str
            elseif r.target == "query" then target = decoded_qs
            elseif r.target == "user_agent" then target = ua
            end
            if target ~= "" then
                local m = ngx.re.match(target, r.pattern, "ijo")
                if m then
                    if trigger(r.group, r.id, r.name, r.action, m[0]) then return end
                end
            end
        end
    end
end

return _M
`
}

func luaInitScript(luaDir string) string {
	return `package.path = "` + luaDir + `/?.lua;" .. package.path
local ezwaf = require "core"
ezwaf.load(true)
-- 在主进程打开私有日志，降权后的工作进程沿用文件句柄。
ezwaf.open_log()
`
}

const luaWorkerScript = `local ezwaf = require "core"
local function poll(premature)
    if premature then return end
    ezwaf.load(false)
end
ngx.timer.every(2, poll)
`

const luaAccessScript = `local ezwaf = require "core"
ezwaf.handle()
`

func openRestyWAFConf(luaDir string) string {
	return `# 由 ezpanel WAF 模块自动管理，请通过面板修改。
lua_shared_dict ezwaf_limit 20m;
lua_shared_dict ezwaf_stats 10m;
init_by_lua_file ` + luaDir + `/init.lua;
init_worker_by_lua_file ` + luaDir + `/worker.lua;
access_by_lua_file ` + luaDir + `/access.lua;
`
}
