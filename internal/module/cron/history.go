package cron

import (
	"errors"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/eventstream"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (m *Module) runs(c *gin.Context) {
	if _, e := m.row(c.Param("id")); e != nil {
		failure(c, e)
		return
	}
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	q := m.db.Model(&store.Task{}).Where("cron_job_id = ?", c.Param("id"))
	var total int64
	var rows []store.Task
	if q.Count(&total).Error != nil || q.Omit("result").Order("started_at DESC,id DESC").Offset((page-1)*size).Limit(size).Find(&rows).Error != nil {
		failure(c, errors.New("执行历史读取失败"))
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		v := task.Summary(r)
		v["runId"] = r.ID
		out = append(out, v)
	}
	response.OK(c, gin.H{"list": out, "total": total})
}
func (m *Module) logs(c *gin.Context) {
	if _, e := m.row(c.Param("id")); e != nil {
		failure(c, e)
		return
	}
	var r store.Task
	q := m.db.Where("cron_job_id = ?", c.Param("id"))
	if runID := c.Query("runId"); runID != "" {
		q = q.Where("id = ?", runID)
	}
	e := q.Order("started_at DESC,id DESC").First(&r).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		response.OK(c, nil)
		return
	}
	if e != nil {
		failure(c, errors.New("执行记录读取失败"))
		return
	}
	c.Params = append(c.Params, gin.Param{Key: "taskId", Value: r.ID})
	m.tasks.Detail(c, "cron")
}

type cronLogView struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Log       string `json:"log"`
	Truncated bool   `json:"truncated"`
	LocalPath string `json:"localPath"`
	Running   bool   `json:"running"`
}

func (m *Module) logView(c *gin.Context) {
	isStream := c.FullPath() == "/api/crons/:id/log-view/stream"
	var changes <-chan struct{}
	if isStream {
		var unsubscribe func()
		changes, unsubscribe = m.tasks.WatchCron(c.Param("id"))
		defer unsubscribe()
	}
	job, err := m.row(c.Param("id"))
	if err != nil {
		failure(c, err)
		return
	}
	query := m.db.WithContext(c.Request.Context()).Where("cron_job_id = ?", job.ID)
	if from, to := c.Query("from"), c.Query("to"); from != "" || to != "" {
		start, startErr := time.ParseInLocation(time.DateOnly, from, m.location)
		end, endErr := time.ParseInLocation(time.DateOnly, to, m.location)
		if startErr != nil || endErr != nil || end.Before(start) {
			response.Fail(c, response.Invalid, "日期范围无效")
			return
		}
		query = query.Where("started_at >= ? AND started_at < ?", start.UTC(), end.AddDate(0, 0, 1).UTC())
	} else {
		switch days := c.DefaultQuery("days", "3"); days {
		case "3", "7", "30":
			n, _ := strconv.Atoi(days)
			query = query.Where("started_at >= ?", time.Now().UTC().AddDate(0, 0, -n))
		case "0":
		default:
			response.Fail(c, response.Invalid, "日期范围无效")
			return
		}
	}
	view, err := m.readLogView(query, job)
	if err != nil {
		failure(c, err)
		return
	}
	if !isStream {
		response.OK(c, view)
		return
	}
	first := true
	eventstream.Serve(c, m.auth, m.tasks.Done(), changes, func(s eventstream.Stream) bool {
		if first {
			first = false
			return s.Send("snapshot", "", view)
		}
		job, err := m.row(job.ID)
		if err != nil {
			s.Send("stream-error", "", "计划任务无法读取")
			return false
		}
		next, err := m.readLogView(query, job)
		if err != nil {
			s.Send("stream-error", "", "任务日志无法读取")
			return false
		}
		if next == view {
			return true
		}
		event, payload := "snapshot", next
		if strings.HasPrefix(next.Log, view.Log) {
			event, payload.Log = "append", next.Log[len(view.Log):]
		}
		view = next
		return s.Send(event, "", payload)
	})
}

