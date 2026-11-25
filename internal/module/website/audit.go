package website

import (
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"ezpanel/internal/store"

	"github.com/gin-gonic/gin"
)

var websiteAuditActions = map[string]string{
	"PUT /columns": "调整网站列表列",
	"POST ":        "创建网站", "PUT /:id": "修改网站设置", "DELETE /:id": "删除网站",
	"PUT /:id/remark": "修改备注", "POST /:id/enable": "启用网站", "POST /:id/disable": "停用网站",
	"POST /:id/ssl": "配置 SSL", "DELETE /:id/ssl": "关闭 SSL", "POST /:id/ssl/check": "检查证书覆盖范围",
	"PUT /:id/rewrite": "修改伪静态", "PUT /:id/redirect": "修改重定向",
	"DELETE /:id/logs": "清空网站日志", "GET /:id/logs/download": "下载网站日志",
	"POST /nginx/reload":        "重载 OpenResty",
	"POST /:id/runtime-restart": "重启项目", "PUT /:id/runtime-registry": "修改依赖镜像源",
	"PUT /:id/python-packages": "修改 Python 依赖文件", "POST /:id/python-packages": "管理 Python 依赖",
	"POST /:id/node-packages": "管理 Node.js 模块",
}

func auditWebsiteRoute(c *gin.Context) {
	path := strings.TrimPrefix(c.FullPath(), "/api/websites")
	if action := websiteAuditActions[c.Request.Method+" "+path]; action != "" {
		c.Set("panel.auditAction", action)
	}
	c.Next()
}

func auditSite(c *gin.Context, action string, details ...string) {
	if action != "" {
		c.Set("panel.auditAction", action)
	}
	if len(details) != 0 {
		c.Set("panel.auditDetail", strings.Join(details, "\n"))
	}
}

func siteChanges(old, row store.Website) (string, []string) {
	details := []string{}
	change := func(label, from, to string) {
		if from != to {
			details = append(details, fmt.Sprintf("%s %s → %s", label, auditValue(from), auditValue(to)))
		}
	}
	added, removed := domainChanges(websiteDomains(old), websiteDomains(row))
	if len(added) != 0 {
		details = append(details, "添加域名 "+strings.Join(added, "、"))
	}
	if len(removed) != 0 {
		details = append(details, "移除域名 "+strings.Join(removed, "、"))
	}
	change("PHP 版本", old.PHPVersion, row.PHPVersion)
	change("名称", old.Name, row.Name)
	change("项目目录", old.Root, row.Root)
	change("站点类型", old.ProjectType, row.ProjectType)
	change("运行环境版本", old.RuntimeVersion, row.RuntimeVersion)
	change("启动方式", old.RuntimeLaunch, row.RuntimeLaunch)
	change("入口文件", old.RuntimeEntry, row.RuntimeEntry)
	change("执行文件", old.RuntimeFile, row.RuntimeFile)
	change("项目端口", fmt.Sprint(old.RuntimePort), fmt.Sprint(row.RuntimePort))
	change("工作进程", fmt.Sprint(old.RuntimeWorkers), fmt.Sprint(row.RuntimeWorkers))
	change("工作线程", fmt.Sprint(old.RuntimeThreads), fmt.Sprint(row.RuntimeThreads))
	change("执行超时", fmt.Sprint(old.RuntimeTimeout), fmt.Sprint(row.RuntimeTimeout))
	change("开机自启动", auditSwitch(old.RuntimeAutoStart), auditSwitch(row.RuntimeAutoStart))
	change("包管理器", old.RuntimePackageManager, row.RuntimePackageManager)
	change("启动脚本", old.RuntimeScript, row.RuntimeScript)
	change("备注", old.Remark, row.Remark)
	if old.RuntimeEnvironment != row.RuntimeEnvironment && old.RuntimeVersion == row.RuntimeVersion {
		details = append(details, "更换同版本运行环境")
	}
	// 启动命令和变量值可能含应用凭据，只记录发生过变更。
	if old.RuntimeCommand != row.RuntimeCommand {
		details = append(details, "修改启动命令")
	}
	if old.RuntimeEnv != row.RuntimeEnv {
		details = append(details, "修改环境变量")
	}
	sameRoute := func(a, b store.ProxyRoute) bool { return reflect.DeepEqual(a, b) }
	if !slices.EqualFunc(old.ProxyRoutes, row.ProxyRoutes, sameRoute) {
		details = append(details, fmt.Sprintf("反向代理规则 %d 条 → %d 条", len(old.ProxyRoutes), len(row.ProxyRoutes)))
		for _, route := range row.ProxyRoutes {
			if !slices.ContainsFunc(old.ProxyRoutes, func(oldRoute store.ProxyRoute) bool { return sameRoute(oldRoute, route) }) {
				details = append(details, fmt.Sprintf("保存规则 %s %s → %s（%s）", route.Name, route.Path, auditURL(route.Target), auditSwitch(route.Enabled)))
			}
		}
		for _, route := range old.ProxyRoutes {
			if !slices.ContainsFunc(row.ProxyRoutes, func(current store.ProxyRoute) bool { return current.Name == route.Name && current.Path == route.Path }) {
				details = append(details, fmt.Sprintf("移除规则 %s %s", route.Name, route.Path))
			}
		}
	}
	if len(details) == 1 {
		switch {
		case len(added) != 0:
			return "添加域名", details
		case len(removed) != 0:
			return "移除域名", details
		case old.PHPVersion != row.PHPVersion:
			return "更换 PHP 版本", details
		}
	}
	return "修改网站设置", details
}

func domainChanges(old, current []string) (added, removed []string) {
	for _, name := range current {
		if !slices.Contains(old, name) {
			added = append(added, name)
		}
	}
	for _, name := range old {
		if !slices.Contains(current, name) {
			removed = append(removed, name)
		}
	}
	return added, removed
}

func auditValue(value string) string {
	if value == "" {
		return "未设置"
	}
	return value
}

func auditSwitch(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "关闭"
}

func auditURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "已配置"
	}
	u.User, u.RawQuery, u.Fragment, u.ForceQuery = nil, "", "", false
	return auditValue(u.String())
}
