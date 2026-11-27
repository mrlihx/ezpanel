package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"ezpanel/internal/task"
)

const deploymentCallback = "_panel/deployment"

func (m *Module) ComposeDeploymentHook(ctx context.Context, project, phase, status string, log *task.Log) error {
	var rows []struct {
		ID, Manifest, Value string
	}
	if err := m.db.WithContext(ctx).Table("plugins").Select("plugins.id, plugins.manifest, settings.value").Joins("JOIN settings ON settings.key = ? || plugins.id", grantPrefix).Where("plugins.enabled = ? AND plugins.type = ?", true, "process").Order("plugins.id").Find(&rows).Error; err != nil {
		return err
	}
	var failures []error
	for _, row := range rows {
		var grant capabilityGrant
		var manifest Manifest
		if json.Unmarshal([]byte(row.Value), &grant) != nil || grant.permissionDisabled("deployment.hooks") || !grant.allowsResource("compose", project) ||
			json.Unmarshal([]byte(row.Manifest), &manifest) != nil || !slices.Contains(manifest.Permissions, "deployment.hooks") {
			continue
		}
		run, _, err := m.readyPlugin(row.ID)
		if err == nil {
			callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err = invokePlugin(callCtx, run, row.ID, deploymentCallback, map[string]string{"project": project, "phase": phase, "status": status, "taskId": log.TaskID()})
			cancel()
		}
		if err != nil {
			failure := fmt.Errorf("插件 %s 的 %s 部署钩子失败：%w", row.ID, phase, err)
			if phase == "before" {
				return failure
			}
			failures = append(failures, failure)
		}
	}
	return errors.Join(failures...)
}
