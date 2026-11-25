package cron

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/module/backup"
	"ezpanel/internal/module/database"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/shirou/gopsutil/v3/mem"
	"gorm.io/gorm"
)

const logTime = "2006-01-02 15:04:05"

var dbIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`)

func (m *Module) execute(ctx context.Context, r store.CronJob, cfg jobConfig, log *task.Log) (any, error) {
	remoteOnly := cfg.BackupStorageID != "" && cfg.BackupStorageID != "local" && !cfg.KeepLocal
	if remoteOnly {
		if err := os.MkdirAll(m.cfg.Layout().TempDir(), 0700); err != nil {
			return nil, err
		}
		dir, err := os.MkdirTemp(m.cfg.Layout().TempDir(), "cron-backup-")
		if err != nil {
			return nil, fmt.Errorf("无法创建备份中转目录: %w", err)
		}
		defer os.RemoveAll(dir)
		cfg.BackupDirectory = dir
	} else if cfg.BackupDirectory != "" {
		dir, err := m.backupDirectory(cfg.BackupDirectory)
		if err != nil {
			return nil, err
		}
		cfg.BackupDirectory = dir
	}
	options := backup.ArchiveOptions{Directory: cfg.BackupDirectory, Temporary: remoteOnly}
	if cfg.ExcludeRules != "" {
		options.Exclude = strings.Split(cfg.ExcludeRules, "\n")
	}
	switch r.Type {
	case "restart_project":
		m.mu.Lock()
		restart := m.restartProject
		m.mu.Unlock()
		if restart == nil {
			return nil, errors.New("项目服务尚未就绪")
		}
		log.Line("重启项目 " + cfg.TargetName)
		return nil, restart(ctx, cfg.TargetID)
	case "shell":
		script, err := os.ReadFile(cfg.ScriptPath)
		if err != nil {
			return nil, errors.New("计划脚本无法读取")
		}
		// 文件是新的输入边界，旧任务和外部编辑后的脚本也执行同一限制。
		if err = m.checkShell(string(script)); err != nil {
			return nil, err
		}
		log.Line("执行管理员脚本；请勿在脚本输出中打印秘密")
		result, e := executor.Stream(ctx, time.Duration(cfg.TimeoutSec)*time.Second, nil, log.Line, "/bin/bash", cfg.ScriptPath)
		log.ExitCode(result.ExitCode)
		return nil, e
	case "backup_db":
		if !cfg.AllDatabases {
			id, err := m.backupDatabase(ctx, r, cfg, cfg.TargetName, remoteOnly, log)
			if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, database.ErrDatabaseMissing) {
				m.disableInvalid(r.ID, "数据库目标已删除或不可访问")
			}
			if id == "" {
				return nil, err
			}
			return map[string]any{"backupId": id}, err
		}
		names, err := m.databases.BackupDatabaseNames(ctx, cfg.TargetID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				m.disableInvalid(r.ID, "数据库服务器已删除")
			}
			return nil, err
		}
		if len(names) == 0 {
			return nil, errors.New("没有可备份的业务数据库")
		}
		log.Line(fmt.Sprintf("共 %d 个业务数据库，按库依次备份", len(names)))
		batch := task.BatchResult{Results: []task.ItemResult{}}
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return batch, err
			}
			log.Line("开始备份数据库 " + name)
			_, err := m.backupDatabase(ctx, r, cfg, name, remoteOnly, log)
			item := task.ItemResult{Target: name, Msg: "备份成功"}
			if err != nil {
				item.Code = response.System
				item.Msg = response.Summary(err.Error())
				batch.FailedCount++
			} else {
				batch.SuccessCount++
			}
			log.Line(name + "：" + item.Msg)
			batch.Results = append(batch.Results, item)
		}
		if batch.FailedCount > 0 {
			return batch, errors.New("数据库备份存在失败，请查看逐库日志")
		}
		return batch, nil
	case "backup_site":
		var sites []store.Website
		q := m.db.Where("root <> ?", "")
		if !cfg.AllSites {
			q = q.Where("id = ?", cfg.TargetID)
		}
		if e := q.Order("id").Find(&sites).Error; e != nil {
			return nil, errors.New("静态网站快照读取失败")
		}
		if len(sites) == 0 {
			if !cfg.AllSites {
				m.disableInvalid(r.ID, "静态网站已删除或已改为反代")
			}
			return nil, errors.New("没有可备份的静态网站")
		}
		batch := task.BatchResult{Results: []task.ItemResult{}}
		for _, site := range sites {
			if e := ctx.Err(); e != nil {
				return batch, e
			}
			started := time.Now()
			log.Line("════════════════════════════════════════════════")
			log.Line(fmt.Sprintf("[%s] 开始备份网站 %s", started.In(m.location).Format(logTime), site.Name))
			release := func() {}
			var e error
			if cfg.AllSites {
				release, e = m.tasks.Acquire("website:" + site.ID)
			}
			if e == nil {
				e = m.backupSite(ctx, r, cfg, site.ID, log, options)
				release()
			}
			item := task.ItemResult{Target: site.Name}
			if e != nil {
				item.Code = response.System
				item.Msg = response.Summary(e.Error())
				batch.FailedCount++
				log.Line("│ 失败原因 " + item.Msg)
			} else {
				item.Msg = "备份成功"
				batch.SuccessCount++
			}
			finished := time.Now()
			status := "备份完成"
			if e != nil {
				status = "备份失败"
			}
			log.Line(fmt.Sprintf("[%s] %s %s，耗时 %.2f 秒", finished.In(m.location).Format(logTime), status, site.Name, finished.Sub(started).Seconds()))
			log.Line("════════════════════════════════════════════════")
			batch.Results = append(batch.Results, item)
		}
		if batch.FailedCount > 0 {
			return batch, errors.New("部分网站备份失败，请检查逐站点结果")
		}
		return batch, nil
	case "backup_directory":
		backup, err := m.backups.CreateDirectory(ctx, cfg.DirectoryPath, r.ID, log, options)
		if err != nil {
			return nil, err
		}
		result := map[string]any{"backupId": backup.ID}
		if err = m.backups.MoveToStorage(ctx, "site", backup.ID, cfg.BackupStorageID, cfg.BackupDirectory, cfg.KeepLocal, log); err != nil {
			if remoteOnly {
				return nil, err
			}
			return result, err
		}
		return result, m.rotateSite(ctx, r, cfg, "", log)
	case "url":
		return m.visitURL(ctx, cfg.urlSettings, log)
	case "renew_certificate":
		m.mu.Lock()
		renew := m.renewCertificates
		m.mu.Unlock()
		if renew == nil {
			return nil, errors.New("证书自动续签服务未初始化")
		}
		return renew(ctx, cfg.RenewBeforeDays, log)
	case "free_memory":
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		before, e := mem.VirtualMemoryWithContext(ctx)
		if e != nil {
			return nil, errors.New("内存状态读取失败")
		}
		path, e := executor.LookPath("sync")
		if e != nil {
			return nil, e
		}
		result, e := executor.Run(ctx, 30*time.Second, path)
		log.ExitCode(result.ExitCode)
		if e != nil {
			return nil, errors.New("sync执行失败，未清理缓存")
		}
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if e = os.WriteFile("/proc/sys/vm/drop_caches", []byte("3\n"), 0200); e != nil {
			return nil, errors.New("缓存清理失败，仅支持有权限的Linux环境")
		}
		after, e := mem.VirtualMemoryWithContext(ctx)
		if e != nil {
			return nil, errors.New("缓存已清理，但执行后内存状态读取失败")
		}
		log.Line(fmt.Sprintf("可用内存 %d → %d 字节；仅清理可回收缓存，不保证提高性能", before.Available, after.Available))
		return map[string]any{"availableBefore": before.Available, "availableAfter": after.Available}, nil
	}
	return nil, errors.New("任务类型无效")
}
func (m *Module) disableInvalid(id, reason string) {
	m.db.Model(&store.CronJob{}).Where("id = ?", id).Updates(map[string]any{"enabled": false, "disabled_reason": reason})
}
func (m *Module) backupSite(ctx context.Context, r store.CronJob, cfg jobConfig, id string, log *task.Log, options backup.ArchiveOptions) error {
	backup, err := m.backups.CreateSite(ctx, id, r.ID, log, options)
	if err != nil {
		return err
	}
	if err = m.backups.MoveToStorage(ctx, "site", backup.ID, cfg.BackupStorageID, cfg.BackupDirectory, cfg.KeepLocal, log); err != nil {
		return err
	}
	return m.rotateSite(ctx, r, cfg, id, log)
}
func (m *Module) backupDatabase(ctx context.Context, r store.CronJob, cfg jobConfig, name string, remoteOnly bool, log *task.Log) (string, error) {
	row, err := m.databases.Backup(ctx, cfg.TargetID, name, r.ID, log, cfg.BackupDirectory, remoteOnly)
	if err != nil {
		return "", err
	}
	if err = m.backups.MoveToStorage(ctx, "database", row.ID, cfg.BackupStorageID, cfg.BackupDirectory, cfg.KeepLocal, log); err != nil {
		if remoteOnly {
			return "", err
		}
		return row.ID, err
	}
	return row.ID, m.rotateDatabase(ctx, r, cfg, name, log)
}

func (m *Module) backupRetentionQuery(cronID, storageID string) *gorm.DB {
	query := m.db.Where("cron_job_id = ?", cronID)
	if storageID == "" || storageID == "local" {
		return query.Where("storage_type = ?", "local")
	}
	return query.Where("storage_type = ? AND storage_id = ?", "remote", storageID)
}

func (m *Module) rotateDatabase(ctx context.Context, r store.CronJob, cfg jobConfig, name string, log *task.Log) error {
	var rows []store.DBBackup
	query := m.backupRetentionQuery(r.ID, cfg.BackupStorageID).Where("server_id = ? AND database = ?", cfg.TargetID, name)
	if e := query.Order("created_at DESC,id DESC").Offset(r.KeepCount).Find(&rows).Error; e != nil {
		return e
	}
	log.Line(fmt.Sprintf("清理过期备份，保留最新 %d 份，本次清理 %d 份", r.KeepCount, len(rows)))
	for _, b := range rows {
		if e := m.backups.RemoveFile(ctx, b.BackupFile, b.FilePath); e != nil {
			return errors.New("新备份成功，旧备份轮转失败")
		}
		if e := m.db.Delete(&b).Error; e != nil {
			return errors.New("旧备份文件已清理，但记录删除失败")
		}
		if b.FilePath != "" {
			log.Line("已删除本地过期备份 " + b.FilePath)
		}
		if b.RemoteKey != "" {
			log.Line("已删除远程过期备份 " + m.backups.StorageName(b.StorageID) + " / " + b.RemoteKey)
		}
	}
	return nil
}
func (m *Module) rotateSite(ctx context.Context, r store.CronJob, cfg jobConfig, siteID string, log *task.Log) error {
	var rows []store.SiteBackup
	query := m.backupRetentionQuery(r.ID, cfg.BackupStorageID)
	if siteID == "" {
		query = query.Where("website_id IS NULL AND site_name = ?", cfg.DirectoryPath)
	} else {
		query = query.Where("website_id = ?", siteID)
	}
	if e := query.Order("created_at DESC,id DESC").Offset(r.KeepCount).Find(&rows).Error; e != nil {
		return e
	}
	log.Line(fmt.Sprintf("清理过期备份，保留最新 %d 份，本次清理 %d 份", r.KeepCount, len(rows)))
	for _, b := range rows {
		if e := m.backups.RemoveFile(ctx, b.BackupFile, b.FilePath); e != nil {
			return errors.New("新备份成功，旧备份轮转失败")
		}
		if e := m.db.Delete(&b).Error; e != nil {
			return errors.New("旧备份文件已清理，但记录删除失败")
		}
		if b.FilePath != "" {
			log.Line("已删除本地过期备份 " + b.FilePath)
		}
		if b.RemoteKey != "" {
			log.Line("已删除远程过期备份 " + m.backups.StorageName(b.StorageID) + " / " + b.RemoteKey)
		}
	}
	return nil
}
