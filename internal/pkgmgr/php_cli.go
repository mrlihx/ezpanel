package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	phpCLIMarkerPrefix = "# ezpanel-managed-php-cli: "
	phpCLIBinDir       = "/usr/local/bin"
	phpCLISysLink      = "/usr/bin/php"
)

type PHPCLIVersion struct {
	ID      string `json:"id"`
	Branch  string `json:"branch"`
	Version string `json:"version"`
	Command string `json:"command"`
	BinPath string `json:"binPath"`
	Running bool   `json:"running"`
}

type PHPCLIState struct {
	DefaultID  string          `json:"defaultId"`
	DefaultBin string          `json:"defaultBin"`
	Installed  []PHPCLIVersion `json:"installed"`
}

func phpCLICommandName(id string) string {
	branch := phpBranch(id)
	if branch == "" {
		return ""
	}
	return "php" + strings.ReplaceAll(branch, ".", "")
}

func phpCLIWrapperContent(id, siteRoot string) string {
	container := "ezpanel-" + id
	root := filepath.Clean(siteRoot)
	if root == "" || root == "." || root == "/" {
		root = "/www/wwwroot"
	}
	return fmt.Sprintf(`#!/bin/sh
%s%s
set -eu
container=%q
root=%q
flags="-i"
if [ -t 0 ] && [ -t 1 ]; then
  flags="-it"
fi
cwd=$(pwd 2>/dev/null || true)
in_root=0
case "$cwd" in
  /tmp|/tmp/*) in_root=1 ;;
  *)
    if [ "$cwd" = "$root" ]; then
      in_root=1
    elif [ "${cwd#"$root"/}" != "$cwd" ]; then
      in_root=1
    fi
    ;;
esac
if [ "$in_root" = "1" ]; then
  exec docker exec $flags -u www-data -w "$cwd" "$container" php "$@"
else
  exec docker exec $flags -u www-data "$container" php "$@"
fi
`, phpCLIMarkerPrefix, id, container, root)
}

func isManagedPHPCLIFile(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return "", false
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		if target == path {
			return "", false
		}
		return isManagedPHPCLIFile(target)
	}
	if !info.Mode().IsRegular() || info.Size() > 8192 {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if id, ok := strings.CutPrefix(strings.TrimSpace(line), phpCLIMarkerPrefix); ok && IsPHP(id) {
			return id, true
		}
	}
	return "", false
}

func (m *Manager) phpCLIBinDir() string {
	return phpCLIBinDir
}

func (m *Manager) phpCLIDefaultPath() string {
	return filepath.Join(m.phpCLIBinDir(), "php")
}

func (m *Manager) phpCLIVersionPath(id string) string {
	cmd := phpCLICommandName(id)
	if cmd == "" {
		return ""
	}
	return filepath.Join(m.phpCLIBinDir(), cmd)
}

