package software

import (
	"context"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Module struct {
	db       *gorm.DB
	tasks    *task.Manager
	packages *pkgmgr.Manager
	auth     *auth.Service
	webCheck func() error
	rsyncMu  sync.Mutex
	cfg      config.Config
}

func New(db *gorm.DB, cfg config.Config, tasks *task.Manager, packages *pkgmgr.Manager, a *auth.Service, webCheck func() error) *Module {
	return &Module{db: db, cfg: cfg, tasks: tasks, packages: packages, auth: a, webCheck: webCheck}
}
func (m *Module) Name() string { return "software" }
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/software")
	r.GET("/status", m.status)
	r.GET("/catalog", m.catalog)
	r.GET("/:id/environments", m.runtimeEnvironments)
	r.GET("/:id/instances", m.databaseInstances)
	r.GET("/:id/instances/:instance", m.databaseInstance)
	r.GET("/:id/instances/:instance/settings", m.databaseInstanceSettings)
	r.PUT("/:id/instances/:instance/settings", m.databaseInstanceSettings)
	r.POST("/:id/instances/:instance/action", m.databaseInstanceAction)
	r.GET("/:id/instances/:instance/password", m.databaseInstancePassword)
	r.PUT("/:id/instances/:instance/port", m.saveDatabaseInstancePort)
	r.PUT("/:id/instances/:instance/remote-access", m.saveDatabaseInstanceRemoteAccess)
	r.GET("/:id/instances/:instance/options", m.databaseInstanceOptions)
	r.PUT("/:id/instances/:instance/options", m.databaseInstanceOptions)
	r.GET("/:id/instances/:instance/log", m.databaseInstanceLog)
	r.DELETE("/:id/instances/:instance/log", m.databaseInstanceLog)
	r.GET("/:id/sources", m.sources)
	r.GET("/:id/versions", m.versions)
	r.GET("/:id/settings", m.settings)
	r.PUT("/:id/settings", m.saveSettings)
	r.PATCH("/docker/settings", m.saveDockerRegistrySettings)
	r.GET("/:id/logs", m.logs)
	m.clamRoutes(r)
	m.ftpRoutes(r)
	r.GET("/rsync/visual", m.rsyncVisual)
	r.PUT("/rsync/visual", m.rsyncVisual)
	r.GET("/rsync/workspace", m.rsyncWorkspace)
	r.PUT("/rsync/workspace", m.rsyncWorkspace)
	r.POST("/rsync/send/:id/run", m.runRsyncSender)
	m.fail2banRoutes(r)
	r.GET("/:id/guard", m.applicationGuard)
	r.PUT("/:id/guard", m.saveApplicationGuard)
	r.POST("/install", m.install)
	r.POST("/update", m.update)
	m.nginxRoutes(r)
	m.phpRoutes(r)
	r.POST("/check-updates", m.checkUpdates)
	r.POST("/uninstall", m.uninstall)
	r.POST("/service", m.service)
	r.GET("/tasks/:taskId", func(c *gin.Context) { m.tasks.Detail(c, "software") })
}
func fail(c *gin.Context, e error) {
	switch {
	case errors.Is(e, pkgmgr.ErrUnsupported):
		response.Fail(c, response.Forbidden, "此系统或软件不可受控管理；需要支持的发行版、systemd 和可确认的安装来源")
	case errors.Is(e, pkgmgr.ErrBusy), errors.Is(e, task.ErrBusy):
		response.Fail(c, response.Conflict, "软件安装或资源操作正在运行，请稍后重试")
	default:
		response.Fail(c, response.System, "软件操作失败，请检查任务日志、软件包锁和服务状态")
	}
}
func (m *Module) status(c *gin.Context) {
	rows := make([]pkgmgr.State, 0, 3)
	for _, id := range []string{"openresty", "mysql", "docker"} {
		state, e := m.packages.Status(c.Request.Context(), id)
		if e != nil {
			fail(c, e)
			return
		}
		rows = append(rows, state)
	}
	var active store.Task
	e := m.db.Omit("result").Where("module = ? AND status = ?", "software", "running").Order("started_at DESC").First(&active).Error
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		response.Fail(c, response.Internal, "")
		return
	}
	var summary any
	if active.ID != "" {
		summary = task.Summary(active)
	}
	response.OK(c, gin.H{"list": rows, "activeTask": summary})
}
func (m *Module) catalog(c *gin.Context) {
	var active []store.Task
	if e := m.db.Omit("result").Where("module = ? AND status = ?", "software", "running").Order("started_at DESC").Find(&active).Error; e != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	summaries := make([]map[string]any, 0, len(active))
	for _, row := range active {
		summaries = append(summaries, task.Summary(row))
	}
	var latest any
	if len(summaries) > 0 {
		latest = summaries[0]
	}
	ctx := c.Request.Context()
	var projects []store.Website
	if err := m.db.WithContext(ctx).
		Select("id", "project_type").
		Where("project_type IN ? AND runtime_version <> '' AND proxy_target <> ''", []string{"python", "nodejs", "go"}).
		Find(&projects).Error; err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	instanceCounts, runningCounts := map[string]int{}, map[string]int{}
	var runningProjects map[string]bool
	if len(projects) > 0 {
		runningProjects = m.packages.RunningProjects(ctx)
	}
	for _, row := range projects {
		instanceCounts[row.ProjectType]++
		if runningProjects[row.ID] {
			runningCounts[row.ProjectType]++
		}
	}
	list := m.packages.Catalog(ctx)
	var phpSites []store.Website
	if err := m.db.WithContext(ctx).
		Select("name", "php_version").
		Where("php_version <> ''").
		Order("name ASC").
		Find(&phpSites).Error; err == nil && len(phpSites) > 0 {
		sitesByPHP := make(map[string][]string)
		for _, site := range phpSites {
			key := "php-" + site.PHPVersion
			sitesByPHP[key] = append(sitesByPHP[key], site.Name)
		}
		for i := range list {
			if sites := sitesByPHP[list[i].ID]; len(sites) > 0 {
				list[i].UsedBySites = sites
			}
		}
	}
	response.OK(c, gin.H{
		"list": list, "activeTasks": summaries, "activeTask": latest,
		"projectInstanceCounts": instanceCounts, "projectRunningCounts": runningCounts,
	})
}

