package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/module/database"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RemoteStorage 由内置渠道或备份插件适配。storageID 定位配置，key 为归档对象名。
// 上传接入计划任务，手动备份接口始终创建本地文件。
type RemoteStorage interface {
	// Upload 接收相对于渠道根目录的归档路径。
	Upload(context.Context, string, string, io.Reader) (key string, err error)
	Open(context.Context, string, string) (io.ReadCloser, error)
	Delete(context.Context, string, string) error
}

type StorageTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// StorageTargetProvider 由存储插件列出已配置且可用的目标。
type StorageTargetProvider interface {
	StorageTargets() []StorageTarget
}
type Module struct {
	db                 *gorm.DB
	cfg                config.Config
	tasks              *task.Manager
	auth               *auth.Service
	remote             RemoteStorage
	channels           *channelStorage
	siteBackupFinished func(siteID, backupID, taskID string, err error)
}

func New(db *gorm.DB, cfg config.Config, tasks *task.Manager, a *auth.Service, remote RemoteStorage) *Module {
	channels := &channelStorage{db: db, key: cfg.AESKey}
	if remote == nil {
		remote = channels
	}
	return &Module{db: db, cfg: cfg, tasks: tasks, auth: a, remote: remote, channels: channels}
}
func (m *Module) Name() string { return "backup" }
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/backups")
	r.GET("/storages", func(c *gin.Context) { response.OK(c, m.StorageTargets()) })
	r.GET("/channels", m.listChannels)
	r.GET("/channels/providers", m.listProviders)
	r.POST("/channels", m.saveChannel)
	r.POST("/channels/test", m.testChannel)
	r.PUT("/channels/:id", m.saveChannel)
	r.PATCH("/channels/:id", m.toggleChannel)
	r.DELETE("/channels/:id", m.deleteChannel)
	r.GET("/:kind", m.list)
	r.GET("/:kind/:id/download", m.download)
	r.DELETE("/:kind/:id", m.delete)
	r.POST("/:kind/delete", m.batchDelete)
	r.POST("/site/:id/create", m.manualSite)
}

func (m *Module) StorageTargets() []StorageTarget {
	targets := []StorageTarget{{ID: "local", Name: "本地磁盘"}}
	if provider, ok := m.remote.(StorageTargetProvider); ok {
		targets = append(targets, provider.StorageTargets()...)
	}
	return targets
}

func (m *Module) StorageName(id string) string {
	var channel backupChannel
	if m.db.Select("name").First(&channel, "id = ?", id).Error == nil {
		return channel.Name
	}
	for _, target := range m.StorageTargets() {
		if target.ID == id {
			return target.Name
		}
	}
	return id
}

func (m *Module) CheckStorage(id string) error {
	for _, target := range m.StorageTargets() {
		if target.ID == id {
			return nil
		}
	}
	return errors.New("备份存储目标不可用")
}

// MoveToStorage 将中转归档上传并更新备份记录。
func (m *Module) MoveToStorage(ctx context.Context, kind, id, storageID, backupRoot string, keepLocal bool, log *task.Log) (err error) {
	if storageID == "local" || storageID == "" {
		return nil
	}
	table := "site_backups"
	if kind == "database" {
		table = "db_backups"
	}
	var row record
	if err := m.db.Table(table).First(&row, "id = ?", id).Error; err != nil {
		return err
	}
	if !keepLocal {
		// 仅远程备份在上传失败时也清除未发布的本地记录。
		defer func() {
			cleanup := os.Remove(row.FilePath)
			if cleanup != nil && !os.IsNotExist(cleanup) {
				err = errors.Join(err, fmt.Errorf("本地中转文件清理失败: %w", cleanup))
			} else {
				log.Line("│ 已清理本地中转文件")
			}
			err = errors.Join(err, m.db.Table(table).Where("id = ? AND storage_type = ?", id, "local").Delete(&record{}).Error)
		}()
	}
	if m.remote == nil {
		return errors.New("远程备份存储不可用")
	}
	if backupRoot == "" {
		backupRoot = m.cfg.Current().BackupRoot
	}
	relative, err := filepath.Rel(backupRoot, row.FilePath)
	if err != nil {
		return err
	}
	relative = filepath.ToSlash(relative)
	file, err := os.Open(row.FilePath)
	if err != nil {
		return err
	}
	defer file.Close()
	name := m.StorageName(storageID)
	var channel backupChannel
	if m.db.Select("protocol", "endpoint").First(&channel, "id = ?", storageID).Error == nil {
		protocol := strings.ToUpper(channel.Protocol)
		if channel.Protocol == "webdav" {
			protocol = "WebDAV"
		}
		name = protocol + " 存储「" + name + "」"
	}
	log.Line(fmt.Sprintf("│ 正在上传 %s 到 %s", relative, name))
	started := time.Now()
	key, err := m.remote.Upload(ctx, storageID, relative, file)
	if err != nil {
		return err
	}
	elapsed := time.Since(started)
	speed := float64(row.Size) / max(elapsed.Seconds(), 0.001) / (1 << 20)
	destination := key
	if endpoint, err := url.Parse(channel.Endpoint); err == nil && endpoint.Host != "" {
		destination = path.Join("/", endpoint.Path, key)
	}
	log.Line(fmt.Sprintf("│ 上传成功，存储位置 %s %s，耗时 %.2f 秒，平均速度 %.2f MiB/s", name, destination, elapsed.Seconds(), speed))
	fields := map[string]any{"storage_type": "remote", "storage_id": storageID, "remote_key": key}
	if !keepLocal {
		fields["file_path"] = ""
	}
	if err = m.db.Table(table).Where("id = ?", id).Updates(fields).Error; err != nil {
		_ = m.remote.Delete(ctx, storageID, key)
		return err
	}
	if keepLocal {
		log.Line("│ 已保留本地备份 " + row.FilePath)
	}
	return nil
}