// EnsurePHPCLIWrapper 为已安装的 PHP 容器生成宿主机包装脚本（如 /usr/local/bin/php83）。
// 如果尚未设置默认 /usr/local/bin/php，则自动将其指向该版本。
func (m *Manager) EnsurePHPCLIWrapper(id string) error {
	if !IsPHP(id) {
		return errors.New("PHP 版本无效")
	}
	binDir := m.phpCLIBinDir()
	if err := os.MkdirAll(binDir, 0755); err != nil {
		return err
	}
	target := m.phpCLIVersionPath(id)
	if _, err := os.Lstat(target); err == nil {
		if _, managed := isManagedPHPCLIFile(target); !managed {
			return fmt.Errorf("%s 已存在且非面板管理，未覆盖", target)
		}
	}
	siteRoot := m.cfg.Current().SiteRoot
	content := phpCLIWrapperContent(id, siteRoot)
	if existing, err := os.ReadFile(target); err == nil && string(existing) == content {
		// 内容未变，无需重复写入磁盘
	} else {
		f, err := os.CreateTemp(binDir, filepath.Base(target)+".tmp-*")
		if err != nil {
			return err
		}
		tmp := f.Name()
		if _, err := f.WriteString(content); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Chmod(tmp, 0755); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	// 如果尚未配置默认 php，则自动指向当前版本。
	defaultPath := m.phpCLIDefaultPath()
	if _, err := os.Lstat(defaultPath); os.IsNotExist(err) {
		_ = m.pointDefaultPHPCLI(target)
	} else if _, managed := isManagedPHPCLIFile(defaultPath); managed {
		// 若已有托管软链指向的目标已失效，则自动修复指向当前版本。
		if resolved, err := os.Readlink(defaultPath); err == nil {
			if _, statErr := os.Stat(resolved); os.IsNotExist(statErr) {
				_ = m.pointDefaultPHPCLI(target)
			}
		}
	}
	return nil
}

func (m *Manager) pointDefaultPHPCLI(versionBinPath string) error {
	defaultPath := m.phpCLIDefaultPath()
	if _, err := os.Lstat(defaultPath); err == nil {
		if _, managed := isManagedPHPCLIFile(defaultPath); !managed {
			return fmt.Errorf("%s 已存在且非面板管理，未覆盖", defaultPath)
		}
	}
	f, err := os.CreateTemp(filepath.Dir(defaultPath), filepath.Base(defaultPath)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_ = f.Close()
	_ = os.Remove(tmp)
	if err := os.Symlink(versionBinPath, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, defaultPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// 同时在 /usr/bin/php 不存在或已指向 /usr/local/bin/php 时维护软链，兼容精简 PATH 的 cron 环境。
	if info, err := os.Lstat(phpCLISysLink); os.IsNotExist(err) {
		_ = os.Symlink(defaultPath, phpCLISysLink)
	} else if err == nil && info.Mode()&os.ModeSymlink != 0 {
		if linkTarget, err := os.Readlink(phpCLISysLink); err == nil && linkTarget == defaultPath {
			// 已正确指向
		}
	}
	return nil
}

// RemovePHPCLIWrapper 卸载 PHP 容器后清理对应的宿主机包装脚本，并在需要时自动切换默认 CLI 到剩余版本。
func (m *Manager) RemovePHPCLIWrapper(ctx context.Context, id string) {
	if !IsPHP(id) {
		return
	}
	versionPath := m.phpCLIVersionPath(id)
	if _, managed := isManagedPHPCLIFile(versionPath); managed {
		_ = os.Remove(versionPath)
	}
	defaultPath := m.phpCLIDefaultPath()
	currentDefault, managed := isManagedPHPCLIFile(defaultPath)
	if !managed {
		// 也处理指向刚被删除文件的悬空软链
		if linkTarget, err := os.Readlink(defaultPath); err != nil || linkTarget != versionPath {
			return
		}
	} else if currentDefault != id {
		return
	}
	// 自动切换到剩余已安装的最高 PHP 版本
	var fallback string
	for i := len(phpBranches) - 1; i >= 0; i-- {
		candidate := "php-" + phpBranches[i]
		if candidate == id {
			continue
		}
		if st, err := m.Status(ctx, candidate); err == nil && st.Installed {
			if err := m.EnsurePHPCLIWrapper(candidate); err == nil {
				fallback = candidate
				break
			}
		}
	}
	if fallback != "" {
		_ = m.pointDefaultPHPCLI(m.phpCLIVersionPath(fallback))
		return
	}
	_ = os.Remove(defaultPath)
	if linkTarget, err := os.Readlink(phpCLISysLink); err == nil && linkTarget == defaultPath {
		_ = os.Remove(phpCLISysLink)
	}
}

// PHPCLIStatus 返回已安装 PHP 版本列表与当前系统默认 CLI 版本，并自动补齐缺失的包装脚本。
func (m *Manager) PHPCLIStatus(ctx context.Context) (PHPCLIState, error) {
	state := PHPCLIState{
		DefaultBin: m.phpCLIDefaultPath(),
		Installed:  []PHPCLIVersion{},
	}
	for _, branch := range phpBranches {
		id := "php-" + branch
		st, err := m.Status(ctx, id)
		if err != nil || !st.Installed {
			continue
		}
		_ = m.EnsurePHPCLIWrapper(id)
		running := st.Running != nil && *st.Running
		ver := ""
		if st.Version != nil {
			ver = *st.Version
		}
		state.Installed = append(state.Installed, PHPCLIVersion{
			ID:      id,
			Branch:  branch,
			Version: ver,
			Command: phpCLICommandName(id),
			BinPath: m.phpCLIVersionPath(id),
			Running: running,
		})
	}
	if defID, ok := isManagedPHPCLIFile(m.phpCLIDefaultPath()); ok {
		for _, item := range state.Installed {
			if item.ID == defID {
				state.DefaultID = defID
				break
			}
		}
	}
	if state.DefaultID == "" && len(state.Installed) > 0 {
		last := state.Installed[len(state.Installed)-1]
		if err := m.pointDefaultPHPCLI(last.BinPath); err == nil {
			state.DefaultID = last.ID
		}
	}
	return state, nil
}

// SetDefaultPHPCLI 将系统默认 /usr/local/bin/php 切换到指定已安装的 PHP 版本。
func (m *Manager) SetDefaultPHPCLI(ctx context.Context, id string) (PHPCLIState, error) {
	if !IsPHP(id) {
		return PHPCLIState{}, errors.New("PHP 版本无效")
	}
	st, err := m.Status(ctx, id)
	if err != nil || !st.Installed {
		return PHPCLIState{}, errors.New("请先安装对应的 PHP 版本")
	}
	if err := m.EnsurePHPCLIWrapper(id); err != nil {
		return PHPCLIState{}, err
	}
	if err := m.pointDefaultPHPCLI(m.phpCLIVersionPath(id)); err != nil {
		return PHPCLIState{}, err
	}
	return m.PHPCLIStatus(ctx)
}
