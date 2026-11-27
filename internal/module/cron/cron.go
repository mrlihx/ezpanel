package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	backupmodule "ezpanel/internal/module/backup"
	"ezpanel/internal/module/database"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	robcron "github.com/robfig/cron/v3"
	"gorm.io/gorm"
)

type Module struct {
	db                *gorm.DB
	cfg               config.Config
	tasks             *task.Manager
	databases         *database.Module
	backups           *backupmodule.Module
	auth              *auth.Service
	engine            *robcron.Cron
	location          *time.Location
	mu                sync.Mutex
	entries           map[string]robcron.EntryID
	running           map[string]bool
	blockedPorts      func() []int
	renewCertificates func(context.Context, int, *task.Log) (any, error)
	restartProject    func(context.Context, string) error
	policy            *pathsafe.Policy
}
type jobConfig struct {
	ScriptPath   string `json:"scriptPath,omitempty"`
	TargetID     string `json:"targetId,omitempty"`
	TargetName   string `json:"targetName,omitempty"`
	AllSites     bool   `json:"allSites,omitempty"`
	AllDatabases bool   `json:"allDatabases,omitempty"`
	urlSettings
	TimeoutSec      int    `json:"timeoutSec,omitempty"`
	RenewBeforeDays int    `json:"renewBeforeDays,omitempty"`
	DirectoryPath   string `json:"directoryPath,omitempty"`
	BackupStorageID string `json:"backupStorageId,omitempty"`
	BackupDirectory string `json:"backupDirectory,omitempty"`
	KeepLocal       bool   `json:"keepLocal,omitempty"`
	ExcludeRules    string `json:"excludeRules,omitempty"`
}
type input struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	Schedule     string `json:"schedule"`
	Script       string `json:"script"`
	TargetID     string `json:"targetId"`
	TargetName   string `json:"targetName"`
	AllSites     bool   `json:"allSites"`
	AllDatabases bool   `json:"allDatabases"`
	urlSettings
	KeepCount       int    `json:"keepCount"`
	TimeoutSec      int    `json:"timeoutSec"`
	RenewBeforeDays int    `json:"renewBeforeDays"`
	DirectoryPath   string `json:"directoryPath"`
	BackupStorageID string `json:"backupStorageId"`
	BackupDirectory string `json:"backupDirectory"`
	KeepLocal       bool   `json:"keepLocal"`
	ExcludeRules    string `json:"excludeRules"`
}

const invalidScheduleReason = "调度无效，请修正表达式后启用"

var parser = robcron.NewParser(robcron.Minute | robcron.Hour | robcron.Dom | robcron.Month | robcron.Dow)

func New(db *gorm.DB, cfg config.Config, tasks *task.Manager, databases *database.Module, a *auth.Service, backups ...*backupmodule.Module) (*Module, error) {
	loc, e := time.LoadLocation(cfg.Timezone)
	if e != nil {
		return nil, e
	}
	policy, e := pathsafe.New(cfg.InstallDir)
	if e != nil {
		return nil, e
	}
	m := &Module{policy: policy, db: db, cfg: cfg, tasks: tasks, databases: databases, auth: a, location: loc, engine: robcron.New(robcron.WithLocation(loc)), entries: map[string]robcron.EntryID{}, running: map[string]bool{}, blockedPorts: func() []int { return []int{cfg.Port} }}
	m.backups = backupmodule.New(db, cfg, tasks, a, nil)
	if len(backups) > 0 {
		m.backups = backups[0]
	}
	if e = os.MkdirAll(filepath.Join(cfg.DataDir(), "cron-scripts"), 0700); e != nil {
		return nil, e
	}
	var rows []store.CronJob
	if e = db.Where("enabled = ?", true).Find(&rows).Error; e != nil {
		return nil, e
	}
	for _, r := range rows {
		if _, _, e = m.preview(r.Schedule); e != nil {
			slog.Warn("跳过无效的计划任务", "id", r.ID, "name", r.Name, "error", e)
			if err := db.Model(&r).Updates(map[string]any{
				"enabled": false, "disabled_reason": invalidScheduleReason,
			}).Error; err != nil {
				slog.Error("标记计划任务失败", "id", r.ID, "error", err)
			}
			continue
		}
		if e = m.schedule(r); e != nil {
			return nil, e
		}
	}

	if _, e = m.engine.AddFunc("0 3 * * *", func() { m.cleanupRetention() }); e != nil {
		return nil, e
	}
	m.engine.Start()
	return m, nil
}
func (m *Module) Name() string { return "cron" }
func (m *Module) Close()       { ctx := m.engine.Stop(); <-ctx.Done() }

