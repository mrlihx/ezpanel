package cron

import (
	"encoding/json"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"fmt"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"slices"
)

var cronColumns = []string{"name", "type", "status", "backupTarget", "backupPath", "schedule", "lastRun", "actions"}

func (m *Module) columns(c *gin.Context) {
	legacyKey := fmt.Sprintf("cron.columns.%d", auth.Current(c).User.ID)
	key := legacyKey + ".v2"
	if c.Request.Method == "GET" {
		var setting store.Setting
		err := m.db.First(&setting, "key = ?", key).Error
		legacy := errors.Is(err, gorm.ErrRecordNotFound)
		if legacy {
			err = m.db.First(&setting, "key = ?", legacyKey).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				response.OK(c, gin.H{"columns": cronColumns})
				return
			}
		}
		var columns []string
		if err != nil || json.Unmarshal([]byte(setting.Value), &columns) != nil {
			failure(c, errors.New("显示列保存或读取失败"))
			return
		}
		if legacy && !slices.Contains(columns, "backupTarget") {
			columns = append(columns, "backupTarget")
		}
		response.OK(c, gin.H{"columns": columns})
		return
	}
	var in struct {
		Columns []string `json:"columns"`
	}
	if !response.Bind(c, &in) {
		return
	}
	seen := map[string]bool{}
	for _, key := range in.Columns {
		valid := false
		for _, allowed := range cronColumns {
			if key == allowed {
				valid = true
				break
			}
		}
		if !valid || seen[key] {
			response.Fail(c, response.Invalid, "显示列无效或重复")
			return
		}
		seen[key] = true
	}
	if len(in.Columns) == 0 || !seen["actions"] {
		response.Fail(c, response.Invalid, "操作列必须显示")
		return
	}
	data, _ := json.Marshal(in.Columns)
	err := m.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&store.Setting{Key: key, Value: string(data)}).Error
	if err != nil {
		failure(c, errors.New("显示列保存或读取失败"))
		return
	}
	response.OK(c, in)
}
