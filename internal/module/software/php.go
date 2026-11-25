package software

import (
	"context"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"gorm.io/gorm"
	"strings"

	"github.com/gin-gonic/gin"
)

func (m *Module) phpRoutes(r *gin.RouterGroup) {
	r.GET("/php/cli", func(c *gin.Context) {
		state, err := m.packages.PHPCLIStatus(c.Request.Context())
		if err != nil {
			response.Fail(c, response.System, response.Summary(err.Error()))
			return
		}
		response.OK(c, state)
	})
	r.PUT("/php/cli", func(c *gin.Context) {
		var in struct {
			ID string `json:"id"`
		}
		if !response.Bind(c, &in) {
			return
		}
		state, err := m.packages.SetDefaultPHPCLI(c.Request.Context(), strings.TrimSpace(in.ID))
		if err != nil {
			response.Fail(c, response.Invalid, response.Summary(err.Error()))
			return
		}
		response.OK(c, state)
	})
	p := r.Group("/:id/php", func(c *gin.Context) {
		if !pkgmgr.IsPHP(c.Param("id")) {
			response.Fail(c, response.Invalid, "PHP版本不存在")
			c.Abort()
		}
	})
	p.GET("/config/:kind", m.phpConfig)
	p.PUT("/config/:kind", m.savePHPConfig)
	p.GET("/extensions", func(c *gin.Context) {
		rows, err := m.packages.PHPExtensions(c.Request.Context(), c.Param("id"))
		if err != nil {
			response.Fail(c, response.System, response.Summary(err.Error()))
			return
		}
		var latest store.Task
		err = m.db.Omit("result").Where("module = ? AND resource_id = ? AND action IN ?", "software", c.Param("id"), []string{"extension-install", "extension-uninstall"}).Order("started_at DESC").First(&latest).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.Internal, "")
			return
		}
		response.OK(c, gin.H{"latestTaskId": latest.ID, "installed": rows, "catalog": pkgmgr.PHPExtensionCatalog(c.Param("id"))})
	})
	p.POST("/extensions", m.phpExtension)
	p.GET("/load", func(c *gin.Context) {
		data, err := m.packages.PHPFPMStatus(c.Request.Context(), c.Param("id"))
		if err != nil {
			response.Fail(c, response.System, response.Summary(err.Error()))
			return
		}
		response.OK(c, data)
	})
	p.GET("/info", func(c *gin.Context) {
		data, err := m.packages.PHPInfo(c.Request.Context(), c.Param("id"))
		if err != nil {
			response.Fail(c, response.System, response.Summary(err.Error()))
			return
		}
		response.OK(c, gin.H{"content": data})
	})
}
func phpConfigKind(c *gin.Context) (string, bool) {
	kind := c.Param("kind")
	if kind != "ini" && kind != "fpm" {
		response.Fail(c, response.Invalid, "配置类型无效")
		return "", false
	}
	return kind, true
}
func (m *Module) phpConfig(c *gin.Context) {
	kind, ok := phpConfigKind(c)
	if !ok {
		return
	}
	data, err := m.packages.Settings(c.Request.Context(), c.Param("id"), kind)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	section := ""
	if kind == "fpm" {
		section = "www"
	}
	response.OK(c, gin.H{"path": data.Path, "content": data.Content, "revision": data.Revision, "values": pkgmgr.PHPDirectives(data.Content, section)})
}
func (m *Module) savePHPConfig(c *gin.Context) {
	kind, ok := phpConfigKind(c)
	if !ok {
		return
	}
	var in struct {
		Content  *string           `json:"content"`
		Values   map[string]string `json:"values"`
		Revision string            `json:"revision"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Revision) != 64 || (in.Content == nil) == (in.Values == nil) || (in.Content != nil && len(*in.Content) > 1<<20) {
		response.Fail(c, response.Invalid, "配置内容或修订号无效")
		return
	}
	settings, err := m.packages.Settings(c.Request.Context(), c.Param("id"), kind)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	var content string
	if in.Content != nil {
		content = *in.Content
	} else {
		section := ""
		if kind == "fpm" {
			section = "www"
		}
		if err = pkgmgr.ValidatePHPValues(kind, in.Values, pkgmgr.PHPDirectives(settings.Content, section)); err != nil {
			response.Fail(c, response.Invalid, err.Error())
			return
		}
		content, err = pkgmgr.SetPHPDirectives(settings.Content, section, in.Values)
		if err != nil {
			response.Fail(c, response.Invalid, err.Error())
			return
		}
	}
	if in.Content != nil {
		err = m.packages.SaveSettings(c.Request.Context(), c.Param("id"), content, in.Revision, kind)
	} else {
		err = m.packages.SavePHPOptions(c.Request.Context(), c.Param("id"), content, in.Revision, kind)
	}
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"saved": true})
}
func (m *Module) phpExtension(c *gin.Context) {
	var in struct {
		Name            string `json:"name"`
		Version         string `json:"version"`
		Action          string `json:"action"`
		AcceptedLicense bool   `json:"acceptedLicense"`
	}
	if !response.Bind(c, &in) {
		return
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if !pkgmgr.ValidPHPExtension(in.Name, in.Version) || (in.Action != "install" && in.Action != "uninstall") {
		response.Fail(c, response.Invalid, "扩展名称、版本或操作无效")
		return
	}
	module := pkgmgr.PHPExtensionModule(in.Name)
	if in.Action == "install" && (module == "sourceguardian" || module == "ioncube") && !in.AcceptedLicense {
		response.Fail(c, response.Invalid, "请先阅读并同意厂商加载器许可条款")
		return
	}
	id := c.Param("id")
	session := auth.Current(c)
	taskID, err := m.tasks.Submit(task.Meta{Module: "software", Action: "extension-" + in.Action, Target: id + "/" + in.Name, ResourceID: id, LockKey: "software-install:" + id, Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		err := m.packages.ChangePHPExtension(ctx, id, in.Name, in.Version, in.Action, log.Line)
		if err != nil {
			log.Line("失败：" + err.Error())
		}
		return gin.H{"extension": in.Name}, err
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": taskID})
}
