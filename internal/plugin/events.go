package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"ezpanel/internal/store"
)

func (m *Module) PublishTaskEvent(row store.Task) {
	event := Event{ResourceID: row.ResourceID, TaskID: row.ID, Status: row.Status, Action: row.Action}
	switch {
	case row.Module == "website" && (strings.HasPrefix(row.Action, "node-") || strings.HasPrefix(row.Action, "python-") || row.Action == "project-autostart"):
		event.Name, event.SiteID = "site.project.finished", row.ResourceID
	case strings.HasPrefix(row.Module, "plugin:") && row.Action == "compose-deploy":
		event.Name = "compose.task.finished"
	case strings.HasPrefix(row.Module, "plugin:") && row.Action == "database-backup":
		event.Name = "database.backup.finished"
	case row.Module == "cron" && row.Action == "backup_db" && row.CronJobID != nil:
		var cfg string
		if err := m.db.Model(&store.CronJob{}).Select("config").Where("id = ?", *row.CronJobID).Scan(&cfg).Error; err != nil {
			slog.Warn("无法读取数据库备份任务的资源", "task", row.ID, "error", err)
			return
		}
		var target struct{ TargetID string }
		if json.Unmarshal([]byte(cfg), &target) != nil || target.TargetID == "" {
			return
		}
		event.ResourceID, event.Name = target.TargetID, "database.backup.finished"
	case row.Module == "docker" && strings.HasPrefix(row.Action, "compose-"):
		event.Name = "compose.task.finished"
	case row.Module == "database" && row.Action == "backup":
		event.Name = "database.backup.finished"
	default:
		return
	}
	m.PublishEvent(event)
}

