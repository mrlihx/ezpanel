package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"gorm.io/gorm"
)

func (m *Module) notifyStartup() {
	select {
	case m.startupWake <- struct{}{}:
	default:
	}
	select {
	case m.eventWake <- struct{}{}:
	default:
	}
}

func (m *Module) startupError(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startupErrors[id]
}

func (m *Module) bootStatus(id, status string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[id] = status
	if err == nil {
		delete(m.startupErrors, id)
	} else {
		m.startupErrors[id] = err.Error()
	}
}

func (m *Module) startOnBoot(rows []store.Plugin) {
	pending := make(map[string]Manifest, len(rows))
	for _, row := range rows {
		select {
		case <-m.closed:
			return
		default:
		}
		release, err := m.waitRestartLock(row.ID, 0)
		if err != nil {
			return
		}
		if err := m.db.Take(&row, "id = ?", row.ID).Error; err != nil {
			release()
			continue
		}
		if row.Operation != nil {
			err = m.recoverOperation(row)
			if err == nil {
				err = m.db.Take(&row, "id = ?", row.ID).Error
			}
			if err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					m.bootStatus(row.ID, "failed", err)
				}
				release()
				continue
			}
			if row.Enabled {
				m.bootStatus(row.ID, "waiting", nil)
			} else {
				m.bootStatus(row.ID, "disabled", nil)
			}
		}
		m.pruneHistory(row.ID)
		m.restoreSchedules(row)
		if row.Enabled && m.status(row) == "waiting" {
			var mf Manifest
			if err := json.Unmarshal([]byte(row.Manifest), &mf); err != nil {
				m.bootStatus(row.ID, "failed", errors.New("插件清单无效"))
			} else {
				pending[row.ID] = mf
			}
		}
		release()
	}
	for len(pending) > 0 {
		var wave sync.WaitGroup
		count := 0
		for _, id := range slices.Sorted(maps.Keys(pending)) {
			state := m.statusByID(id)
			if state != "waiting" && state != "blocked" {
				delete(pending, id)
				continue
			}
			if waveReady := m.waitingDependency(pending[id]); waveReady != "" {
				continue
			}
			delete(pending, id)
			wave.Go(func() { m.startFromQueue(id) })
			count++
			if count == 3 {
				break
			}
		}
		wave.Wait()
		progress := false
		for id, manifest := range pending {
			if dependency := m.waitingDependency(manifest); dependency != "" {
				m.mu.Lock()
				if m.statuses[id] == "waiting" || m.statuses[id] == "blocked" {
					m.statuses[id] = "blocked"
					m.startupErrors[id] = fmt.Sprintf("等待依赖插件 %s 启动", dependency)
					if m.statuses[dependency] == "failed" && m.startupErrors[dependency] != "" {
						m.startupErrors[id] = fmt.Sprintf("依赖插件 %s 启动失败：%s", dependency, m.startupErrors[dependency])
					}
				}
				m.mu.Unlock()
			} else {
				progress = true
			}
		}
		if !progress && len(pending) > 0 {
			select {
			case <-m.closed:
				return
			case <-m.startupWake:
			}
		}
	}
}

func (m *Module) waitingDependency(manifest Manifest) string {
	for _, id := range slices.Sorted(maps.Keys(manifest.Dependencies)) {
		if m.statusByID(id) != "running" {
			return id
		}
	}
	return ""
}

func (m *Module) startFromQueue(id string) {
	release, err := m.waitRestartLock(id, 0)
	if err != nil {
		if !errors.Is(err, task.ErrBusy) {
			slog.Debug("取消启动插件", "plugin", id, "error", err)
		}
		return
	}
	defer release()
	var row store.Plugin
	if m.db.Take(&row, "id = ?", id).Error != nil || !row.Enabled || row.Operation != nil {
		return
	}
	state := m.status(row)
	if state != "waiting" && state != "blocked" {
		return
	}
	if err := m.start(row); err != nil {
		m.bootStatus(id, "failed", err)
		slog.Warn("插件启动失败", "plugin", id, "error", err)
	} else {
		m.notifyStartup()
	}
}
