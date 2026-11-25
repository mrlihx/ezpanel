package file

import (
	"encoding/json"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type directoryTab struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type fileWorkspace struct {
	Tabs   []directoryTab `json:"tabs"`
	Active string         `json:"active"`
}

func (m *Module) workspace(c *gin.Context) {
	var setting store.Setting
	key := fmt.Sprintf("files.workspace.%d", auth.Current(c).User.ID)
	err := m.db.Where("key = ?", key).First(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		response.OK(c, fileWorkspace{Tabs: []directoryTab{{ID: "1", Path: "/"}}, Active: "1"})
		return
	}
	if err != nil {
		response.Fail(c, response.System, "读取文件工作区失败")
		return
	}
	var workspace fileWorkspace
	if json.Unmarshal([]byte(setting.Value), &workspace) != nil {
		response.Fail(c, response.System, "文件工作区记录损坏")
		return
	}
	response.OK(c, workspace)
}

func (m *Module) saveWorkspace(c *gin.Context) {
	var workspace fileWorkspace
	if !response.Bind(c, &workspace) {
		return
	}
	ids := make(map[string]bool, len(workspace.Tabs))
	for i, tab := range workspace.Tabs {
		if tab.ID == "" || len(tab.ID) > 64 || ids[tab.ID] || !filepath.IsAbs(tab.Path) || strings.ContainsRune(tab.Path, 0) {
			response.Fail(c, response.Invalid, "文件工作区参数无效")
			return
		}
		ids[tab.ID] = true
		workspace.Tabs[i].Path = filepath.Clean(tab.Path)
	}
	if !ids[workspace.Active] {
		response.Fail(c, response.Invalid, "当前目录选项卡不存在")
		return
	}
	// 这里只保存目录位置；恢复后仍由文件接口执行原有路径保护。
	data, err := json.Marshal(workspace)
	if err != nil {
		response.Fail(c, response.System, "保存文件工作区失败")
		return
	}
	key := fmt.Sprintf("files.workspace.%d", auth.Current(c).User.ID)
	err = m.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&store.Setting{Key: key, Value: string(data)}).Error
	if err != nil {
		response.Fail(c, response.System, "保存文件工作区失败")
		return
	}
	response.OK(c, nil)
}
