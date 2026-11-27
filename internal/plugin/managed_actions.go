package plugin

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/module/database"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (m *Module) authorizeResource(c *gin.Context, kind, id string) bool {
	grant, err := m.grant(identity(c).id)
	if err != nil || !grant.allowsResource(kind, id) {
		response.Fail(c, response.Forbidden, "插件未获此资源授权")
		m.hostAudit(c, "resource.denied", kind+":"+id, false)
		return false
	}
	return true
}

func (m *Module) deployCompose(c *gin.Context) {
	name := c.Param("name")
	if !m.authorizeResource(c, "compose", name) {
		return
	}
	m.submitManagedAction(c, "compose-deploy", name, name, "docker:compose:"+name, func(ctx context.Context, log *task.Log) (any, error) {
		return nil, m.services.Docker.PluginDeployCompose(ctx, name, log)
	})
}

func logLines(c *gin.Context) (int, bool) {
	lines, err := strconv.Atoi(c.DefaultQuery("lines", "200"))
	if err != nil || lines < 1 || lines > 1000 {
		response.Fail(c, response.Invalid, "日志行数须为 1 至 1000")
		return 0, false
	}
	return lines, true
}

func (m *Module) siteLogs(c *gin.Context) {
	id := c.Param("site")
	if !m.authorizeResource(c, "sites", id) {
		return
	}
	lines, ok := logLines(c)
	if !ok {
		return
	}
	kind := c.DefaultQuery("type", "access")
	if kind != "access" && kind != "error" {
		response.Fail(c, response.Invalid, "网站日志类型须为 access 或 error")
		return
	}
	var start, end time.Time
	for _, field := range []struct {
		name  string
		value *time.Time
	}{{"startTime", &start}, {"endTime", &end}} {
		if raw := c.Query(field.name); raw != "" {
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				response.Fail(c, response.Invalid, "日志时间须使用 RFC3339")
				return
			}
			*field.value = value
		}
	}
	if !start.IsZero() && !end.IsZero() && start.After(end) {
		response.Fail(c, response.Invalid, "日志开始时间不能晚于结束时间")
		return
	}
	text, truncated, err := m.services.Websites.PluginLogTail(c.Request.Context(), id, kind, c.Query("keyword"), lines, start, end)
	if err != nil {
		response.Fail(c, response.System, "无法读取此网站日志")
	}
	m.hostAudit(c, "sites.logs.read", id, err == nil)
	if err == nil {
		response.OK(c, gin.H{"content": redact(text, m.hostSecrets(identity(c))), "truncated": truncated})
	}
}

func (m *Module) containerLogs(c *gin.Context) {
	id := c.Param("container")
	if !m.authorizeResource(c, "containers", id) {
		return
	}
	lines, ok := logLines(c)
	if !ok {
		return
	}
	since, until := c.Query("since"), c.Query("until")
	for _, value := range []string{since, until} {
		if value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 {
				response.Fail(c, response.Invalid, "容器日志时间须为 Unix 秒")
				return
			}
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	text, err := m.services.Docker.PluginContainerLogs(ctx, id, lines, since, until)
	if err != nil {
		response.Fail(c, response.System, "无法读取此容器日志")
	}
	m.hostAudit(c, "containers.logs.read", id, err == nil)
	if err == nil {
		response.OK(c, gin.H{"content": redact(text, m.hostSecrets(identity(c)))})
	}
}

func (m *Module) submitManagedAction(c *gin.Context, action, target, id, lock string, run func(context.Context, *task.Log) (any, error)) {
	plugin := "plugin:" + identity(c).id
	taskID, err := m.tasks.Submit(task.Meta{Module: plugin, Action: action, Target: target, ResourceID: id, Username: plugin, IP: "127.0.0.1", LockKey: lock}, func(ctx context.Context, log *task.Log) (any, error) {
		log.Line(action + "：" + target)
		return run(ctx, log)
	})
	if err != nil {
		code := response.System
		if errors.Is(err, task.ErrBusy) {
			code = response.Conflict
		}
		response.Fail(c, code, "无法提交操作，请检查是否有正在运行的任务")
		return
	}
	response.OK(c, gin.H{"taskId": taskID})
}

func (m *Module) restartSite(c *gin.Context) {
	id := c.Param("site")
	if !m.authorizeResource(c, "sites", id) {
		return
	}
	m.submitManagedAction(c, "site-restart", id, id, "website:"+id, func(ctx context.Context, log *task.Log) (any, error) {
		return nil, m.services.Websites.RestartProject(ctx, id)
	})
}

func (m *Module) siteDetail(c *gin.Context) {
	id := c.Param("site")
	if !m.authorizeResource(c, "sites", id) {
		return
	}
	var row store.Website
	if err := m.db.WithContext(c.Request.Context()).Select("id, name, project_type, runtime_version, runtime_port, runtime_pending, enabled, proxy_routes").First(&row, "id = ?", id).Error; err != nil {
		response.Fail(c, response.NotFound, "网站不存在或详情无法读取")
		return
	}
	routes := make([]gin.H, 0, len(row.ProxyRoutes))
	for _, route := range row.ProxyRoutes {
		routes = append(routes, gin.H{"name": route.Name, "path": route.Path, "enabled": route.Enabled})
	}
	response.OK(c, gin.H{"id": row.ID, "name": row.Name, "projectType": row.ProjectType, "enabled": row.Enabled,
		"runtimeVersion": row.RuntimeVersion, "runtimePort": row.RuntimePort, "runtimePending": row.RuntimePending, "proxyRoutes": routes})
}

func (m *Module) controlSite(c *gin.Context) {
	id := c.Param("site")
	if !m.authorizeResource(c, "sites", id) {
		return
	}
	var in struct{ Action string }
	if !response.Bind(c, &in) {
		return
	}
	if in.Action != "start" && in.Action != "stop" {
		response.Fail(c, response.Invalid, "网站操作须为 start 或 stop")
		return
	}
	m.submitManagedAction(c, "site-"+in.Action, id, id, "website:"+id, func(ctx context.Context, log *task.Log) (any, error) {
		_, err := m.services.Websites.SetSiteEnabled(ctx, id, in.Action == "start")
		if err != nil {
			log.Line(response.Summary(err.Error()))
		}
		return nil, err
	})
}

func (m *Module) controlContainer(c *gin.Context) {
	id := c.Param("container")
	if !m.authorizeResource(c, "containers", id) {
		return
	}
	var in struct{ Action string }
	if !response.Bind(c, &in) {
		return
	}
	switch in.Action {
	case "start", "stop", "restart":
	default:
		response.Fail(c, response.Invalid, "仅支持启动、停止和重启容器")
		return
	}
	m.submitManagedAction(c, "container-"+in.Action, id, id, "docker:container:"+id, func(ctx context.Context, log *task.Log) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return nil, m.services.Docker.PluginContainerControl(ctx, id, in.Action)
	})
}

