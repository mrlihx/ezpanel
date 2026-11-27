package plugin

import (
	"encoding/json"
	"fmt"

	"ezpanel/internal/store"
)

func (m *Module) checkDependencies(manifest Manifest, running bool) error {
	stack := map[string]bool{manifest.ID: true}
	checked := map[string]bool{}
	var visit func(Manifest) error
	visit = func(current Manifest) error {
		for id, minimum := range current.Dependencies {
			if stack[id] {
				return fmt.Errorf("发现循环依赖：%s", id)
			}
			var row store.Plugin
			if err := m.db.First(&row, "id = ?", id).Error; err != nil {
				return fmt.Errorf("请先安装依赖插件 %s（至少 %s）", id, minimum)
			}
			if cmp, err := CompareVersion(row.Version, minimum); err != nil || cmp < 0 {
				return fmt.Errorf("依赖插件 %s 至少需要版本 %s", id, minimum)
			}
			if running && (!row.Enabled || m.status(row) != "running") {
				return fmt.Errorf("请先启动依赖插件 %s", id)
			}
			if !checked[id] {
				var next Manifest
				if err := json.Unmarshal([]byte(row.Manifest), &next); err != nil {
					return fmt.Errorf("依赖插件 %s 的清单无法读取", id)
				}
				stack[id] = true
				if err := visit(next); err != nil {
					return err
				}
				delete(stack, id)
				checked[id] = true
			}
		}
		return nil
	}
	return visit(manifest)
}

func (m *Module) checkDependents(id, version string) error {
	var rows []store.Plugin
	if err := m.db.Select("id, manifest").Where("enabled = ? AND id <> ?", true, id).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var mf Manifest
		if err := json.Unmarshal([]byte(row.Manifest), &mf); err != nil {
			return err
		}
		if minimum, ok := mf.Dependencies[id]; ok {
			cmp, _ := CompareVersion(version, minimum)
			if version == "" || cmp < 0 {
				return fmt.Errorf("插件 %s 依赖 %s（至少 %s），请先停用 %s", row.ID, id, minimum, row.ID)
			}
		}
	}
	return nil
}
