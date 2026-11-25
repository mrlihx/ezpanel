package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 先备份并登记，再删除原库；任一步失败都保留已生成的恢复材料。
func (m *Module) dropDatabase(c *gin.Context) {
	s, ok := m.row(c)
	if !ok {
		return
	}
	name := c.Param("name")
	var in struct {
		Confirmation string `json:"confirmation"`
		ConfirmName  string `json:"confirmName"`
		Permanent    bool   `json:"permanent"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !identifier.MatchString(name) || protectedDB(name) || (in.ConfirmName != name || in.Confirmation != "删除数据库") {
		response.Fail(c, response.Invalid, "请输入业务数据库名称确认删除")
		return
	}
	if !in.Permanent {
		if _, err := clientExecutable(s, true); err != nil {
			response.Fail(c, response.Invalid, "缺少备份客户端，不支持回收；请安装客户端或明确选择永久删除")
			return
		}
	}
	id, err := m.tasks.Submit(task.Meta{Module: "database", Action: "delete", Target: s.Name + "/" + name, ResourceID: s.ID, Username: auth.Current(c).User.Username, IP: m.auth.ClientIP(c), LockKey: LockKey(s.ID)}, func(ctx context.Context, log *task.Log) (any, error) {
		return m.deleteDatabase(ctx, s, name, in.Permanent, log)
	})
	if err != nil {
		failure(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
func (m *Module) deleteDatabase(ctx context.Context, s store.DBServer, name string, permanent bool, log *task.Log) (any, error) {
	db, err := m.connect(ctx, s, true)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var trash store.DatabaseTrash
	if !permanent {
		trash = store.DatabaseTrash{ID: store.ID(), ServerID: &s.ID, ServerName: s.Name, DatabaseName: name, State: "backed_up", DeletedAt: time.Now().UTC()}
		if err = db.QueryRowContext(ctx, "SELECT DEFAULT_CHARACTER_SET_NAME,DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", name).Scan(&trash.Charset, &trash.Collation); err != nil {
			return nil, errors.New("读取原库字符集失败，未删除")
		}
		var metadata store.DatabaseMetadata
		if err = m.db.First(&metadata, "server_id = ? AND name = ?", s.ID, name).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		if metadata.Name != "" {
			raw, _ := json.Marshal(struct {
				Row      store.DatabaseMetadata
				Password string
			}{metadata, metadata.PasswordEnc})
			trash.Metadata = string(raw)
		}
		backup, err := m.backupDatabase(ctx, s.ID, name, "", log, true, "", false)
		if err != nil {
			return nil, err
		}
		trash.BackupID = backup.ID
		if err = m.db.Create(&trash).Error; err != nil {
			return nil, errors.New("回收站登记失败，备份已保留，未删除原库")
		}
	}
	if _, err = db.ExecContext(ctx, "DROP DATABASE `"+name+"`"); err != nil {
		return nil, errors.New("删除失败，原库及已生成备份请在列表中检查")
	}
	err = m.db.Transaction(func(tx *gorm.DB) error {
		if trash.ID != "" {
			if err := tx.Model(&trash).Update("state", "recycled").Error; err != nil {
				return err
			}
		}
		if err := store.DisableTargetJobs(tx, "backup_db", s.ID, name, "数据库已删除"); err != nil {
			return err
		}
		if err := tx.Where("engine = ? AND server_id = ? AND name = ?", "mysql", s.ID, name).Delete(&databaseRecord{}).Error; err != nil {
			return err
		}
		return tx.Where("server_id = ? AND name = ?", s.ID, name).Delete(&store.DatabaseMetadata{}).Error
	})
	if err != nil {
		return nil, errors.New("原库已删除，面板记录更新失败；恢复备份仍保留")
	}
	log.Line("数据库已删除；数据库账号及授权保留")
	return gin.H{"trashId": trash.ID, "permanent": permanent}, nil
}

func (m *Module) trashList(c *gin.Context) {
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	var total int64
	var rows []store.DatabaseTrash
	if err := m.db.Model(&store.DatabaseTrash{}).Count(&total).Error; err != nil {
		failure(c, err)
		return
	}
	if err := m.db.Order("deleted_at DESC,id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		failure(c, err)
		return
	}
	out := []gin.H{}
	for _, r := range rows {
		out = append(out, gin.H{"id": r.ID, "serverId": r.ServerID, "serverName": r.ServerName, "name": r.DatabaseName, "deletedAt": r.DeletedAt, "state": r.State, "backupId": r.BackupID})
	}
	response.OK(c, gin.H{"list": out, "total": total})
}

const (
	trashImporting           = "importing"
	trashImportFailed        = "import_failed"
	trashRegistrationPending = "registration_pending"
)

func (m *Module) restoreTrash(c *gin.Context) {
	var row store.DatabaseTrash
	if err := m.db.First(&row, "id = ?", c.Param("id")).Error; err != nil {
		failure(c, err)
		return
	}
	if row.ServerID == nil {
		response.Fail(c, response.Invalid, "原服务器连接已删除，无法自动恢复；备份仍保留")
		return
	}
	var in struct {
		ConfirmName string `json:"confirmName"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.ConfirmName != row.DatabaseName {
		response.Fail(c, response.Invalid, "请输入数据库名称确认恢复")
		return
	}
	sid := *row.ServerID
	id, err := m.tasks.Submit(task.Meta{Module: "database", Action: "restore", Target: row.ServerName + "/" + row.DatabaseName, ResourceID: sid, Username: auth.Current(c).User.Username, IP: m.auth.ClientIP(c), LockKey: LockKey(sid)}, func(ctx context.Context, log *task.Log) (any, error) {
		if err := m.db.WithContext(ctx).First(&row, "id = ?", row.ID).Error; err != nil {
			return nil, err
		}
		if row.State == trashRegistrationPending {
			server, err := m.server(sid)
			if err != nil {
				return nil, err
			}
			db, err := m.connect(ctx, server, true)
			if err != nil {
				return nil, err
			}
			err = databaseExists(ctx, db, row.DatabaseName)
			db.Close()
			if err != nil {
				return nil, fmt.Errorf("无法确认已导入的数据库，未执行登记：%w", err)
			}
			log.Line("SQL 已导入，继续恢复面板登记")
		} else if err := m.restore(ctx, row.BackupID, &row, log); err != nil {
			return nil, err
		}
		err := m.db.Transaction(func(tx *gorm.DB) error {
			if row.Metadata != "" {
				var saved struct {
					Row      store.DatabaseMetadata
					Password string
				}
				if err := json.Unmarshal([]byte(row.Metadata), &saved); err != nil {
					return err
				}
				saved.Row.PasswordEnc = saved.Password
				if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&saved.Row).Error; err != nil {
					return err
				}
			}
			return tx.Delete(&row).Error
		})
		if err != nil {
			return nil, errors.New("SQL 已导入，但面板登记失败；再次恢复只会重试登记")
		}
		log.Line("原库已恢复，备份保留在历史记录中；原计划任务保持停用")
		return gin.H{"database": row.DatabaseName}, nil
	})
	if err != nil {
		failure(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
func (m *Module) purgeDatabaseTrash(row store.DatabaseTrash) error {
	key := "backup:" + row.BackupID
	if row.ServerID != nil {
		key = LockKey(*row.ServerID)
	}
	release, err := m.tasks.Acquire(key)
	if err != nil {
		return err
	}
	defer release()
	var backup store.DBBackup
	if err = m.db.First(&backup, "id = ?", row.BackupID).Error; err != nil {
		return err
	}
	if err = os.Remove(backup.FilePath); err != nil && !os.IsNotExist(err) {
		return errors.New("回收备份文件删除失败，记录保留")
	}
	return m.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&row).Error; err != nil {
			return err
		}
		return tx.Delete(&backup).Error
	})
}
func (m *Module) purgeTrash(c *gin.Context) {
	var row store.DatabaseTrash
	if err := m.db.First(&row, "id = ?", c.Param("id")).Error; err != nil {
		failure(c, err)
		return
	}
	var in struct {
		ConfirmName string `json:"confirmName"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.ConfirmName != row.DatabaseName {
		response.Fail(c, response.Invalid, "请输入数据库名称确认彻底删除")
		return
	}
	if err := m.purgeDatabaseTrash(row); err != nil {
		failure(c, err)
		return
	}
	response.OK(c, nil)
}
func (m *Module) emptyTrash(c *gin.Context) {
	var in struct {
		Confirmation string `json:"confirmation"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Confirmation != "确认清空数据库回收站" {
		response.Fail(c, response.Invalid, "请输入确认清空数据库回收站")
		return
	}
	var rows []store.DatabaseTrash
	if err := m.db.Find(&rows).Error; err != nil {
		failure(c, err)
		return
	}
	for _, row := range rows {
		if err := m.purgeDatabaseTrash(row); err != nil {
			failure(c, err)
			return
		}
	}
	response.OK(c, nil)
}
