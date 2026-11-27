package plugin

import (
	"encoding/json"
	"slices"

	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

func (m *Module) resourceActions(c *gin.Context) {
	kind := c.Param("kind")
	if !slices.Contains([]string{"sites", "containers", "databases", "certificates"}, kind) {
		response.Fail(c, response.Invalid, "未知资源菜单")
		return
	}
	var rows []struct{ ID, Manifest, Value string }
	if err := m.db.WithContext(c.Request.Context()).Table("plugins").
		Select("plugins.id, plugins.manifest, COALESCE(settings.value, '{}') AS value").
		Joins("LEFT JOIN settings ON settings.key = ? || plugins.id", grantPrefix).
		Where("plugins.enabled = ?", true).Order("plugins.name, plugins.id").Find(&rows).Error; err != nil {
		response.Fail(c, response.System, "无法读取插件资源菜单")
		return
	}
	items := []gin.H{}
	for _, row := range rows {
		var manifest Manifest
		var grant capabilityGrant
		if m.statusByID(row.ID) != "running" || json.Unmarshal([]byte(row.Manifest), &manifest) != nil ||
			manifest.ResourceActions[kind] == "" || !slices.Contains(manifest.Permissions, kind+".read") ||
			json.Unmarshal([]byte(row.Value), &grant) != nil || grant.permissionDisabled(kind+".read") {
			continue
		}
		items = append(items, gin.H{"pluginId": row.ID, "title": manifest.ResourceActions[kind], "ids": *grant.resourceIDs(kind), "all": slices.Contains(grant.AllResources, kind)})
	}
	response.OK(c, items)
}
