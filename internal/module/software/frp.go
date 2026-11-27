package software

import (
	"context"
	"net/http"
	"path/filepath"

	"ezpanel/internal/auth"
	"ezpanel/internal/logview"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

func (m *Module) frpRoutes(r *gin.RouterGroup) {
	r.GET("/frp/services", func(c *gin.Context) {
		rows, err := m.packages.FRPServices(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		response.OK(c, gin.H{"list": rows})
	})
	r.GET("/frp/services/:role/config", m.frpConfig)
	r.PUT("/frp/services/:role/config", m.frpConfig)
	r.POST("/frp/services/:role/action", m.frpAction)
	r.GET("/frp/services/:role/log", m.frpLog)
	r.DELETE("/frp/services/:role/log", m.frpLog)
}
func (m *Module) frpConfig(c *gin.Context) {
	role := c.Param("role")
	if !pkgmgr.ValidFRPRole(role) {
		response.Fail(c, response.Invalid, "Frp 服务类型无效")
		return
	}
	if c.Request.Method == http.MethodGet {
		out, err := m.packages.FRPConfig(role)
		if err != nil {
			response.Fail(c, response.Invalid, response.Summary(err.Error()))
			return
		}
		response.OK(c, out)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var input pkgmgr.FRPConfigInput
	if !response.Bind(c, &input) {
		return
	}
	c.Set("panel.auditName", role)
	release, err := m.tasks.Acquire("software-install:frp")
	if err != nil {
		fail(c, err)
		return
	}
	defer release()
	mode, err := m.packages.SaveFRPConfig(c.Request.Context(), role, input)
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"mode": mode})
}
func (m *Module) frpAction(c *gin.Context) {
	role := c.Param("role")
	var input struct {
		Action  string `json:"action"`
		Version string `json:"version"`
	}
	if !response.Bind(c, &input) {
		return
	}
	labels := map[string]string{"install": "安装", "reinstall": "重新安装", "uninstall": "卸载", "start": "启动", "stop": "停止", "restart": "重启", "reload": "重载配置"}
	if !pkgmgr.ValidFRPRole(role) || labels[input.Action] == "" || input.Action == "reload" && role != "frpc" {
		response.Fail(c, response.Invalid, "Frp 服务操作无效")
		return
	}
	rows, err := m.packages.FRPServices(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	var service pkgmgr.FRPService
	for _, row := range rows {
		if row.Role == role {
			service = row
		}
	}
	if input.Action == "install" && service.Installed || input.Action != "install" && !service.Installed {
		response.Fail(c, response.Invalid, "服务状态已变化，请刷新后重试")
		return
	}
	if input.Action == "reinstall" {
		input.Version = service.Version
	}
	if input.Action == "install" && input.Version == "" {
		response.Fail(c, response.Invalid, "请选择安装版本")
		return
	}
	c.Set("panel.auditName", role)
	if input.Action != "install" && input.Action != "reinstall" && input.Action != "uninstall" {
		release, err := m.tasks.Acquire("software-install:frp")
		if err != nil {
			fail(c, err)
			return
		}
		defer release()
		if err = m.packages.FRPAction(c.Request.Context(), role, input.Action, "", nil); err != nil {
			fail(c, err)
			return
		}
		rows, err = m.packages.FRPServices(c.Request.Context())
		if err != nil {
			fail(c, err)
			return
		}
		response.OK(c, gin.H{"list": rows})
		return
	}
	session := auth.Current(c)
	id, err := m.tasks.Submit(task.Meta{Module: "software", Action: input.Action, Target: labels[input.Action] + " Frp 服务[" + role + "]", ResourceID: "frp", LockKey: "software-install:frp", Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		err := m.packages.FRPAction(ctx, role, input.Action, input.Version, log.Line)
		if err != nil {
			return nil, err
		}
		return m.packages.FRPServices(ctx)
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
func (m *Module) frpLog(c *gin.Context) {
	role := c.Param("role")
	if !pkgmgr.ValidFRPRole(role) {
		response.Fail(c, response.Invalid, "Frp 服务类型无效")
		return
	}
	path := filepath.Join(m.cfg.Layout().AppLogDir("frp"), role+".log")
	if c.Request.Method == http.MethodDelete {
		if err := logview.Clear(path); err != nil {
			response.Fail(c, response.System, response.Summary(err.Error()))
			return
		}
		response.OK(c, nil)
		return
	}
	if c.Query("stream") == "true" {
		m.streamSoftwareLog(c, path)
		return
	}
	text, err := logview.Read(path)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	response.OK(c, text)
}