type eventDelivery struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	PluginID  string    `json:"-"`
	EventName string    `json:"eventName"`
	Payload   string    `json:"-"`
	Status    string    `json:"status"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"lastError,omitempty"`
	NextAt    time.Time `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (eventDelivery) TableName() string { return "plugin_event_deliveries" }

func (m *Module) PublishEvent(event Event) {
	select {
	case <-m.closed:
		return
	default:
	}
	if err := m.enqueueEvent(event); err != nil {
		slog.Warn("保存插件事件失败", "event", event.Name, "task", event.TaskID, "error", err)
		return
	}
	select {
	case m.eventWake <- struct{}{}:
	default:
	}
}

func acceptsEvent(grant capabilityGrant, event Event) bool {
	if !slices.Contains(grant.Events, event.Name) || grant.permissionDisabled("events.subscribe") {
		return false
	}
	if perm := eventPermissions[event.Name]; perm != "" && grant.permissionDisabled(perm) {
		return false
	}
	switch event.Name {
	case "site.backup.finished", "site.project.finished":
		return grant.allowsResource("sites", event.SiteID)
	case "database.backup.finished":
		return grant.allowsResource("databases", event.ResourceID)
	case "certificate.renewal.finished":
		return grant.allowsResource("certificates", event.ResourceID)
	case "compose.task.finished":
		return grant.allowsResource("compose", event.ResourceID)
	default:
		return true
	}
}

func (m *Module) enqueueEvent(event Event) error {
	var subscriptions []struct {
		ID, Manifest, Value string
	}
	if err := m.db.Table("plugins").Select("plugins.id, plugins.manifest, settings.value").Joins("JOIN settings ON settings.key = ? || plugins.id", grantPrefix).Where("plugins.enabled = ?", true).Find(&subscriptions).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	event.ID, event.Time = store.ID(), now
	payload, _ := json.Marshal(event)
	var deliveries []eventDelivery
	for _, sub := range subscriptions {
		var grant capabilityGrant
		var manifest Manifest
		if json.Unmarshal([]byte(sub.Value), &grant) != nil || !acceptsEvent(grant, event) || json.Unmarshal([]byte(sub.Manifest), &manifest) != nil || !slices.Contains(manifest.Events, event.Name) {
			continue
		}
		deliveries = append(deliveries, eventDelivery{ID: store.ID(), PluginID: sub.ID, EventName: event.Name, Payload: string(payload), Status: "pending", NextAt: now, CreatedAt: now, UpdatedAt: now})
	}
	if len(deliveries) > 0 {
		return m.db.CreateInBatches(deliveries, 50).Error
	}
	return nil
}

func (m *Module) dispatchEvents() {
	retry := time.NewTicker(15 * time.Second)
	cleanup := time.NewTicker(time.Hour)
	defer retry.Stop()
	defer cleanup.Stop()
	const workers = 4
	active := map[string]bool{}
	done := make(chan string, workers)
	var running sync.WaitGroup
	defer running.Wait()
	for {
		if available := workers - len(active); available > 0 {
			for _, row := range m.pendingEvents(active, available) {
				active[row.PluginID] = true
				running.Go(func() {
					m.attemptDelivery(row)
					done <- row.PluginID
				})
			}
		}
		select {
		case <-m.closed:
			return
		case <-m.eventWake:
		case <-retry.C:
		case id := <-done:
			delete(active, id)
		case <-cleanup.C:
			_ = m.db.Where("updated_at < ?", time.Now().UTC().AddDate(0, 0, -7)).Delete(&eventDelivery{}).Error
			_ = m.db.Exec("DELETE FROM plugin_webhook_receipts WHERE expires_at < ?", time.Now().Unix()).Error
		}
	}
}

func (m *Module) pendingEvents(active map[string]bool, limit int) []eventDelivery {
	excluded := make([]string, 0, len(active))
	for id := range active {
		excluded = append(excluded, id)
	}
	m.mu.Lock()
	for id, status := range m.statuses {
		if status == "waiting" || status == "blocked" || status == "recovering" || status == "starting" {
			excluded = append(excluded, id)
		}
	}
	m.mu.Unlock()
	query := m.db.Model(&eventDelivery{}).Select("*, ROW_NUMBER() OVER (PARTITION BY plugin_id ORDER BY next_at, id) AS position").
		Where("status = ? AND next_at <= ?", "pending", time.Now().UTC())
	if len(excluded) > 0 {
		query = query.Where("plugin_id NOT IN ?", excluded)
	}
	var deliveries []eventDelivery
	if err := m.db.Table("(?) AS ready", query).Where("position = 1").Order("next_at, id").Limit(limit).Find(&deliveries).Error; err != nil {
		slog.Warn("无法读取待投递插件事件", "error", err)
		return nil
	}
	return deliveries
}

func (m *Module) attemptDelivery(row eventDelivery) {
	var event Event
	grant, err := m.grant(row.PluginID)
	run, manifest, startupErr := m.readyPlugin(row.PluginID)
	status := "sent"
	if json.Unmarshal([]byte(row.Payload), &event) != nil || err == nil && !acceptsEvent(grant, event) || startupErr == nil && !slices.Contains(manifest.Events, event.Name) {
		status = "skipped"
		err = errors.New("订阅无效或已撤销")
	} else {
		if err == nil {
			err = startupErr
		}
		if err == nil {
			ctx, cancel := context.WithTimeout(run.ctx, 10*time.Second)
			_, err = invokePlugin(ctx, run, row.PluginID, eventCallback, event)
			cancel()
		}
	}
	row.Attempts++
	now := time.Now().UTC()
	lastError := ""
	if err != nil {
		lastError = err.Error()
		if status != "skipped" {
			status = "failed"
			if row.Attempts < 5 {
				status = "pending"
				row.NextAt = now.Add(15 * time.Second * time.Duration(1<<(row.Attempts-1)))
			}
		}
	}
	if err := m.db.Model(&row).Where("status = ?", "pending").Updates(map[string]any{"status": status, "attempts": row.Attempts, "last_error": lastError, "next_at": row.NextAt, "updated_at": now}).Error; err != nil {
		slog.Warn("保存插件事件投递结果失败", "plugin", row.PluginID, "event", row.EventName, "error", err)
	}
}
