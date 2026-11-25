package pkgmgr

import (
	"context"
	"errors"
	"ezpanel/internal/executor"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type savedConfig struct {
	path     string
	data     []byte
	link     string
	mode     os.FileMode
	uid, gid int
}

// 包管理器通常保留数据，但 RPM remove 会删除未修改的配置；先保存包声明的 /etc 文件。
func (m *Manager) preserveConfigs(ctx context.Context, sys System, packages []string) ([]savedConfig, error) {
	var configs []savedConfig
	seen := map[string]bool{}
	for _, pkg := range packages {
		var r executor.Result
		var e error
		if sys.Manager == "apt-get" {
			r, e = m.Run(ctx, 30*time.Second, nil, "/usr/bin/dpkg-query", "-W", "-f=${Conffiles}\n", pkg)
		} else {
			r, e = m.Run(ctx, 30*time.Second, nil, "/usr/bin/rpm", "-qc", pkg)
		}
		if e != nil || r.Truncated {
			return nil, fmt.Errorf("无法完整读取 %s 的配置清单，已拒绝卸载", pkg)
		}
		for _, line := range strings.Split(r.Stdout, "\n") {
			path := strings.TrimSpace(line)
			if sys.Manager == "apt-get" {
				// Conffiles 每行末尾为校验和，路径允许包含空格。
				path, _, _ = strings.Cut(path, " obsolete")
				at := strings.LastIndexByte(path, ' ')
				if at < 0 {
					continue
				}
				path = path[:at]
			}
			if path == "" || path == "(none)" || seen[path] {
				continue
			}
			if !strings.HasPrefix(path, "/etc/") || filepath.Clean(path) != path {
				return nil, errors.New("配置文件不在受控 /etc 范围，已拒绝卸载")
			}
			seen[path] = true
			info, e := os.Lstat(path)
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			if e != nil {
				return nil, e
			}
			if info.IsDir() {
				continue
			}
			config := savedConfig{path: path, mode: info.Mode()}
			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				config.uid, config.gid = int(stat.Uid), int(stat.Gid)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				config.link, e = os.Readlink(path)
			} else if info.Mode().IsRegular() {
				config.data, e = os.ReadFile(path)
			} else {
				return nil, fmt.Errorf("不支持保留配置文件 %s 的类型，已拒绝卸载", path)
			}
			if e != nil {
				return nil, e
			}
			configs = append(configs, config)
		}
	}
	return configs, nil
}

func restoreConfigs(configs []savedConfig) error {
	var failures []error
	for _, config := range configs {
		if e := os.MkdirAll(filepath.Dir(config.path), 0755); e != nil {
			failures = append(failures, e)
			continue
		}
		var e error
		if config.link != "" {
			current, err := os.Readlink(config.path)
			if errors.Is(err, os.ErrNotExist) {
				e = os.Symlink(config.link, config.path)
			} else if err != nil || current != config.link {
				e = fmt.Errorf("配置链接 %s 已变化，无法恢复", config.path)
			}
		} else {
			e = os.WriteFile(config.path, config.data, config.mode.Perm())
			if e == nil {
				e = os.Chmod(config.path, config.mode.Perm())
			}
		}
		if e == nil {
			e = os.Lchown(config.path, config.uid, config.gid)
		}
		if e != nil {
			failures = append(failures, fmt.Errorf("保留配置 %s 失败: %w", config.path, e))
		}
	}
	return errors.Join(failures...)
}
