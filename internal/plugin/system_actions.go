package plugin

import (
	"context"
	"time"

	"ezpanel/internal/module/security"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

func (m *Module) appStatus(c *gin.Context) {
	id := c.Param("app")
	if !pkgmgr.ValidID(id) {
		response.Fail(c, response.NotFound, "应用不存在")
		return
	}
	if !m.authorizeResource(c, "apps", id) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 25*time.Second)
	defer cancel()
	state, err := m.services.Packages.Status(ctx, id)
	if err != nil {
		response.Fail(c, response.System, "无法读取应用状态")
		return
	}
	response.OK(c, gin.H{"id": state.ID, "name": state.Name, "installed": state.Installed, "running": state.Running, "version": state.Version})
}

func (m *Module) controlApp(c *gin.Context) {
	id := c.Param("app")
	if !m.authorizeResource(c, "apps", id) {
		return
	}
	var in struct{ Action string }
	if !response.Bind(c, &in) {
		return
	}
	switch in.Action {
	case "start", "stop", "restart", "reload":
	default:
		response.Fail(c, response.Invalid, "应用操作无效")
		return
	}
	m.submitManagedAction(c, "app-"+in.Action, pkgmgr.Name(id), id, "plugin-app:"+id, func(ctx context.Context, log *task.Log) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		state, err := m.services.Packages.Service(ctx, id, in.Action)
		if err != nil {
			log.Line(response.Summary(err.Error()))
		}
		return gin.H{"running": state.Running}, err
	})
}

func (m *Module) securitySnapshot(c *gin.Context) {
	status, err := m.services.Security.PluginStatus()
	if err != nil {
		response.Fail(c, response.System, "无法读取安全模块状态")
		return
	}
	enabled, err := m.services.WAF.PluginStatus(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, "无法读取 WAF 状态")
		return
	}
	response.OK(c, struct {
		security.PluginSecurityStatus
		WAFEnabled bool `json:"wafEnabled"`
	}{status, enabled})
}

func (m *Module) scanHardening(c *gin.Context) {
	m.submitManagedAction(c, "hardening-scan", "系统加固", "", "hardening:scan", func(context.Context, *task.Log) (any, error) {
		return nil, m.services.Security.ScanHardening()
	})
}

func (m *Module) auditHistory(c *gin.Context) {
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	rows, total := store.QueryOpLogs(m.db, store.OpLogFilter{
		Module: "plugin:" + identity(c).id,
		Page:   page,
		Size:   size,
	})
	response.OK(c, gin.H{"list": rows, "total": total})
}
