package plugin

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/module/sysinfo"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"ezpanel/internal/version"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Module struct {
	db            *gorm.DB
	cfg           config.Config
	tasks         *task.Manager
	auth          *auth.Service
	mu            sync.Mutex
	statuses      map[string]string
	startupErrors map[string]string
	core          string
	runs          map[string]*running
	crashes       map[string][]time.Time
	closed        chan struct{}
	closeOnce     sync.Once
	generations   map[string]uint64
	hostURL       string
	hostPort      int
	host          *http.Server
	metrics       *sysinfo.Module
	policy        *pathsafe.Policy
	services      Services
	scheduleMu    sync.Mutex
	schedules     map[string]func()
	eventWake     chan struct{}
	fileList      directoryListCache
	startupWake   chan struct{}
	background    sync.WaitGroup
}
type upload struct {
	Owner         string    `json:"owner"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Manifest      Manifest  `json:"manifest"`
	RevokedGrants []string  `json:"revokedGrants,omitempty"`
}
type PermissionChanges struct {
	Permissions []string `json:"permissions"`
	ExecAllow   []string `json:"execAllow"`
	FSRoots     []string `json:"fsRoots"`
}

func New(db *gorm.DB, cfg config.Config, tasks *task.Manager, a *auth.Service, metrics *sysinfo.Module) (*Module, error) {
	core := version.Version
	if core == "0.0.0-dev" {
		core = CoreVersion
	}
	if _, e := versionParts(core); e != nil {
		return nil, errors.New("发布版本必须为 x.y.z")
	}
	policy, e := pathsafe.New(cfg.InstallDir)
	if e != nil {
		return nil, e
	}
	m := &Module{metrics: metrics, policy: policy, db: db, cfg: cfg, tasks: tasks, auth: a, statuses: map[string]string{}, startupErrors: map[string]string{}, core: core, runs: map[string]*running{}, crashes: map[string][]time.Time{}, closed: make(chan struct{}), generations: map[string]uint64{}, schedules: map[string]func(){}, eventWake: make(chan struct{}, 1), startupWake: make(chan struct{}, 1)}
	for _, p := range []string{m.packages(), m.private(), m.staging(), filepath.Join(m.packages(), ".backup"), filepath.Dir(m.logPath(""))} {
		mode := os.FileMode(0755)
		if strings.HasPrefix(filepath.Base(p), ".") || p == m.private() || p == filepath.Dir(m.logPath("")) {
			mode = 0700
		}
		if p == m.private() {
			mode = 0711
		}
		if e := os.MkdirAll(p, mode); e != nil {
			return nil, e
		}
		if e := os.Chmod(p, mode); e != nil {
			return nil, e
		}
	}
	for _, stage := range []string{m.activeUploads(), m.dataStage()} {
		if e := os.RemoveAll(stage); e != nil {
			return nil, e
		}
		if e := os.Mkdir(stage, 0700); e != nil {
			return nil, e
		}
	}
	if e := m.cleanUploads(); e != nil {
		return nil, e
	}
	if e := m.listenHost(); e != nil {
		return nil, e
	}
	return m, nil
}

func (m *Module) Start(services Services) error {
	m.services = services
	var rows []store.Plugin
	if e := m.db.Find(&rows).Error; e != nil {
		return e
	}
	for _, p := range rows {
		if p.Operation != nil {
			m.setStatus(p.ID, "recovering")
		} else if p.Enabled {
			m.setStatus(p.ID, "waiting")
		}
	}
	m.background.Go(func() { m.startOnBoot(rows) })
	m.background.Go(m.dispatchEvents)
	return nil
}
func (m *Module) packages() string      { return m.cfg.Layout().PluginsDir() }
func (m *Module) private() string       { return m.cfg.Layout().PluginDataDir() }
func (m *Module) staging() string       { return filepath.Join(m.packages(), ".staging") }
func (m *Module) activeUploads() string { return filepath.Join(m.staging(), ".active") }
func (m *Module) dataStage() string     { return filepath.Join(m.private(), ".restore") }
func (m *Module) logPath(id string) string {
	return filepath.Join(m.cfg.DataDir(), "plugin-logs", id+".log")
}
func (m *Module) Name() string { return "plugins" }

// ManagementPorts 返回当前 Host API 和运行中插件的监听端口快照。
func (m *Module) ManagementPorts() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	ports := make([]int, 0, len(m.runs)+1)
	if m.hostPort != 0 {
		ports = append(ports, m.hostPort)
	}
	for _, run := range m.runs {
		ports = append(ports, run.port)
	}
	return ports
}

func (m *Module) Routes(g *gin.RouterGroup) {
	g.GET("/plugin/:id/ui/:version/*asset", m.ui)
	g.Any("/plugin/:id/api/*rest", m.proxy)
	vault := g.Group("/api/plugin-credentials")
	vault.GET("", m.listCredentials)
	vault.POST("", m.saveCredential)
	vault.PUT("/:credential", m.saveCredential)
	vault.DELETE("/:credential", m.deleteCredential)
	r := g.Group("/api/plugins")
	r.GET("", m.list)
	r.GET("/:id", m.detail)
	r.POST("/:id/ui-session", m.frameSession)
	r.POST("/:id/ui-isolation", m.setUIIsolation)
	r.GET("/:id/tasks/:task", m.frameTask)
	r.POST("/inspect", m.inspect)
	r.POST("/install", m.install)
	r.POST("/:id/enable", m.enable)
	r.POST("/:id/disable", m.disable)
	r.POST("/:id/restart", func(c *gin.Context) { m.lifecycle(c, "restart") })
	r.PUT("/:id/config", m.configure)
	r.GET("/:id/logs", m.logs)
	r.POST("/:id/upgrade", m.upgrade)
	r.GET("/:id/rollback", m.rollbackInfo)
	r.POST("/:id/rollback", m.rollbackVersion)
	r.POST("/:id/uninstall", m.uninstall)
	r.GET("/:id/capabilities", m.capabilities)
	r.GET("/:id/choices/:kind", m.resourceChoices)
	r.PUT("/:id/capabilities", m.saveCapabilities)
	r.PUT("/:id/permissions", m.savePermissions)
	r.POST("/:id/jobs/:job/run", m.runJob)
	r.GET("/site-actions/:site", m.siteActions)
	r.GET("/resource-actions/:kind", m.resourceActions)
}
func (m *Module) status(p store.Plugin) string {
	return m.statusByID(p.ID)
}

func (m *Module) statusByID(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.statuses[id]; s != "" {
		return s
	}
	return "disabled"
}
func (m *Module) cleanUploads() error {
	entries, e := os.ReadDir(m.staging())
	if e != nil {
		return e
	}
	for _, f := range entries {
		if !f.IsDir() || f.Name() == ".active" {
			continue
		}
		dir := filepath.Join(m.staging(), f.Name())
		b, e := os.ReadFile(filepath.Join(dir, "upload.json"))
		var u upload
		info, statErr := f.Info()
		expired := statErr == nil && time.Since(info.ModTime()) > 10*time.Minute
		if e == nil && json.Unmarshal(b, &u) == nil {
			expired = time.Now().After(u.ExpiresAt)
		}
		if expired {
			if e := os.RemoveAll(dir); e != nil {
				return e
			}
		}
	}
	return nil
}
func (m *Module) list(c *gin.Context) {
	var rows []store.Plugin
	if m.db.Select("id, name, version, type, enabled, manifest, installed_at").Order("installed_at").Find(&rows).Error != nil {
		response.Fail(c, 5000, "")
		return
	}
	var activeTasks []store.Task
	if m.db.Select("id, resource_id").Where("module = ? AND status = ?", "plugins", "running").Find(&activeTasks).Error != nil {
		response.Fail(c, 5000, "")
		return
	}
	activeByPlugin := make(map[string]string, len(activeTasks))
	for _, t := range activeTasks {
		if _, exists := activeByPlugin[t.ResourceID]; !exists {
			activeByPlugin[t.ResourceID] = t.ID
		}
	}
	out := make([]gin.H, 0, len(rows))
	for _, p := range rows {
		var manifest Manifest
		if json.Unmarshal([]byte(p.Manifest), &manifest) != nil {
			response.Fail(c, 5000, "")
			return
		}
		openMode := manifest.OpenMode
		if openMode == "" {
			openMode = "embed"
		}
		uiMode := manifest.UIMode
		if uiMode == "" {
			uiMode = "isolated"
		}
		out = append(out, gin.H{
			"id": p.ID, "name": p.Name, "version": p.Version, "type": p.Type, "enabled": p.Enabled,
			"runtimeStatus": m.status(p), "runtimeError": m.startupError(p.ID), "menu": manifest.Menu, "settings": manifest.Settings,
			"description": manifest.Description, "author": manifest.Author, "homepage": manifest.Homepage, "openMode": openMode,
			"installedAt": p.InstalledAt, "activeTaskId": activeByPlugin[p.ID], "uiMode": uiMode,
			"hasCapabilities":      hasCapabilities(manifest),
			"fileAction":           manifest.FileAction,
			"dashboard":            manifest.Dashboard,
			"notificationProvider": manifest.NotificationProvider,
			"dnsProvider":          manifest.DNSProvider,
			"ssoProvider":          manifest.SSOProvider,
			"resourceActions":      manifest.ResourceActions,
		})
	}
	response.OK(c, out)
}

func (m *Module) SSOProvider(id string) (string, bool) {
	var row store.Plugin
	var manifest Manifest
	if m.db.Select("id, manifest").Take(&row, "id = ? AND enabled = ?", id, true).Error != nil ||
		m.status(row) != "running" || json.Unmarshal([]byte(row.Manifest), &manifest) != nil || manifest.SSOProvider == nil {
		return "", false
	}
	return manifest.SSOProvider.Title, true
}
func (m *Module) row(c *gin.Context) (store.Plugin, bool) {
	var p store.Plugin
	e := m.db.First(&p, "id = ?", c.Param("id")).Error
	if e != nil {
		code := response.Internal
		if errors.Is(e, gorm.ErrRecordNotFound) {
			code = response.NotFound
		}
		response.Fail(c, code, "")
		return p, false
	}
	return p, true
}
func (m *Module) detail(c *gin.Context) {
	p, ok := m.row(c)
	if !ok {
		return
	}
	var mf Manifest
	var values map[string]string
	if json.Unmarshal([]byte(p.Manifest), &mf) != nil || json.Unmarshal([]byte(p.Config), &values) != nil {
		response.Fail(c, 5000, "")
		return
	}
	cfg := map[string]any{}
	for _, v := range mf.Env {
		if v.Secret {
			cfg[v.Key] = gin.H{"isSet": values[v.Key] != ""}
		} else {
			cfg[v.Key] = gin.H{"value": values[v.Key]}
		}
	}
	grant, _ := m.grant(p.ID)
	response.OK(c, gin.H{
		"manifest":            publicManifest(mf),
		"accessPreview":       previewManifestWithDisabled(mf, grant.DisabledPermissions, true),
		"disabledPermissions": grant.DisabledPermissions,
		"config":              cfg,
		"enabled":             p.Enabled,
		"runtimeStatus":       m.status(p),
		"installedAt":         p.InstalledAt,
	})
}

func (m *Module) savePermissions(c *gin.Context) {
	var in struct {
		DisabledPermissions []string `json:"disabledPermissions"`
	}
	if !response.Bind(c, &in) {
		return
	}
	id := c.Param("id")
	release, err := m.tasks.Acquire("plugin:" + id)
	if err != nil {
		response.Fail(c, response.Conflict, "插件忙")
		return
	}
	defer release()
	p, ok := m.row(c)
	if !ok {
		return
	}
	if p.Operation != nil {
		response.Fail(c, response.Conflict, "插件存在未恢复的操作记录")
		return
	}
	var mf Manifest
	if json.Unmarshal([]byte(p.Manifest), &mf) != nil {
		response.Fail(c, response.System, "无法读取插件清单")
		return
	}
	disabled := slices.Clone(in.DisabledPermissions)
	for _, code := range disabled {
		if !slices.Contains(mf.Permissions, code) {
			response.Fail(c, response.Invalid, "包含插件未声明的权限："+code)
			return
		}
	}
	slices.Sort(disabled)
	disabled = slices.Compact(disabled)
	existing, err := loadTransitionGrant(m.db, id)
	if err != nil {
		response.Fail(c, response.System, "无法读取插件当前授权")
		return
	}
	existing.DisabledPermissions = disabled
	if err := m.saveGrants(id, mf, existing); err != nil {
		response.Fail(c, response.System, err.Error())
		return
	}
	m.replaceSchedules(id, mf, existing)
	response.OK(c, gin.H{"disabledPermissions": disabled})
}
func (m *Module) inspect(c *gin.Context) {
	m.mu.Lock()
	e := m.cleanUploads()
	m.mu.Unlock()
	if e != nil {
		response.Fail(c, 5000, "")
		return
	}
	// multipart 的边界开销独立限制；压缩包本体仍按真实写入字节限制。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxUpload+(1<<20))
	reader, e := c.Request.MultipartReader()
	if e != nil {
		response.Fail(c, 3001, "需要 multipart ZIP 文件")
		return
	}
	id := store.ID()
	dir := filepath.Join(m.activeUploads(), id)
	if e = os.Mkdir(dir, 0700); e != nil {
		response.Fail(c, 5000, "")
		return
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(dir)
		}
	}()
	archive := filepath.Join(dir, "package.zip")
	digest := sha256.New()
	var signature []byte
	found := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var maxBytes *http.MaxBytesError
			if errors.As(err, &maxBytes) {
				response.Fail(c, response.TooLarge, "上传文件超过限额")
			} else {
				response.Fail(c, response.Invalid, "上传内容格式无效或连接已中断")
			}
			return
		}
		if part.FormName() == "signature" && part.FileName() != "" && signature == nil {
			signature, err = io.ReadAll(io.LimitReader(part, ed25519.SignatureSize+1))
			if err != nil || len(signature) != ed25519.SignatureSize {
				response.Fail(c, response.Invalid, "签名文件须为 64 字节的 Ed25519 原始签名")
				return
			}
			part.Close()
			continue
		}
		if part.FormName() != "file" || part.FileName() == "" || found {
			part.Close()
			response.Fail(c, 3001, "仅允许一个 ZIP 和一个可选签名文件")
			return
		}
		found = true
		f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			response.Fail(c, 5000, "")
			return
		}
		n, copyErr := io.Copy(io.MultiWriter(f, digest), io.LimitReader(part, MaxUpload+1))
		part.Close()
		closeErr := f.Close()
		if n > MaxUpload {
			response.Fail(c, response.TooLarge, "压缩包不得超过 200MiB")
			return
		}
		if copyErr != nil || closeErr != nil {
			response.Fail(c, 3001, "上传未完成")
			return
		}
	}
	if !found {
		response.Fail(c, 3001, "缺少 file")
		return
	}
	contents := filepath.Join(dir, "contents")
	if e = os.Mkdir(contents, 0755); e != nil {
		response.Fail(c, 5000, "")
		return
	}
	if e = unpack(archive, contents); e != nil {
		code := 3001
		if errors.Is(e, ErrLimit) {
			code = response.TooLarge
		}
		response.Fail(c, code, e.Error())
		return
	}
	os.Remove(archive)
	f, e := os.Open(filepath.Join(contents, "plugin.yaml"))
	if e != nil {
		response.Fail(c, 3001, "缺少 plugin.yaml")
		return
	}
	mf, e := ReadManifest(f, contents, m.cfg.InstallDir, m.cfg.Dev, m.core)
	f.Close()
	if e != nil {
		code := 3001
		if errors.Is(e, ErrCompatibility) {
			code = 3002
		}
		response.Fail(c, code, e.Error())
		return
	}
	signed, e := m.verifySignature(mf.ID, digest.Sum(nil), signature)
	if e != nil {
		response.Fail(c, response.Invalid, e.Error())
		return
	}
	if e := m.checkDependencies(mf, false); e != nil {
		response.Fail(c, response.Conflict, e.Error())
		return
	}
	changes := PermissionChanges{Permissions: mf.Permissions, ExecAllow: mf.ExecAllow, FSRoots: mf.FSRoots}
	var revoked []string
	var old store.Plugin
	e = m.db.First(&old, "id = ?", mf.ID).Error
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		response.Fail(c, 5000, "")
		return
	}
	installedVersion := ""
	if e == nil {
		installedVersion = old.Version
		var before Manifest
		if json.Unmarshal([]byte(old.Manifest), &before) != nil {
			response.Fail(c, 5000, "")
			return
		}
		if before.Sandbox != nil && mf.Type == "process" && mf.Sandbox == nil {
			if slices.Contains(mf.Permissions, "exec.run") {
				response.Fail(c, response.Invalid, "已启用进程隔离的插件无法升级到特权命令模式")
				return
			}
			mf.Sandbox = before.Sandbox
		}
		if old.Type == "static" && mf.Type == "process" && mf.Sandbox == nil && runtime.GOOS == "linux" && !slices.Contains(mf.Permissions, "exec.run") {
			mf.Sandbox = &Sandbox{}
		}
		changes = permissionChanges(before, mf)
		revoked, e = m.grantPreview(mf.ID, before, mf)
		if e != nil {
			response.Fail(c, response.System, "无法检查升级对授权的影响")
			return
		}
		if before.UIMode == "legacy" {
			mf.UIMode = "legacy"
		} else {
			mf.UIMode = "isolated"
		}
	} else {
		if mf.Type == "process" && mf.Sandbox == nil && runtime.GOOS == "linux" && !slices.Contains(mf.Permissions, "exec.run") {
			mf.Sandbox = &Sandbox{}
		}
		mf.UIMode = "isolated"
	}
	u := upload{Owner: auth.Current(c).User.Username, ExpiresAt: time.Now().UTC().Add(10 * time.Minute), Manifest: mf, RevokedGrants: revoked}
	b, _ := json.Marshal(u)
	if e = os.WriteFile(filepath.Join(dir, "upload.json"), b, 0600); e != nil {
		response.Fail(c, 5000, "")
		return
	}
	ready := filepath.Join(m.staging(), id)
	if e = os.Rename(dir, ready); e != nil {
		response.Fail(c, 5000, "")
		return
	}
	keep = true
	time.AfterFunc(time.Until(u.ExpiresAt), func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, err := os.Stat(filepath.Join(ready, "upload.json")); err == nil {
			os.RemoveAll(ready)
		}
	})
	response.OK(c, gin.H{"uploadId": id, "expiresAt": u.ExpiresAt, "installedVersion": installedVersion, "manifest": publicManifest(mf), "accessPreview": previewManifest(mf), "permissionChanges": changes, "revokedGrants": revoked, "signatureVerified": signed})
}
func permissionChanges(old, next Manifest) PermissionChanges {
	diff := func(before, after []string, roots bool) []string {
		out := []string{}
		for _, v := range after {
			covered := false
			for _, b := range before {
				if v == b || (roots && within(v, b)) {
					covered = true
					break
				}
			}
			if !covered {
				out = append(out, v)
			}
		}
		return out
	}
	return PermissionChanges{diff(old.Permissions, next.Permissions, false), diff(old.ExecAllow, next.ExecAllow, false), diff(old.FSRoots, next.FSRoots, true)}
}
func (m *Module) consume(c *gin.Context, id string) (upload, string, bool) {
	var u upload
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		response.Fail(c, 3001, "uploadId 无效")
		return u, "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dir := filepath.Join(m.staging(), id)
	b, e := os.ReadFile(filepath.Join(dir, "upload.json"))
	if e != nil || json.Unmarshal(b, &u) != nil || u.Owner != auth.Current(c).User.Username || time.Now().After(u.ExpiresAt) {
		response.Fail(c, 3001, "上传已过期、已消费或不属于当前管理员")
		return u, "", false
	}
	active := filepath.Join(m.activeUploads(), id)
	if e = os.Rename(dir, active); e != nil {
		response.Fail(c, 5000, "")
		return u, "", false
	}
	return u, active, true
}
func (m *Module) install(c *gin.Context) {
	var in struct {
		UploadID string `json:"uploadId"`
	}
	if !response.Bind(c, &in) {
		return
	}
	u, dir, ok := m.consume(c, in.UploadID)
	if !ok {
		return
	}
	mf := u.Manifest
	var count int64
	if e := m.db.Model(&store.Plugin{}).Where("id = ?", mf.ID).Count(&count).Error; e != nil {
		os.RemoveAll(dir)
		response.Fail(c, 5000, "")
		return
	}
	if count > 0 {
		os.RemoveAll(dir)
		response.Fail(c, 3002, "插件已安装")
		return
	}
	session := auth.Current(c)
	id, e := m.tasks.Submit(task.Meta{Module: "plugins", Action: "install", Target: mf.ID, ResourceID: mf.ID, LockKey: "plugin:" + mf.ID, Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		defer os.RemoveAll(dir)
		if err := m.checkDependencies(mf, false); err != nil {
			return nil, err
		}
		var count int64
		if e := m.db.Model(&store.Plugin{}).Where("id = ?", mf.ID).Count(&count).Error; e != nil {
			return nil, e
		}
		if count > 0 {
			return nil, errors.New("插件已安装")
		}
		dst := filepath.Join(m.packages(), mf.ID)
		if _, e := os.Lstat(dst); !os.IsNotExist(e) {
			return nil, errors.New("包目录已存在或不可访问")
		}
		contents := filepath.Join(dir, "contents")
		for _, entry := range mf.Entry {
			if e := os.Chmod(filepath.Join(contents, entry), 0755); e != nil {
				return nil, e
			}
		}
		values := map[string]string{}
		for _, v := range mf.Env {
			value := v.Default
			if v.Secret && value != "" {
				var e error
				value, e = store.Encrypt(m.cfg.AESKey, value)
				if e != nil {
					return nil, e
				}
			}
			values[v.Key] = value
		}
		manifest, _ := json.Marshal(publicManifest(mf))
		cfg, _ := json.Marshal(values)
		now := time.Now().UTC()
		if e := os.Rename(contents, dst); e != nil {
			return nil, e
		}
		p := store.Plugin{ID: mf.ID, Name: mf.Name, Version: mf.Version, Type: mf.Type, Manifest: string(manifest), Config: string(cfg), InstalledAt: now, UpdatedAt: now}
		if e := m.db.Transaction(func(tx *gorm.DB) error {
			if e := tx.Create(&p).Error; e != nil {
				return e
			}
			if disabled := defaultDisabledPermissions(mf); len(disabled) > 0 {
				b, _ := json.Marshal(capabilityGrant{DisabledPermissions: disabled})
				return tx.Save(&store.Setting{Key: grantPrefix + mf.ID, Value: string(b)}).Error
			}
			return nil
		}); e != nil {
			if cleanup := os.RemoveAll(dst); cleanup != nil {
				return nil, errors.New("安装记录失败；本次包目录清理失败，需人工处理")
			}
			return nil, e
		}
		log.Line("插件已安装为 disabled，未执行包内代码")
		return gin.H{"pluginId": mf.ID}, nil
	})
	if e != nil {
		os.RemoveAll(dir)
		response.Fail(c, response.Conflict, "插件忙或任务提交失败")
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
func (m *Module) enable(c *gin.Context)  { m.lifecycle(c, "enable") }
func (m *Module) disable(c *gin.Context) { m.lifecycle(c, "disable") }
