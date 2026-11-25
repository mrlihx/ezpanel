package website

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

func (m *Module) projectLoad(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	if !pkgmgr.IsProjectRuntime(row.ProjectType) || row.RuntimeEnvironment == "" {
		response.Fail(c, response.Invalid, "该项目没有可读取的运行环境")
		return
	}
	var pid int64
	if raw := c.Query("pid"); raw != "" {
		var err error
		pid, err = strconv.ParseInt(raw, 10, 32)
		if err != nil || pid < 1 {
			response.Fail(c, response.Invalid, "进程 PID 无效")
			return
		}
	}
	data, err := m.packages.ProjectLoad(c.Request.Context(), row.ID, int32(pid), row.RuntimePort)
	if err != nil {
		failure(c, err)
		return
	}
	response.OK(c, data)
}

func (m *Module) projectService(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	if !pkgmgr.IsProjectRuntime(row.ProjectType) || row.RuntimeEnvironment == "" {
		response.Fail(c, response.Invalid, "该站点没有项目运行环境")
		return
	}
	data, err := m.packages.ProjectService(c.Request.Context(), row.ID)
	if err != nil {
		failure(c, err)
		return
	}
	response.OK(c, gin.H{"container": data, "configuredPort": row.RuntimePort, "pending": row.RuntimePending, "target": row.ProxyTarget})
}

func (m *Module) StartProjectsOnBoot(ctx context.Context) {
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return
	}
	marker := filepath.Join(m.cfg.DataDir(), "projects-boot-id")
	previous, _ := os.ReadFile(marker)
	if strings.TrimSpace(string(previous)) == strings.TrimSpace(string(bootID)) {
		return
	}
	var enabled []store.Website
	if err := m.db.Where("enabled = ? AND runtime_auto_start = ? AND runtime_command <> ''", true, true).Find(&enabled).Error; err != nil {
		slog.Error("查询开机启动项目失败", "error", err)
		return
	}
	// 同一次开机仅调度一次，面板重启和应用异常退出均不触发重试。
	if err := atomicWrite(marker, bootID, 0600); err != nil {
		slog.Error("记录项目开机启动批次失败", "error", err)
		return
	}
	for _, site := range enabled {
		if ctx.Err() != nil {
			return
		}
		_, err := m.tasks.Submit(task.Meta{Module: "website", Action: "project-autostart", Target: site.Name, ResourceID: site.ID, LockKey: "website:" + site.ID}, func(ctx context.Context, log *task.Log) (any, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			var row store.Website
			if err := m.db.First(&row, "id = ?", site.ID).Error; err != nil {
				return nil, err
			}
			if !row.RuntimeAutoStart || !row.Enabled || m.packages.ProjectRunning(ctx, row.ID) {
				return nil, nil
			}
			old := row
			old.Enabled = false
			return nil, m.changeProject(ctx, &row, old, false, func() error { return m.db.Save(&row).Error })
		})
		if err != nil {
			slog.Warn("提交项目开机启动任务失败", "site", site.Name, "error", err)
		}
	}
}
