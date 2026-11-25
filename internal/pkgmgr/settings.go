package pkgmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"ezpanel/internal/executor"
	"ezpanel/internal/logview"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Settings struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Revision string `json:"revision"`
	Missing  bool   `json:"missing,omitempty"`
}

func (m *Manager) configPath(id string) string {
	if id == "openresty" {
		return m.layout().OpenRestyConfig()
	}
	if IsPHP(id) {
		return m.phpDirectory(id) + "/php-fpm.conf"
	}
	candidates := map[string][]string{"nginx": {"/etc/nginx/nginx.conf"}, "mysql": {"/etc/mysql/mysql.conf.d/mysqld.cnf", "/etc/my.cnf"}, "mariadb": {"/etc/mysql/mariadb.conf.d/50-server.cnf", "/etc/my.cnf.d/mariadb-server.cnf", "/etc/my.cnf"}, "redis": {"/etc/redis/redis.conf", "/etc/redis.conf"}, "docker": {"/etc/docker/daemon.json"}}
	for _, path := range candidates[id] {
		if _, e := os.Stat(path); e == nil {
			return path
		}
	}
	if len(candidates[id]) > 0 {
		return candidates[id][0]
	}
	return ""
}
func (m *Manager) Settings(ctx context.Context, id string, kind ...string) (Settings, error) {
	if IsDatabaseContainer(id) {
		return m.DatabaseInstanceSettings(ctx, id, "default")
	}
	state, e := m.Status(ctx, id)
	if e != nil {
		return Settings{}, e
	}
	if !state.Installed || !state.Manageable {
		return Settings{}, ErrUnsupported
	}
	path := m.configPath(id)
	if isHostTool(id) {
		key := ""
		if len(kind) > 0 {
			key = kind[0]
		}
		paths := hostToolConfigs[id][key]
		path = paths[0]
		if state.PkgManager != "apt-get" {
			path = paths[1]
		}
	}
	if IsPHP(id) && len(kind) > 0 && kind[0] == "ini" {
		path = m.phpDirectory(id) + "/php.ini"
	}
	if path == "" {
		return Settings{}, ErrUnsupported
	}
	b, e := os.ReadFile(path)
	missing := false
	if os.IsNotExist(e) && isHostTool(id) {
		switch path {
		case "/etc/fail2ban/jail.local":
			b = []byte("[DEFAULT]\nbantime = 10m\nfindtime = 10m\nmaxretry = 5\n\n[sshd]\n# enabled = true\n# backend = systemd\n")
		case rsyncConfig:
			b = []byte(m.rsyncDefaultConfig())
		case "/etc/pure-ftpd/conf/Bind", "/etc/pure-ftpd/conf/PassivePortRange":
			b = []byte{}
		case "/etc/rsyncd.secrets", rsyncSecrets:
			b = []byte{}
		default:
			return Settings{}, e
		}
		e, missing = nil, true
	}
	if os.IsNotExist(e) && id == "docker" {
		b = DefaultDockerDaemonJSON()
		e = nil
	}
	if e != nil {
		return Settings{}, e
	}
	if len(b) > 1<<20 {
		return Settings{}, errors.New("配置超过1MiB，请在文件管理中编辑")
	}
	hash := sha256.Sum256(b)
	out := Settings{Path: path, Content: string(b), Revision: hex.EncodeToString(hash[:]), Missing: missing}

	return out, nil
}
func (m *Manager) SaveSettings(ctx context.Context, id, content, revision string, kind ...string) error {
	return m.saveSettings(ctx, id, content, revision, false, kind...)
}

func (m *Manager) SavePHPOptions(ctx context.Context, id, content, revision, kind string) error {
	return m.saveSettings(ctx, id, content, revision, true, kind)
}