func (m *Module) containerStats(c *gin.Context) {
	id := c.Param("container")
	if !m.authorizeResource(c, "containers", id) {
		return
	}
	data, err := m.services.Docker.PluginContainerStats(c.Request.Context(), id)
	m.hostAudit(c, "containers.metrics.read", id, err == nil)
	if err != nil {
		response.Fail(c, response.System, "无法读取容器采样")
		return
	}
	response.OK(c, data)
}

func (m *Module) databaseNames(c *gin.Context) {
	id := c.Param("server")
	if !m.authorizeResource(c, "databases", id) {
		return
	}
	rows, err := m.services.Database.BackupDatabaseNames(c.Request.Context(), id)
	if err != nil {
		response.Fail(c, response.System, "无法读取可备份数据库")
		return
	}
	response.OK(c, rows)
}

func (m *Module) databaseBackup(c *gin.Context) {
	id := c.Param("server")
	if !m.authorizeResource(c, "databases", id) {
		return
	}
	var in struct{ Name string }
	if !response.Bind(c, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 256 || strings.ContainsRune(in.Name, 0) {
		response.Fail(c, response.Invalid, "数据库名称无效")
		return
	}
	m.submitManagedAction(c, "database-backup", in.Name, id, database.LockKey(id), func(ctx context.Context, log *task.Log) (any, error) {
		row, err := m.services.Database.Backup(ctx, id, in.Name, "", log, "", false)
		return gin.H{"backupId": row.ID}, err
	})
}

func (m *Module) databaseBackups(c *gin.Context) {
	id := c.Param("server")
	if !m.authorizeResource(c, "databases", id) {
		return
	}
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	rows := []store.DBBackup{}
	db := m.db.WithContext(c.Request.Context()).Model(&store.DBBackup{}).Where("server_id = ? AND storage_type = ?", id, "local")
	var total int64
	if err := db.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		response.Fail(c, response.System, "无法读取数据库备份")
		return
	}
	if int64(page-1) <= total/int64(size) {
		if err := db.Session(&gorm.Session{}).Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			response.Fail(c, response.System, "无法读取数据库备份")
			return
		}
	}
	response.OK(c, gin.H{"list": rows, "total": total})
}

func (m *Module) databaseRestore(c *gin.Context) {
	id := c.Param("server")
	if !m.authorizeResource(c, "databases", id) {
		return
	}
	var in struct{ BackupID, ConfirmDatabase string }
	if !response.Bind(c, &in) {
		return
	}
	if in.BackupID == "" || in.ConfirmDatabase == "" {
		response.Fail(c, response.Invalid, "请选择备份并精确确认数据库名称")
		return
	}
	m.submitManagedAction(c, "database-restore", in.ConfirmDatabase, id, database.LockKey(id), func(ctx context.Context, log *task.Log) (any, error) {
		err := m.services.Database.RestorePluginBackup(ctx, id, in.BackupID, in.ConfirmDatabase, log)
		if err != nil {
			log.Line(response.Summary(err.Error()))
		}
		return gin.H{"backupId": in.BackupID}, err
	})
}
