package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

const serviceCallback = "_panel/service"

func (m *Module) callService(c *gin.Context) {
	caller := identity(c)
	id, name := c.Param("id"), c.Param("name")
	defer func() { m.hostAudit(c, "services.call", id+"/"+name, c.Writer.Status() < 400) }()
	if _, ok := caller.manifest.Dependencies[id]; !ok {
		response.Fail(c, response.Forbidden, "仅允许调用已声明依赖的插件服务")
		return
	}
	run, manifest, err := m.readyPlugin(id)
	if err != nil || !slices.Contains(manifest.Services, name) {
		response.Fail(c, response.NotFound, "插件服务未提供或不可用")
		return
	}
	if err := m.checkPermission(id, manifest, "services.provide"); err != nil {
		response.Fail(c, response.Forbidden, err.Error())
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var data json.RawMessage
	if !response.Bind(c, &data) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	reply, err := invokePlugin(ctx, run, id, serviceCallback, gin.H{"caller": caller.id, "name": name, "data": data})
	if err != nil || len(reply.Data) == 0 {
		response.Fail(c, response.System, "插件服务调用失败或返回无效")
		return
	}
	response.OK(c, reply.Data)
}