func (m *Module) phpUsedBySites(ctx context.Context, appID string) ([]string, error) {
	var sites []store.Website
	if err := m.db.WithContext(ctx).
		Select("name").
		Where("php_version = ?", strings.TrimPrefix(appID, "php-")).
		Order("name ASC").
		Find(&sites).Error; err != nil {
		return nil, err
	}
	names := make([]string, 0, len(sites))
	for _, s := range sites {
		if s.Name != "" {
			names = append(names, s.Name)
		}
	}
	return names, nil
}

func formatPHPInUseError(appID string, sites []string) string {
	const maxShow = 3
	label := pkgmgr.Name(appID)
	if len(sites) <= maxShow {
		return fmt.Sprintf("无法卸载 %s：当前正被网站 %s 使用，请先切换这些网站的 PHP 版本或改为纯静态", label, strings.Join(sites, "、"))
	}
	return fmt.Sprintf("无法卸载 %s：当前正被网站 %s 等 %d 个站点使用，请先切换这些网站的 PHP 版本或改为纯静态", label, strings.Join(sites[:maxShow], "、"), len(sites))
}
func (m *Module) install(c *gin.Context) {
	m.change(c, "install")
}
func (m *Module) uninstall(c *gin.Context) {
	m.change(c, "uninstall")
}
func (m *Module) update(c *gin.Context) { m.change(c, "update") }
func (m *Module) change(c *gin.Context, action string) {
	uninstall := action == "uninstall"
	var in struct {
		ID            string `json:"id"`
		Version       string `json:"version"`
		ConfirmName   string `json:"confirmName"`
		Source        string `json:"source"`
		Mirror        string `json:"mirror"`
		EnvironmentID string `json:"environmentId"`
		NewInstance   bool   `json:"newInstance"`
		InstanceName  string `json:"instanceName"`
		InstanceID    string `json:"instanceId"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !pkgmgr.ValidID(in.ID) || in.ID == "nginx" {
		fail(c, pkgmgr.ErrUnsupported)
		return
	}
	if uninstall && in.ConfirmName != pkgmgr.Name(in.ID) {
		response.Fail(c, response.Invalid, "请输入完整软件名称以确认卸载")
		return
	}
	projectRuntime := pkgmgr.IsProjectRuntime(in.ID)
	if uninstall && projectRuntime && in.EnvironmentID == "" {
		response.Fail(c, response.Invalid, "请选择要卸载的运行环境")
		return
	}
	if uninstall && pkgmgr.IsPHP(in.ID) {
		sites, err := m.phpUsedBySites(c.Request.Context(), in.ID)
		if err != nil {
			response.Fail(c, response.Internal, "")
			return
		}
		if len(sites) > 0 {
			response.Fail(c, response.Conflict, formatPHPInUseError(in.ID, sites))
			return
		}
	}
	if in.NewInstance {
		in.InstanceName = strings.TrimSpace(in.InstanceName)
		if action != "install" || !pkgmgr.IsDatabaseContainer(in.ID) || len(in.InstanceName) > 100 || strings.IndexFunc(in.InstanceName, unicode.IsControl) >= 0 {
			response.Fail(c, response.Invalid, "数据库实例名称或操作无效")
			return
		}
	}
	if in.InstanceID != "" && (action != "update" || !pkgmgr.IsDatabaseContainer(in.ID)) {
		response.Fail(c, response.Invalid, "数据库实例操作无效")
		return
	}
	var choice pkgmgr.SourceSelection
	if !uninstall {
		var e error
		choice, e = m.packages.SelectSource(in.ID, pkgmgr.SourceSelection{Source: in.Source, Mirror: in.Mirror})
		if e != nil {
			response.Fail(c, response.Invalid, response.Summary(e.Error()))
			return
		}
		if in.Version == "" && in.Source != "" {
			response.Fail(c, response.Invalid, "请先选择下载源和软件版本")
			return
		}
	}
	if _, e := m.packages.Detect(); e != nil && !pkgmgr.UsesDocker(in.ID) {
		fail(c, e)
		return
	}
	session := auth.Current(c)
	lockID := in.ID
	if in.ID == "mysql" || in.ID == "mariadb" {
		lockID = "database-server"
	}
	appLabel := pkgmgr.Name(in.ID)
	if in.Version != "" {
		appLabel += " " + in.Version
	}
	targetDesc := map[string]string{
		"install":   "安装应用[" + appLabel + "]",
		"uninstall": "卸载应用[" + pkgmgr.Name(in.ID) + "]",
		"update":    "更新应用[" + appLabel + "]",
	}[action]
	if targetDesc == "" {
		targetDesc = action + "[" + appLabel + "]"
	}
	id, e := m.tasks.Submit(task.Meta{Module: "software", Action: action, Target: targetDesc, ResourceID: in.ID, LockKey: "software-install:" + lockID, Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		var state pkgmgr.State
		var e error
		if uninstall && projectRuntime {
			query := m.db.Model(&store.Website{}).Where("project_type = ? AND runtime_environment = ?", in.ID, in.EnvironmentID)
			var count int64
			if e = query.Count(&count).Error; e != nil {
				return state, e
			}
			if count > 0 {
				return state, errors.New("该运行环境仍被网站使用")
			}
			return nil, m.packages.RemoveRuntimeEnvironment(in.ID, in.EnvironmentID)
		}
		if action == "install" && projectRuntime {
			item, err := m.packages.InstallRuntimeEnvironment(ctx, in.ID, in.Version, log.Line, choice)
			return gin.H{"environmentId": item.ID, "version": item.Version}, err
		}
		if in.NewInstance {
			return nil, m.packages.InstallDatabaseInstance(ctx, in.ID, in.InstanceName, in.Version, log.Line, choice)
		}
		if action == "uninstall" && pkgmgr.IsPHP(in.ID) {
			sites, err := m.phpUsedBySites(ctx, in.ID)
			if err != nil {
				return state, err
			}
			if len(sites) > 0 {
				return state, errors.New(formatPHPInUseError(in.ID, sites))
			}
		}
		if uninstall {
			state, e = m.packages.Uninstall(ctx, in.ID, log.Line)
		} else if action == "update" && in.InstanceID != "" {
			e = m.packages.UpdateDatabaseInstance(ctx, in.ID, in.InstanceID, in.Version, log.Line, choice)
		} else if action == "update" {
			state, e = m.packages.UpdateRelease(ctx, in.ID, in.Version, log.Line, choice)
		} else {
			if in.Version != "" {
				state, e = m.packages.InstallRelease(ctx, in.ID, in.Version, log.Line, choice)
			} else {
				state, e = m.packages.Install(ctx, in.ID, log.Line)
			}
		}
		if e != nil {
			log.Line(response.Summary(e.Error()))
			return state, e
		}
		if !uninstall && in.ID == "docker" {
			m.packages.EnsureDefaultDockerMirrors(ctx)
		}
		if !uninstall && in.ID == "openresty" {
			if e = m.webCheck(); e != nil {
				log.Line(e.Error())
				return state, e
			}
		}
		if uninstall {
			log.Line("卸载完成并重新检测软件状态；已保留配置和数据")
		} else {
			log.Line("软件操作完成，状态已重新检测")
		}
		return state, nil
	})
	if e != nil {
		fail(c, e)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
func (m *Module) service(c *gin.Context) {
	var in struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if !response.Bind(c, &in) {
		return
	}
	state, e := m.packages.Service(c.Request.Context(), in.ID, in.Action)
	if e != nil {
		if errors.Is(e, pkgmgr.ErrUnsupported) || errors.Is(e, pkgmgr.ErrBusy) {
			fail(c, e)
		} else {
			response.Fail(c, response.System, response.Summary(e.Error()))
		}
		return
	}
	response.OK(c, gin.H{"running": state.Running})
}

func (m *Module) versions(c *gin.Context) {
	id := c.Param("id")
	if !pkgmgr.ValidID(id) {
		response.Fail(c, response.Invalid, "软件不存在")
		return
	}
	choice, e := m.packages.SelectSource(id, pkgmgr.SourceSelection{Source: c.Query("source"), Mirror: c.Query("mirror")})
	if e != nil {
		response.Fail(c, response.Invalid, response.Summary(e.Error()))
		return
	}
	refresh := c.Query("refresh") == "true"
	if !refresh && pkgmgr.UsesDocker(id) && !pkgmgr.IsProjectRuntime(id) {
		m.packages.RefreshContainerIfEmpty(c.Request.Context(), id)
	}
	rows, e := m.packages.Releases(c.Request.Context(), id, refresh, choice)
	if e != nil {
		response.Fail(c, response.System, response.Summary(e.Error()))
		return
	}
	if !pkgmgr.UsesDocker(id) {
		state, err := m.packages.Status(c.Request.Context(), id)
		if err == nil && state.Installed && state.Version != nil && !slices.ContainsFunc(rows, func(row pkgmgr.Release) bool { return row.Version == *state.Version }) {
			rows = append(append([]pkgmgr.Release(nil), rows...), pkgmgr.Release{Version: *state.Version, Source: "当前已安装", Method: "compile"})
		}
	}
	meta := m.packages.VersionInfo(id, choice)
	response.OK(c, gin.H{"list": rows, "syncedAt": meta.SyncedAt, "notice": meta.Notice})
}
func (m *Module) sources(c *gin.Context) {
	id := c.Param("id")
	if !pkgmgr.ValidID(id) {
		response.Fail(c, response.Invalid, "软件不存在")
		return
	}
	out := gin.H{"list": pkgmgr.SourceOptions(id), "current": m.packages.InstalledSource(id)}
	if pkgmgr.UsesDocker(id) {
		out["container"] = true
		out["mirrors"] = m.packages.DockerMirrors()
	}
	response.OK(c, out)
}
func (m *Module) settings(c *gin.Context) {
	data, e := m.packages.Settings(c.Request.Context(), c.Param("id"), c.Query("kind"))
	if e != nil {
		response.Fail(c, response.System, response.Summary(e.Error()))
		return
	}
	response.OK(c, data)
}
func (m *Module) saveSettings(c *gin.Context) {
	var in struct {
		Content  string `json:"content"`
		Revision string `json:"revision"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Content) > 1<<20 || len(in.Revision) != 64 {
		response.Fail(c, response.Invalid, "配置内容或修订号无效")
		return
	}
	if e := m.packages.SaveSettings(c.Request.Context(), c.Param("id"), in.Content, in.Revision, c.Query("kind")); e != nil {
		response.Fail(c, response.System, response.Summary(e.Error()))
		return
	}
	response.OK(c, gin.H{"saved": true})
}

