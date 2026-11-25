//go:build !linux

package sysinfo

import (
	"context"
	"ezpanel/internal/response"
)

func terminatePID(context.Context, int32, int64) error {
	return &processFailure{response.Forbidden, "进程终止仅支持具备pidfd的Linux系统"}
}
