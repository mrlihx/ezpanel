package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (m *Module) snapshotVersion(id string) (store.Plugin, error) {
	var metadata struct {
		Plugin   store.Plugin
		Manifest string
	}
	dir, err := m.historyPath(id)
	if err != nil {
		return metadata.Plugin, err
	}
	if _, err := os.Stat(filepath.Join(dir, "complete")); err != nil {
		return metadata.Plugin, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return metadata.Plugin, err
	}
	if json.Unmarshal(data, &metadata) != nil || metadata.Plugin.ID != id {
		return metadata.Plugin, errors.New("历史快照记录无效")
	}
	metadata.Plugin.Manifest = metadata.Manifest
	return metadata.Plugin, nil
}

func (m *Module) rollbackInfo(c *gin.Context) {
	p, ok := m.row(c)
	if !ok {
		return
	}
	previous, err := m.snapshotVersion(p.ID)
	if err != nil || previous.Version == p.Version {
		response.Fail(c, response.NotFound, "没有可恢复的历史版本快照")
		return
	}
	var currentManifest, previousManifest Manifest
	if json.Unmarshal([]byte(p.Manifest), &currentManifest) != nil || json.Unmarshal([]byte(previous.Manifest), &previousManifest) != nil {
		response.Fail(c, response.System, "无法读取版本清单")
		return
	}
	revoked, err := m.grantPreview(p.ID, currentManifest, previousManifest)
	if err != nil {
		response.Fail(c, response.System, "无法检查回滚对授权的影响")
		return
	}
	response.OK(c, gin.H{"version": previous.Version, "updatedAt": previous.UpdatedAt, "revokedGrants": revoked})
}

func (m *Module) rollbackVersion(c *gin.Context) {
	var input struct{ Version string }
	if !response.Bind(c, &input) {
		return
	}
	p, ok := m.row(c)
	if !ok {
		return
	}
	session := auth.Current(c)
	taskID, err := m.tasks.Submit(task.Meta{Module: "plugins", Action: "rollback", Target: p.ID, ResourceID: p.ID, LockKey: "plugin:" + p.ID, Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		if err := m.db.First(&p, "id = ?", p.ID).Error; err != nil {
			return nil, err
		}
		previous, err := m.snapshotVersion(p.ID)
		if err != nil || previous.Version != input.Version || p.Version == input.Version || p.Operation != nil {
			return nil, errors.New("历史快照不再可用，请重新确认")
		}
		var currentManifest, previousManifest Manifest
		if json.Unmarshal([]byte(p.Manifest), &currentManifest) != nil || json.Unmarshal([]byte(previous.Manifest), &previousManifest) != nil {
			return nil, errors.New("无法读取版本清单")
		}
		if currentManifest.Sandbox != nil && previousManifest.Type == "process" && previousManifest.Sandbox == nil && slices.Contains(previousManifest.Permissions, "exec.run") {
			return nil, errors.New("历史版本需要特权命令，无法在当前进程隔离模式下恢复")
		}
		if err := m.checkDependencies(previousManifest, p.Enabled); err != nil {
			return nil, err
		}
		if err := m.checkDependents(p.ID, previous.Version); err != nil {
			return nil, err
		}
		if currentManifest.BackupProvider != nil && previousManifest.BackupProvider == nil {
			if err := m.services.Backups.PluginStorageInUse(p.ID); err != nil {
				return nil, err
			}
		}
		if currentManifest.NotificationProvider != nil && previousManifest.NotificationProvider == nil {
			if err := m.services.Alerts.PluginProviderInUse(p.ID); err != nil {
				return nil, err
			}
		}
		if currentManifest.DNSProvider != nil && previousManifest.DNSProvider == nil {
			if err := m.services.Certificates.DNSProviderInUse(p.ID); err != nil {
				return nil, err
			}
		}
		return m.restorePreviousVersion(p, previous.Version, log)
	})
	if err != nil {
		response.Fail(c, response.Conflict, "插件忙或任务提交失败")
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": taskID})
}

func (m *Module) restorePreviousVersion(p store.Plugin, version string, log *task.Log) (any, error) {
	backup, err := m.historyPath(p.ID)
	if err != nil {
		return nil, err
	}
	if err := m.checkSnapshotSpace(p.ID); err != nil {
		return nil, err
	}
	var before Manifest
	if err := json.Unmarshal([]byte(p.Manifest), &before); err != nil {
		return nil, err
	}
	rescue := filepath.Join(m.historyDir(p.ID), log.ID())
	op := operation{Kind: "upgrade", TaskID: log.ID(), Phase: "snapshotting", Snapshot: rescue}
	if err := m.record(&p, op); err != nil {
		return nil, err
	}
	m.stop(p.ID)
	resume := func(cause error) (any, error) {
		if p.Enabled {
			if err := m.start(p); err != nil {
				m.setStatus(p.ID, "failed")
				cause = errors.Join(cause, err)
			}
		}
		clearErr := m.db.Model(&p).Update("operation", nil).Error
		if clearErr == nil {
			m.pruneHistory(p.ID)
		}
		return nil, errors.Join(cause, clearErr)
	}
	if err := m.snapshot(p, rescue); err != nil {
		return resume(err)
	}
	op.Phase = "replacing"
	if err := m.record(&p, op); err != nil {
		return resume(err)
	}
	restoreCurrent := func(cause error) (any, error) {
		if err := m.restore(p.ID, rescue, nil); err != nil {
			_ = m.record(&p, op)
			m.setStatus(p.ID, "failed")
			return nil, errors.Join(cause, err, errors.New("原版本恢复失败，已保留完整快照"))
		}
		return resume(cause)
	}
	if err := m.restore(p.ID, backup, p.Operation); err != nil {
		return restoreCurrent(err)
	}
	var restored store.Plugin
	if err := m.db.First(&restored, "id = ?", p.ID).Error; err != nil {
		return restoreCurrent(err)
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(restored.Manifest), &manifest); err != nil {
		return restoreCurrent(err)
	}
	if before.UIMode == "legacy" {
		manifest.UIMode = "legacy"
	} else {
		manifest.UIMode = "isolated"
	}
	if before.Sandbox != nil && restored.Type == "process" && manifest.Sandbox == nil {
		manifest.Sandbox = before.Sandbox
	}
	data, _ := json.Marshal(manifest)
	restored.Manifest = string(data)
	restored.Enabled = p.Enabled
	if err := m.db.Model(&restored).Updates(map[string]any{"manifest": restored.Manifest, "enabled": p.Enabled, "updated_at": time.Now().UTC()}).Error; err != nil {
		return restoreCurrent(err)
	}
	if restored.Enabled {
		if err := m.start(restored); err != nil {
			return restoreCurrent(err)
		}
	}
	if err := m.db.Transaction(func(tx *gorm.DB) error {
		if err := m.reconcileGrants(tx, p.ID, before, manifest); err != nil {
			return err
		}
		if err := tx.Exec("INSERT INTO plugin_history (plugin_id, snapshot) VALUES (?, ?) ON CONFLICT (plugin_id) DO UPDATE SET snapshot=excluded.snapshot", p.ID, log.ID()).Error; err != nil {
			return err
		}
		return tx.Model(&restored).Update("operation", nil).Error
	}); err != nil {
		return restoreCurrent(err)
	}
	m.restoreSchedules(restored)
	m.pruneHistory(p.ID)
	m.notifyStartup()
	log.Line("已恢复插件快照 " + version)
	return gin.H{"pluginId": p.ID, "version": version}, nil
}
