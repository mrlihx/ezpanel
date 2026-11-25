package website

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	certificates "ezpanel/internal/module/certificate"
	"ezpanel/internal/module/security"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var errStorage = errors.New("网站数据读取或保存失败")

type Module struct {
	trafficMu sync.Mutex
	traffic   map[string]*siteTraffic
	db        *gorm.DB
	cfg       config.Config
	tasks     *task.Manager
	packages  *pkgmgr.Manager
	auth      *auth.Service
	firewall  *security.Module
	policy    *pathsafe.Policy
	mu        sync.Mutex
}

func New(db *gorm.DB, cfg config.Config, tasks *task.Manager, packages *pkgmgr.Manager, a *auth.Service, firewall *security.Module) (*Module, error) {
	p, e := pathsafe.New(cfg.InstallDir)
	if e != nil {
		return nil, e
	}
	m := &Module{traffic: map[string]*siteTraffic{}, db: db, cfg: cfg, tasks: tasks, packages: packages, auth: a, policy: p, firewall: firewall}
	return m, nil
}
func (m *Module) Name() string { return "website" }
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/websites", auditWebsiteRoute)
	r.GET("/env", m.environment)
	r.GET("/columns", m.columns)
	r.PUT("/columns", m.columns)
	r.GET("/:id/runtime-logs", m.runtimeLogs)
	r.GET("/:id/runtime-load", m.projectLoad)
	r.GET("/:id/runtime-service", m.projectService)
	r.POST("/:id/runtime-restart", m.restartProject)
	r.GET("/:id/runtime-registry", m.runtimeRegistry)
	r.PUT("/:id/runtime-registry", m.saveRuntimeRegistry)
	r.GET("/:id/python-packages", m.pythonPackages)
	r.PUT("/:id/python-packages", m.savePythonRequirements)
	r.POST("/:id/python-packages", m.changePythonPackage)
	r.GET("/:id/node-packages", m.nodePackages)
	r.POST("/:id/node-packages", m.changeNodePackage)
	r.GET("/php-versions", func(c *gin.Context) { response.OK(c, gin.H{"list": m.packages.PHPVersions(c.Request.Context())}) })
	r.GET("/advanced", m.getAdvanced)
	r.PUT("/advanced/pages/:kind", m.saveDefaultPage)
	r.PUT("/advanced/default-site", m.saveDefaultSite)
	r.PUT("/advanced/tls", m.saveTLSProtocols)
	r.GET("", m.list)
	r.POST("", m.create)
	r.POST("/nginx/reload", m.reloadHandler)
	r.GET("/:id", m.detail)
	r.PUT("/:id", m.update)
	r.PUT("/:id/remark", m.saveRemark)
	r.DELETE("/:id", m.delete)
	r.POST("/:id/enable", m.toggle)
	r.POST("/:id/disable", m.toggle)
	r.POST("/:id/ssl", m.ssl)
	r.POST("/:id/ssl/check", m.checkSSL)
	r.DELETE("/:id/ssl", m.ssl)
	r.GET("/:id/config", m.configuration)
	r.GET("/:id/ssl", m.sslCertificate)
	r.POST("/:id/fix-permissions", m.fixPermissions)
	r.PUT("/:id/rewrite", m.saveRewrite)
	r.PUT("/:id/redirect", m.saveRedirect)
	r.GET("/:id/logs", m.logs)
	r.GET("/:id/logs/stream", m.streamLogs)
	r.GET("/:id/statistics", m.statistics)
	r.DELETE("/:id/logs", m.clearLog)
	r.GET("/:id/logs/download", m.downloadLog)
}
func failure(c *gin.Context, e error) {
	switch {
	case errors.Is(e, pkgmgr.ErrUnsupported):
		response.Fail(c, response.Forbidden, "OpenResty 未安装或不可受管；源码安装和未知服务需手工管理")
	case errors.Is(e, pkgmgr.ErrBusy), errors.Is(e, task.ErrBusy):
		response.Fail(c, response.Conflict, "站点备份、安装或配置操作正在运行，请稍后重试")
	case errors.Is(e, gorm.ErrRecordNotFound):
		response.Fail(c, response.NotFound, "站点不存在")
	case errors.Is(e, errStorage):
		response.Fail(c, response.Internal, "")
	default:
		response.Fail(c, response.System, response.Summary(e.Error()))
	}
}
func (m *Module) environment(c *gin.Context) {
	state, e := m.packages.Status(c.Request.Context(), "openresty")
	if e != nil {
		failure(c, e)
		return
	}
	var active store.Task
	e = m.db.Where("module = ? AND resource_id = ? AND status = ?", "software", "openresty", "running").Order("started_at DESC").First(&active).Error
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		failure(c, errStorage)
		return
	}
	var id *string
	if active.ID != "" {
		id = &active.ID
	}
	response.OK(c, gin.H{"application": state, "installed": state.Installed, "version": state.Version, "running": state.Running, "manageable": state.Manageable, "activeTaskId": id})
}
func (m *Module) view(row store.Website, running bool, preloadedRoots ...[]store.Website) gin.H {
	var allRoots []store.Website
	if len(preloadedRoots) > 0 {
		allRoots = preloadedRoots[0]
	}
	allowed, reason := m.canDeleteWithRoots(row, allRoots)
	var expires *time.Time
	if row.SSLEnabled {
		path, _ := m.certPaths(row.ID)
		if data, e := os.ReadFile(path); e == nil {
			if block, _ := pem.Decode(data); block != nil {
				if cert, e := x509.ParseCertificate(block.Bytes); e == nil {
					expires = &cert.NotAfter
				}
			}
		}
	}
	command := row.RuntimeCommand
	if row.RuntimeLaunch == "uwsgi" || row.RuntimeLaunch == "gunicorn" || row.RuntimeLaunch == "script" || row.RuntimeLaunch == "file" {
		command = ""
	}
	return gin.H{"runtimeEnvironment": row.RuntimeEnvironment, "runtimeVersion": row.RuntimeVersion, "runtimeLaunch": row.RuntimeLaunch, "runtimeEntry": row.RuntimeEntry, "runtimeCommand": command, "runtimePort": row.RuntimePort, "runtimeWorkers": row.RuntimeWorkers, "runtimeThreads": row.RuntimeThreads, "runtimeTimeout": row.RuntimeTimeout, "runtimeFile": row.RuntimeFile, "runtimeAutoStart": row.RuntimeAutoStart, "runtimePackageManager": row.RuntimePackageManager, "runtimeScript": row.RuntimeScript, "runtimeConfigured": row.RuntimeCommand != "", "projectType": row.ProjectType, "rewriteRule": row.RewriteRule, "redirectEnabled": row.RedirectEnabled, "redirectTarget": row.RedirectTarget, "redirectCode": row.RedirectCode, "redirectKeepUri": row.RedirectKeepURI, "id": row.ID, "name": row.Name, "domains": websiteDomains(row), "phpVersion": row.PHPVersion, "type": row.Type, "root": row.Root, "proxyRoutes": append([]store.ProxyRoute{}, row.ProxyRoutes...), "remark": row.Remark, "enabled": row.Enabled, "effectiveRunning": row.Enabled && running, "sslEnabled": row.SSLEnabled, "forceHttps": row.ForceHTTPS, "sslExpireAt": expires, "canDeleteFiles": allowed, "deleteFilesDisabledReason": reason, "createdAt": row.CreatedAt, "updatedAt": row.UpdatedAt}
}
func (m *Module) running(ctx context.Context) bool {
	state, e := m.packages.Status(ctx, "openresty")
	return e == nil && state.Running != nil && *state.Running
}
func (m *Module) list(c *gin.Context) {
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	query := m.db.Model(&store.Website{})
	if project := c.Query("projectType"); project != "" {
		if project != "php" && project != "proxy" && !pkgmgr.IsProjectRuntime(project) {
			response.Fail(c, response.Invalid, "项目分类无效")
			return
		}
		query = query.Where("project_type = ?", project)
	}
	if keyword := c.Query("keyword"); keyword != "" {
		escaped := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(keyword)
		query = query.Where("name LIKE ? ESCAPE '\\' OR domains LIKE ? ESCAPE '\\'", "%"+escaped+"%", "%"+escaped+"%")
	}
	var total int64
	var rows []store.Website
	if query.Count(&total).Error != nil || query.Order("created_at DESC, id DESC").Offset((page-1)*size).Limit(size).Find(&rows).Error != nil {
		failure(c, errStorage)
		return
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	var counts []struct {
		WebsiteID string
		Count     int64
	}
	if len(ids) > 0 && m.db.Model(&store.SiteBackup{}).Select("website_id, COUNT(*) AS count").Where("website_id IN ?", ids).Group("website_id").Scan(&counts).Error != nil {
		failure(c, errStorage)
		return
	}
	backups := map[string]int64{}
	for _, count := range counts {
		backups[count.WebsiteID] = count.Count
	}
	running := m.running(c.Request.Context())
	var runningProjects map[string]bool
	for _, row := range rows {
		if row.RuntimeVersion != "" {
			runningProjects = m.packages.RunningProjects(c.Request.Context())
			break
		}
	}
	var allRoots []store.Website
	if err := m.db.Select("id, root").Where("root <> ?", "").Find(&allRoots).Error; err != nil {
		allRoots = nil
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		item := m.view(row, running && (row.RuntimeVersion == "" || runningProjects[row.ID]), allRoots)
		if row.RuntimeVersion != "" {
			item["runtimeRunning"] = runningProjects[row.ID]
		}
		item["backupCount"] = backups[row.ID]
		item["dailyTraffic"], item["trafficNotice"] = m.dailyTraffic(row.ID, siteLogDirectory(row))
		items = append(items, item)
	}
	response.OK(c, gin.H{"list": items, "total": total})
}
func (m *Module) row(c *gin.Context) (store.Website, bool) {
	var row store.Website
	if e := m.db.First(&row, "id = ?", c.Param("id")).Error; e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			failure(c, e)
		} else {
			failure(c, errStorage)
		}
		return row, false
	}
	c.Set("panel.auditResource", row.ID)
	c.Set("panel.auditName", row.Name)
	return row, true
}
func (m *Module) detail(c *gin.Context) {
	row, ok := m.row(c)
	if ok {
		out := m.view(row, m.running(c.Request.Context()))
		out["logDirectory"] = filepath.Join(siteLogRoot, siteLogDirectory(row))
		if row.RuntimeVersion != "" {
			c.Header("Cache-Control", "no-store")
			out["runtimeEnv"] = m.runtimeEnv(row)
			out["effectiveRunning"] = out["effectiveRunning"] == true && m.packages.ProjectRunning(c.Request.Context(), row.ID)
			if row.ProjectType == "python" && (row.RuntimeLaunch == "uwsgi" || row.RuntimeLaunch == "gunicorn") {
				out["runtimeConfig"] = wsgiConfig(row.RuntimeLaunch, row.RuntimeEntry, row.RuntimePort, row.RuntimeWorkers, row.RuntimeThreads, row.RuntimeTimeout)
			}
		}
		response.OK(c, out)
	}
}
func (m *Module) configuration(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	data, e := os.ReadFile(m.confPath(row.ID, true))
	if os.IsNotExist(e) && !row.Enabled {
		data, e = os.ReadFile(m.confPath(row.ID, false))
	}
	if e != nil {
		failure(c, errors.New("站点配置文件无法读取，请检查 OpenResty 配置目录"))
		return
	}
	response.OK(c, gin.H{"content": string(data)})
}

