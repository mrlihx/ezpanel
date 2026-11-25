package pkgmgr

import (
	"ezpanel/internal/config"
	"os"
	"path/filepath"
)

// /www是网站数据的公共父目录，子目录各自保留业务所需权限。
func EnsureWWWRoot() error {
	if err := os.MkdirAll("/www", 0755); err != nil {
		return err
	}
	return os.Chmod("/www", 0755)
}

// 容器的工作进程需要穿过挂载目录。已有目录只补执行权限，保留读写权限。
func EnsureWebDirectory(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0755); err != nil {
			return err
		}
		return os.Chmod(path, 0755)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &os.PathError{Op: "建站", Path: path, Err: os.ErrInvalid}
	}
	if info.Mode().Perm()&0111 == 0111 {
		return nil
	}
	return os.Chmod(path, info.Mode()|0111)
}

func (m *Manager) layout() config.Layout { return m.cfg.Current().Layout() }
func (m *Manager) Layout() config.Layout { return m.layout() }

func (m *Manager) defaultInstallRoot(id string) string {
	l := m.layout()
	if IsPHP(id) {
		return l.PHPDir(phpBranch(id))
	}
	if IsDatabaseContainer(id) {
		return l.AppInstanceDir(id, "default")
	}
	return l.AppDir(id)
}

func (m *Manager) installRoot(id string) string { return m.defaultInstallRoot(id) }

func (m *Manager) databaseInstanceRoot(id, instance string) string {
	return m.layout().AppInstanceDir(id, instance)
}

func (m *Manager) serviceDataDir(id string) string {
	return filepath.Join(m.defaultInstallRoot(id), "data")
}

func (m *Manager) serviceStateDir(id string) string {
	return filepath.Join(m.defaultInstallRoot(id), ".state")
}

func (m *Manager) serviceTempDir(id string) string {
	return filepath.Join(m.defaultInstallRoot(id), "tmp")
}

func (m *Manager) versionFile(id string) string {
	return filepath.Join(m.serviceStateDir(id), ".panel-version")
}

func ensurePublicTree(dirs ...string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ensureAppParents(id string) error {
	l := m.layout()
	dirs := []string{l.Root(), l.AppsDir()}
	if IsPHP(id) {
		dirs = append(dirs, l.PHPBaseDir())
	} else if IsDatabaseContainer(id) {
		dirs = append(dirs, l.AppDir(id))
	}
	return ensurePublicTree(dirs...)
}

func (m *Manager) ensureContainerLogDir(id, instance string) (string, error) {
	l := m.layout()
	var dir string
	var uid, gid int
	switch {
	case id == "openresty":
		dir = l.OpenRestyLogDir()
	case id == "waf":
		dir = l.WAFLogDir()
	case IsPHP(id):
		dir = l.PHPLogDir(phpBranch(id))
	case IsDatabaseContainer(id):
		dir = l.AppInstanceLogDir(id, instance)
		switch id {
		case "mysql", "mongodb", "redis":
			uid, gid = 999, 999
		case "postgresql":
			uid, gid = 70, 70
		case "memcached":
			uid, gid = 11211, 11211
		}
	default:
		dir = l.AppLogDir(id)
	}
	if err := ensurePublicTree(l.Root(), l.LogDir(), filepath.Dir(dir), dir); err != nil {
		return "", err
	}
	if uid > 0 && os.Geteuid() == 0 {
		_ = os.Chown(dir, uid, gid)
	}
	return dir, nil
}

func (m *Manager) installAliases(id, prefix string) error {
	root := m.installRoot(id)
	for _, name := range []string{"bin", "sbin", "lib", "lib64", "include", "share"} {
		if _, err := os.Stat(filepath.Join(prefix, name)); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		path := filepath.Join(root, name)
		if target, err := os.Readlink(path); err == nil && target == "current/"+name {
			continue
		}
		if err := os.Symlink("current/"+name, path); err != nil {
			return err
		}
	}
	return nil
}
