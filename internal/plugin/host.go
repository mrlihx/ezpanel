package plugin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/version"
	"github.com/gin-gonic/gin"
)

type hostIdentity struct {
	id       string
	manifest Manifest
	run      *running
}

func (m *Module) listenHost() error {
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return e
	}
	m.hostURL = "http://" + l.Addr().String()
	m.hostPort = l.Addr().(*net.TCPAddr).Port
	g := gin.New()
	g.UseRawPath = true
	g.UnescapePathValues = true
	g.RedirectTrailingSlash = false
	g.Use(func(c *gin.Context) {
		provided := c.GetHeader("X-Plugin-Token")
		m.mu.Lock()
		id := ""
		var current *running
		for candidate, run := range m.runs {
			if provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(run.token)) == 1 && (m.statuses[candidate] == "running" || (m.statuses[candidate] == "starting" && c.Request.URL.Path == "/host/v1/info")) {
				id = candidate
				current = run
				break
			}
		}
		if current == nil {
			m.mu.Unlock()
			response.Fail(c, response.Forbidden, "插件身份无效")
			return
		}
		now := time.Now()
		current.budget = min(20, current.budget+now.Sub(current.lastRequest).Seconds()*20)
		current.lastRequest = now
		if current.budget < 1 {
			m.mu.Unlock()
			response.Fail(c, response.RateLimited, "插件请求超限")
			return
		}
		current.budget--
		m.mu.Unlock()
		var p store.Plugin
		if m.db.Select("manifest", "operation").Take(&p, "id = ?", id).Error != nil || p.Operation != nil && c.Request.URL.Path != "/host/v1/info" {
			response.Fail(c, response.Forbidden, "插件身份无效")
			return
		}
		var mf Manifest
		if json.Unmarshal([]byte(p.Manifest), &mf) != nil {
			response.Fail(c, 5000, "")
			return
		}
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		go func() {
			select {
			case <-ctx.Done():
			case <-current.ctx.Done():
				cancel()
			}
		}()
		c.Request = c.Request.WithContext(ctx)
		c.Set("plugin.identity", hostIdentity{id, mf, current})
		c.Next()
	})
	r := g.Group("/host/v1")
	r.GET("/info", func(c *gin.Context) {
		i := identity(c)
		response.OK(c, gin.H{"panelVersion": version.Version, "sdkVersion": SDKVersion, "pluginId": i.id, "dataDir": filepath.Join(m.private(), i.id)})
	})
	r.POST("/log", m.hostLog)
	r.GET("/kv", m.kvList)
	r.GET("/kv/:key", m.kvGet)
	r.PUT("/kv/:key", m.kvPut)
	r.DELETE("/kv/:key", m.kvDelete)
	r.GET("/sysinfo", m.permission("sysinfo.read"), func(c *gin.Context) { response.OK(c, m.metrics.Snapshot()) })
	r.GET("/fs/read", m.permission("fs.read"), m.fsRead)
	r.GET("/fs/stat", m.permission("fs.read"), m.fsStat)
	r.GET("/fs/list", m.permission("fs.read"), m.fsList)
	r.GET("/fs/hash", m.permission("fs.read"), m.fsHash)
	r.GET("/fs/stream", m.permission("fs.read"), m.fsOpen)
	r.PUT("/fs/stream", m.permission("fs.write"), m.fsUpload)
	r.POST("/fs/write", m.permission("fs.write"), m.fsWrite)
	r.POST("/fs/mkdir", m.permission("fs.write"), m.fsMkdir)
	r.POST("/fs/delete", m.permission("fs.write"), m.fsDelete)
	r.POST("/exec", m.permission("exec.run"), m.hostExec)
	for _, resource := range []string{"sites", "containers", "databases", "certificates", "backups", "compose", "apps"} {
		r.GET("/resources/"+resource, m.permission(resource+".read"), func(c *gin.Context) { m.listResources(c, resource) })
	}
	r.POST("/notify", m.permission("notifications.send"), m.notifyChannel)
	r.POST("/services/:id/:name", m.permission("services.call"), m.callService)
	r.POST("/sites/:site/backup", m.permission("backups.create"), m.siteBackup)
	r.GET("/sites/:site/backups", m.permission("backups.read"), m.siteBackupHistory)
	r.POST("/sites/:site/backups/:id/verify", m.permission("backups.verify"), m.verifySiteBackup)
	r.POST("/sites/:site/backups/:id/restore", m.permission("backups.restore"), m.restoreSiteBackup)
	r.POST("/sites/:site/certificate", m.permission("certificates.bind"), m.bindSiteCertificate)
	r.POST("/certificates/:id/renew", m.permission("certificates.renew"), m.renewCertificate)
	r.GET("/sites/:site/logs", m.permission("sites.logs.read"), m.siteLogs)
	r.POST("/sites/:site/restart", m.permission("sites.restart"), m.restartSite)
	r.GET("/sites/:site", m.permission("sites.read"), m.siteDetail)
	r.POST("/sites/:site/control", m.permission("sites.control"), m.controlSite)
	r.GET("/containers/:container/logs", m.permission("containers.logs.read"), m.containerLogs)
	r.GET("/containers/:container/stats", m.permission("containers.metrics.read"), m.containerStats)
	r.POST("/containers/:container/control", m.permission("containers.control"), m.controlContainer)
	r.POST("/compose/:name/deploy", m.permission("compose.deploy"), m.deployCompose)
	r.GET("/monitoring/history", m.permission("monitoring.read"), m.metrics.HistoryPoints)
	r.GET("/apps/:app", m.permission("apps.read"), m.appStatus)
	r.POST("/apps/:app/control", m.permission("apps.control"), m.controlApp)
	r.GET("/security", m.permission("security.read"), m.securitySnapshot)
	r.POST("/security/scan", m.permission("hardening.scan"), m.scanHardening)
	r.GET("/audit", m.permission("audit.read"), m.auditHistory)
	r.GET("/credentials/:slot", m.permission("credentials.read"), m.hostCredential)
	r.POST("/databases/:server/backup", m.permission("databases.backup"), m.databaseBackup)
	r.GET("/databases/:server/names", m.permission("databases.backup"), m.databaseNames)
	r.GET("/databases/:server/backups", m.permission("databases.restore"), m.databaseBackups)
	r.POST("/databases/:server/restore", m.permission("databases.restore"), m.databaseRestore)
	r.GET("/tasks/:task", m.hostTask)
	r.POST("/tasks/:task/progress", m.permission("jobs.run"), m.jobProgress)
	g.NoRoute(func(c *gin.Context) { response.Fail(c, response.NotFound, "接口不存在") })
	m.host = &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: g}
	go m.host.Serve(l)
	return nil
}