func (m *Module) logs(c *gin.Context) {
	text, e := m.packages.Logs(c.Request.Context(), c.Param("id"))
	if e != nil {
		response.Fail(c, response.System, response.Summary(e.Error()))
		return
	}
	response.OK(c, gin.H{"content": text})
}

func (m *Module) checkUpdates(c *gin.Context) {
	states := m.packages.Catalog(c.Request.Context())
	type result struct {
		ID     string `json:"id"`
		Notice string `json:"notice"`
	}
	category := strings.TrimSpace(c.Query("category"))
	eligible := []pkgmgr.State{}
	for _, state := range states {
		if !state.Updateable {
			continue
		}
		if category != "" && state.Category != category {
			continue
		}
		eligible = append(eligible, state)
	}
	rows := make([]result, len(eligible))
	for i, state := range eligible {
		if c.Request.Context().Err() != nil {
			break
		}
		_, e := m.packages.Releases(c.Request.Context(), state.ID, true)
		rows[i].ID = state.ID
		if e != nil {
			rows[i].Notice = response.Summary(e.Error())
		} else {
			rows[i].Notice = m.packages.VersionInfo(state.ID).Notice
		}
		if i+1 < len(eligible) {
			select {
			case <-c.Request.Context().Done():
			case <-time.After(300 * time.Millisecond):
			}
		}
	}
	response.OK(c, gin.H{"list": rows})
}
