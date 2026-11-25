//go:build !linux

package sysinfo

import (
	"context"
	"errors"
)

func collectNetwork(context.Context, networkSnapshot) (networkSnapshot, error) {
	return networkSnapshot{}, errors.New("进程网络详情仅支持 Linux 的 inet_diag 与 /proc 接口")
}
