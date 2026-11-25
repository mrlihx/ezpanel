package plugin

import (
	"errors"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
	"time"
)

func kvKey(c *gin.Context) (string, bool) {
	key := c.Param("key")
	if len(key) < 1 || len(key) > 128 || strings.ContainsRune(key, 0) {
		response.Fail(c, response.Invalid, "KV key 必须为 1-128 字节")
		return "", false
	}
	return key, true
}
func (m *Module) kvList(c *gin.Context) {
	i := identity(c)
	prefix := c.Query("prefix")
	if len(prefix) > 128 {
		response.Fail(c, response.Invalid, "prefix 过长")
		return
	}
	var rows []store.PluginKV
	if m.db.Where("plugin_id = ?", i.id).Order("key").Find(&rows).Error != nil {
		response.Fail(c, 5000, "")
		return
	}
	out := []gin.H{}
	for _, r := range rows {
		if strings.HasPrefix(r.Key, prefix) {
			out = append(out, gin.H{"key": r.Key, "value": redact(r.Value, m.hostSecrets(i))})
		}
	}
	response.OK(c, gin.H{"list": out})
}
func (m *Module) kvGet(c *gin.Context) {
	key, ok := kvKey(c)
	if !ok {
		return
	}
	i := identity(c)
	var row store.PluginKV
	e := m.db.First(&row, "plugin_id = ? AND key = ?", i.id, key).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		response.Fail(c, response.NotFound, "KV 不存在")
		return
	}
	if e != nil {
		response.Fail(c, 5000, "")
		return
	}
	response.OK(c, gin.H{"value": redact(row.Value, m.hostSecrets(i))})
}

var errKVLimit = errors.New("KV 项目数量已达 1000")

func (m *Module) kvPut(c *gin.Context) {
	key, ok := kvKey(c)
	if !ok {
		return
	}
	var in struct {
		Value string `json:"value"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Value) > 64<<10 {
		response.Fail(c, response.TooLarge, "KV value 超过 64KiB")
		return
	}
	i := identity(c)
	e := m.db.Transaction(func(tx *gorm.DB) error {
		var existing int64
		if e := tx.Model(&store.PluginKV{}).Where("plugin_id = ? AND key = ?", i.id, key).Count(&existing).Error; e != nil {
			return e
		}
		if existing == 0 {
			var count int64
			if e := tx.Model(&store.PluginKV{}).Where("plugin_id = ?", i.id).Count(&count).Error; e != nil {
				return e
			}
			if count >= 1000 {
				return errKVLimit
			}
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "plugin_id"}, {Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&store.PluginKV{PluginID: i.id, Key: key, Value: in.Value, UpdatedAt: time.Now().UTC()}).Error
	})
	if e != nil {
		code := response.Internal
		if errors.Is(e, errKVLimit) {
			code = response.TooLarge
		}
		response.Fail(c, code, "KV 写入失败或超限")
		return
	}
	response.OK(c, nil)
}
func (m *Module) kvDelete(c *gin.Context) {
	key, ok := kvKey(c)
	if !ok {
		return
	}
	if m.db.Where("plugin_id = ? AND key = ?", identity(c).id, key).Delete(&store.PluginKV{}).Error != nil {
		response.Fail(c, 5000, "")
		return
	}
	response.OK(c, nil)
}
