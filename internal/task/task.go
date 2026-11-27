package task

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/eventstream"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"fmt"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrBusy = errors.New("目标正忙")

type Meta struct{ Module, Action, Target, ResourceID, CronJobID, Username, IP, LockKey string }
type ItemResult struct {
	Target string `json:"target"`
	Code   int    `json:"code"`
	Msg    string `json:"msg"`
}
type BatchResult struct {
	Results      []ItemResult `json:"results"`
	SuccessCount int          `json:"successCount"`
	FailedCount  int          `json:"failedCount"`
}
type Manager struct {
	ctx            context.Context
	cancel         context.CancelFunc
	db             *gorm.DB
	cfg            config.Config
	mu             sync.Mutex
	locks          map[string]bool
	composeCancels map[string]context.CancelFunc
	closed         bool
	wg             sync.WaitGroup
	secrets        []string
	eventMu        sync.Mutex
	listeners      map[string]map[chan struct{}]struct{}
	completed      func(store.Task)
}
type Log struct {
	mu        sync.Mutex
	file      *os.File
	size, max int64
	truncated bool
	rowID     string
	cronID    string
	err       error
	exitCode  *int
	manager   *Manager
	secrets   []string
}

func New(ctx context.Context, db *gorm.DB, cfg config.Config) (*Manager, error) {
	dir := filepath.Join(cfg.DataDir(), "task-logs")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{ctx: ctx, cancel: cancel, db: db, cfg: cfg, locks: map[string]bool{}, composeCancels: map[string]context.CancelFunc{}, secrets: []string{cfg.JWTSecret, cfg.AESKey, cfg.Entry, cfg.InstallDir}}
	var running []store.Task
	if e := db.Where("status = ?", "running").Find(&running).Error; e != nil {
		cancel()
		return nil, e
	}
	for _, r := range running {
		now := time.Now().UTC()
		if sharedLog(r) && r.LogSize == nil {
			if file, err := os.Open(r.LogPath); err == nil {
				base, baseErr := logBase(file)
				end, sizeErr := file.Seek(0, io.SeekEnd)
				file.Close()
				if baseErr == nil && sizeErr == nil {
					size := max(0, base+end-int64(logHeaderSize)-r.LogStart)
					r.LogSize = &size
				}
			}
		}
		r.Status = "interrupted"
		r.FinishedAt = &now
		r.ErrorCode = response.System
		r.ErrorMsg = "面板重启，任务中断；目标可能已部分改变"
		if e := db.Transaction(func(tx *gorm.DB) error {
			if e := tx.Save(&r).Error; e != nil {
				return e
			}
			if r.CronJobID != nil {
				if e := tx.Model(&store.CronJob{}).Where("id = ?", *r.CronJobID).Updates(map[string]any{"last_status": "interrupted"}).Error; e != nil {
					return e
				}
			}
			return tx.Create(&store.OpLog{Module: r.Module, Action: r.Action, Target: r.Target, ResourceID: r.ResourceID, TaskID: &r.ID, Phase: "completed", ErrorCode: r.ErrorCode, ErrorMsg: r.ErrorMsg, CreatedAt: now}).Error
		}); e != nil {
			cancel()
			return nil, e
		}
	}
	return m, nil
}

func (m *Manager) redact(s string, extra ...string) string {
	currentEntry := m.cfg.Current().Entry
	for _, values := range [][]string{m.secrets, {currentEntry}, extra} {
		for _, v := range values {
			if v != "" {
				s = strings.ReplaceAll(s, v, "[REDACTED]")
			}
		}
	}
	return strings.ToValidUTF8(s, "�")
}
func (l *Log) Secrets(values ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.secrets = append(l.secrets, values...)
}
func (l *Log) EnvSecrets(env []string) {
	var values []string
	for _, s := range env {
		k, v, ok := strings.Cut(s, "=")
		k = strings.ToUpper(k)
		if ok && (k == "MYSQL_PWD" || strings.Contains(k, "PASSWORD") || strings.Contains(k, "TOKEN") || strings.Contains(k, "SECRET") || strings.Contains(k, "KEY")) {
			values = append(values, v)
		}
	}
	l.Secrets(values...)
}
func (l *Log) ExitCode(code int) { l.mu.Lock(); defer l.mu.Unlock(); l.exitCode = &code }
func (l *Log) TaskID() string    { return l.rowID }
func (l *Log) Line(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.truncated || l.err != nil {
		return
	}
	s = l.manager.redact(s, l.secrets...)
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", " ") + "\n"
	if l.size+int64(len(s)) > l.max {
		l.truncated = true
		l.err = l.manager.db.Model(&store.Task{}).Where("id = ?", l.rowID).Update("log_truncated", true).Error
		l.manager.notify("status:" + l.rowID)
		l.manager.notify("task:" + l.rowID)
		l.manager.NotifyCron(l.cronID)
		return
	}
	n, e := io.WriteString(l.file, s)
	l.size += int64(n)
	if e != nil {
		l.err = e
	}
	l.manager.notify("task:" + l.rowID)
	l.manager.NotifyCron(l.cronID)
}