type record struct {
	store.BackupFile
	ID           string    `json:"id"`
	FilePath     string    `json:"-"`
	ServerID     *string   `json:"-"`
	WebsiteID    *string   `json:"-"`
	ConnectionID string    `json:"-"`
	Size         int64     `json:"size"`
	CreatedAt    time.Time `json:"createdAt"`
	LocalCopy    bool      `json:"localCopy,omitempty" gorm:"-"`
}

func fail(c *gin.Context, err error) {
	code := response.System
	if errors.Is(err, gorm.ErrRecordNotFound) || os.IsNotExist(err) {
		code = response.NotFound
	}
	if errors.Is(err, task.ErrBusy) {
		code = response.Conflict
	}
	response.Fail(c, code, response.Summary(err.Error()))
}
func (m *Module) query(c *gin.Context) (*gorm.DB, string, bool) {
	switch c.Param("kind") {
	case "site":
		return m.db.Table("site_backups"), "site_backups", true
	case "mongodb":
		return m.db.Table("mongo_backups"), "mongo_backups", true
	case "database":
		return m.db.Table("db_backups").Where("id NOT IN (SELECT backup_id FROM database_trash)"), "db_backups", true
	default:
		response.Fail(c, response.Invalid, "备份类型无效")
		return nil, "", false
	}
}
func (m *Module) list(c *gin.Context) {
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	q, _, ok := m.query(c)
	if !ok {
		return
	}
	kind := c.Param("kind")
	if kind == "site" {
		if id := c.Query("websiteId"); id != "" {
			q = q.Where("website_id = ?", id)
		}
	} else {
		if kind == "mongodb" {
			if id := c.Query("connectionId"); id != "" {
				q = q.Where("connection_id = ?", id)
			}
		} else if id := c.Query("serverId"); id != "" {
			q = q.Where("server_id = ?", id)
		}
		if name := c.Query("database"); name != "" {
			q = q.Where("database = ?", name)
		}
	}
	var total int64
	rows := make([]record, 0)
	if err := q.Count(&total).Error; err != nil {
		fail(c, err)
		return
	}
	if err := q.Order("created_at DESC,id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		fail(c, err)
		return
	}
	for i := range rows {
		rows[i].LocalCopy = rows[i].StorageType == "remote" && rows[i].FilePath != ""
		if rows[i].FileName == "" {
			rows[i].FileName = filepath.Base(rows[i].FilePath)
		}
	}
	response.OK(c, gin.H{"list": rows, "total": total})
}
func (m *Module) row(c *gin.Context) (record, string, bool) {
	var row record
	q, table, ok := m.query(c)
	if !ok {
		return row, table, false
	}
	if err := q.First(&row, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, err)
		return row, table, false
	}
	if row.FileName == "" {
		row.FileName = filepath.Base(row.FilePath)
	}
	return row, table, true
}
func (m *Module) download(c *gin.Context) {
	row, _, ok := m.row(c)
	if !ok {
		return
	}
	reader, size, err := m.openBackup(c.Request.Context(), row)
	if err != nil {
		if errors.Is(err, errRemoteUnavailable) {
			response.Fail(c, response.Forbidden, err.Error())
		} else {
			fail(c, err)
		}
		return
	}
	defer reader.Close()
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": row.FileName}))
	c.DataFromReader(http.StatusOK, size, "application/octet-stream", reader, nil)
}

