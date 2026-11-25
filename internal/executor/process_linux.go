package executor

import (
	"errors"
	"os/exec"
	"syscall"
)

func pluginCredentials(cmd *exec.Cmd, o ProcessOptions) error {
	if o.UID == 0 || o.GID == 0 {
		return errors.New("插件必须使用非 root UID/GID")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: &syscall.Credential{Uid: o.UID, Gid: o.GID, Groups: []uint32{}, NoSetGroups: false}}
	return nil
}