func (m *Module) ValidatePluginSchedule(expression string) error {
	_, _, err := m.preview(expression)
	return err
}

func (m *Module) AddPluginSchedule(expression string, run func()) (func(), error) {
	schedule, _, err := m.preview(expression)
	if err != nil {
		return nil, err
	}
	id := m.engine.Schedule(schedule, robcron.FuncJob(run))
	return func() { m.engine.Remove(id) }, nil
}

// SetBlockedPorts 供插件运行器提供当前Host API及插件监听端口；每次实际拨号重新取值。
func (m *Module) SetBlockedPorts(f func() []int) { m.mu.Lock(); m.blockedPorts = f; m.mu.Unlock() }

// SetCertificateRenewer 注入证书续签与部署流程，计划任务只负责调度。
func (m *Module) SetCertificateRenewer(f func(context.Context, int, *task.Log) (any, error)) {
	m.mu.Lock()
	m.renewCertificates = f
	m.mu.Unlock()
}
func (m *Module) SetProjectRestarter(f func(context.Context, string) error) {
	m.mu.Lock()
	m.restartProject = f
	m.mu.Unlock()
}
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/crons")
	r.GET("", m.list)
	r.POST("", m.save)
	r.POST("/preview", m.previewHandler)
	r.POST("/test-url", m.testURL)
	r.GET("/columns", m.columns)
	r.PUT("/columns", m.columns)
	r.GET("/:id", m.detail)
	r.PUT("/:id", m.save)
	r.DELETE("/:id", m.delete)
	r.POST("/:id/enable", m.toggle)
	r.POST("/:id/disable", m.toggle)
	r.POST("/:id/run", m.runHandler)
	r.GET("/:id/runs", m.runs)
	r.GET("/:id/logs", m.logs)
	r.GET("/:id/log-view", m.logView)
	r.GET("/:id/log-view/stream", m.logView)
	r.DELETE("/:id/logs", m.clearLogs)
}
func (m *Module) preview(expression string) (robcron.Schedule, []time.Time, error) {
	if len(strings.Fields(expression)) != 5 || strings.Contains(expression, "CRON_TZ") || strings.Contains(expression, "TZ=") || strings.Contains(expression, "@") {
		return nil, nil, errors.New("仅支持五段Cron（分 时 日 月 周）")
	}
	s, e := parser.Parse(expression)
	if e != nil {
		return nil, nil, errors.New("Cron表达式无效")
	}
	times := []time.Time{}
	next := time.Now().In(m.location)
	for range 3 {
		next = s.Next(next)
		if next.IsZero() {
			break
		}
		times = append(times, next.In(m.location))
	}
	if len(times) == 0 {
		return nil, nil, errors.New("Cron表达式没有下一次执行时间")
	}
	return s, times, nil
}
func description(expression string) string {
	p := strings.Fields(expression)
	if len(p) != 5 {
		return expression
	}
	if p[2] == "*" && p[3] == "*" && p[4] == "*" {
		if p[0] == "*" && p[1] == "*" {
			return "每分钟"
		}
		if strings.HasPrefix(p[0], "*/") && p[1] == "*" {
			return "每" + strings.TrimPrefix(p[0], "*/") + "分钟"
		}
		if p[1] == "*" {
			return "每小时第" + p[0] + "分钟"
		}
		if strings.HasPrefix(p[1], "*/") {
			return "每" + strings.TrimPrefix(p[1], "*/") + "小时的第" + p[0] + "分钟"
		}
		return "每天 " + p[1] + ":" + p[0]
	}
	if p[2] != "*" && p[4] != "*" {
		return "自定义 " + expression + "（日与周为OR关系）"
	}
	return "自定义 " + expression
}
func (m *Module) previewHandler(c *gin.Context) {
	var p struct {
		Schedule string `json:"schedule"`
	}
	if !response.Bind(c, &p) {
		return
	}
	_, times, e := m.preview(p.Schedule)
	if e != nil {
		response.Fail(c, response.Invalid, e.Error())
		return
	}
	response.OK(c, gin.H{"timezone": m.cfg.Timezone, "scheduleDesc": description(p.Schedule), "nextRuns": times})
}
func (m *Module) schedule(r store.CronJob) error {
	if old, ok := m.entries[r.ID]; ok {
		m.engine.Remove(old)
		delete(m.entries, r.ID)
	}
	if !r.Enabled {
		return nil
	}
	id, e := m.engine.AddFunc(r.Schedule, func() { _, _ = m.submit(r.ID, "", "", true) })
	if e == nil {
		m.entries[r.ID] = id
	}
	return e
}
func failure(c *gin.Context, e error) {
	switch {
	case errors.Is(e, task.ErrBusy):
		response.Fail(c, response.Conflict, "任务或备份目标正在运行")
	case errors.Is(e, gorm.ErrRecordNotFound):
		response.Fail(c, response.NotFound, "计划任务不存在")
	default:
		response.Fail(c, response.System, response.Summary(e.Error()))
	}
}
func (m *Module) row(id string) (store.CronJob, error) {
	var r store.CronJob
	e := m.db.First(&r, "id = ?", id).Error
	return r, e
}
func decode(r store.CronJob) jobConfig {
	var cfg jobConfig
	json.Unmarshal([]byte(r.Config), &cfg)
	if r.Type == "renew_certificate" && cfg.RenewBeforeDays == 0 {
		cfg.RenewBeforeDays = 15
	}
	return cfg
}
func (m *Module) view(r store.CronJob) gin.H {
	cfg := decode(r)
	target := cfg.TargetName
	if target == "" {
		target = r.Name
	}
	if cfg.AllSites {
		target = "所有静态网站"
	} else if cfg.AllDatabases {
		target = "所有业务数据库"
	} else if cfg.URL != "" {
		target = safeURL(cfg.URL)
	} else if cfg.TargetID != "" && target == "" {
		target = cfg.TargetID
	}
	if r.Type == "renew_certificate" {
		target = "所有托管的 Let's Encrypt 证书"
	}
	if r.Type == "backup_directory" {
		target = cfg.DirectoryPath
	}
	backupPath := m.localBackupPath(r.Type, cfg)
	backupTarget := ""
	if backupSubdirs[r.Type] != "" {
		backupTarget = "本地备份"
		if cfg.BackupStorageID != "" && cfg.BackupStorageID != "local" {
			backupTarget = m.backups.StorageName(cfg.BackupStorageID)
			if cfg.KeepLocal {
				backupTarget += " + 本地备份"
			}
		}
	}
	return gin.H{"backupPath": backupPath, "backupTarget": backupTarget, "backupStorageId": cfg.BackupStorageID, "id": r.ID, "name": r.Name, "type": r.Type, "schedule": r.Schedule, "scheduleDesc": description(r.Schedule), "timezone": m.cfg.Timezone, "target": target, "keepCount": r.KeepCount, "enabled": r.Enabled, "disabledReason": r.DisabledReason, "scheduleInvalid": r.DisabledReason == invalidScheduleReason, "lastRunAt": r.LastRunAt, "lastStatus": r.LastStatus, "createdAt": r.CreatedAt}
}
func (m *Module) list(c *gin.Context) {
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	q := m.db.Model(&store.CronJob{})
	if keyword := c.Query("keyword"); keyword != "" {
		q = q.Where("instr(name, ?) > 0", keyword)
	}
	if typ := c.Query("type"); typ != "" {
		q = q.Where("type = ?", typ)
	}
	switch c.Query("status") {
	case "enabled":
		q = q.Where("enabled = ?", true)
	case "disabled":
		q = q.Where("enabled = ?", false)
	case "failed":
		q = q.Where("last_status = ?", "failed")
	case "":
	default:
		response.Fail(c, response.Invalid, "任务状态无效")
		return
	}
	var total int64
	var rows []store.CronJob
	if q.Count(&total).Error != nil || q.Order("created_at DESC,id DESC").Offset((page-1)*size).Limit(size).Find(&rows).Error != nil {
		failure(c, errors.New("计划任务列表读取失败"))
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, m.view(r))
	}
	response.OK(c, gin.H{"list": out, "total": total})
}
func (m *Module) detail(c *gin.Context) {
	r, e := m.row(c.Param("id"))
	if e != nil {
		failure(c, e)
		return
	}
	cfg := decode(r)
	v := m.view(r)
	v["targetId"] = cfg.TargetID
	v["targetName"] = cfg.TargetName
	v["allSites"] = cfg.AllSites
	v["allDatabases"] = cfg.AllDatabases
	v["url"] = cfg.URL
	v["urlMethod"] = cfg.Method
	v["userAgent"] = cfg.UserAgent
	v["urlParams"] = cfg.Params
	v["backupDirectory"] = cfg.BackupDirectory
	v["keepLocal"] = cfg.KeepLocal
	v["excludeRules"] = cfg.ExcludeRules
	v["timeoutSec"] = cfg.TimeoutSec
	v["renewBeforeDays"] = cfg.RenewBeforeDays
	v["directoryPath"] = cfg.DirectoryPath
	v["backupStorageId"] = cfg.BackupStorageID
	v["script"] = ""
	if cfg.ScriptPath != "" {
		b, e := os.ReadFile(cfg.ScriptPath)
		if e != nil {
			failure(c, errors.New("任务脚本无法读取"))
			return
		}
		v["script"] = string(b)
	}
	response.OK(c, v)
}
func (m *Module) validate(p *input) (jobConfig, error) {
	cfg := jobConfig{}
	if p.Name == "" || len([]rune(p.Name)) > 64 {
		return cfg, errors.New("任务名称须为1至64字")
	}
	if _, _, e := m.preview(p.Schedule); e != nil {
		return cfg, e
	}
	switch p.Type {
	case "shell":
		if len(p.Script) > 64<<10 || !utf8.ValidString(p.Script) || strings.ContainsRune(p.Script, 0) {
			return cfg, errors.New("脚本须为64KiB内UTF-8文本")
		}
		if err := m.checkShell(p.Script); err != nil {
			return cfg, err
		}
		if p.TimeoutSec == 0 {
			p.TimeoutSec = 3600
		}
		if p.TimeoutSec < 1 || p.TimeoutSec > 86400 {
			return cfg, errors.New("脚本超时须为1至86400秒")
		}
		cfg.TimeoutSec = p.TimeoutSec
	case "backup_site":
		cfg.AllSites = p.AllSites
		if p.AllSites {
			if p.TargetID != "" {
				return cfg, errors.New("所有网站模式不应传入单一目标")
			}
			var n int64
			if m.db.Model(&store.Website{}).Where("root <> ?", "").Count(&n).Error != nil || n == 0 {
				return cfg, errors.New("没有可备份的静态网站")
			}
		} else {
			var site store.Website
			if m.db.First(&site, "id = ? AND root <> ?", p.TargetID, "").Error != nil {
				return cfg, errors.New("静态网站不存在")
			}
			cfg.TargetID = site.ID
			cfg.TargetName = site.Name
		}
	case "backup_db":
		var s store.DBServer
		if m.db.First(&s, "id = ?", p.TargetID).Error != nil {
			return cfg, errors.New("数据库服务器不存在")
		}
		cfg.TargetID = p.TargetID
		cfg.AllDatabases = p.AllDatabases
		if !cfg.AllDatabases {
			if !dbIdentifier.MatchString(p.TargetName) {
				return cfg, errors.New("数据库名无效")
			}
			switch strings.ToLower(p.TargetName) {
			case "mysql", "information_schema", "performance_schema", "sys":
				return cfg, errors.New("系统数据库不能作为计划备份目标")
			}
			cfg.TargetName = p.TargetName
		}
	case "restart_project":
		var site store.Website
		if m.db.First(&site, "id = ? AND runtime_environment <> '' AND runtime_command <> ''", p.TargetID).Error != nil {
			return cfg, errors.New("项目不存在或尚未配置启动命令")
		}
		cfg.TargetID, cfg.TargetName = site.ID, site.Name
	case "backup_directory":
		root, e := m.policy.Check(p.DirectoryPath, false)
		if e != nil {
			return cfg, errors.New("备份目录不可读或指向受保护目录")
		}
		if e = m.policy.CheckTree(root); e != nil {
			return cfg, errors.New("备份来源包含受保护目录")
		}
		info, e := os.Stat(root)
		if e != nil || !info.IsDir() {
			return cfg, errors.New("备份来源必须是目录")
		}
		cfg.DirectoryPath = root
	case "url":
		if err := p.urlSettings.validate(); err != nil {
			return cfg, err
		}
		cfg.urlSettings = p.urlSettings
	case "free_memory":
	case "renew_certificate":
		if p.RenewBeforeDays == 0 {
			p.RenewBeforeDays = 15
		}
		if p.RenewBeforeDays < 1 || p.RenewBeforeDays > 90 {
			return cfg, errors.New("续签提前天数须为1至90天")
		}
		cfg.RenewBeforeDays = p.RenewBeforeDays
	default:
		return cfg, errors.New("任务类型无效")
	}
	if backupSubdirs[p.Type] != "" {
		if p.BackupStorageID == "" {
			p.BackupStorageID = "local"
		}
		if e := m.backups.CheckStorage(p.BackupStorageID); e != nil {
			return cfg, e
		}
		cfg.BackupStorageID = p.BackupStorageID
		cfg.KeepLocal = cfg.BackupStorageID != "local" && p.KeepLocal
		if p.BackupDirectory != "" && (cfg.BackupStorageID == "local" || cfg.KeepLocal) {
			dir, err := m.backupDirectory(p.BackupDirectory)
			if err != nil {
				return cfg, err
			}
			cfg.BackupDirectory = dir
		}
		if p.Type == "backup_directory" && (cfg.BackupStorageID == "local" || cfg.KeepLocal) {
			dir := cfg.BackupDirectory
			if dir == "" {
				dir = m.cfg.Current().BackupRoot
			}
			dir = filepath.Join(dir, "directory", pathsafe.BackupName(filepath.Base(cfg.DirectoryPath)))
			if pathsafe.Contains(cfg.DirectoryPath, dir) {
				return cfg, errors.New("备份目录位于来源目录内，拒绝递归备份")
			}
		}
		if p.Type != "backup_db" {
			if len(p.ExcludeRules) > 8192 {
				return cfg, errors.New("排除规则最多 8 KiB")
			}
			rules := []string{}
			for line := range strings.SplitSeq(p.ExcludeRules, "\n") {
				rule := strings.TrimSpace(line)
				if rule == "" {
					continue
				}
				rule = strings.TrimPrefix(rule, "./")
				if rule == "" || rule == "." || rule == ".." || strings.ContainsAny(rule, "\\\x00\r") || path.IsAbs(rule) || path.Clean(rule) != rule || strings.HasPrefix(rule, "../") {
					return cfg, errors.New("排除规则须为相对路径或通配符，目录末尾不要加斜杠")
				}
				if _, err := path.Match(rule, ""); err != nil {
					return cfg, errors.New("排除规则包含无效的通配符")
				}
				rules = append(rules, rule)
			}
			cfg.ExcludeRules = strings.Join(rules, "\n")
		}
		if p.KeepCount == 0 {
			p.KeepCount = 3
		}
		if p.KeepCount < 1 || p.KeepCount > 100 {
			return cfg, errors.New("备份保留数量须为1至100")
		}
	} else {
		p.KeepCount = 0
	}
	return cfg, nil
}

