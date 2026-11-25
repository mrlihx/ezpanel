package auth

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// classifyAudit maps raw route + HTTP method into a clean module category and human-readable Chinese detail.
// Returns skip=true when the route should not be recorded in operation logs.
func classifyAudit(c *gin.Context, module, method, action, name string) (auditModule, target string, skip bool) {
	path := c.FullPath()
	auditModule = module

	// 1. 计划任务：只记录 新增、修改、删除、执行
	if module == "cron" {
		switch {
		case method == "POST" && (path == "/api/crons" || path == "/api/crons/"):
			return "cron", withBracket("新增计划任务", name), false
		case method == "PUT" && path == "/api/crons/:id":
			return "cron", withBracket("修改计划任务", firstNonEmpty(name, c.Param("id"))), false
		case method == "DELETE" && path == "/api/crons/:id":
			return "cron", withBracket("删除计划任务", firstNonEmpty(name, c.Param("id"))), false
		case method == "POST" && path == "/api/crons/:id/run":
			return "cron", withBracket("执行计划任务", firstNonEmpty(name, c.Param("id"))), false
		default:
			return "cron", "", true
		}
	}

	// 2. 应用配置：将所有应用的设置、性能调整、端口、远程访问、扩展等归类为 app_config
	if isAppConfigRoute(module, path) {
		auditModule = "app_config"
		return auditModule, formatAppConfigTarget(c, method, path, name), false
	}

	// 3. 应用管理
	if module == "software" {
		appName := firstNonEmpty(name, c.Param("id"))
		switch {
		case strings.HasSuffix(path, "/install"):
			return "software", withBracket("安装应用", appName), false
		case strings.HasSuffix(path, "/uninstall"):
			return "software", withBracket("卸载应用", appName), false
		case strings.HasSuffix(path, "/update"):
			return "software", withBracket("更新应用", appName), false
		case strings.HasSuffix(path, "/service") || strings.Contains(path, "/instances/:instance/action"):
			return "software", withBracket("控制应用服务", appName), false
		case strings.HasSuffix(path, "/check-updates"):
			return "software", "检查应用更新", false
		default:
			return "software", withBracket(defaultVerb(method)+"应用", appName), false
		}
	}

	// 4. 网站管理
	if module == "website" {
		site := firstNonEmpty(name, c.Param("id"))
		switch {
		case method == "POST" && path == "/api/websites":
			return "website", withBracket("创建网站", site), false
		case method == "DELETE" && path == "/api/websites/:id":
			return "website", withBracket("删除网站", site), false
		case strings.HasSuffix(path, "/remark"):
			return "website", withBracket("修改网站备注", site), false
		case strings.HasSuffix(path, "/ssl") || strings.Contains(path, "/ssl/"):
			return "website", withBracket("配置网站 SSL", site), false
		case strings.HasSuffix(path, "/rewrite"):
			return "website", withBracket("修改伪静态规则", site), false
		case strings.HasSuffix(path, "/redirect"):
			return "website", withBracket("修改网站重定向", site), false
		case strings.HasSuffix(path, "/proxy") || strings.Contains(path, "/proxy/"):
			return "website", withBracket("修改反向代理", site), false
		case strings.HasSuffix(path, "/php"):
			return "website", withBracket("切换网站 PHP 版本", site), false
		case strings.HasSuffix(path, "/waf"):
			return "waf", withBracket("修改站点 WAF 配置", site), false
		case strings.HasSuffix(path, "/start") || strings.HasSuffix(path, "/stop") || strings.HasSuffix(path, "/restart"):
			return "website", withBracket("控制网站运行状态", site), false
		default:
			if action != "" && name != "" {
				return "website", action + "[" + name + "]", false
			}
			return "website", withBracket(defaultVerb(method)+"网站配置", site), false
		}
	}

	// 5. 数据库管理
	if module == "database" {
		dbName := firstNonEmpty(name, c.Param("name"), c.Param("table"), c.Param("id"))
		switch {
		case strings.Contains(path, "/sqlite/files") && strings.Contains(path, "/backups"):
			if method == "POST" && strings.HasSuffix(path, "/restore") {
				return "database", withBracket("恢复 SQLite 备份", c.Param("filename")), false
			}
			if method == "DELETE" {
				return "database", withBracket("删除 SQLite 备份", c.Param("filename")), false
			}
			return "database", withBracket("备份 SQLite 数据库", dbName), false
		case strings.Contains(path, "/sqlite/files") && strings.HasSuffix(path, "/rows"):
			return "database", withBracket(defaultVerb(method)+" SQLite 数据行", c.Param("table")), false
		case strings.Contains(path, "/sqlite/files") && strings.HasSuffix(path, "/query"):
			return "database", "执行 SQLite SQL 查询", false
		case path == "/api/db/sqlite/files" && method == "POST":
			return "database", withBracket("添加 SQLite 数据库", dbName), false
		case path == "/api/db/sqlite/files/:id" && method == "DELETE":
			return "database", withBracket("移除 SQLite 数据库", dbName), false
		case strings.Contains(path, "/backups"):
			if method == "DELETE" {
				return "database", withBracket("删除数据库备份", firstNonEmpty(c.Param("filename"), dbName)), false
			}
			return "database", withBracket("备份数据库", dbName), false
		case strings.HasSuffix(path, "/password/reveal"):
			return "database", "", true
		case strings.Contains(path, "/password"):
			return "database", withBracket("修改数据库密码", dbName), false
		case strings.HasSuffix(path, "/metadata") || strings.HasSuffix(path, "/remark"):
			return "database", withBracket("修改数据库备注", dbName), false
		case strings.HasSuffix(path, "/access"):
			return "database", withBracket("修改数据库访问权限", dbName), false
		case strings.HasSuffix(path, "/query"):
			return "database", withBracket("执行 SQL 查询", dbName), false
		case strings.HasSuffix(path, "/databases") && method == "POST":
			return "database", withBracket("新建数据库", dbName), false
		case strings.HasSuffix(path, "/databases/:name") && method == "DELETE":
			return "database", withBracket("删除数据库", dbName), false
		case strings.Contains(path, "/servers"):
			return "database", withBracket(defaultVerb(method)+"远程数据库配置", dbName), false
		default:
			if action != "" && name != "" {
				return "database", action + "[" + name + "]", false
			}
			return "database", withBracket(defaultVerb(method)+"数据库", dbName), false
		}
	}

	// 6. 文件管理
	if module == "file" {
		switch {
		case strings.HasSuffix(path, "/create"):
			return "file", withBracket("新建文件/目录", name), false
		case strings.HasSuffix(path, "/content"):
			return "file", withBracket("编辑保存文件", name), false
		case strings.HasSuffix(path, "/rename"):
			return "file", withBracket("重命名文件", name), false
		case strings.HasSuffix(path, "/move"):
			return "file", withBracket("移动文件", name), false
		case strings.HasSuffix(path, "/copy"):
			return "file", withBracket("复制文件", name), false
		case strings.HasSuffix(path, "/delete") || strings.Contains(path, "/trash"):
			return "file", withBracket("删除/清理文件", name), false
		case strings.HasSuffix(path, "/compress"):
			return "file", withBracket("压缩文件", name), false
		case strings.HasSuffix(path, "/extract"):
			return "file", withBracket("解压文件", name), false
		case strings.HasSuffix(path, "/chmod") || strings.HasSuffix(path, "/chown") || strings.Contains(path, "/permissions"):
			return "file", withBracket("修改文件权限", name), false
		case strings.HasSuffix(path, "/remark"):
			return "file", withBracket("修改文件备注", name), false
		case strings.Contains(path, "/upload"):
			return "file", withBracket("上传文件", name), false
		case strings.HasSuffix(path, "/download"):
			return "file", withBracket("下载文件", name), false
		default:
			return "file", withBracket(defaultVerb(method)+"文件", name), false
		}
	}

	// 7. 面板设置
	if module == "settings" {
		return "settings", firstNonEmpty(withBracket(action, name), "修改面板设置"), false
	}

	// 8. WAF
	if module == "waf" {
		switch {
		case strings.HasSuffix(path, "/config"):
			return "waf", "修改 WAF 全局配置", false
		case strings.Contains(path, "/rules"):
			return "waf", withBracket(defaultVerb(method)+" WAF 规则", firstNonEmpty(name, c.Param("id"))), false
		case strings.Contains(path, "/exemptions"):
			return "waf", withBracket(defaultVerb(method)+" WAF 白名单", firstNonEmpty(name, c.Param("id"))), false
		case strings.Contains(path, "/sites"):
			return "waf", withBracket("修改站点 WAF 策略", firstNonEmpty(name, c.Param("siteId"), c.Param("id"))), false
		default:
			return "waf", withBracket(defaultVerb(method)+" WAF 配置", name), false
		}
	}

	// 9. Docker
	if module == "docker" {
		targetID := firstNonEmpty(name, c.Param("id"), c.Param("name"))
		switch {
		case strings.Contains(path, "/containers") && strings.HasSuffix(path, "/start"):
			return "docker", withBracket("启动容器", targetID), false
		case strings.Contains(path, "/containers") && strings.HasSuffix(path, "/stop"):
			return "docker", withBracket("停止容器", targetID), false
		case strings.Contains(path, "/containers") && strings.HasSuffix(path, "/restart"):
			return "docker", withBracket("重启容器", targetID), false
		case strings.Contains(path, "/containers") && method == "DELETE":
			return "docker", withBracket("删除容器", targetID), false
		case strings.Contains(path, "/containers") && method == "POST":
			return "docker", withBracket("创建容器", targetID), false
		case strings.Contains(path, "/images") && strings.HasSuffix(path, "/pull"):
			return "docker", withBracket("拉取镜像", targetID), false
		case strings.Contains(path, "/images") && method == "DELETE":
			return "docker", withBracket("删除镜像", targetID), false
		case strings.Contains(path, "/networks"):
			return "docker", withBracket(defaultVerb(method)+" Docker 网络", targetID), false
		case strings.Contains(path, "/volumes"):
			return "docker", withBracket(defaultVerb(method)+" Docker 存储卷", targetID), false
		case strings.Contains(path, "/compose"):
			return "docker", withBracket(defaultVerb(method)+" Compose 编排", targetID), false
		default:
			return "docker", withBracket(defaultVerb(method)+" Docker 资源", targetID), false
		}
	}

	// 10. 安全管理
	if module == "security" {
		targetID := firstNonEmpty(name, c.Param("id"))
		switch {
		case strings.Contains(path, "/firewall"):
			return "security", withBracket(defaultVerb(method)+"防火墙规则", targetID), false
		case strings.Contains(path, "/ssh"):
			return "security", "修改 SSH 安全设置", false
		case strings.Contains(path, "/fail2ban"):
			return "security", withBracket(defaultVerb(method)+" Fail2ban 规则", targetID), false
		default:
			return "security", withBracket(defaultVerb(method)+"安全设置", targetID), false
		}
	}

	// 11. 证书管理
	if module == "certificate" {
		certName := firstNonEmpty(name, c.Param("id"))
		switch {
		case strings.Contains(path, "/orders") || strings.Contains(path, "/apply"):
			return "certificate", withBracket("申请 SSL 证书", certName), false
		case strings.Contains(path, "/renew"):
			return "certificate", withBracket("续签 SSL 证书", certName), false
		case strings.Contains(path, "/deploy"):
			return "certificate", withBracket("部署 SSL 证书", certName), false
		case method == "DELETE":
			return "certificate", withBracket("删除 SSL 证书", certName), false
		default:
			return "certificate", withBracket(defaultVerb(method)+" SSL 证书", certName), false
		}
	}

	// 12. 告警管理
	if module == "alerts" {
		targetID := firstNonEmpty(name, c.Param("id"))
		switch {
		case strings.Contains(path, "/channels") && strings.HasSuffix(path, "/test"):
			return "alerts", withBracket("测试告警通道", targetID), false
		case strings.Contains(path, "/channels"):
			return "alerts", withBracket(defaultVerb(method)+"告警通道", targetID), false
		case strings.Contains(path, "/rules"):
			return "alerts", withBracket(defaultVerb(method)+"告警规则", targetID), false
		default:
			return "alerts", withBracket(defaultVerb(method)+"告警配置", targetID), false
		}
	}

	// 13. 应用插件
	if module == "plugins" || strings.HasPrefix(module, "plugin:") {
		pluginID := firstNonEmpty(name, c.Param("id"))
		switch {
		case strings.HasSuffix(path, "/enable"):
			return "plugins", withBracket("启用插件", pluginID), false
		case strings.HasSuffix(path, "/disable"):
			return "plugins", withBracket("停用插件", pluginID), false
		case strings.HasSuffix(path, "/config"):
			return "plugins", withBracket("修改插件配置", pluginID), false
		case method == "DELETE":
			return "plugins", withBracket("卸载插件", pluginID), false
		case method == "POST":
			return "plugins", withBracket("安装插件", pluginID), false
		default:
			return "plugins", withBracket(defaultVerb(method)+"插件", pluginID), false
		}
	}

	// 14. 系统工具
	if module == "toolbox" {
		tool := firstNonEmpty(name, c.Param("tool"), c.Param("id"))
		switch {
		case strings.Contains(path, "/swap"):
			return "toolbox", "调整 Swap 虚拟内存", false
		case strings.Contains(path, "/dns"):
			return "toolbox", "修改系统 DNS 设置", false
		case strings.Contains(path, "/timezone") || strings.Contains(path, "/time"):
			return "toolbox", "修改系统时区/时间", false
		case strings.Contains(path, "/hostname"):
			return "toolbox", "修改主机名", false
		case strings.Contains(path, "/kernel"):
			return "toolbox", "修改内核参数", false
		case strings.Contains(path, "/disk") || strings.Contains(path, "/mount"):
			return "toolbox", withBracket("磁盘挂载操作", tool), false
		case strings.Contains(path, "/clean"):
			return "toolbox", "执行系统垃圾清理", false
		default:
			return "toolbox", withBracket(defaultVerb(method)+"系统工具", tool), false
		}
	}

	// 15. 默认回退
	if action != "" && name != "" {
		return auditModule, action + "[" + name + "]", false
	}
	if name != "" {
		return auditModule, path + " [" + name + "]", false
	}
	return auditModule, path, false
}