// 只清理本次创建的默认页和空目录；原有内容无论成功失败都保留。
func prepareRoot(row *store.Website, siteRoot, oldRoot string, defaultIndex ...string) (func(), error) {
	cleanup := func() {}
	if row.Root == "" {
		return cleanup, nil
	}
	if row.Type == "static" {
		if row.Root == "/www" || strings.HasPrefix(row.Root, "/www/") {
			if err := pkgmgr.EnsureWWWRoot(); err != nil {
				return cleanup, err
			}
		}
		if err := pkgmgr.EnsureWebDirectory(siteRoot); err != nil {
			return cleanup, err
		}
	}
	if row.Root == oldRoot {
		return cleanup, nil
	}
	created := []string{}
	current := row.Root
	for {
		_, e := os.Lstat(current)
		if e == nil {
			break
		}
		if !os.IsNotExist(e) {
			return cleanup, errors.New("站点目录无法访问")
		}
		created = append(created, current)
		current = filepath.Dir(current)
	}
	row.RootCreatedByPanel = len(created) > 0
	cleanup = func() {
		for _, p := range created {
			_ = os.Remove(p)
		}
	}
	for i := len(created) - 1; i >= 0; i-- {
		if e := os.Mkdir(created[i], 0755); e != nil {
			cleanup()
			return func() {}, errors.New("创建站点目录失败")
		}
		if e := os.Chmod(created[i], 0755); e != nil {
			cleanup()
			return func() {}, errors.New("设置新目录权限失败")
		}
		if row.Type == "static" {
			_ = os.Chown(created[i], 33, 33)
		}
	}
	if row.Type != "static" {
		return cleanup, nil
	}
	index := filepath.Join(row.Root, "index.html")
	f, e := os.OpenFile(index, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	if os.IsExist(e) {
		return cleanup, nil
	}
	if e != nil {
		cleanup()
		return func() {}, errors.New("默认页创建失败，请检查站点目录权限")
	}
	content := defaultPageTemplates["index"]
	if len(defaultIndex) > 0 && defaultIndex[0] != "" {
		content = defaultIndex[0]
	}
	_, e = f.WriteString(content)
	if e == nil {
		e = f.Chmod(0755)
		_ = f.Chown(33, 33)
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	oldCleanup := cleanup
	cleanup = func() { _ = os.Remove(index); oldCleanup() }
	if e != nil {
		cleanup()
		return func() {}, errors.New("默认页写入失败")
	}
	return cleanup, nil
}
func (m *Module) create(c *gin.Context) {
	var in input
	if !response.Bind(c, &in) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, e := m.validate(in, store.Website{ID: store.ID(), Enabled: true})
	if e != nil {
		response.Fail(c, response.Invalid, e.Error())
		return
	}
	if row.RuntimeEnvironment != "" {
		row.Enabled = false
	}
	cleanup, e := prepareRoot(&row, m.cfg.Current().SiteRoot, "", m.readDefaultPage("index"))
	if e != nil {
		failure(c, e)
		return
	}
	c.Set("panel.auditResource", row.ID)
	c.Set("panel.auditName", row.Name)
	details := []string{"项目类型 " + row.ProjectType, "绑定域名 " + auditValue(strings.Join(websiteDomains(row), "、"))}
	if row.Root != "" {
		details = append(details, "项目目录 "+row.Root)
	}
	if row.PHPVersion != "" {
		details = append(details, "PHP 版本 "+row.PHPVersion)
	}
	auditSite(c, "", details...)
	e = m.changeProject(c.Request.Context(), &row, store.Website{}, false, func() error { return m.db.Create(&row).Error })
	if e != nil {
		cleanup()
		failure(c, e)
		return
	}
	result := m.view(row, m.running(c.Request.Context()))
	ports := []int{}
	for _, server := range siteServers(row) {
		ports = append(ports, server.Port)
	}
	result["firewallPorts"] = ports
	response.OK(c, result)
}
func (m *Module) update(c *gin.Context) {
	var in input
	if !response.Bind(c, &in) {
		return
	}
	release, e := m.tasks.Acquire("website:" + c.Param("id"))
	if e != nil {
		failure(c, e)
		return
	}
	defer release()
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.row(c)
	if !ok {
		return
	}
	row, e := m.validate(in, old)
	if e != nil {
		response.Fail(c, response.Invalid, e.Error())
		return
	}
	action, details := siteChanges(old, row)
	auditSite(c, action, details...)
	if row.SSLEnabled {
		cp, kp := m.certPaths(row.ID)
		cert, e1 := os.ReadFile(cp)
		key, e2 := os.ReadFile(kp)
		if e1 != nil || e2 != nil {
			failure(c, errors.New("已有证书无法读取，请重新上传证书"))
			return
		}
		domains := websiteDomains(row)
		if row.SSLAllowMismatch {
			domains = nil
		}
		if _, e = certificate(string(cert), string(key), domains); e != nil {
			response.Fail(c, response.Invalid, e.Error())
			return
		}
	}
	if row.Root != old.Root {
		row.RootCreatedByPanel = false
	}
	cleanup, e := prepareRoot(&row, m.cfg.Current().SiteRoot, old.Root, m.readDefaultPage("index"))
	if e != nil {
		failure(c, e)
		return
	}
	commit := func() error { return m.db.Save(&row).Error }
	if row.RuntimeEnvironment != "" {
		if row.RuntimeEnvironment != old.RuntimeEnvironment || row.RuntimeVersion == "" {
			environment, err := m.packages.RuntimeEnvironment(row.ProjectType, row.RuntimeEnvironment)
			if err != nil {
				cleanup()
				failure(c, err)
				return
			}
			row.RuntimeVersion = environment.Version
		}
		row.RuntimePending = old.ProxyTarget != "" && (old.RuntimePending || m.runtimeChanged(row, old))
		if m.render(row) == m.render(old) {
			e = commit()
		} else {
			e = m.change(c.Request.Context(), row, false, nil, nil, commit)
		}
	} else {
		e = m.changeProject(c.Request.Context(), &row, old, false, commit)
	}
	if e != nil {
		cleanup()
		failure(c, e)
		return
	}
	action, details = siteChanges(old, row)
	auditSite(c, action, details...)
	if row.Name != old.Name {
		m.trafficMu.Lock()
		delete(m.traffic, row.ID)
		m.trafficMu.Unlock()
	}
	response.OK(c, m.view(row, m.running(c.Request.Context())))
}
func (m *Module) toggle(c *gin.Context) {
	release, e := m.tasks.Acquire("website:" + c.Param("id"))
	if e != nil {
		failure(c, e)
		return
	}
	defer release()
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.row(c)
	if !ok {
		return
	}
	old := row
	row.Enabled = strings.HasSuffix(c.FullPath(), "/enable")
	if row.Enabled && row.RuntimeEnvironment != "" && row.RuntimeCommand == "" {
		response.Fail(c, response.Invalid, "请先在运行设置中保存启动配置")
		return
	}
	if e = m.validateSites(row); e != nil {
		response.Fail(c, response.Invalid, e.Error())
		return
	}
	if e = m.changeProject(c.Request.Context(), &row, old, false, func() error { return m.db.Save(&row).Error }); e != nil {
		failure(c, e)
		return
	}
	response.OK(c, m.view(row, m.running(c.Request.Context())))
}
func (m *Module) ssl(c *gin.Context) {
	var in struct {
		CertificateID       string `json:"certificateId"`
		Cert                string `json:"cert"`
		Key                 string `json:"key"`
		ForceHTTPS          bool   `json:"forceHttps"`
		AllowDomainMismatch bool   `json:"allowDomainMismatch"`
	}
	enabled := c.Request.Method == "POST"
	if enabled && !response.Bind(c, &in) {
		return
	}
	release, e := m.tasks.Acquire("website:" + c.Param("id"))
	if e != nil {
		failure(c, e)
		return
	}
	defer release()
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.row(c)
	if !ok {
		return
	}
	old := row
	var cert, key *string
	var auditCertificate string
	if enabled {
		action := "开启 SSL"
		if old.SSLEnabled {
			action = "更新 SSL"
		}
		auditSite(c, action, "强制 HTTPS "+auditSwitch(in.ForceHTTPS))
		if in.CertificateID != "" {
			in.Cert, in.Key, e = certificates.ReadPEM(m.db, m.cfg, in.CertificateID)
			if e != nil {
				failure(c, e)
				return
			}
		}
		if row.SSLEnabled {
			certPath, keyPath := m.certPaths(row.ID)
			if strings.TrimSpace(in.Cert) == "" {
				data, err := os.ReadFile(certPath)
				if err != nil {
					failure(c, err)
					return
				}
				in.Cert = string(data)
			}
			if strings.TrimSpace(in.Key) == "" {
				data, err := os.ReadFile(keyPath)
				if err != nil {
					failure(c, err)
					return
				}
				in.Key = string(data)
			}
		}
		leaf, err := certificate(in.Cert, in.Key, nil)
		if err != nil {
			response.Fail(c, response.Invalid, err.Error())
			return
		}
		auditCertificate = "证书到期时间 " + leaf.NotAfter.UTC().Format(time.RFC3339)
		missing := uncoveredDomains(leaf, websiteDomains(row))
		confirmed := in.AllowDomainMismatch
		if len(missing) > 0 && !confirmed && row.SSLAllowMismatch {
			// 当前证书页只修改强制HTTPS时不带证书ID，同一证书保留先前确认。
			path, _ := m.certPaths(row.ID)
			current, err := os.ReadFile(path)
			if err != nil {
				failure(c, err)
				return
			}
			confirmed = strings.TrimSpace(in.Cert) == strings.TrimSpace(string(current))
		}
		if len(missing) > 0 && !confirmed {
			response.Fail(c, response.Invalid, "证书未覆盖全部站点域名，请确认提示后再部署")
			return
		}
		row.SSLAllowMismatch = len(missing) > 0
		if in.CertificateID == "" {
			stored, e := certificates.Store(m.db, m.cfg, in.Cert, in.Key, "import", "", "", "")
			if e != nil {
				failure(c, e)
				return
			}
			in.CertificateID = stored.ID
		}
		cert, key = &in.Cert, &in.Key
		row.CertificateID = in.CertificateID
	}
	if !enabled {
		row.CertificateID = ""
		row.SSLAllowMismatch = false
	}
	row.SSLEnabled, row.ForceHTTPS = enabled, enabled && in.ForceHTTPS
	details := []string{
		"绑定域名 " + auditValue(strings.Join(websiteDomains(row), "、")),
		"强制 HTTPS " + auditSwitch(old.ForceHTTPS) + " → " + auditSwitch(row.ForceHTTPS),
	}
	if enabled {
		details = append(details, "证书 ID "+row.CertificateID, auditCertificate)
		if old.CertificateID != "" && old.CertificateID != row.CertificateID {
			details = append(details, "原证书 ID "+old.CertificateID)
		}
		if row.SSLAllowMismatch {
			details = append(details, "已确认继续使用域名覆盖不完整的证书")
		}
	}
	auditSite(c, "", details...)
	if e = m.validateSites(row); e != nil {
		response.Fail(c, response.Invalid, e.Error())
		return
	}
	if e = m.change(c.Request.Context(), row, false, cert, key, func() error { return m.db.Save(&row).Error }); e != nil {
		failure(c, e)
		return
	}
	response.OK(c, m.view(row, m.running(c.Request.Context())))
}
func (m *Module) reloadHandler(c *gin.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.packages.ApplicationChange("openresty", func() error {
		state, e := m.packages.Status(c.Request.Context(), "openresty")
		if e != nil {
			return e
		}
		if !state.Manageable {
			return pkgmgr.ErrUnsupported
		}
		if e = m.openRestyTest(c.Request.Context()); e != nil {
			return e
		}
		if state.Running == nil || !*state.Running {
			return errors.New("OpenResty 已停止，配置检查已通过；请通过服务菜单明确启动")
		}
		return m.reload(c.Request.Context())
	})
	if e != nil {
		failure(c, e)
		return
	}
	response.OK(c, gin.H{"running": true})
}
func (m *Module) delete(c *gin.Context) {
	deleteFiles := false
	if value, exists := c.GetQuery("deleteFiles"); exists {
		var e error
		deleteFiles, e = strconv.ParseBool(value)
		if e != nil {
			response.Fail(c, response.Invalid, "deleteFiles 必须为布尔值")
			return
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.row(c)
	if !ok {
		return
	}
	if c.Query("confirmName") != row.Name {
		response.Fail(c, response.Invalid, "确认名称与站点名称不一致")
		return
	}
	if deleteFiles {
		auditSite(c, "删除网站及目录", "永久删除目录 "+row.Root)
		allowed, reason := m.canDelete(row)
		if !allowed {
			response.Fail(c, response.Forbidden, reason)
			return
		}
		session := auth.Current(c)
		id, e := m.tasks.Submit(task.Meta{Module: "website", Action: "删除网站及目录", Target: row.Name + "\n永久删除目录 " + row.Root, ResourceID: row.ID, LockKey: "website:" + row.ID, Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(parent context.Context, log *task.Log) (any, error) {
			ctx, cancel := context.WithTimeout(parent, time.Hour)
			defer cancel()
			m.mu.Lock()
			var current store.Website
			if e := m.db.First(&current, "id = ?", row.ID).Error; e != nil {
				m.mu.Unlock()
				return nil, errors.New("站点已不存在或无法读取，未删除目录")
			}
			if current.Name != row.Name || current.Root != row.Root {
				m.mu.Unlock()
				return nil, errors.New("站点名称或目录已变更，请重新确认")
			}
			if allowed, reason := m.canDelete(current); !allowed {
				m.mu.Unlock()
				return nil, errors.New(reason)
			}
			if e := m.changeProject(ctx, &current, current, true, func() error { return m.deleteRecord(current) }); e != nil {
				m.mu.Unlock()
				log.Line(e.Error())
				return nil, e
			}
			m.mu.Unlock()
			log.Line("站点配置已删除并提交，开始清理授权目录")
			// WalkDir 不跟随符号链接，取消后保留真实残留，站点删除不伪回滚。
			paths := []string{}
			e := filepath.WalkDir(current.Root, func(path string, entry fs.DirEntry, e error) error {
				if e != nil {
					return e
				}
				if e = ctx.Err(); e != nil {
					return e
				}
				paths = append(paths, path)
				return nil
			})
			if e == nil {
				for i := len(paths) - 1; i >= 0; i-- {
					if e = ctx.Err(); e != nil {
						break
					}
					if e = os.Remove(paths[i]); e != nil {
						break
					}
				}
			}
			result := gin.H{"siteDeleted": true, "directoryDeleted": e == nil}
			if e != nil {
				result["remainingPath"] = current.Root
				log.Line("站点已删除，目录清理失败，残留路径：" + current.Root)
				return result, errors.New("站点已删除，目录清理失败，请检查任务残留路径")
			}
			return result, nil
		})
		if e != nil {
			if errors.Is(e, task.ErrBusy) {
				failure(c, e)
			} else {
				failure(c, errStorage)
			}
			return
		}
		c.Set("panel.taskSubmitted", true)
		response.OK(c, gin.H{"taskId": id})
		return
	}
	auditSite(c, "", "保留网站目录")
	release, e := m.tasks.Acquire("website:" + row.ID)
	if e != nil {
		failure(c, e)
		return
	}
	defer release()
	if e = m.changeProject(c.Request.Context(), &row, row, true, func() error { return m.deleteRecord(row) }); e != nil {
		failure(c, e)
		return
	}
	response.OK(c, gin.H{"siteDeleted": true, "directoryDeleted": false})
}

func (m *Module) deleteRecord(row store.Website) error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		if e := store.DisableTargetJobs(tx, "backup_site", row.ID, "", "静态网站已删除"); e != nil {
			return e
		}
		if e := store.DisableTargetJobs(tx, "restart_project", row.ID, "", "项目已删除"); e != nil {
			return e
		}
		return tx.Delete(&row).Error
	})
}

// 行内备注独立写入，不触发OpenResty重载或覆盖其他站点配置。
func (m *Module) saveRemark(c *gin.Context) {
	var in struct {
		Remark string `json:"remark"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !utf8.ValidString(in.Remark) || utf8.RuneCountInString(in.Remark) > 512 || strings.ContainsRune(in.Remark, 0) {
		response.Fail(c, response.Invalid, "备注最多512字，不能包含空字符")
		return
	}
	row, ok := m.row(c)
	if !ok {
		return
	}
	if err := m.db.Model(&store.Website{}).Where("id = ?", row.ID).Update("remark", in.Remark).Error; err != nil {
		failure(c, errStorage)
		return
	}
	auditSite(c, "", "备注 "+auditValue(row.Remark)+" → "+auditValue(in.Remark))
	response.OK(c, gin.H{"remark": in.Remark})
}

func (m *Module) fixPermissions(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	if row.Root == "" {
		response.Fail(c, response.Invalid, "该网站未配置站点目录")
		return
	}
	realRoot, err := m.policy.Check(row.Root, true)
	if err != nil {
		response.Fail(c, response.Invalid, "站点目录受保护或无效")
		return
	}
	uid, gid, ownerLabel := 0, 0, "root"
	if row.Type == "static" {
		uid, gid, ownerLabel = 33, 33, "www-data"
	}
	err = filepath.WalkDir(realRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if err := os.Chmod(path, 0755); err != nil {
			return err
		}
		_ = os.Chown(path, uid, gid)
		return nil
	})
	if err != nil {
		response.Fail(c, response.System, "修复目录权限失败："+response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"owner": ownerLabel, "mode": "755"})
}
