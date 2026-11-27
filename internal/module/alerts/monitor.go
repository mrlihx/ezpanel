package alerts

import (
	"context"
	"errors"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type target struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PluginCheck struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PluginChecks interface {
	MonitorChecks(context.Context) ([]PluginCheck, error)
	RunMonitorCheck(context.Context, string) (name, problem string, err error)
}

func (m *Module) SetPluginChecks(checks PluginChecks) { m.checks = checks }

var projectTypes = []string{"go", "python", "nodejs"}
var databaseTypes = []string{"mysql", "postgresql", "mongodb", "redis", "memcached"}

func (m *Module) targets(c *gin.Context) {
	ctx := c.Request.Context()
	db := m.db.WithContext(ctx)
	rows := []target{}
	var err error
	switch c.Query("kind") {
	case "service":
		for _, app := range m.packages.Catalog(ctx) {
			if !pkgmgr.IsDatabaseContainer(app.ID) && app.Installed && app.Running != nil {
				rows = append(rows, target{ID: app.ID, Name: app.Name})
			}
		}
		for _, engine := range databaseTypes {
			instances, scanErr := m.packages.DatabaseInstances(ctx, engine)
			if scanErr != nil {
				continue
			}
			for _, instance := range instances {
				rows = append(rows, target{ID: engine + "/" + instance.ID, Name: pkgmgr.Name(engine) + " · " + instance.Name})
			}
		}
	case "project":
		err = db.Model(&store.Website{}).Select("id, name").Where("project_type IN ?", projectTypes).Order("name, id").Find(&rows).Error
	case "certificate":
		err = db.Table("websites AS w").Select("w.id, w.name").Joins("JOIN certificates AS cert ON cert.id = w.certificate_id").Where("w.ssl_enabled = ?", true).Order("w.name, w.id").Find(&rows).Error
	case "plugin":
		if m.checks != nil {
			var checks []PluginCheck
			checks, err = m.checks.MonitorChecks(ctx)
			for _, check := range checks {
				rows = append(rows, target(check))
			}
		}
	default:
		response.Fail(c, response.Invalid, "告警类型无效")
		return
	}
	if err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	response.OK(c, gin.H{"list": rows})
}

func (m *Module) checkTarget(ctx context.Context, in RuleInput, now time.Time) (name, problem string, err error) {
	switch in.Kind {
	case "service":
		if engine, id, ok := strings.Cut(in.Target, "/"); ok {
			instance, err := m.packages.DatabaseInstance(ctx, engine, id)
			if err != nil || instance.ID == "" {
				return "", "", errors.New("无法读取所选数据库实例的状态")
			}
			name = pkgmgr.Name(engine) + " · " + instance.Name
			if !instance.Running {
				problem = name + " 已停止运行"
			}
			return name, problem, nil
		}
		app, err := m.packages.Status(ctx, in.Target)
		if err != nil || !app.Installed || app.Running == nil {
			return "", "", errors.New("无法读取所选服务的运行状态")
		}
		if !*app.Running {
			problem = app.Name + " 已停止运行"
		}
		return app.Name, problem, nil
	case "project":
		var row store.Website
		if err := m.db.WithContext(ctx).Select("id, name").Where("id = ? AND project_type IN ?", in.Target, projectTypes).Take(&row).Error; err != nil {
			return "", "", errors.New("无法读取所选项目")
		}
		service, err := m.packages.ProjectService(ctx, row.ID)
		if err != nil {
			return "", "", errors.New("无法读取所选项目的运行状态")
		}
		if !service.Running {
			problem = row.Name + " 已停止运行"
		}
		return row.Name, problem, nil
	case "certificate":
		var row struct {
			Name     string
			NotAfter time.Time
		}
		if err := m.db.WithContext(ctx).Table("websites AS w").Select("w.name, cert.not_after").Joins("JOIN certificates AS cert ON cert.id = w.certificate_id").Where("w.id = ? AND w.ssl_enabled = ?", in.Target, true).Take(&row).Error; err != nil {
			return "", "", errors.New("站点未启用可检测的 HTTPS 证书")
		}
		if !row.NotAfter.After(now.Add(time.Duration(in.Days) * 24 * time.Hour)) {
			problem = fmt.Sprintf("%s 使用的 HTTPS 证书到期时间为 %s", row.Name, row.NotAfter.Format(time.RFC3339))
		}
		return row.Name, problem, nil
	case "plugin":
		if m.checks != nil {
			return m.checks.RunMonitorCheck(ctx, in.Target)
		}
		return "", "", errors.New("未配置插件检测")
	default:
		return "", "", errors.New("告警类型无效")
	}
}

type pendingDelivery struct {
	ID          string
	RuleID      string
	ChannelID   string
	ChannelName string
	Kind        string
	Title       string
	Message     string
	CreatedAt   time.Time
}

func (pendingDelivery) TableName() string { return "alert_delivery_queue" }

func (m *Module) Run(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.runDelivery(ctx)
	}()
	defer func() { <-done }()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		if err := m.checkDue(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("告警检查失败", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Module) checkDue(ctx context.Context) error {
	var rules []rule
	if err := m.db.WithContext(ctx).Where("enabled = ? AND next_check_at <= ?", true, time.Now().UTC()).Order("next_check_at, id").Limit(20).Find(&rules).Error; err != nil {
		return err
	}
	zone, err := time.LoadLocation(m.cfg.Current().Timezone)
	if err != nil {
		zone = time.UTC
	}
	for _, row := range rules {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		now := time.Now().UTC()
		if err := m.checkRule(ctx, row, now, now.In(zone).Format(time.DateOnly)); err != nil {
			slog.Warn("告警任务执行失败", "rule", row.ID, "error", err)
		}
	}
	return nil
}

func (m *Module) checkRule(ctx context.Context, row rule, now time.Time, date string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	name, problem, probeErr := m.checkTarget(probeCtx, row.RuleInput, now)
	cancel()
	updates := map[string]any{
		"state": "normal", "last_checked_at": now, "next_check_at": now.Add(time.Duration(row.IntervalSeconds) * time.Second),
	}
	if probeErr != nil {
		updates["state"] = "error"
	} else {
		updates["target_name"] = name
		if problem != "" {
			updates["state"] = "triggered"
		}
	}
	if row.SentDate != date {
		row.SentToday = 0
	}
	var channels []channel
	db := m.db.WithContext(ctx)
	if problem != "" && row.SentToday < row.MaxDaily {
		selected := db.Model(&ruleChannel{}).Select("channel_id").Where("rule_id = ?", row.ID)
		if err := db.Where("enabled = ? AND id IN (?)", true, selected).Order("id").Find(&channels).Error; err != nil {
			return err
		}
		if len(channels) > 0 {
			updates["sent_date"] = date
			updates["sent_today"] = row.SentToday + 1
		}
	}
	// 检测进度、每日额度和待投递记录一起提交。
	return db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&rule{}).Where("id = ? AND enabled = ? AND updated_at = ?", row.ID, true, row.UpdatedAt).UpdateColumns(updates)
		if result.Error != nil || result.RowsAffected == 0 || len(channels) == 0 {
			return result.Error
		}
		queue := make([]pendingDelivery, 0, len(channels))
		for _, item := range channels {
			queue = append(queue, pendingDelivery{
				ID: store.ID(), RuleID: row.ID, ChannelID: item.ID, ChannelName: item.Name,
				Kind: item.Kind, Title: row.Title, Message: problem, CreatedAt: now,
			})
		}
		return tx.CreateInBatches(queue, 100).Error
	})
}

