package software

import (
	"ezpanel/internal/logview"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (m *Module) databaseInstances(c *gin.Context) {
	id := c.Param("id")
	if !pkgmgr.IsDatabaseContainer(id) {
		response.Fail(c, response.Invalid, "数据库服务无效")
		return
	}
	items, err := m.packages.DatabaseInstances(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"list": items})
}

func (m *Module) databaseInstance(c *gin.Context) {
	item, err := m.packages.DatabaseInstance(c.Request.Context(), c.Param("id"), c.Param("instance"))
	if err != nil {
		fail(c, err)
		return
	}
	if item.ContainerID == "" {
		response.Fail(c, response.NotFound, "数据库实例不存在")
		return
	}
	response.OK(c, item)
}

func (m *Module) databaseInstanceAction(c *gin.Context) {
	id, instance := c.Param("id"), c.Param("instance")
	var in struct {
		Action string `json:"action"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !pkgmgr.IsDatabaseContainer(id) ||
		(in.Action != "start" && in.Action != "stop" && in.Action != "restart" && in.Action != "remove") {
		response.Fail(c, response.Invalid, "数据库实例操作无效")
		return
	}
	if err := m.packages.DatabaseInstanceAction(c.Request.Context(), id, instance, in.Action); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	if (id == "mysql" && instance != "default" || id == "mongodb" || id == "redis") && (in.Action == "start" || in.Action == "restart") {
		ctx := c.Request.Context()
		row, err := m.packages.DatabaseInstance(ctx, id, instance)
		if err == nil && id == "mysql" {
			err = m.db.WithContext(ctx).Model(&store.DBServer{}).Where("is_local = ? AND type = ? AND instance_id = ?", true, id, instance).Update("port", row.Port).Error
		} else if err == nil {
			connection := id + "-" + instance
			if instance == "default" {
				connection = "local"
			}
			err = m.db.WithContext(ctx).Model(&store.Setting{}).Where("key = ?", "database."+id+"."+connection).
				Update("value", gorm.Expr("json_set(value, '$.port', ?)", row.Port)).Error
		}
		if err != nil {
			fail(c, err)
			return
		}
	}
	response.OK(c, nil)
}

func (m *Module) saveDatabaseInstancePort(c *gin.Context) {
	var in struct {
		Port int `json:"port"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if err := m.packages.SaveDatabaseInstancePort(c.Request.Context(), c.Param("id"), c.Param("instance"), in.Port); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, nil)
}

func (m *Module) saveDatabaseInstanceRemoteAccess(c *gin.Context) {
	id, instance := c.Param("id"), c.Param("instance")
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !response.Bind(c, &in) {
		return
	}
	row, err := m.packages.SaveDatabaseInstanceRemoteAccess(c.Request.Context(), id, instance, in.Enabled)
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, row)
}

func (m *Module) databaseInstancePassword(c *gin.Context) {
	id, instance := c.Param("id"), c.Param("instance")
	if !pkgmgr.IsDatabaseContainer(id) {
		response.Fail(c, response.Invalid, "数据库服务无效")
		return
	}
	password, err := m.packages.DatabaseInstancePassword(id, instance)
	if err != nil {
		response.Fail(c, response.NotFound, "实例初始密码不可用")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"password": password})
}

func (m *Module) databaseInstanceSettings(c *gin.Context) {
	ctx, id, instance := c.Request.Context(), c.Param("id"), c.Param("instance")
	if c.Request.Method == "PUT" {
		var in struct {
			Content  string `json:"content"`
			Revision string `json:"revision"`
		}
		if !response.Bind(c, &in) {
			return
		}
		if err := m.packages.SaveDatabaseInstanceSettings(ctx, id, instance, in.Content, in.Revision); err != nil {
			response.Fail(c, response.Invalid, response.Summary(err.Error()))
			return
		}
	}
	out, err := m.packages.DatabaseInstanceSettings(ctx, id, instance)
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, out)
}

func (m *Module) databaseInstanceOptions(c *gin.Context) {
	ctx, id, instance := c.Request.Context(), c.Param("id"), c.Param("instance")
	if c.Request.Method == "PUT" {
		var in struct {
			Values   map[string]string `json:"values"`
			Revision string            `json:"revision"`
		}
		if !response.Bind(c, &in) {
			return
		}
		if err := m.packages.SaveDatabaseInstanceOptions(ctx, id, instance, in.Revision, in.Values); err != nil {
			response.Fail(c, response.Invalid, response.Summary(err.Error()))
			return
		}
	}
	config, values, err := m.packages.DatabaseInstanceOptions(ctx, id, instance)
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"revision": config.Revision, "values": values})
}

func (m *Module) databaseInstanceLog(c *gin.Context) {
	ctx, id, instance := c.Request.Context(), c.Param("id"), c.Param("instance")
	if id != "mysql" && id != "mongodb" && id != "postgresql" {
		response.Fail(c, response.Invalid, "日志类型无效")
		return
	}
	row, err := m.packages.DatabaseInstance(ctx, id, instance)
	if err != nil {
		fail(c, err)
		return
	}
	if row.ContainerID == "" {
		response.Fail(c, response.NotFound, "数据库实例不存在")
		return
	}
	if id == "mysql" {
		out, err := m.packages.MySQLErrorLog(ctx, row, c.Request.Method == "DELETE")
		if err != nil {
			fail(c, err)
			return
		}
		response.OK(c, out)
		return
	}
	if c.Request.Method == "DELETE" {
		response.Fail(c, response.Invalid, "此日志不支持清空")
		return
	}
	sources, err := m.packages.DatabaseLogSources(ctx, id, row)
	if err != nil {
		fail(c, err)
		return
	}
	path := ""
	if len(sources) > 0 && sources[0].ID == "file" {
		out, err := logview.Read(sources[0].Path)
		if err != nil {
			fail(c, err)
			return
		}
		out.Clearable = false
		response.OK(c, out)
		return
	}
	if row.Running {
		// 读取运行中容器挂载的配置，避免使用尚未生效的日志路径。
		path = m.packages.MongoInstanceLogPath(ctx, row)
	}
	out, err := m.packages.DatabaseInstanceLog(ctx, id, row, path)
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, out)
}