var backupSubdirs = map[string]string{"backup_site": "site", "backup_db": "database", "backup_directory": "directory"}

func (m *Module) localBackupPath(kind string, cfg jobConfig) string {
	subdir := backupSubdirs[kind]
	if subdir == "" || (cfg.BackupStorageID != "" && cfg.BackupStorageID != "local" && !cfg.KeepLocal) {
		return ""
	}
	root := cfg.BackupDirectory
	if root == "" {
		root = m.cfg.Current().BackupRoot
	}
	return filepath.Join(root, subdir)
}

func (m *Module) backupDirectory(raw string) (string, error) {
	dir, err := m.policy.Check(raw, true)
	if err != nil || m.policy.CheckTree(dir) != nil {
		return "", errors.New("备份保存路径不可用或受保护")
	}
	for _, root := range []string{m.cfg.InstallDir} {
		if pathsafe.Contains(root, dir) || pathsafe.Contains(dir, root) {
			return "", errors.New("备份保存路径不能与程序目录重叠")
		}
	}
	if root := m.cfg.Current().SiteRoot; pathsafe.Contains(root, dir) || pathsafe.Contains(dir, root) {
		return "", errors.New("备份保存路径不能与建站目录重叠")
	}
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return "", errors.New("备份保存路径必须是目录")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return dir, nil
}

