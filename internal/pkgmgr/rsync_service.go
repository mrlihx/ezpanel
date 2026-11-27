package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const rsyncConfig = "/etc/rsyncd.conf"

func (m *Manager) rsyncDefaultConfig() string {
	return "address = 0.0.0.0\nport = 873\nlog file = " + m.layout().RsyncLog() + "\n"
}

func (m *Manager) prepareRsync() error {
	if err := ensurePublicTree(m.layout().Root(), m.layout().LogDir(), m.layout().RsyncLogDir()); err != nil {
		return err
	}
	if _, err := os.Lstat(rsyncConfig); !os.IsNotExist(err) {
		return err
	}
	return writeHostConfig(rsyncConfig, []byte(m.rsyncDefaultConfig()))
}

func (m *Manager) rsyncDetachedPath() string {
	return filepath.Join(m.cfg.DataDir(), "rsync-detached")
}

func (m *Manager) enableRsync(ctx context.Context, unit string) error {
	if _, err := m.Run(ctx, 30*time.Second, nil, systemctlPath, "enable", "--now", unit); err != nil {
		return err
	}
	if _, err := m.Run(ctx, 10*time.Second, nil, systemctlPath, "is-active", "--quiet", unit); err != nil {
		return errors.New("Rsync 接收服务未启动，请检查服务日志")
	}
	if err := os.Remove(m.rsyncDetachedPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// 系统依赖要求保留包时，只移除面板管理的接收服务。
func (m *Manager) detachRsync(ctx context.Context, state State, dependency string, log func(string)) (State, error) {
	result, err := m.Run(ctx, 30*time.Second, nil, systemctlPath, "disable", "--now", state.Unit)
	if err != nil {
		return state, fmt.Errorf("停用 Rsync 接收服务失败：%s", strings.TrimSpace(result.Stdout+" "+result.Stderr))
	}
	if err := writeHostConfig(m.rsyncDetachedPath(), []byte(dependency+"\n")); err != nil {
		return state, err
	}
	log("已停用 Rsync 接收服务。保留系统软件包以满足 " + dependency + " 的依赖")
	return m.Status(ctx, "rsync")
}
