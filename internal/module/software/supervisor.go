package software

import (
	"net/http"
	"path/filepath"

	"ezpanel/internal/logview"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

func (m *Module) supervisorRoutes(r *gin.RouterGroup) {
	r.GET("/supervisor/programs", m.supervisorPrograms)
	r.PUT("/supervisor/programs", m.supervisorPrograms)
	r.POST("/supervisor/programs/action", m.supervisorPrograms)
	r.GET("/supervisor/log", m.supervisorLog)
	r.DELETE("/supervisor/log", m.supervisorLog)
}

func (m *Module) supervisorPrograms(c *gin.Context) {
	action := "list"
	var input map[string]any
	if c.Request.Method != http.MethodGet {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
		if !response.Bind(c, &input) {
			return
		}
		release, err := m.tasks.Acquire("software-install:supervisor")
		if err != nil {
			fail(c, err)
			return
		}
		defer release()
		c.Set("panel.auditName", input["name"])
		c.Set("panel.auditAction", input["action"])
		action = "save"
		if c.Request.Method == http.MethodPost {
			action = "action"
		}
	}
	out, err := m.packages.Supervisor(c.Request.Context(), action, input)
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, out)
}

func (m *Module) supervisorLog(c *gin.Context) {
	snapshot, err := m.packages.SupervisorSnapshot(c.Request.Context())
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	kind := c.DefaultQuery("kind", "stdout")
	if kind != "stdout" && kind != "stderr" {
		response.Fail(c, response.Invalid, "日志类型无效")
		return
	}
	path := ""
	if c.Query("name") == "" {
		path = snapshot.MainLog
	}
	for _, program := range snapshot.List {
		if program.Name != c.Query("name") {
			continue
		}
		for _, process := range program.Processes {
			if process.Name == c.Query("process") {
				path = process.Stdout
				if kind == "stderr" {
					path = process.Stderr
				}
			}
		}
	}
	if !filepath.IsAbs(path) {
		response.Fail(c, response.NotFound, "该进程未配置独立日志文件")
		return
	}
	if c.Request.Method == http.MethodDelete {
		if err := logview.Clear(path); err != nil {
			response.Fail(c, response.System, response.Summary(err.Error()))
			return
		}
		response.OK(c, nil)
		return
	}
	if c.Query("stream") != "true" {
		text, err := logview.Read(path)
		if err != nil {
			response.Fail(c, response.System, response.Summary(err.Error()))
			return
		}
		response.OK(c, text)
		return
	}
	m.streamSoftwareLog(c, path)
}
