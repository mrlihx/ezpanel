package pkgmgr

import (
	"context"
	"time"
)

// 初次受控安装默认开启守护，后续保留管理员通过设置显式关闭的策略。
func (m *Manager) ensureGuard(ctx context.Context, unit string) error {
	path := "/etc/systemd/system/" + unit + ".d/ezpanel-guard.conf"
	if err := writeInitial(path, "[Service]\nRestart=always\nRestartSec=3s\n", 0644); err != nil {
		return err
	}
	_, err := m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "daemon-reload")
	return err
}