func (m *Manager) SetCompletionHook(hook func(store.Task)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completed = hook
}

func (m *Manager) Submit(meta Meta, fn func(context.Context, *Log) (any, error)) (string, error) {
	m.mu.Lock()
	if m.closed || m.ctx.Err() != nil {
		m.mu.Unlock()
		return "", errors.New("任务管理器已关闭")
	}
	if meta.LockKey != "" && m.locks[meta.LockKey] {
		m.mu.Unlock()
		return "", ErrBusy
	}
	if meta.LockKey != "" {
		m.locks[meta.LockKey] = true
	}
	m.wg.Add(1)
	m.mu.Unlock()
	release := func() { m.mu.Lock(); delete(m.locks, meta.LockKey); m.mu.Unlock(); m.wg.Done() }
	id := store.ID()
	path := filepath.Join(m.cfg.DataDir(), "task-logs", id+".log")
	var f *os.File
	var start int64
	var e error
	if meta.CronJobID != "" {
		path = m.CronLogPath(meta.CronJobID)
		f, start, e = openCronLog(path)
	} else {
		f, e = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	}
	if e != nil {
		release()
		return "", e
	}
	target := meta.Target
	if u, e := url.Parse(target); e == nil && u.IsAbs() && u.Host != "" {
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		target = u.String()
	}
	target = response.Summary(m.redact(target))
	row := store.Task{ID: id, Module: meta.Module, Action: meta.Action, Target: target, ResourceID: meta.ResourceID, Status: "running", LogPath: path, StartedAt: time.Now().UTC()}
	if meta.CronJobID != "" {
		row.CronJobID = &meta.CronJobID
		row.LogStart = start
	}
	var username *string
	if meta.Username != "" {
		username = &meta.Username
	}
	audit := func(phase string, success bool) store.OpLog {
		return store.OpLog{Username: username, IP: meta.IP, Module: row.Module, Action: row.Action, Target: row.Target, ResourceID: row.ResourceID, TaskID: &id, Phase: phase, Success: success, ErrorCode: row.ErrorCode, ErrorMsg: row.ErrorMsg, CreatedAt: time.Now().UTC()}
	}
	e = m.db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Create(&row).Error; e != nil {
			return e
		}
		if row.CronJobID != nil {
			if e := tx.Model(&store.CronJob{}).Where("id = ?", *row.CronJobID).Updates(map[string]any{"last_status": "running", "last_run_at": row.StartedAt}).Error; e != nil {
				return e
			}
		}
		a := audit("submitted", true)
		return tx.Create(&a).Error
	})
	if e != nil {
		f.Close()
		if !sharedLog(row) {
			_ = os.Remove(path)
		}
		release()
		return "", e
	}
	m.NotifyCron(meta.CronJobID)
	m.notify("module:" + meta.Module)
	ctx := m.ctx
	var cancel context.CancelFunc
	if meta.Module == "docker" && strings.HasPrefix(meta.Action, "compose-") {
		ctx, cancel = context.WithCancel(ctx)
		m.mu.Lock()
		m.composeCancels[id] = cancel
		m.mu.Unlock()
	}
	go func() {
		defer release()
		log := &Log{file: f, max: m.cfg.Current().TaskLogMaxBytes, manager: m, rowID: id, cronID: meta.CronJobID}
		var result any
		var runErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("后台任务发生 panic", "taskId", id, "panic", r, "stack", string(debug.Stack()))
					log.Line(fmt.Sprintf("任务执行发生异常：%v", r))
					runErr = fmt.Errorf("任务执行异常终止：%v", r)
				}
			}()
			result, runErr = fn(ctx, log)
		}()
		var userCancelled bool
		if cancel != nil {
			m.mu.Lock()
			delete(m.composeCancels, id)
			userCancelled = ctx.Err() != nil && m.ctx.Err() == nil
			m.mu.Unlock()
			cancel()
		}
		if userCancelled {
			log.Line("任务已取消")
		}
		log.mu.Lock()
		closeErr := f.Close()
		if runErr == nil {
			runErr = log.err
		}
		if runErr == nil {
			runErr = closeErr
		}
		if sharedLog(row) {
			row.LogSize = &log.size
		}
		row.LogTruncated = log.truncated
		row.ExitCode = log.exitCode
		extras := append([]string{}, log.secrets...)
		log.mu.Unlock()
		row.Status = "success"
		if runErr != nil {
			row.Status = "failed"
			row.ErrorCode = response.System
			row.ErrorMsg = "任务执行失败，请检查任务日志"
		}

		if result != nil {
			b, e := json.Marshal(result)
			if e != nil {
				row.Status = "failed"
				row.ErrorCode = response.Internal
				row.ErrorMsg = "任务结果编码失败"
			} else {
				var batch struct {
					FailedCount int `json:"failedCount"`
				}
				json.Unmarshal(b, &batch)
				if batch.FailedCount > 0 {
					row.Status = "failed"
					row.ErrorCode = response.System
					row.ErrorMsg = "批量操作存在失败项目"
				}
				var value any
				dec := json.NewDecoder(strings.NewReader(string(b)))
				dec.UseNumber()
				if e := dec.Decode(&value); e != nil {
					row.Status = "failed"
					row.ErrorCode = response.Internal
					row.ErrorMsg = "任务结果编码失败"
				} else {
					resultSecrets := append(append([]string{}, m.secrets...), extras...)
					// result仅承载业务元数据；完整凭据值脱敏，避免短密码破坏不透明ID。
					var clean func(any) any
					clean = func(v any) any {
						switch x := v.(type) {
						case string:
							for _, secret := range resultSecrets {
								if secret != "" && x == secret {
									return "[REDACTED]"
								}
							}
							return x
						case []any:
							for i := range x {
								x[i] = clean(x[i])
							}
						case map[string]any:
							for k, v := range x {
								x[k] = clean(v)
							}
						}
						return v
					}
					encoded, _ := json.Marshal(clean(value))
					s := string(encoded)
					row.Result = &s
				}
			}
		}
		if userCancelled || m.ctx.Err() != nil {
			row.Status = "interrupted"
			row.ErrorCode = response.System
			row.ErrorMsg = "任务中断，目标可能已部分改变"
			if userCancelled {
				row.ErrorMsg = "任务已取消，目标可能已部分改变"
			}
		}
		now := time.Now().UTC()
		row.FinishedAt = &now
		e := m.db.Transaction(func(tx *gorm.DB) error {
			if e := tx.Save(&row).Error; e != nil {
				return e
			}
			if row.CronJobID != nil {
				if e := tx.Model(&store.CronJob{}).Where("id = ?", *row.CronJobID).Update("last_status", row.Status).Error; e != nil {
					return e
				}
			}
			a := audit("completed", row.Status == "success")
			return tx.Create(&a).Error
		})
		if e != nil {
			slog.Error("任务结束状态保存失败", "taskId", id)
		} else {
			m.mu.Lock()
			hook := m.completed
			m.mu.Unlock()
			if hook != nil {
				hook(row)
			}
		}
		m.notify("task:" + id)
		m.notify("status:" + id)
		m.NotifyCron(meta.CronJobID)
		m.notify("module:" + meta.Module)
	}()
	return id, nil
}
func (m *Manager) Close() { m.mu.Lock(); m.closed = true; m.mu.Unlock(); m.cancel(); m.wg.Wait() }
func (m *Manager) ReadLogs(row store.Task, offset, limit int) ([]string, int, error) {
	f, source, e := logSection(row)
	if e != nil {
		return nil, 0, e
	}
	defer f.Close()
	r := bufio.NewReader(source)
	logs := []string{}
	line := 0
	for {
		b, e := r.ReadString('\n')
		if len(b) > 0 && strings.HasSuffix(b, "\n") {
			if line >= offset && len(logs) < limit {
				logs = append(logs, strings.TrimSuffix(b, "\n"))
			}
			line++
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, 0, e
		}
		if len(logs) == limit {
			break
		}
	}
	return logs, line, nil
}
func (m *Manager) Routes(g *gin.RouterGroup, service *auth.Service) {
	g.GET("/api/tasks", func(c *gin.Context) {
		page, size, ok := response.Page(c, 100, 20)
		if !ok {
			return
		}
		query := m.db.Model(&store.Task{})
		for _, field := range []struct{ param, column string }{{"module", "module"}, {"status", "status"}, {"resourceId", "resource_id"}} {
			if s := c.Query(field.param); s != "" {
				if field.param == "status" && s == "finished" {
					query = query.Where("status <> ?", "running")
				} else {
					query = query.Where(field.column+" = ?", s)
				}
			}
		}
		var total int64
		var rows []store.Task
		if e := query.Count(&total).Error; e != nil {
			response.Fail(c, response.Internal, "")
			return
		}
		if e := query.Omit("result").Order("started_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; e != nil {
			response.Fail(c, response.Internal, "")
			return
		}
		items := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			v := Summary(row)
			items = append(items, v)
		}
		response.OK(c, gin.H{"list": items, "total": total})
	})
	g.GET("/api/tasks/:taskId", func(c *gin.Context) { m.Detail(c, "") })
	g.GET("/api/tasks/:taskId/stream", func(c *gin.Context) { m.stream(c, service) })
	g.GET("/api/tasks/changes", func(c *gin.Context) {
		module := c.Query("module")
		if module != "software" && module != "cron" {
			response.Fail(c, response.Invalid, "任务模块无效")
			return
		}
		changes, close := m.subscribe("module:" + module)
		defer close()
		eventstream.Serve(c, service, m.Done(), changes, func(s eventstream.Stream) bool {
			return s.Send("changed", "", nil)
		})
	})
	g.POST("/api/tasks/:taskId/cancel", service.Audit("task"), func(c *gin.Context) {
		m.mu.Lock()
		cancel := m.composeCancels[c.Param("taskId")]
		if cancel != nil {
			cancel()
		}
		m.mu.Unlock()
		if cancel == nil {
			response.Fail(c, response.Conflict, "任务已结束或不支持取消")
			return
		}
		response.OK(c, gin.H{"cancelled": true})
	})
}

