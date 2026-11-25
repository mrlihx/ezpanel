package pkgmgr

import (
	"context"
	"errors"
	"strings"
	"time"
)

type ClamAVStatus struct {
	Version        string `json:"version"`
	UpdaterRunning bool   `json:"updaterRunning"`
}

func (m *Manager) ClamAVInfo(ctx context.Context) ClamAVStatus {
	var info ClamAVStatus
	if bin, err := m.LookPath("clamscan"); err == nil {
		out, _ := m.Run(ctx, 5*time.Second, nil, bin, "--version")
		info.Version = strings.TrimSpace(out.Stdout)
	}
	_, info.UpdaterRunning = m.clamAVUpdater(ctx)
	return info
}

func (m *Manager) clamAVUpdater(ctx context.Context) (string, bool) {
	for _, unit := range []string{"clamav-freshclam.service", "clamav-freshclam-once.timer"} {
		out, _ := m.Run(ctx, 3*time.Second, nil, systemctlPath, "is-active", unit)
		if strings.TrimSpace(out.Stdout) == "active" {
			return unit, true
		}
	}
	return "", false
}

func (m *Manager) UpdateClamAVSignatures(ctx context.Context, log func(string)) (err error) {
	state, err := m.Status(ctx, "clamav")
	if err != nil {
		return err
	}
	if !state.Installed {
		return ErrUnsupported
	}
	bin, err := m.LookPath("freshclam")
	if err != nil {
		return err
	}
	if unit, active := m.clamAVUpdater(ctx); active {
		if _, err = m.Run(ctx, 20*time.Second, nil, systemctlPath, "stop", unit); err != nil {
			return err
		}
		defer func() {
			_, restartErr := m.Run(context.WithoutCancel(ctx), 20*time.Second, nil, systemctlPath, "start", unit)
			err = errors.Join(err, restartErr)
		}()
	}
	conf := "/etc/freshclam.conf"
	if state.PkgManager == "apt-get" {
		conf = "/etc/clamav/freshclam.conf"
	}
	_, err = m.Stream(ctx, 10*time.Minute, nil, log, bin, "--stdout", "--config-file="+conf)
	return err
}

func (m *Manager) ScanClamAVDirectory(ctx context.Context, path string, quick bool, log func(string)) (bool, error) {
	command := "clamscan"
	args := []string{"--infected", "--recursive", "--follow-dir-symlinks=0", "--follow-file-symlinks=0"}
	if quick {
		state, err := m.Status(ctx, "clamav")
		if err != nil {
			return false, err
		}
		if state.Running == nil || !*state.Running {
			return false, errors.New("快速扫描需要启动 ClamAV 服务")
		}
		command = "clamdscan"
		conf := hostToolConfigs["clamav"][""][1]
		if state.PkgManager == "apt-get" {
			conf = hostToolConfigs["clamav"][""][0]
		}
		args = []string{"--infected", "--fdpass", "--multiscan", "--config-file=" + conf}
	}
	bin, err := m.LookPath(command)
	if err != nil {
		return false, errors.New("找不到 ClamAV 扫描工具")
	}
	result, err := m.Stream(ctx, time.Hour, nil, log, bin, append(args, "--", path)...)
	if result.ExitCode == 1 {
		return true, nil
	}
	return false, err
}