func isAppConfigRoute(module, path string) bool {
	if module == "database" && strings.Contains(path, "/extensions") {
		return true
	}
	if module != "software" {
		return false
	}
	for _, seg := range []string{
		"/settings", "/options", "/port", "/remote-access",
		"/config", "/extensions", "/php/", "/nginx/",
		"/ftp/", "/clamav/", "/rsync/", "/supervisor/", "/phpmyadmin/",
	} {
		if strings.Contains(path, seg) {
			return true
		}
	}
	return false
}

func formatAppConfigTarget(c *gin.Context, method, path, name string) string {
	appName := firstNonEmpty(name, c.Param("id"), c.Param("instance"))
	switch {
	case strings.Contains(path, "/extensions"):
		ext := firstNonEmpty(c.Param("name"), name)
		if method == "DELETE" {
			return withBracket("卸载应用扩展", ext)
		}
		return withBracket("安装应用扩展", ext)
	case strings.HasSuffix(path, "/port"):
		return withBracket("修改应用端口", appName)
	case strings.HasSuffix(path, "/remote-access"):
		return withBracket("修改远程访问设置", appName)
	case strings.HasSuffix(path, "/options"):
		return withBracket("调整应用性能参数", appName)
	case strings.HasSuffix(path, "/settings") || strings.Contains(path, "/config"):
		return withBracket("修改应用配置文件", appName)
	default:
		return withBracket(defaultVerb(method)+"应用设置", appName)
	}
}

func withBracket(verb, item string) string {
	item = strings.TrimSpace(item)
	if item == "" {
		return verb
	}
	return verb + "[" + item + "]"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func defaultVerb(method string) string {
	switch method {
	case "POST":
		return "新增"
	case "PUT", "PATCH":
		return "修改"
	case "DELETE":
		return "删除"
	default:
		return "操作"
	}
}
