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
