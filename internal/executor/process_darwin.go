package executor

import (
	"errors"
	"os/exec"
	"syscall"
)

func pluginCredentials(cmd *exec.Cmd, o ProcessOptions) error {
	if !o.Dev {
		return errors.New("非 Linux 插件进程仅支持 dev")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
