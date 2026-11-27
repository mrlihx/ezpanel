package plugin

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"ezpanel/internal/executor"
	"gorm.io/gorm"
)

func (m *Module) sandboxUID(id string) (int, error) {
	var uid int
	err := m.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("plugin_sandbox_users").Select("uid").Where("plugin_id = ?", id).Scan(&uid).Error; err != nil {
			return err
		}
		if uid != 0 {
			if _, err := user.LookupId(strconv.Itoa(uid)); !errors.As(err, new(user.UnknownUserIdError)) {
				return errors.New("插件隔离 UID 已被系统账号占用")
			}
			return nil
		}
		for attempt := 0; attempt < 8; attempt++ {
			var random [4]byte
			if _, err := rand.Read(random[:]); err != nil {
				return err
			}
			uid = 200000 + int(binary.LittleEndian.Uint32(random[:])&0xffffff)
			if _, err := user.LookupId(strconv.Itoa(uid)); !errors.As(err, new(user.UnknownUserIdError)) {
				continue
			}
			result := tx.Exec("INSERT INTO plugin_sandbox_users (plugin_id, uid) VALUES (?, ?) ON CONFLICT DO NOTHING", id, uid)
			if result.Error != nil || result.RowsAffected == 1 {
				return result.Error
			}
		}
		return errors.New("无法分配插件隔离 UID")
	})
	return uid, err
}

func preparePluginData(data string, uid, gid int) error {
	if err := os.MkdirAll(data, 0700); err != nil {
		return err
	}
	root, err := openAuthorizedRoot(data)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		return err
	}
	owner := info.Sys().(*syscall.Stat_t)
	if int(owner.Uid) != uid || int(owner.Gid) != gid {
		if err := dir.Chown(0, 0); err != nil {
			return err
		}
		if err := dir.Chmod(0700); err != nil {
			return err
		}
		var paths []string
		err := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.IsDir() && info.Sys().(*syscall.Stat_t).Nlink > 1 {
				return errors.New("插件数据包含硬链接，无法安全切换隔离用户")
			}
			if path != "." {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return err
		}
		// 根目录最后更新。迁移中断时仍可按原所有者识别并重试。
		for i := len(paths) - 1; i >= 0; i-- {
			if err := root.Lchown(paths[i], uid, gid); err != nil {
				return err
			}
		}
		if err := dir.Chown(uid, gid); err != nil {
			return err
		}
	}
	return dir.Chmod(0700)
}

func (m *Module) sandboxOptions(id string, port int, box Sandbox, options executor.ProcessOptions) (executor.ProcessOptions, func(), error) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return options, nil, errors.New("插件沙箱需要 Linux systemd 和面板服务权限")
	}
	bin, err := executor.LookPath("systemd-run")
	if err != nil {
		return options, nil, errors.New("系统缺少 systemd-run，无法启用插件沙箱")
	}
	drop, err := executor.LookPath("setpriv")
	if err != nil {
		return options, nil, errors.New("系统缺少 setpriv，无法启用插件沙箱")
	}
	file, err := os.CreateTemp(m.staging(), ".sandbox-env-*")
	if err != nil {
		return options, nil, err
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	escape := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	for _, entry := range options.Env {
		key, value, _ := strings.Cut(entry, "=")
		if _, err := fmt.Fprintf(file, "%s=\"%s\"\n", key, escape.Replace(value)); err != nil {
			file.Close()
			cleanup()
			return options, nil, err
		}
	}
	if err := file.Close(); err != nil {
		cleanup()
		return options, nil, err
	}
	if box.CPUPercent == 0 {
		box.CPUPercent = 100
	}
	if box.MemoryMiB == 0 {
		box.MemoryMiB = 256
	}
	if box.MaxProcesses == 0 {
		box.MaxProcesses = 64
	}
	unit := "ezpanel-plugin-" + id + ".service"
	propertyPath := func(path string) string { return strings.ReplaceAll(path, "%", "%%") }
	args := []string{"--quiet", "--wait", "--pipe", "--collect", "--unit=" + unit, "--service-type=exec",
		"--property=EnvironmentFile=" + propertyPath(file.Name()),
		"--property=WorkingDirectory=" + propertyPath(options.Dir),
		"--property=ReadWritePaths=" + strconv.Quote(propertyPath(options.Dir)),
		"--property=ProtectSystem=strict", "--property=ProtectHome=read-only",
		"--property=PrivateTmp=yes", "--property=PrivateDevices=yes", "--property=NoNewPrivileges=yes",
		"--property=ProtectKernelTunables=yes", "--property=ProtectKernelModules=yes",
		"--property=ProtectControlGroups=yes", "--property=ProtectProc=invisible", "--property=RestrictSUIDSGID=yes",
		"--property=RestrictNamespaces=yes", "--property=CapabilityBoundingSet=CAP_SETUID CAP_SETGID", "--property=LockPersonality=yes",
		"--property=RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6",
		"--property=SocketBindDeny=any", fmt.Sprintf("--property=SocketBindAllow=ipv4:tcp:%d", port),
		"--property=StopPropagatedFrom=ezpanel.service", "--property=TimeoutStopSec=5",
		fmt.Sprintf("--property=CPUQuota=%d%%", box.CPUPercent),
		fmt.Sprintf("--property=MemoryMax=%dM", box.MemoryMiB),
		fmt.Sprintf("--property=TasksMax=%d", box.MaxProcesses)}
	if box.Network != "outbound" {
		args = append(args, "--property=IPAddressDeny=any", "--property=IPAddressAllow=localhost")
	}
	options.Args = append(args, "--", drop,
		"--reuid="+strconv.FormatUint(uint64(options.UID), 10),
		"--regid="+strconv.FormatUint(uint64(options.GID), 10),
		"--clear-groups", "--no-new-privs", "--", propertyPath(options.Path))
	options.Path, options.Service, options.Env = bin, unit, executor.DefaultEnv()
	return options, cleanup, nil
}
