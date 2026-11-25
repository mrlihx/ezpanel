//go:build linux

package sysinfo

import (
	"context"

	"ezpanel/internal/response"
	"golang.org/x/sys/unix"
)

func terminatePID(ctx context.Context, pid int32, started int64) error {
	// 先固定目标；后续即使 PID 被复用，也只向这个 pidfd 指向的进程发信号。
	fd, e := unix.PidfdOpen(int(pid), 0)
	if e != nil {
		return &processFailure{response.System, "无法固定目标进程，系统不支持或进程已退出；未发送信号"}
	}
	defer unix.Close(fd)
	identity, ancestors, e := liveIdentity(ctx, pid)
	if e != nil {
		return e
	}
	if identity.Started != started {
		return &processFailure{response.Conflict, "进程身份已变化，请刷新列表"}
	}
	if reason := protection(identity, ancestors); reason != "" {
		return &processFailure{response.Forbidden, reason}
	}
	if e := ctx.Err(); e != nil {
		return &processFailure{response.System, "身份读取超时，未发送信号"}
	}
	if e := unix.PidfdSendSignal(fd, unix.SIGTERM, nil, 0); e != nil {
		return &processFailure{response.System, "SIGTERM发送失败，进程可能已退出或权限不足"}
	}
	return nil
}
