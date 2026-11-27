package plugin

import (
	"context"
	"errors"
	"time"

	"ezpanel/internal/module/alerts"
)

const notificationCallback = "_panel/notification"

func (m *Module) NotificationProviderReady(id string) bool {
	_, manifest, err := m.readyPlugin(id)
	return err == nil && manifest.NotificationProvider != nil && m.checkPermission(id, manifest, "notifications.provider") == nil
}

func (m *Module) SendProviderNotification(ctx context.Context, id string, notice alerts.ProviderNotice) error {
	run, manifest, err := m.readyPlugin(id)
	if err != nil || manifest.NotificationProvider == nil {
		return errors.New("通知渠道插件未运行")
	}
	if err := m.checkPermission(id, manifest, "notifications.provider"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err = invokePlugin(ctx, run, id, notificationCallback, notice)
	return err
}
