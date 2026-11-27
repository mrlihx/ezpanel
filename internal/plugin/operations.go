package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type operation struct {
	Kind     string `json:"kind"`
	TaskID   string `json:"taskId"`
	Phase    string `json:"phase"`
	Snapshot string `json:"snapshot"`
	Purge    bool   `json:"purge"`
}

func (m *Module) record(p *store.Plugin, op operation) error {
	b, _ := json.Marshal(op)
	s := string(b)
	p.Operation = &s
	return m.db.Model(p).Update("operation", s).Error
}

// 快照只复制托管目录，不跟随符号链接，也不触碰插件自己管理的外部资源。
func copyTree(src, dst string) error {
	info, e := os.Lstat(src)
	if e != nil {
		return e
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, e := os.Readlink(src)
		if e != nil {
			return e
		}
		return os.Symlink(link, dst)
	}
	if info.IsDir() {
		// 先用可写权限填充目录，再还原原权限和所有者。
		if e = os.MkdirAll(dst, 0700); e != nil {
			return e
		}
		entries, e := os.ReadDir(src)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if e = copyTree(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); e != nil {
				return e
			}
		}
	} else if info.Mode().IsRegular() {
		in, e := os.Open(src)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if e != nil {
			return e
		}
		_, e = io.Copy(out, in)
		closeErr := out.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	} else {
		return errors.New("私有数据包含不可快照的设备、socket 或管道")
	}
	if e = os.Chmod(dst, info.Mode().Perm()); e != nil {
		return e
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && os.Geteuid() == 0 {
		if e = os.Chown(dst, int(st.Uid), int(st.Gid)); e != nil {
			return e
		}
	}
	return nil
}
func (m *Module) snapshot(p store.Plugin, path string) error {
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	if e := copyTree(filepath.Join(m.packages(), p.ID), filepath.Join(path, "package")); e != nil {
		return e
	}
	data := filepath.Join(m.private(), p.ID)
	if _, e := os.Lstat(data); e == nil {
		if e = copyTree(data, filepath.Join(path, "data")); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	var kv []store.PluginKV
	if e := m.db.Where("plugin_id = ?", p.ID).Find(&kv).Error; e != nil {
		return e
	}
	b, e := json.Marshal(struct {
		Plugin   store.Plugin     `json:"plugin"`
		Manifest string           `json:"manifest"`
		Config   string           `json:"config"`
		KV       []store.PluginKV `json:"kv"`
	}{p, p.Manifest, p.Config, kv})
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(path, "state.json"), b, 0600); e != nil {
		return e
	}
	return os.WriteFile(filepath.Join(path, "complete"), []byte("1"), 0600)
}
func (m *Module) restore(id, path string, pending *string) (err error) {
	if _, e := os.Stat(filepath.Join(path, "complete")); e != nil {
		return errors.New("快照未完成，禁止替换当前数据")
	}
	b, e := os.ReadFile(filepath.Join(path, "state.json"))
	if e != nil {
		return e
	}
	var state struct {
		Plugin   store.Plugin     `json:"plugin"`
		Manifest string           `json:"manifest"`
		Config   string           `json:"config"`
		KV       []store.PluginKV `json:"kv"`
	}
	if json.Unmarshal(b, &state) != nil || state.Plugin.ID != id {
		return errors.New("快照元数据损坏")
	}
	m.stop(id)
	// 在目标文件系统准备完整副本，确认两个目录都能复制后再替换。
	parts := []struct {
		name, parent, destination string
		stage, prepared           string
		parked, replaced          bool
	}{
		{name: "package", parent: m.activeUploads(), destination: filepath.Join(m.packages(), id)},
		{name: "data", parent: m.dataStage(), destination: filepath.Join(m.private(), id)},
	}
	for i := range parts {
		part := &parts[i]
		part.stage = filepath.Join(part.parent, ".restore-"+id)
		if e = os.RemoveAll(part.stage); e != nil {
			return e
		}
		if e = os.Mkdir(part.stage, 0700); e != nil {
			return e
		}
		defer func() {
			if err != nil && (part.parked || part.replaced) {
				var undoErr error
				if part.replaced {
					undoErr = os.Rename(part.destination, part.prepared)
				}
				if undoErr == nil && part.parked {
					undoErr = os.Rename(filepath.Join(part.stage, "previous"), part.destination)
				}
				if undoErr != nil {
					err = errors.Join(err, undoErr)
					return
				}
			}
			_ = os.RemoveAll(part.stage)
		}()
		source := filepath.Join(path, part.name)
		if _, e = os.Lstat(source); part.name == "data" && os.IsNotExist(e) {
			continue
		} else if e != nil {
			return e
		}
		part.prepared = filepath.Join(part.stage, part.name)
		if e = copyTree(source, part.prepared); e != nil {
			return e
		}
	}
	for i := range parts {
		part := &parts[i]
		if e = os.Rename(part.destination, filepath.Join(part.stage, "previous")); e == nil {
			part.parked = true
		} else if !os.IsNotExist(e) {
			return e
		}
		if part.prepared != "" {
			if e = os.Rename(part.prepared, part.destination); e != nil {
				return e
			}
			part.replaced = true
		}
	}
	state.Plugin.Manifest = state.Manifest
	state.Plugin.Config = state.Config
	state.Plugin.Operation = pending
	return m.db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Where("plugin_id = ?", id).Delete(&store.PluginKV{}).Error; e != nil {
			return e
		}
		for _, kv := range state.KV {
			if e := tx.Create(&kv).Error; e != nil {
				return e
			}
		}
		return tx.Save(&state.Plugin).Error
	})
}
func (m *Module) recoverOperation(p store.Plugin) error {
	var op operation
	if p.Operation == nil {
		return nil
	}
	if json.Unmarshal([]byte(*p.Operation), &op) != nil {
		return errors.New("插件操作记录损坏")
	}
	if op.Kind == "uninstall" {
		return m.remove(p, op.Purge)
	}
	if op.Kind != "upgrade" {
		return errors.New("未知中断操作，等待人工处理")
	}
	if op.Phase == "snapshotting" {
		return m.db.Model(&p).Update("operation", nil).Error
	}
	return m.restore(p.ID, op.Snapshot, nil)
}
func (m *Module) upgrade(c *gin.Context) {
	var in struct {
		UploadID string            `json:"uploadId"`
		Values   map[string]string `json:"values"`
	}
	if !response.Bind(c, &in) {
		return
	}
	p, ok := m.row(c)
	if !ok {
		return
	}
	u, dir, ok := m.consume(c, in.UploadID)
	if !ok {
		return
	}
	cmp, e := CompareVersion(u.Manifest.Version, p.Version)
	if e != nil || u.Manifest.ID != p.ID || cmp <= 0 {
		os.RemoveAll(dir)
		response.Fail(c, 3002, "升级必须为同一插件的更高版本")
		return
	}
	var old map[string]string
	if json.Unmarshal([]byte(p.Config), &old) != nil {
		os.RemoveAll(dir)
		response.Fail(c, 5000, "")
		return
	}
	var beforeManifest Manifest
	if json.Unmarshal([]byte(p.Manifest), &beforeManifest) != nil {
		os.RemoveAll(dir)
		response.Fail(c, 5000, "")
		return
	}
	for _, nextEnv := range u.Manifest.Env {
		for _, oldEnv := range beforeManifest.Env {
			if nextEnv.Key == oldEnv.Key && nextEnv.Secret != oldEnv.Secret {
				if _, provided := in.Values[nextEnv.Key]; !provided {
					delete(old, nextEnv.Key)
				}
			}
		}
	}
	candidate, e := m.candidate(u.Manifest, old, in.Values, false)
	if e != nil {
		os.RemoveAll(dir)
		response.Fail(c, response.Invalid, e.Error())
		return
	}
	missingRequired := false
	for _, v := range u.Manifest.Env {
		if v.Required && candidate[v.Key] == "" {
			missingRequired = true
			break
		}
	}
	session := auth.Current(c)
	id, e := m.tasks.Submit(task.Meta{Module: "plugins", Action: "upgrade", Target: p.ID, ResourceID: p.ID, LockKey: "plugin:" + p.ID, Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		defer os.RemoveAll(dir)
		var current store.Plugin
		if e := m.db.First(&current, "id = ?", p.ID).Error; e != nil {
			return nil, e
		}
		if current.Version != p.Version || current.Config != p.Config {
			return nil, errors.New("插件配置已变化，请重新检查升级")
		}
		if current.Operation != nil {
			return nil, errors.New("插件存在未恢复的操作记录")
		}
		p = current
		revoked, err := m.grantPreview(p.ID, beforeManifest, u.Manifest)
		if err != nil {
			return nil, err
		}
		if !slices.Equal(revoked, u.RevokedGrants) {
			return nil, errors.New("插件授权已变化，请重新检查升级")
		}
		if err := m.checkDependencies(u.Manifest, p.Enabled); err != nil {
			return nil, err
		}
		if beforeManifest.BackupProvider != nil && u.Manifest.BackupProvider == nil {
			if err := m.services.Backups.PluginStorageInUse(p.ID); err != nil {
				return nil, err
			}
		}
		if beforeManifest.NotificationProvider != nil && u.Manifest.NotificationProvider == nil {
			if err := m.services.Alerts.PluginProviderInUse(p.ID); err != nil {
				return nil, err
			}
		}
		if beforeManifest.DNSProvider != nil && u.Manifest.DNSProvider == nil {
			if err := m.services.Certificates.DNSProviderInUse(p.ID); err != nil {
				return nil, err
			}
		}
		if e := m.checkSnapshotSpace(p.ID); e != nil {
			return nil, e
		}
		backup := filepath.Join(m.historyDir(p.ID), log.ID())
		op := operation{Kind: "upgrade", TaskID: log.ID(), Phase: "snapshotting", Snapshot: backup}
		if e := m.record(&p, op); e != nil {
			return nil, e
		}
		m.stop(p.ID)
		abortBeforeReplace := func(cause error) (any, error) {
			clearErr := m.db.Model(&p).Update("operation", nil).Error
			if clearErr == nil {
				m.pruneHistory(p.ID)
			}
			if p.Enabled {
				if startErr := m.start(p); startErr != nil {
					m.setStatus(p.ID, "failed")
					cause = errors.Join(cause, errors.New("旧插件重新启动失败"), startErr)
				}
			}
			return nil, errors.Join(cause, clearErr)
		}
		if e := m.snapshot(p, backup); e != nil {
			return abortBeforeReplace(errors.Join(errors.New("完整快照失败"), e))
		}
		op.Phase = "replacing"
		if e := m.record(&p, op); e != nil {
			return abortBeforeReplace(errors.Join(errors.New("无法记录插件升级阶段"), e))
		}
		rollback := func(message string, cause error) (any, error) {
			m.stop(p.ID)
			if e := m.restore(p.ID, backup, nil); e != nil {
				m.setStatus(p.ID, "failed")
				return nil, errors.Join(errors.New(message+"；完整快照恢复失败，已保留操作记录"), cause, e)
			}
			m.pruneHistory(p.ID)
			if p.Enabled {
				if e := m.start(p); e != nil {
					m.setStatus(p.ID, "failed")
					return nil, errors.Join(errors.New(message+"；旧包已恢复，旧进程启动失败"), cause, e)
				}
			}
			return nil, errors.Join(errors.New(message+"；已恢复完整旧快照"), cause)
		}
		for _, entry := range u.Manifest.Entry {
			if e := os.Chmod(filepath.Join(dir, "contents", entry), 0755); e != nil {
				return rollback("新版入口准备失败", e)
			}
		}
		pkg := filepath.Join(m.packages(), p.ID)
		if e := os.RemoveAll(pkg); e != nil {
			return rollback("旧包移除失败", e)
		}
		if e := os.Rename(filepath.Join(dir, "contents"), pkg); e != nil {
			return rollback("新版包替换失败", e)
		}
		next := p
		next.Version = u.Manifest.Version
		next.Name = u.Manifest.Name
		next.Type = u.Manifest.Type
		if missingRequired {
			next.Enabled = false
		}
		manifest, _ := json.Marshal(publicManifest(u.Manifest))
		cfg, _ := json.Marshal(candidate)
		next.Manifest = string(manifest)
		next.Config = string(cfg)
		next.UpdatedAt = time.Now().UTC()
		if e := m.db.Save(&next).Error; e != nil {
			return rollback("候选配置保存失败", e)
		}
		if next.Enabled {
			if e := m.start(next); e != nil {
				return rollback("新版启动失败", e)
			}
		} else if missingRequired {
			m.setStatus(p.ID, "disabled")
		}
		if e := m.db.Transaction(func(tx *gorm.DB) error {
			if err := m.reconcileGrants(tx, p.ID, beforeManifest, u.Manifest); err != nil {
				return err
			}
			if err := tx.Exec("INSERT INTO plugin_history (plugin_id, snapshot) VALUES (?, ?) ON CONFLICT (plugin_id) DO UPDATE SET snapshot=excluded.snapshot", p.ID, log.ID()).Error; err != nil {
				return err
			}
			return tx.Model(&next).Update("operation", nil).Error
		}); e != nil {
			return rollback("升级提交失败", e)
		}
		m.restoreSchedules(next)
		m.pruneHistory(p.ID)
		m.notifyStartup()
		if missingRequired {
			log.Line("插件升级已提交；因新增必填配置项尚未填写，插件已暂停，请在设置窗口完善配置后启动")
		} else {
			log.Line("插件升级已提交；仅托管快照可回滚，外部业务副作用不在恢复范围")
		}
		return gin.H{"pluginId": p.ID, "version": next.Version}, nil
	})
	if e != nil {
		os.RemoveAll(dir)
		response.Fail(c, response.Conflict, "插件忙或任务提交失败")
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
func (m *Module) remove(p store.Plugin, purge bool) error {
	m.stop(p.ID)
	if e := os.RemoveAll(filepath.Join(m.packages(), p.ID)); e != nil {
		return errors.New("插件包删除失败，仍有残留")
	}
	if e := os.RemoveAll(filepath.Join(m.packages(), ".backup", p.ID)); e != nil {
		return errors.New("插件快照删除失败，仍有残留")
	}
	if e := os.RemoveAll(filepath.Join(m.packages(), ".backup", ".rollback", p.ID)); e != nil {
		return errors.New("插件恢复快照删除失败，仍有残留")
	}
	if e := os.RemoveAll(m.historyDir(p.ID)); e != nil {
		return errors.New("插件历史快照删除失败，仍有残留")
	}
	if purge {
		if e := os.RemoveAll(filepath.Join(m.private(), p.ID)); e != nil {
			return errors.New("插件私有数据删除失败，仍有残留")
		}
		for _, suffix := range []string{"", ".1", ".2"} {
			if err := os.Remove(m.logPath(p.ID) + suffix); err != nil && !os.IsNotExist(err) {
				return errors.New("插件日志删除失败")
			}
		}
	}
	err := m.db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Delete(&store.Setting{}, "key = ?", grantPrefix+p.ID).Error; e != nil {
			return e
		}
		if e := tx.Where("plugin_id = ?", p.ID).Delete(&store.PluginKV{}).Error; e != nil {
			return e
		}
		if purge {
			if e := tx.Exec("DELETE FROM plugin_sandbox_users WHERE plugin_id = ?", p.ID).Error; e != nil {
				return e
			}
		}
		return tx.Delete(&p).Error
	})
	if err == nil {
		m.replaceSchedules(p.ID, Manifest{}, capabilityGrant{})
	}
	return err
}
func (m *Module) uninstall(c *gin.Context) {
	var in struct {
		PurgeData bool `json:"purgeData"`
	}
	if !response.Bind(c, &in) {
		return
	}
	p, ok := m.row(c)
	if !ok {
		return
	}
	s := auth.Current(c)
	id, e := m.tasks.Submit(task.Meta{Module: "plugins", Action: "uninstall", Target: p.ID, ResourceID: p.ID, LockKey: "plugin:" + p.ID, Username: s.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		if err := m.checkDependents(p.ID, ""); err != nil {
			return nil, err
		}
		if err := m.services.Backups.PluginStorageInUse(p.ID); err != nil {
			return nil, err
		}
		if err := m.services.Alerts.PluginProviderInUse(p.ID); err != nil {
			return nil, err
		}
		if err := m.services.Certificates.DNSProviderInUse(p.ID); err != nil {
			return nil, err
		}
		if e := m.record(&p, operation{Kind: "uninstall", TaskID: log.ID(), Phase: "removing", Purge: in.PurgeData}); e != nil {
			return nil, e
		}
		if e := m.remove(p, in.PurgeData); e != nil {
			return nil, e
		}
		log.Line("插件包、配置和 KV 已删除；外部业务资源保留")
		return gin.H{"pluginId": p.ID, "privateDataRetained": !in.PurgeData}, nil
	})
	if e != nil {
		response.Fail(c, response.Conflict, "插件忙或任务提交失败")
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
