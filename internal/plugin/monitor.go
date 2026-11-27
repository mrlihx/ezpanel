package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"ezpanel/internal/module/alerts"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
)

const monitorCallback = "_panel/check"

func (m *Module) MonitorChecks(ctx context.Context) ([]alerts.PluginCheck, error) {
	var rows []store.Plugin
	if err := m.db.WithContext(ctx).Select("id, name, manifest").Where("enabled = ? AND type = ?", true, "process").Order("name, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	checks := []alerts.PluginCheck{}
	for _, row := range rows {
		var mf Manifest
		if m.status(row) == "running" && json.Unmarshal([]byte(row.Manifest), &mf) == nil && m.checkPermission(row.ID, mf, "monitoring.provide") == nil {
			for _, check := range mf.Checks {
				checks = append(checks, alerts.PluginCheck{ID: row.ID + "/" + check.ID, Name: row.Name + " · " + check.Title})
			}
		}
	}
	return checks, nil
}

func (m *Module) RunMonitorCheck(ctx context.Context, target string) (string, string, error) {
	id, name, _ := strings.Cut(target, "/")
	run, mf, err := m.readyPlugin(id)
	index := slices.IndexFunc(mf.Checks, func(check MonitorCheck) bool { return check.ID == name })
	if err != nil || index < 0 {
		return "", "", errors.New("插件检测不可用")
	}
	if err := m.checkPermission(id, mf, "monitoring.provide"); err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	reply, err := invokePlugin(ctx, run, id, monitorCallback, map[string]string{"checkId": name})
	var out struct {
		Healthy *bool
		Message string
	}
	if err != nil || json.Unmarshal(reply.Data, &out) != nil || out.Healthy == nil {
		return "", "", errors.New("插件检测未返回有效结果")
	}
	title := mf.Name + " · " + mf.Checks[index].Title
	if *out.Healthy {
		return title, "", nil
	}
	message := strings.TrimSpace(response.Summary(out.Message, run.log.secretValues()...))
	if message == "" {
		message = title + " 检测异常"
	}
	return title, message, nil
}