func (m *Module) readLogView(query *gorm.DB, job store.CronJob) (cronLogView, error) {
	const maxRows = 1000
	var rows []store.Task
	if err := query.Session(&gorm.Session{}).Select("id", "status", "log_path", "log_start", "log_size", "log_truncated").Order("started_at DESC,id DESC").Limit(maxRows + 1).Find(&rows).Error; err != nil {
		return cronLogView{}, err
	}
	remaining := int64(1 << 20)
	truncated := len(rows) > maxRows
	blocks := make([]string, 0, min(len(rows), maxRows))
	for _, row := range rows[:min(len(rows), maxRows)] {
		log, err := task.ReadLogTail(row, remaining)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return cronLogView{}, errors.New("任务日志无法读取")
		}
		if log.Content != "" {
			blocks = append(blocks, log.Content)
		}
		remaining -= int64(len(log.Content))
		if log.Truncated || remaining <= 0 {
			truncated = true
			break
		}
	}
	slices.Reverse(blocks)
	path := m.tasks.CronLogPath(job.ID)
	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	localPath := m.localBackupPath(job.Type, decode(job))
	return cronLogView{
		Name: job.Name, Path: path, Size: size,
		Log: strings.Join(blocks, "\n"), Truncated: truncated, LocalPath: localPath,
		Running: job.LastStatus != nil && *job.LastStatus == "running",
	}, nil
}

func (m *Module) removeRuns(cronID string) error {
	defer m.tasks.NotifyCron(cronID)
	var rows []store.Task
	if e := m.db.Where("cron_job_id = ? AND status <> ?", cronID, "running").Find(&rows).Error; e != nil {
		return e
	}
	paths := map[string]bool{}
	for _, r := range rows {
		if e := m.tasks.RemoveLogs(r); e != nil {
			return errors.New("部分任务日志删除失败，保留对应记录")
		}
		if e := m.db.Delete(&r).Error; e != nil {
			return errors.New("任务日志已删除，但记录清理失败")
		}
		paths[r.LogPath] = true
	}
	for path := range paths {
		if err := m.tasks.CompactCronLog(path); err != nil {
			return err
		}
	}
	return nil
}
func (m *Module) clearLogs(c *gin.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, e := m.row(c.Param("id")); e != nil {
		failure(c, e)
		return
	}
	if e := m.removeRuns(c.Param("id")); e != nil {
		failure(c, e)
		return
	}
	response.OK(c, nil)
}
func (m *Module) cleanupRetention() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var rows []store.Task
	cutoff := now.AddDate(0, 0, -m.cfg.TaskRetentionDays)
	var removed int64
	if e := m.db.Where("status <> ? AND finished_at < ?", "running", cutoff).Order("finished_at,id").Limit(500).Find(&rows).Error; e != nil {
		return
	}
	paths := map[string]bool{}
	jobs := map[string]struct{}{}
	for _, r := range rows {
		if e := m.tasks.RemoveLogs(r); e != nil {
			continue
		}
		if result := m.db.Delete(&r); result.Error == nil {
			removed += result.RowsAffected
			if r.CronJobID != nil {
				jobs[*r.CronJobID] = struct{}{}
			}
			paths[r.LogPath] = true
		}
	}
	for path := range paths {
		if err := m.tasks.CompactCronLog(path); err != nil {
			slog.Warn("整理计划任务日志失败", "error", err)
		}
	}
	for id := range jobs {
		m.tasks.NotifyCron(id)
	}
	logCutoff := now.AddDate(0, 0, -m.cfg.OpLogRetentionDays)
	if _, err := store.ClearOpLogs(m.db, store.OpLogFilter{}, &logCutoff); err != nil {
		slog.Warn("清理过期操作日志失败", "error", err)
	}
	if err := m.db.Exec("DELETE FROM alert_delivery_logs WHERE created_at < ?", logCutoff).Error; err != nil {
		slog.Warn("清理过期告警投递日志失败", "error", err)
	}
}