func (m *Manager) saveSettings(ctx context.Context, id, content, revision string, reloadPHP bool, kind ...string) error {
	if IsDatabaseContainer(id) {
		return m.SaveDatabaseInstanceSettings(ctx, id, "default", content, revision)
	}
	if e := m.lock(id); e != nil {
		return e
	}
	defer m.unlock(id)
	current, e := m.Settings(ctx, id, kind...)
	if e != nil {
		return e
	}
	if current.Revision != revision {
		return errors.New("配置已被修改，请重新加载后保存")
	}
	if id == "docker" && !json.Valid([]byte(content)) {
		return errors.New("Docker 配置必须是有效 JSON")
	}
	path := current.Path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	} else if !os.IsNotExist(err) {
		return err
	}
	mode := os.FileMode(0640)
	uid, gid := -1, -1
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(stat.Uid), int(stat.Gid)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if current.Path == "/etc/rsyncd.secrets" || current.Path == rsyncSecrets {
		mode = 0600
	}
	if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".panel-config-")
	if e != nil {
		return e
	}
	temp := f.Name()
	defer os.Remove(temp)
	if e = f.Chmod(mode); e != nil {
		f.Close()
		return e
	}
	if uid >= 0 {
		if e = f.Chown(uid, gid); e != nil {
			f.Close()
			return e
		}
	}
	_, e = f.WriteString(content)
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	if e = os.WriteFile(path+".panel-backup", []byte(current.Content), 0600); e != nil {
		return e
	}
	if e = os.Rename(temp, path); e != nil {
		return e
	}
	if id == "openresty" {
		if _, err := m.OpenRestyCommand(ctx, "-t"); err != nil {
			return errors.Join(err, os.WriteFile(path, []byte(current.Content), mode))
		}
	}
	if id == "nginx" {
		bin, err := m.LookPath("nginx")
		if err == nil {
			_, err = m.Run(ctx, 30*time.Second, nil, bin, "-t")
		}
		if err != nil {
			if restoreErr := os.WriteFile(path, []byte(current.Content), mode); restoreErr != nil {
				return errors.Join(err, restoreErr)
			}
			return errors.New("Nginx 配置检查失败，已恢复原配置")
		}
	}
	if id == "docker" {
		bin, err := m.LookPath("dockerd")
		if err == nil {
			env := executor.DefaultEnv()
			// 独立安装的 docker-proxy 与 dockerd 一起放在实际安装目录。
			if real, resolveErr := filepath.EvalSymlinks(bin); resolveErr == nil {
				env[0] = "PATH=" + filepath.Dir(real) + ":" + strings.TrimPrefix(env[0], "PATH=")
			}
			_, err = m.Run(ctx, 30*time.Second, env, bin, "--validate", "--config-file", path)
		}
		if err != nil {
			if restoreErr := os.WriteFile(path, []byte(current.Content), mode); restoreErr != nil {
				return errors.Join(err, restoreErr)
			}
			return errors.New("Docker配置检查失败，已恢复原配置")
		}
	}
	if IsPHP(id) {
		if err := m.applyPHPConfig(ctx, id, reloadPHP); err != nil {
			if restore := os.WriteFile(path, []byte(current.Content), mode); restore != nil {
				return errors.Join(err, restore)
			}
			return fmt.Errorf("%w，已恢复原配置", err)
		}
	}
	return nil
}
func (m *Manager) Logs(ctx context.Context, id string) (string, error) {
	if UsesDocker(id) && !IsProjectRuntime(id) {
		sources, err := m.LogSources(ctx, id)
		if err != nil || len(sources) == 0 {
			return "", err
		}
		source := sources[0]
		if source.Container != "" {
			result, err := m.ReadServiceOutput(ctx, source)
			return result.Stdout + result.Stderr, err
		}
		text, err := logview.Read(source.Path)
		return text.Content, err
	}
	state, e := m.Status(ctx, id)
	if e != nil {
		return "", e
	}
	if !state.Manageable || state.Unit == "" {
		return "", ErrUnsupported
	}
	r, e := m.Run(ctx, 30*time.Second, nil, "/usr/bin/journalctl", "--unit", state.Unit, "--lines=200", "--no-pager", "--output=short-iso")
	return r.Stdout, e
}
