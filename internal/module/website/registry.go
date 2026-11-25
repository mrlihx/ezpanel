package website

import (
	"net/url"
	"strings"
	"unicode"

	"ezpanel/internal/response"

	"github.com/gin-gonic/gin"
)

type projectRegistry struct {
	URL string `json:"url"`
}

func (m *Module) runtimeRegistry(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	if !hasProjectRegistry(row.ProjectType) {
		response.Fail(c, response.Invalid, "仅 Python 和 Node.js 项目支持设置依赖源")
		return
	}
	response.OK(c, projectRegistry{URL: row.RuntimeRegistry})
}

func (m *Module) saveRuntimeRegistry(c *gin.Context) {
	var in projectRegistry
	if !response.Bind(c, &in) {
		return
	}
	in.URL = strings.TrimSpace(in.URL)
	if in.URL != "" {
		u, err := url.Parse(in.URL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
			u.ForceQuery || u.RawQuery != "" || strings.Contains(in.URL, "#") || len(in.URL) > 2048 ||
			strings.IndexFunc(in.URL, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			response.Fail(c, response.Invalid, "请输入不含登录信息、查询参数和片段的 HTTPS 镜像地址")
			return
		}
		in.URL = u.String()
	}
	release, err := m.tasks.Acquire("website:" + c.Param("id"))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.row(c)
	if !ok {
		return
	}
	if !hasProjectRegistry(row.ProjectType) {
		response.Fail(c, response.Invalid, "仅 Python 和 Node.js 项目支持设置依赖源")
		return
	}
	auditSite(c, "", "镜像源 "+auditValue(row.RuntimeRegistry)+" → "+auditValue(in.URL))
	if in.URL != row.RuntimeRegistry {
		updates := map[string]any{"runtime_registry": in.URL}
		// 临时依赖任务直接读取新地址，运行容器随下次启动应用配置。
		if row.ProxyTarget != "" {
			updates["runtime_pending"] = true
		}
		if err := m.db.Model(&row).Updates(updates).Error; err != nil {
			failure(c, errStorage)
			return
		}
	}
	response.OK(c, in)
}

func hasProjectRegistry(kind string) bool {
	return kind == "python" || kind == "nodejs"
}