// Detail 供模块别名复用同一份任务及日志，不复制任务记录。
func (m *Manager) Detail(c *gin.Context, module string) {
	offset, limit := 0, 200
	for _, p := range []struct {
		key      string
		dst      *int
		min, max int
	}{{"offset", &offset, 0, int(^uint(0) >> 1)}, {"limit", &limit, 1, 1000}} {
		if s, ok := c.GetQuery(p.key); ok {
			v, e := strconv.Atoi(s)
			if e != nil || v < p.min || v > p.max {
				response.Fail(c, response.Invalid, "日志分页参数无效")
				return
			}
			*p.dst = v
		}
	}
	var row store.Task
	if e := m.db.First(&row, "id = ?", c.Param("taskId")).Error; e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			response.Fail(c, response.NotFound, "任务不存在")
		} else {
			response.Fail(c, response.Internal, "")
		}
		return
	}
	if module != "" && row.Module != module {
		response.Fail(c, response.NotFound, "任务不存在")
		return
	}
	logs, next, e := m.ReadLogs(row, offset, limit)
	if e != nil {
		response.Fail(c, response.System, "任务日志无法读取")
		return
	}
	v := view(row)
	if module == "software" {
		v["softwareId"] = row.ResourceID
	}
	v["logs"] = logs
	v["nextOffset"] = next
	response.OK(c, v)
}

