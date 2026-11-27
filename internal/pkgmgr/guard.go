package pkgmgr

import (
	"context"
	"fmt"
	"strings"
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

func (m *Manager) enableSystemdStartup(ctx context.Context, timeout time.Duration, unit string) error {
	triggers, err := m.Run(ctx, timeout, nil, systemctlPath, "show", unit, "--property=TriggeredBy", "--value")
	if err != nil {
		return fmt.Errorf("读取服务激活单元失败：%w", err)
	}
	units := append([]string{unit}, strings.Fields(triggers.Stdout)...)
	args := []string{"enable"}
	for _, name := range units {
		result, err := m.Run(ctx, timeout, nil, systemctlPath, "is-enabled", name)
		switch strings.TrimSpace(result.Stdout) {
		case "disabled", "enabled-runtime", "linked", "linked-runtime":
			args = append(args, name)
		case "enabled", "static", "indirect", "alias", "generated", "transient", "masked", "masked-runtime":
			// 静态单元由依赖、socket 或 timer 启动，本身无需 enable。
		default:
			if err != nil {
				return fmt.Errorf("读取 %s 的开机自启状态失败：%w", name, err)
			}
		}
	}
	if len(args) > 1 {
		if _, err := m.Run(ctx, timeout, nil, systemctlPath, args...); err != nil {
			return fmt.Errorf("服务已启动，设置开机自启失败：%w", err)
		}
	}
	return nil
}
