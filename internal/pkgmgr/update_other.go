//go:build !linux

package pkgmgr

import (
	"context"
	"errors"
)

func (m *Manager) hotNginx(ctx context.Context, prefix, unit string, old oldProcess, log func(string)) error {
	return errors.New("当前系统不支持Nginx受控热升级")
}
