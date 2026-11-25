package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	db          *gorm.DB
	cfg         config.Config
	tasks       *task.Manager
	auth        *auth.Service
	mu          sync.Mutex
	statuses    map[string]string
	core        string
	runs        map[string]*running
	crashes     map[string][]time.Time
	closed      chan struct{}
	closeOnce   sync.Once
	generations map[string]uint64
	hostURL     string
	hostPort    int
	host        *http.Server
	metrics     *sysinfo.Module
	policy      *pathsafe.Policy
}
type upload struct {
	Owner     string    `json:"owner"`
	ExpiresAt time.Time `json:"expiresAt"`
	Manifest  Manifest  `json:"manifest"`
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
	m := &Module{metrics: metrics, policy: policy, db: db, cfg: cfg, tasks: tasks, auth: a, statuses: map[string]string{}, core: core, runs: map[string]*running{}, crashes: map[string][]time.Time{}, closed: make(chan struct{}), generations: map[string]uint64{}}
	for _, p := range []string{m.packages(), m.private(), m.staging(), filepath.Join(m.packages(), ".backup")} {
		mode := os.FileMode(0755)
		if strings.HasPrefix(filepath.Base(p), ".") || p == m.private() {
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
	if e := m.cleanUploads(); e != nil {
		return nil, e
	}
	if e := m.listenHost(); e != nil {
		return nil, e
	}
	var rows []store.Plugin
	if e := db.Find(&rows).Error; e != nil {
		_ = m.host.Close()
		return nil, e
	}
	for _, p := range rows {
		if p.Operation != nil {
			if e := m.recoverOperation(p); e != nil {
				m.setStatus(p.ID, "failed")
				continue
			}
			if e := m.db.First(&p, "id = ?", p.ID).Error; e != nil {
				continue
			}
		}
		if p.Enabled {
			if e := m.start(p); e != nil {
				m.setStatus(p.ID, "failed")
			}
		}
	}
	return m, nil
}
func (m *Module) packages() string { return m.cfg.Layout().PluginsDir() }
func (m *Module) private() string  { return m.cfg.Layout().PluginDataDir() }
func (m *Module) staging() string  { return filepath.Join(m.packages(), ".staging") }
func (m *Module) Name() string     { return "plugins" }

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
	r := g.Group("/api/plugins")
	r.GET("", m.list)
	r.GET("/:id", m.detail)
	r.POST("/inspect", m.inspect)
	r.POST("/install", m.install)
	r.POST("/:id/enable", m.enable)
	r.POST("/:id/disable", m.disable)
	r.POST("/:id/restart", func(c *gin.Context) { m.lifecycle(c, "restart") })
	r.PUT("/:id/config", m.configure)
	r.GET("/:id/logs", m.logs)
	r.POST("/:id/upgrade", m.upgrade)
	r.POST("/:id/uninstall", m.uninstall)
}
func (m *Module) status(p store.Plugin) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.statuses[p.ID]; s != "" {
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
		if !f.IsDir() {
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
	if m.db.Order("installed_at").Find(&rows).Error != nil {
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
		out = append(out, gin.H{"id": p.ID, "name": p.Name, "version": p.Version, "type": p.Type, "enabled": p.Enabled, "runtimeStatus": m.status(p), "menu": manifest.Menu, "description": manifest.Description, "installedAt": p.InstalledAt, "activeTaskId": activeByPlugin[p.ID]})
	}
	response.OK(c, out)
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
	response.OK(c, gin.H{"manifest": publicManifest(mf), "config": cfg, "enabled": p.Enabled, "runtimeStatus": m.status(p), "installedAt": p.InstalledAt})
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
	dir := filepath.Join(m.staging(), id)
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
		if part.FormName() != "file" || part.FileName() == "" || found {
			part.Close()
			response.Fail(c, 3001, "仅允许一个 file 文件")
			return
		}
		found = true
		f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			response.Fail(c, 5000, "")
			return
		}
		n, copyErr := io.Copy(f, io.LimitReader(part, MaxUpload+1))
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
	changes := PermissionChanges{Permissions: mf.Permissions, ExecAllow: mf.ExecAllow, FSRoots: mf.FSRoots}
	var old store.Plugin
	e = m.db.First(&old, "id = ?", mf.ID).Error
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		response.Fail(c, 5000, "")
		return
	}
	if e == nil {
		var before Manifest
		if json.Unmarshal([]byte(old.Manifest), &before) != nil {
			response.Fail(c, 5000, "")
			return
		}
		changes = permissionChanges(before, mf)
	}
	u := upload{Owner: auth.Current(c).User.Username, ExpiresAt: time.Now().UTC().Add(10 * time.Minute), Manifest: mf}
	b, _ := json.Marshal(u)
	if e = os.WriteFile(filepath.Join(dir, "upload.json"), b, 0600); e != nil {
		response.Fail(c, 5000, "")
		return
	}
	keep = true
	time.AfterFunc(time.Until(u.ExpiresAt), func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if _, err := os.Stat(filepath.Join(dir, "upload.json")); err == nil {
			os.RemoveAll(dir)
		}
	})
	response.OK(c, gin.H{"uploadId": id, "expiresAt": u.ExpiresAt, "manifest": publicManifest(mf), "permissionChanges": changes})
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
	if e = os.Remove(filepath.Join(dir, "upload.json")); e != nil {
		response.Fail(c, 5000, "")
		return u, "", false
	}
	return u, dir, true
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
		if e := m.db.Create(&p).Error; e != nil {
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