func (m *Module) jobProgress(c *gin.Context) {
	var in struct {
		Message string
		Percent *int
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Message) > 1024 || in.Percent == nil || *in.Percent < 0 || *in.Percent > 100 {
		response.Fail(c, response.Invalid, "任务进度或说明无效")
		return
	}
	m.mu.Lock()
	log := identity(c).run.jobLogs[c.Param("task")]
	if log != nil {
		log.Line(fmt.Sprintf("%d%% %s", *in.Percent, redact(in.Message, m.hostSecrets(identity(c)))))
	}
	m.mu.Unlock()
	if log == nil {
		response.Fail(c, response.NotFound, "插件任务已结束或不存在")
		return
	}
	response.OK(c, nil)
}
func identity(c *gin.Context) hostIdentity { return c.MustGet("plugin.identity").(hostIdentity) }
func (m *Module) permission(permission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		i := identity(c)
		if err := m.checkPermission(i.id, i.manifest, permission); err != nil {
			response.Fail(c, response.Forbidden, err.Error())
			m.hostAudit(c, permission, c.Request.URL.Path, false)
			return
		}
		c.Next()
	}
}
func (m *Module) hostSecrets(i hostIdentity) []string { return i.run.log.secretValues() }
func (m *Module) hostAudit(c *gin.Context, action, target string, success bool) {
	i := identity(c)
	code, _ := c.Get("panel.errorCode")
	n, _ := code.(int)
	_ = m.db.Create(&store.OpLog{Module: "plugin:" + i.id, Action: action, Target: response.Summary(target, m.hostSecrets(i)...), IP: "127.0.0.1", Phase: "completed", Success: success, ErrorCode: n, CreatedAt: time.Now().UTC()}).Error
}
func (m *Module) hostLog(c *gin.Context) {
	var in struct {
		Level string `json:"level"`
		Msg   string `json:"msg"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Msg) > 4096 || !map[string]bool{"debug": true, "info": true, "warn": true, "error": true}[in.Level] {
		response.Fail(c, response.Invalid, "日志级别或长度无效")
		return
	}
	i := identity(c)
	if _, e := i.run.log.Write([]byte("[" + in.Level + "] " + in.Msg + "\n")); e != nil {
		response.Fail(c, 5000, "")
		return
	}
	response.OK(c, nil)
}