var errRemoteUnavailable = errors.New("远程备份存储插件不可用，备份记录已保留")

func (m *Module) openBackup(ctx context.Context, row record) (io.ReadCloser, int64, error) {
	var err error
	if row.FilePath != "" || row.StorageType != "remote" {
		var f *os.File
		f, err = os.Open(row.FilePath)
		if err == nil {
			var info os.FileInfo
			info, err = f.Stat()
			if err == nil && !info.Mode().IsRegular() {
				err = errors.New("备份文件不是普通文件")
			}
			if err != nil {
				f.Close()
			} else {
				return f, info.Size(), nil
			}
		}
	}
	if row.StorageType == "remote" {
		if m.remote == nil {
			return nil, 0, errRemoteUnavailable
		}
		reader, err := m.remote.Open(ctx, row.StorageID, row.RemoteKey)
		return reader, row.Size, err
	}
	return nil, 0, err
}

func (m *Module) OpenSiteArchive(ctx context.Context, siteID, backupID string) (io.ReadCloser, int64, error) {
	var row record
	if err := m.db.WithContext(ctx).Table("site_backups").Where("id = ? AND website_id = ?", backupID, siteID).Take(&row).Error; err != nil {
		return nil, 0, err
	}
	return m.openBackup(ctx, row)
}

// 保留数量清理与列表删除共用存储适配；插件失败时不删除备份记录。
func (m *Module) RemoveFile(ctx context.Context, meta store.BackupFile, path string) error {
	var err error
	if meta.StorageType == "remote" {
		if m.remote == nil {
			return errors.New("远程备份存储插件不可用，备份记录已保留")
		}
		err = m.remote.Delete(ctx, meta.StorageID, meta.RemoteKey)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if path == "" {
		return nil
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (m *Module) remove(ctx context.Context, table string, row record) error {
	key := "backup:" + row.ID
	if row.ConnectionID != "" {
		key = database.LockKey("mongodb:" + row.ConnectionID)
	}
	if row.ServerID != nil {
		key = database.LockKey(*row.ServerID)
	}
	if row.WebsiteID != nil {
		key = "website:" + *row.WebsiteID
	}
	release, err := m.tasks.Acquire(key)
	if err != nil {
		return err
	}
	defer release()
	if err = m.RemoveFile(ctx, row.BackupFile, row.FilePath); err != nil {
		return err
	}
	return m.db.Table(table).Delete(&record{}, "id = ?", row.ID).Error
}
func (m *Module) delete(c *gin.Context) {
	row, table, ok := m.row(c)
	if !ok {
		return
	}
	if err := m.remove(c.Request.Context(), table, row); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, nil)
}
func (m *Module) batchDelete(c *gin.Context) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.IDs) < 1 || len(in.IDs) > 100 {
		response.Fail(c, response.Invalid, "每批可删除1至100份备份")
		return
	}
	q, table, ok := m.query(c)
	if !ok {
		return
	}
	var rows []record
	if err := q.Where("id IN ?", in.IDs).Find(&rows).Error; err != nil {
		fail(c, err)
		return
	}
	indexed := map[string]record{}
	for _, row := range rows {
		indexed[row.ID] = row
	}
	deleted := []string{}
	failures := []gin.H{}
	for _, id := range in.IDs {
		row, found := indexed[id]
		if !found {
			failures = append(failures, gin.H{"id": id, "message": "备份不存在或已移至数据库回收站"})
			continue
		}
		if err := m.remove(c.Request.Context(), table, row); err != nil {
			failures = append(failures, gin.H{"id": id, "message": response.Summary(err.Error())})
			continue
		}
		deleted = append(deleted, id)
		delete(indexed, id)
	}
	response.OK(c, gin.H{"deletedIds": deleted, "failed": failures})
}
func (m *Module) manualSite(c *gin.Context) {
	var site store.Website
	if err := m.db.First(&site, "id = ? AND root <> ?", c.Param("id"), "").Error; err != nil {
		fail(c, err)
		return
	}
	id, err := m.SubmitSiteBackup(site.ID, site.Name, "website", auth.Current(c).User.Username, m.auth.ClientIP(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
