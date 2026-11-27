package security

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"ezpanel/internal/hardening"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

func (m *Module) ConfigureHardening(ctx context.Context) error {
	manager, err := hardening.New(filepath.Join(m.cfg.DataDir(), "hardening"))
	if err != nil {
		return fmt.Errorf("加载系统加固配置：%w", err)
	}
	m.hardening = manager
	go manager.Start(ctx)
	return nil
}

func (m *Module) hardeningRoutes(r *gin.RouterGroup) {
	r.GET("/hardening", m.hardeningState)
	r.PUT("/hardening", m.hardeningSettings)
	r.POST("/hardening/scan", m.hardeningScan)
	r.POST("/hardening/rules", m.hardeningRule)
	r.PUT("/hardening/rules/:id", m.hardeningRule)
	r.DELETE("/hardening/rules/:id", m.hardeningRule)
	r.POST("/hardening/rules/:id/action", m.hardeningAction)
}

func (m *Module) hardeningState(c *gin.Context) {
	if m.hardening == nil {
		response.Fail(c, response.System, "系统加固尚未初始化")
		return
	}
	response.OK(c, struct {
		hardening.State
		RecoveryCommand string `json:"recoveryCommand"`
	}{m.hardening.State(), fmt.Sprintf("ezpanel hardening-recover --install-dir '%s'", strings.ReplaceAll(m.cfg.InstallDir, "'", "'\"'\"'"))})
}

func (m *Module) hardeningSettings(c *gin.Context) {
	if m.hardening == nil {
		response.Fail(c, response.System, "系统加固模块未初始化")
		return
	}
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if !response.Bind(c, &input) {
		return
	}
	if input.Enabled == nil {
		response.Fail(c, response.Invalid, "请选择系统加固开关状态")
		return
	}
	action := "disable"
	if *input.Enabled {
		action = "enable"
	}
	c.Set("panel.auditAction", action)
	if err := m.hardening.SetEnabled(*input.Enabled); err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	m.hardeningState(c)
}

func (m *Module) hardeningScan(c *gin.Context) {
	if m.hardening == nil {
		response.Fail(c, response.System, "系统加固模块未初始化")
		return
	}
	c.Set("panel.auditAction", "scan")
	if err := m.hardening.Scan(); err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	m.hardeningState(c)
}

func (m *Module) hardeningRule(c *gin.Context) {
	if m.hardening == nil {
		response.Fail(c, response.System, "系统加固模块未初始化")
		return
	}
	id := c.Param("id")
	if c.Request.Method == http.MethodDelete {
		for _, row := range m.hardening.State().Rules {
			if row.ID == id {
				c.Set("panel.auditName", row.Path)
				break
			}
		}
		if err := m.hardening.DeleteRule(id); err != nil {
			response.Fail(c, response.Invalid, response.Summary(err.Error()))
			return
		}
	} else {
		var input hardening.RuleInput
		if !response.Bind(c, &input) {
			return
		}
		c.Set("panel.auditName", input.Path)
		if _, err := m.hardening.SaveRule(id, input); err != nil {
			response.Fail(c, response.Invalid, response.Summary(err.Error()))
			return
		}
	}
	m.hardeningState(c)
}

func (m *Module) hardeningAction(c *gin.Context) {
	if m.hardening == nil {
		response.Fail(c, response.System, "系统加固模块未初始化")
		return
	}
	var input struct {
		Action  string `json:"action"`
		Minutes int    `json:"minutes"`
	}
	if !response.Bind(c, &input) {
		return
	}
	c.Set("panel.auditAction", input.Action)
	for _, row := range m.hardening.State().Rules {
		if row.ID == c.Param("id") {
			c.Set("panel.auditName", row.Path)
			break
		}
	}
	if err := m.hardening.Action(c.Param("id"), input.Action, input.Minutes); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	m.hardeningState(c)
}
