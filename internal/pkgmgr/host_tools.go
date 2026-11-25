package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ezpanel/internal/executor"
)

var hostToolConfigs = map[string]map[string][2]string{
	"ftp": {
		"":        {"/etc/default/pure-ftpd-common", "/etc/pure-ftpd/pure-ftpd.conf"},
		"bind":    {"/etc/pure-ftpd/conf/Bind", ""},
		"passive": {"/etc/pure-ftpd/conf/PassivePortRange", ""},
	},
	"fail2ban":   {"": {"/etc/fail2ban/jail.local", "/etc/fail2ban/jail.local"}},
	"supervisor": {"": {"/etc/supervisor/supervisord.conf", "/etc/supervisord.conf"}},
	"clamav":     {"": {"/etc/clamav/clamd.conf", "/etc/clamd.d/scan.conf"}, "updates": {"/etc/clamav/freshclam.conf", "/etc/freshclam.conf"}},
	"rsync":      {"": {"/etc/rsyncd.conf", "/etc/rsyncd.conf"}, "credentials": {"/etc/rsyncd.secrets", "/etc/rsyncd.secrets"}, "receiver-secrets": {rsyncSecrets, rsyncSecrets}},
}

func isHostTool(id string) bool { return hostToolConfigs[id] != nil }

var packageVersion = regexp.MustCompile(`^[0-9][a-zA-Z0-9.+:~_-]*$`)

func (m *Manager) hostToolVersion(ctx context.Context, sys System, pkg string) (string, error) {
	if sys.Manager == "apt-get" {
		aptCache, err := m.LookPath("apt-cache")
		if err != nil {
			return "", err
		}
		result, err := m.Run(ctx, 30*time.Second, nil, aptCache, "policy", pkg)
		if err != nil {
			return "", err
		}
		for line := range strings.SplitSeq(result.Stdout, "\n") {
			if raw, ok := strings.CutPrefix(strings.TrimSpace(line), "Candidate:"); ok {
				if version := strings.TrimSpace(raw); packageVersion.MatchString(version) {
					return version, nil
				}
			}
		}
	} else {
		result, err := m.Run(ctx, 30*time.Second, nil, sys.Command, "-q", "repoquery", "--latest-limit=1", "--qf", "%{version}-%{release}", pkg)
		if err != nil {
			return "", err
		}
		for line := range strings.SplitSeq(result.Stdout, "\n") {
			if version := strings.TrimSpace(line); packageVersion.MatchString(version) {
				return version, nil
			}
		}
	}
	return "", fmt.Errorf("当前系统软件源没有 %s 的候选版本", pkg)
}

// 首次安装同步更新病毒库，避免 clamd 在下载完成前被 systemd 跳过。
func (m *Manager) prepareClamAV(ctx context.Context, state State, log func(string)) error {
	directory := "/var/lib/clamav"
	config := "/etc/freshclam.conf"
	if state.PkgManager == "apt-get" {
		config = "/etc/clamav/freshclam.conf"
	}
	if content, err := m.ReadFile(config); err == nil {
		for line := range strings.SplitSeq(string(content), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "DatabaseDirectory" {
				directory = fields[1]
			}
		}
	}
	main, _ := filepath.Glob(filepath.Join(directory, "main.c[vl]d"))
	daily, _ := filepath.Glob(filepath.Join(directory, "daily.c[vl]d"))
	if len(main) != 0 && len(daily) != 0 {
		return nil
	}
	freshclam, err := m.LookPath("freshclam")
	if err != nil {
		return fmt.Errorf("找不到 ClamAV 病毒库更新程序: %w", err)
	}
	const systemctl = "/usr/bin/systemctl"
	const updater = "clamav-freshclam.service"
	if state.PkgManager == "apt-get" {
		if _, err = m.Run(ctx, 30*time.Second, nil, systemctl, "stop", updater); err != nil {
			return err
		}
	}
	if log == nil {
		log = func(string) {}
	}
	log("首次下载 ClamAV 病毒库，完成后启动扫描服务")
	result, err := m.Stream(ctx, 9*time.Minute, nil, log, freshclam, "--stdout", "--config-file="+config)
	if state.PkgManager == "apt-get" {
		_, updateErr := m.Run(context.WithoutCancel(ctx), 30*time.Second, nil, systemctl, "enable", "--now", updater)
		if err == nil {
			err = updateErr
		}
	}
	if err != nil {
		return fmt.Errorf("ClamAV 病毒库初始化失败: %s %w", strings.TrimSpace(result.Stderr+" "+result.Stdout), err)
	}
	return nil
}

func (m *Manager) newerDebPackage(ctx context.Context, next, previous string) bool {
	if next == previous {
		return false
	}
	_, err := m.Run(ctx, 5*time.Second, nil, "/usr/bin/dpkg", "--compare-versions", next, "gt", previous)
	return err == nil
}

func (m *Manager) updateHostTool(ctx context.Context, id, version string, log func(string)) (State, error) {
	var state State
	if log == nil {
		log = func(string) {}
	}
	err := m.ApplicationChange(id, func() error {
		sys, err := m.Detect()
		if err != nil {
			return err
		}
		if sys.Manager != "apt-get" {
			return ErrUnsupported
		}
		state, err = m.Status(ctx, id)
		if err != nil {
			return err
		}
		if !state.Updateable || state.Version == nil {
			return errors.New("该应用不由当前系统仓库管理")
		}
		application, _ := app(id)
		pkg := application.packagesFor(sys)[0]
		m.packageMu.Lock()
		defer m.packageMu.Unlock()
		candidate, err := m.hostToolVersion(ctx, sys, pkg)
		if err != nil {
			return err
		}
		if candidate != version || !m.newerDebPackage(ctx, candidate, *state.Version) {
			return errors.New("请选择系统仓库提供的较新版本")
		}
		env := append(executor.DefaultEnv(), "DEBIAN_FRONTEND=noninteractive")
		args := []string{"-y", "--no-remove", "--only-upgrade", "-o", "Dpkg::Options::=--force-confold", "install", pkg + "=" + version}
		log("正在预下载升级包")
		if _, err := m.Stream(ctx, 15*time.Minute, env, log, sys.Command, append([]string{"--download-only"}, args...)...); err != nil {
			return err
		}
		backup := filepath.Join(m.cfg.Current().BackupRoot, "software", id, time.Now().UTC().Format("20060102T150405.000000000Z"))
		if err := os.MkdirAll(backup, 0700); err != nil {
			return err
		}
		for _, configurations := range hostToolConfigs[id] {
			path := configurations[0]
			content, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			destination := filepath.Join(backup, strings.TrimPrefix(path, "/"))
			if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
				return err
			}
			if err := os.WriteFile(destination, content, 0600); err != nil {
				return err
			}
		}
		log("已备份面板管理的配置：" + backup)
		if _, err := m.Stream(ctx, 10*time.Minute, env, log, sys.Command, args...); err != nil {
			return fmt.Errorf("系统软件包升级失败，配置备份位于 %s：%w", backup, err)
		}
		return nil
	})
	if err != nil {
		return state, err
	}
	return m.Status(ctx, id)
}
