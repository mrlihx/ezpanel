package oplog

import (
	"encoding/csv"
	"errors"
	"ezpanel/internal/config"
	"ezpanel/internal/geoip"
	"ezpanel/internal/logview"
	"ezpanel/internal/pkgmgr"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Module struct {
	db       *gorm.DB
	auth     *auth.Service
	cfg      config.Config
	packages *pkgmgr.Manager
	runtime  *logview.Runtime
}

func New(db *gorm.DB, a *auth.Service, cfg config.Config, packages *pkgmgr.Manager, runtime *logview.Runtime) *Module {
	return &Module{db: db, auth: a, cfg: cfg, packages: packages, runtime: runtime}
}
func (m *Module) Name() string { return "oplog" }
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/oplogs")
	r.GET("", m.list)
	r.GET("/export", m.export)
	r.GET("/stats", m.stats)
	r.GET("/:id", m.detail)
	m.logRoutes(g)
	r.POST("/clear", m.clear)
}
func (m *Module) list(c *gin.Context) {
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	f, ok := m.filter(c)
	if !ok {
		return
	}
	f.Page = page
	f.Size = size
	rows, total := store.QueryOpLogs(m.db, f)
	for i := range rows {
		rows[i].Location = geoip.Location(rows[i].IP)
	}
	response.OK(c, gin.H{"list": rows, "total": total})
}
func (m *Module) detail(c *gin.Context) {
	id, e := strconv.ParseUint(c.Param("id"), 10, 64)
	if e != nil || id == 0 {
		response.Fail(c, response.Invalid, "日志ID无效")
		return
	}
	r, e := store.GetOpLog(m.db, id)
	if e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			response.Fail(c, response.NotFound, "日志不存在")
		} else {
			response.Fail(c, response.Internal, "")
		}
		return
	}
	r.Location = geoip.Location(r.IP)
	response.OK(c, r)
}
func (m *Module) clear(c *gin.Context) {
	var p struct {
		BeforeDays *int `json:"beforeDays"`
	}
	if !response.Bind(c, &p) {
		return
	}
	if p.BeforeDays == nil || *p.BeforeDays < 0 || *p.BeforeDays > 36500 {
		response.Fail(c, response.Invalid, "beforeDays须为0至36500整数")
		return
	}
	f, ok := m.filter(c)
	if !ok {
		return
	}
	var before *time.Time
	if *p.BeforeDays > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -*p.BeforeDays)
		before = &cutoff
	}
	deleted, e := store.ClearOpLogs(m.db, f, before)
	if e != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	user := auth.Current(c).User.Username
	_ = m.db.Create(&store.OpLog{Username: &user, IP: m.auth.ClientIP(c), Module: "oplog", Action: "clear", Target: "保留" + strconv.Itoa(*p.BeforeDays) + "天；删除" + strconv.FormatInt(deleted, 10) + "条", Phase: "completed", Success: true, CreatedAt: time.Now().UTC()}).Error
	c.Set("panel.auditRecorded", true)
	response.OK(c, gin.H{"deletedCount": deleted})
}

func (m *Module) filter(c *gin.Context) (store.OpLogFilter, bool) {
	f := store.OpLogFilter{
		Module:  c.Query("module"),
		Keyword: c.Query("keyword"),
	}
	if value, exists := c.GetQuery("success"); exists {
		if value != "true" && value != "false" {
			response.Fail(c, response.Invalid, "success须为true或false")
			return f, false
		}
		b := value == "true"
		f.Success = &b
	}
	var start, end time.Time
	for _, field := range []struct {
		name string
		dst  *time.Time
	}{{"startTime", &start}, {"endTime", &end}} {
		if raw := c.Query(field.name); raw != "" {
			parsed, e := time.Parse(time.RFC3339, raw)
			if e != nil {
				response.Fail(c, response.Invalid, "时间须为RFC3339")
				return f, false
			}
			*field.dst = parsed.UTC()
		}
	}
	if !start.IsZero() && !end.IsZero() && start.After(end) {
		response.Fail(c, response.Invalid, "开始时间不能晚于结束时间")
		return f, false
	}
	f.Start = start
	f.End = end

	switch c.Query("kind") {
	case "login", "operation", "":
		f.Kind = c.Query("kind")
	default:
		response.Fail(c, response.Invalid, "日志类型无效")
		return f, false
	}
	if resource := c.Query("resourceId"); resource != "" {
		f.ResourceID = resource
		var taskIDs []string
		_ = m.db.Model(&store.Task{}).Where("module = ? AND resource_id = ?", "website", resource).Pluck("id", &taskIDs).Error
		if len(taskIDs) > 0 {
			f.ResourceTaskIDs = make(map[string]bool, len(taskIDs))
			for _, id := range taskIDs {
				f.ResourceTaskIDs[id] = true
			}
		}
	}
	return f, true
}
func (m *Module) stats(c *gin.Context) {
	f, ok := m.filter(c)
	if !ok {
		return
	}
	response.OK(c, store.OpLogIPStats(m.db, f))
}
func (m *Module) export(c *gin.Context) {
	f, ok := m.filter(c)
	if !ok {
		return
	}
	f.OrderAsc = true
	rows, _ := store.QueryOpLogs(m.db, f)
	c.Header("Content-Disposition", `attachment; filename="panel-logs.csv"`)
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Status(http.StatusOK)
	_, _ = c.Writer.Write([]byte("\xef\xbb\xbf"))
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{"用户", "IP", "操作类型", "行为", "详情", "用户代理", "状态", "时间"})
	for _, r := range rows {
		user := ""
		if r.Username != nil {
			user = *r.Username
		}
		status := "失败"
		if r.Success {
			status = "成功"
		}
		if r.Phase == "submitted" {
			status = "任务已提交"
		}
		cells := []string{user, r.IP, r.Module, r.Action, r.Target, r.UserAgent, status, r.CreatedAt.Format(time.RFC3339)}
		for i, v := range cells {
			trimmed := strings.TrimSpace(v)
			if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
				cells[i] = "'" + v
			}
		}
		if err := writer.Write(cells); err != nil {
			c.Error(err)
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		c.Error(err)
	}
}
