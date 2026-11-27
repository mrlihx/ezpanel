package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"ezpanel/internal/store"
	"ezpanel/internal/task"
)

func (m *Module) SetSiteBackupFinished(fn func(siteID, backupID, taskID string, err error)) {
	m.siteBackupFinished = fn
}

func (m *Module) ReportSiteBackup(siteID, backupID, taskID string, err error) {
	if m.siteBackupFinished != nil {
		m.siteBackupFinished(siteID, backupID, taskID, err)
	}
}

func (m *Module) SubmitSiteBackup(siteID, name, module, username, ip string) (string, error) {
	return m.tasks.Submit(task.Meta{Module: module, Action: "backup", Target: name, ResourceID: siteID, Username: username, IP: ip, LockKey: "website:" + siteID}, func(ctx context.Context, log *task.Log) (any, error) {
		row, err := m.CreateSite(ctx, siteID, "", log, ArchiveOptions{})
		m.ReportSiteBackup(siteID, row.ID, log.TaskID(), err)
		if err != nil {
			return nil, err
		}
		return map[string]string{"backupId": row.ID}, nil
	})
}

func (m *Module) CreateSite(ctx context.Context, id, cronID string, log *task.Log, options ArchiveOptions) (store.SiteBackup, error) {
	var backup store.SiteBackup
	var site store.Website
	if e := m.db.First(&site, "id = ? AND root <> ?", id, "").Error; e != nil {
		return backup, errors.New("网站项目目录已失效")
	}
	path, size, e := m.archiveDirectory(ctx, site.Root, site.Name, "site", log, options)
	if e != nil {
		return backup, e
	}
	backup = store.SiteBackup{BackupFile: store.BackupFile{FileName: filepath.Base(path), StorageType: "local", Method: "manual"}, ID: store.ID(), WebsiteID: &site.ID, SiteName: site.Name, FilePath: path, Size: size, CreatedAt: time.Now().UTC()}
	if cronID != "" {
		backup.CronJobID = &cronID
		backup.Method = "automatic"
	}
	if e = m.db.Create(&backup).Error; e != nil {
		os.Remove(path)
		return backup, errors.New("网站备份登记失败")
	}
	log.Line(site.Name + "：备份已登记 " + backup.ID)
	return backup, nil
}
