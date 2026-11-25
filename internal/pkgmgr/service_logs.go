package pkgmgr

import (
	"os"
	"path/filepath"
)

const WebsiteLogRoot = "/www/wwwlogs"

// ContainerLogDir is the fixed log directory mounted inside every managed container.
const ContainerLogDir = "/var/log/ezpanel"

const (
	containerMySQLErrorLog   = ContainerLogDir + "/error.log"
	containerMySQLSlowLog    = ContainerLogDir + "/slow.log"
	containerPostgresLog     = ContainerLogDir + "/postgresql.log"
	containerMongoLog        = ContainerLogDir + "/mongodb.log"
	containerRedisLog        = ContainerLogDir + "/redis.log"
	containerMemcachedLog    = ContainerLogDir + "/memcached.log"
	containerOpenRestyLog    = ContainerLogDir + "/error.log"
	containerWAFLogDir       = ContainerLogDir + "/waf"
	containerWAFEventLog     = containerWAFLogDir + "/events.jsonl"
	containerPHPFPMErrorLog  = ContainerLogDir + "/fpm-error.log"
)

func ContainerWAFEventLog() string { return containerWAFEventLog }

func (m *Manager) serviceLogDir(id, instance string) string {
	l := m.layout()
	switch {
	case id == "openresty":
		return l.OpenRestyLogDir()
	case IsPHP(id):
		return l.PHPLogDir(phpBranch(id))
	case IsDatabaseContainer(id):
		return l.AppInstanceLogDir(id, instance)
	default:
		return l.AppLogDir(id)
	}
}

func (m *Manager) mountedLogPath(hostLogDir, containerPath string) string {
	if hostLogDir == "" || containerPath == "" || containerPath == "stderr" {
		return ""
	}
	clean := filepath.Clean(containerPath)
	if !filepath.IsAbs(clean) {
		return ""
	}
	rel, err := filepath.Rel(ContainerLogDir, clean)
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return ""
	}
	return filepath.Join(hostLogDir, rel)
}

func (m *Manager) mountedDatabaseLogPath(id string, row DatabaseInstance, path string) string {
	if path == "" || path == "stderr" {
		return ""
	}
	if local := m.mountedLogPath(m.layout().AppInstanceLogDir(id, row.ID), path); local != "" {
		return local
	}
	root := map[string]string{"mysql": "/var/lib/mysql", "postgresql": "/var/lib/postgresql/data", "redis": "/data", "mongodb": "/data/db"}[id]
	if root == "" || row.DataDir == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	relative, err := filepath.Rel(root, filepath.Clean(path))
	if err != nil || !filepath.IsLocal(relative) || relative == "." {
		return ""
	}
	return filepath.Join(row.DataDir, relative)
}

func (m *Manager) memcachedLogPath(instance string) string {
	return filepath.Join(m.layout().AppInstanceLogDir("memcached", instance), "memcached.log")
}

func (m *Manager) prepareMemcachedLog(instance string) error {
	dir, err := m.ensureContainerLogDir("memcached", instance)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "memcached.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		_ = f.Chown(11211, 11211)
	}
	return f.Close()
}

func (info containerInfo) memcachedLog() string {
	for _, mount := range info.Mounts {
		if mount.Destination == ContainerLogDir {
			return filepath.Join(mount.Source, "memcached.log")
		}
	}
	return ""
}