func view(r store.Task) map[string]any {
	v := Summary(r)
	var result json.RawMessage
	if r.Result != nil {
		raw := json.RawMessage(*r.Result)
		if json.Valid(raw) {
			result = raw
		}
	}
	v["result"] = result
	return v
}

// Acquire 用于同步资源变更，与 Submit 的 LockKey 共用同一把锁。
func (m *Manager) Acquire(key string) (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return nil, errors.New("任务管理器已关闭")
	}
	if m.locks[key] {
		return nil, ErrBusy
	}
	m.locks[key] = true
	return func() { m.mu.Lock(); delete(m.locks, key); m.mu.Unlock() }, nil
}

// Summary 与全局任务列表使用相同字段，不包含日志和结果。
func Summary(r store.Task) map[string]any {
	var durationMs *int64
	if r.FinishedAt != nil {
		value := max(0, r.FinishedAt.Sub(r.StartedAt).Milliseconds())
		durationMs = &value
	}
	return map[string]any{"durationMs": durationMs, "taskId": r.ID, "module": r.Module, "action": r.Action, "target": r.Target, "resourceId": r.ResourceID, "cronJobId": r.CronJobID, "status": r.Status, "exitCode": r.ExitCode, "startedAt": r.StartedAt, "finishedAt": r.FinishedAt, "errorCode": r.ErrorCode, "errorMsg": r.ErrorMsg, "logTruncated": r.LogTruncated}
}

// ID 返回当前任务ID，供备份文件关联原始任务。
func (l *Log) ID() string { return l.rowID }