const deliveryWorkers = 4

func (m *Module) runDelivery(ctx context.Context) {
	type completion struct {
		id  string
		err error
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	done := make(chan completion, deliveryWorkers)
	active := make(map[string]bool, deliveryWorkers)
	retry := false
	for ctx.Err() == nil {
		if !retry && len(active) < deliveryWorkers {
			inFlight := make([]string, 0, len(active))
			for id := range active {
				inFlight = append(inFlight, id)
			}
			rows, err := m.queuedDeliveries(ctx, deliveryWorkers-len(active), inFlight)
			if err != nil && ctx.Err() == nil {
				slog.Warn("读取待投递告警失败", "error", err)
			}
			for _, row := range rows {
				active[row.ID] = true
				go func() { done <- completion{row.ID, m.deliverQueued(ctx, row)} }()
			}
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
			retry = false
		case result := <-done:
			delete(active, result.id)
			if result.err != nil {
				retry = true
				slog.Warn("记录告警投递结果失败", "id", result.id, "error", result.err)
			}
		}
	}
	for range len(active) {
		<-done
	}
}

func (m *Module) queuedDeliveries(ctx context.Context, limit int, inFlight []string) ([]pendingDelivery, error) {
	var rows []pendingDelivery
	query := m.db.WithContext(ctx).Order("created_at, id").Limit(limit)
	if len(inFlight) > 0 {
		query = query.Where("id NOT IN ?", inFlight)
	}
	err := query.Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (m *Module) deliverQueued(ctx context.Context, row pendingDelivery) error {
	if ctx.Err() != nil {
		return nil
	}
	var item channel
	sendErr := m.db.WithContext(ctx).First(&item, "id = ?", row.ChannelID).Error
	switch {
	case errors.Is(sendErr, gorm.ErrRecordNotFound), sendErr == nil && !item.Enabled:
		sendErr = errors.New("告警通道已停用或删除")
	case sendErr != nil:
		return sendErr
	default:
		sendErr = m.send(ctx, item, row.Title, row.Message)
	}
	// 已开始的投递在关闭时也保留结果，避免启动后重复发送。
	return m.db.Transaction(func(tx *gorm.DB) error {
		log := deliveryResult(row.RuleID, row.Title, channel{Name: row.ChannelName, Kind: row.Kind}, sendErr)
		if err := tx.Create(log).Error; err != nil {
			return err
		}
		return tx.Delete(&row).Error
	})
}
