package database

import (
	"errors"
	"strings"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

func (m *Module) databaseMetadata(c *gin.Context) {
	if _, ok := m.row(c); !ok {
		return
	}
	name := c.Param("name")
	if !identifier.MatchString(name) || protectedDB(name) {
		response.Fail(c, response.Invalid, "数据库名称无效")
		return
	}
	var in struct {
		Remark string `json:"remark"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len([]rune(in.Remark)) > 512 || strings.ContainsRune(in.Remark, 0) {
		response.Fail(c, response.Invalid, "备注最多512字")
		return
	}
	release, err := m.tasks.Acquire(LockKey(c.Param("sid")))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	row := store.DatabaseMetadata{ServerID: c.Param("sid"), Name: name, Remark: in.Remark}
	if err = m.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "server_id"}, {Name: "name"}}, DoUpdates: clause.AssignmentColumns([]string{"remark"})}).Create(&row).Error; err != nil {
		failure(c, err)
		return
	}
	response.OK(c, nil)
}
func (m *Module) revealPassword(c *gin.Context) {
	if _, ok := m.row(c); !ok {
		return
	}
	var row store.DatabaseMetadata
	if err := m.db.First(&row, "server_id = ? AND name = ?", c.Param("sid"), c.Param("name")).Error; err != nil {
		failure(c, errors.New("未记录此库的专属账号密码"))
		return
	}
	if row.Username == "" {
		failure(c, errors.New("未记录此库的专属账号密码"))
		return
	}
	value := ""
	if row.PasswordEnc != "" {
		var err error
		value, err = store.Decrypt(m.cfg.AESKey, row.PasswordEnc)
		if err != nil {
			failure(c, err)
			return
		}
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"password": value})
}