func (m *Module) save(c *gin.Context) {
	var p input
	if !response.Bind(c, &p) {
		return
	}
	cfg, e := m.validate(&p)
	if e != nil {
		response.Fail(c, response.Invalid, e.Error())
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := store.CronJob{ID: store.ID(), Enabled: true}
	oldCfg := jobConfig{}
	var oldScript []byte
	if id := c.Param("id"); id != "" {
		r, e = m.row(id)
		if e != nil {
			failure(c, e)
			return
		}
		if m.running[id] || m.hasRunning(id) {
			failure(c, task.ErrBusy)
			return
		}
		oldCfg = decode(r)
		if oldCfg.ScriptPath != "" {
			oldScript, e = os.ReadFile(oldCfg.ScriptPath)
			if e != nil {
				failure(c, errors.New("旧脚本读取失败"))
				return
			}
		}
	}
	r.Name = p.Name
	r.Type = p.Type
	r.Schedule = strings.Join(strings.Fields(p.Schedule), " ")
	r.KeepCount = p.KeepCount
	r.DisabledReason = ""
	if p.Type == "shell" {
		cfg.ScriptPath = filepath.Join(m.cfg.DataDir(), "cron-scripts", r.ID+".sh")
		temp := cfg.ScriptPath + ".part"
		if e = os.WriteFile(temp, []byte(p.Script), 0700); e == nil {
			e = os.Rename(temp, cfg.ScriptPath)
		}
		if e != nil {
			os.Remove(temp)
			failure(c, errors.New("任务脚本保存失败"))
			return
		}
	}
	b, _ := json.Marshal(cfg)
	r.Config = string(b)
	if e = m.db.Save(&r).Error; e != nil {
		if cfg.ScriptPath != "" {
			if oldCfg.ScriptPath != "" {
				os.WriteFile(oldCfg.ScriptPath, oldScript, 0700)
			} else {
				os.Remove(cfg.ScriptPath)
			}
		}
		failure(c, errors.New("任务配置保存失败"))
		return
	}
	if oldCfg.ScriptPath != "" && cfg.ScriptPath == "" {
		os.Remove(oldCfg.ScriptPath)
	}
	if e = m.schedule(r); e != nil {
		failure(c, e)
		return
	}
	response.OK(c, gin.H{"id": r.ID})
}
func (m *Module) toggle(c *gin.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, e := m.row(c.Param("id"))
	if e != nil {
		failure(c, e)
		return
	}
	r.Enabled = strings.HasSuffix(c.FullPath(), "/enable")
	if r.Enabled {
		cfg := decode(r)
		p := input{
			Name: r.Name, Type: r.Type, Schedule: r.Schedule, KeepCount: r.KeepCount,
			TargetID: cfg.TargetID, TargetName: cfg.TargetName, AllSites: cfg.AllSites,
			AllDatabases: cfg.AllDatabases, urlSettings: cfg.urlSettings,
			TimeoutSec: cfg.TimeoutSec, RenewBeforeDays: cfg.RenewBeforeDays,
			DirectoryPath: cfg.DirectoryPath, BackupStorageID: cfg.BackupStorageID,
			BackupDirectory: cfg.BackupDirectory, KeepLocal: cfg.KeepLocal, ExcludeRules: cfg.ExcludeRules,
		}
		if cfg.ScriptPath != "" {
			b, e := os.ReadFile(cfg.ScriptPath)
			if e != nil {
				failure(c, errors.New("脚本不存在"))
				return
			}
			p.Script = string(b)
		}
		if _, e = m.validate(&p); e != nil {
			response.Fail(c, response.Invalid, e.Error())
			return
		}
		r.DisabledReason = ""
	}
	if e = m.db.Save(&r).Error; e != nil {
		failure(c, e)
		return
	}
	if e = m.schedule(r); e != nil {
		failure(c, e)
		return
	}
	response.OK(c, nil)
}
func (m *Module) delete(c *gin.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, e := m.row(c.Param("id"))
	if e != nil {
		failure(c, e)
		return
	}
	if m.running[r.ID] || m.hasRunning(r.ID) {
		failure(c, task.ErrBusy)
		return
	}
	cfg := decode(r)
	if e = m.removeRuns(r.ID); e != nil {
		failure(c, e)
		return
	}
	if e = m.db.Delete(&r).Error; e != nil {
		failure(c, e)
		return
	}
	m.tasks.NotifyCron(r.ID)
	if entry, ok := m.entries[r.ID]; ok {
		m.engine.Remove(entry)
		delete(m.entries, r.ID)
	}
	if cfg.ScriptPath != "" {
		os.Remove(cfg.ScriptPath)
	}
	response.OK(c, nil)
}
func (m *Module) runHandler(c *gin.Context) {
	id, e := m.submit(c.Param("id"), auth.Current(c).User.Username, m.auth.ClientIP(c), false)
	if e != nil {
		failure(c, e)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"runId": id})
}
func (m *Module) submit(id, user, ip string, auto bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, e := m.row(id)
	if e != nil {
		return "", e
	}
	if auto {
		user = "system"
	}
	if auto && !r.Enabled {
		return "", nil
	}
	if m.running[id] {
		if auto {
			m.skipped(r, "同一计划的上次执行尚未结束")
		}
		return "", task.ErrBusy
	}
	cfg := decode(r)
	lock := "cron:" + id
	if r.Type == "backup_db" {
		lock = database.LockKey(cfg.TargetID)
	} else if r.Type == "restart_project" || (r.Type == "backup_site" && !cfg.AllSites) {
		lock = "website:" + cfg.TargetID
	} else if r.Type == "backup_directory" {
		lock = "directory:" + cfg.DirectoryPath
	}
	m.running[id] = true
	taskID, e := m.tasks.Submit(task.Meta{Module: "cron", Action: r.Type, Target: m.view(r)["target"].(string), ResourceID: r.ID, CronJobID: r.ID, Username: user, IP: ip, LockKey: lock}, func(ctx context.Context, log *task.Log) (any, error) {
		defer func() { m.mu.Lock(); delete(m.running, id); m.mu.Unlock() }()
		started := time.Now()
		location, locErr := time.LoadLocation(m.cfg.Current().Timezone)
		if locErr != nil {
			location = time.UTC
		}
		log.Line("────────────────────────────────────────────────")
		log.Line(fmt.Sprintf("[%s] 开始执行 %s", started.In(location).Format(logTime), r.Name))
		result, e := m.execute(ctx, r, cfg, log)
		finished := time.Now()
		status := "执行成功"
		if e != nil {
			status = "执行失败"
			log.Line("失败原因 " + e.Error())
		}
		log.Line(fmt.Sprintf("[%s] %s，总耗时 %.2f 秒", finished.In(location).Format(logTime), status, finished.Sub(started).Seconds()))
		log.Line("────────────────────────────────────────────────")
		return result, e
	})
	if e != nil {
		delete(m.running, id)
		if auto && errors.Is(e, task.ErrBusy) {
			m.skipped(r, "任务目标正在使用")
		}
	}
	return taskID, e
}
func (m *Module) skipped(r store.CronJob, reason string) {
	slog.Info("跳过计划任务", "id", r.ID, "name", r.Name, "reason", reason)
}

func (m *Module) hasRunning(id string) bool {
	var count int64
	// 任务函数返回后仍需等待结束状态持久化，期间不允许删除关联记录。
	return m.db.Model(&store.Task{}).Where("cron_job_id = ? AND status = ?", id, "running").Count(&count).Error != nil || count > 0
}
