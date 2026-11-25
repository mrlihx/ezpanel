package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
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
		if e = os.MkdirAll(dst, info.Mode().Perm()); e != nil {
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
func (m *Module) restore(id, path string) error {
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
	pkg := filepath.Join(m.packages(), id)
	data := filepath.Join(m.private(), id)
	if e = os.RemoveAll(pkg); e != nil {
		return e
	}
	if e = copyTree(filepath.Join(path, "package"), pkg); e != nil {
		return e
	}
	if e = os.RemoveAll(data); e != nil {
		return e
	}
	if _, e = os.Lstat(filepath.Join(path, "data")); e == nil {
		if e = copyTree(filepath.Join(path, "data"), data); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	state.Plugin.Manifest = state.Manifest
	state.Plugin.Config = state.Config
	state.Plugin.Operation = nil
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
	return m.restore(p.ID, op.Snapshot)
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
					os.RemoveAll(dir)
					response.Fail(c, response.Invalid, "配置秘密属性变化，请显式填写："+nextEnv.Key)
					return
				}
			}
		}
	}
	candidate, e := m.candidate(u.Manifest, old, in.Values, true)
	if e != nil {
		os.RemoveAll(dir)
		response.Fail(c, response.Invalid, e.Error())
		return
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
		p = current
		backup := filepath.Join(m.packages(), ".backup", p.ID)
		if e := os.RemoveAll(backup); e != nil {
			return nil, e
		}
		op := operation{Kind: "upgrade", TaskID: log.ID(), Phase: "snapshotting", Snapshot: backup}
		if e := m.record(&p, op); e != nil {
			return nil, e
		}
		m.stop(p.ID)
		abortBeforeReplace := func(cause error) (any, error) {
			clearErr := m.db.Model(&p).Update("operation", nil).Error
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
		rollback := func(cause string) (any, error) {
			m.stop(p.ID)
			if e := m.restore(p.ID, backup); e != nil {
				m.setStatus(p.ID, "failed")
				return nil, errors.New(cause + "；完整快照恢复失败，保留操作记录等待人工处理")
			}
			if p.Enabled {
				if e := m.start(p); e != nil {
					m.setStatus(p.ID, "failed")
					return nil, errors.New(cause + "；旧包已恢复但旧进程恢复失败")
				}
			}
			return nil, errors.New(cause + "；已恢复完整旧快照")
		}
		for _, entry := range u.Manifest.Entry {
			if e := os.Chmod(filepath.Join(dir, "contents", entry), 0755); e != nil {
				return rollback("新版入口准备失败")
			}
		}
		pkg := filepath.Join(m.packages(), p.ID)
		if e := os.RemoveAll(pkg); e != nil {
			return rollback("旧包移除失败")
		}
		if e := os.Rename(filepath.Join(dir, "contents"), pkg); e != nil {
			return rollback("新版包替换失败")
		}
		next := p
		next.Version = u.Manifest.Version
		next.Name = u.Manifest.Name
		next.Type = u.Manifest.Type
		manifest, _ := json.Marshal(publicManifest(u.Manifest))
		cfg, _ := json.Marshal(candidate)
		next.Manifest = string(manifest)
		next.Config = string(cfg)
		next.UpdatedAt = time.Now().UTC()
		if e := m.db.Save(&next).Error; e != nil {
			return rollback("候选配置保存失败")
		}
		if p.Enabled {
			if e := m.start(next); e != nil {
				return rollback("新版启动失败")
			}
		}
		if e := m.db.Model(&next).Update("operation", nil).Error; e != nil {
			return rollback("升级提交失败")
		}
		log.Line("插件升级已提交；仅托管快照可回滚，外部业务副作用不在恢复范围")
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
	if purge {
		if e := os.RemoveAll(filepath.Join(m.private(), p.ID)); e != nil {
			return errors.New("插件私有数据删除失败，仍有残留")
		}
	}
	return m.db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Where("plugin_id = ?", p.ID).Delete(&store.PluginKV{}).Error; e != nil {
			return e
		}
		return tx.Delete(&p).Error
	})
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
