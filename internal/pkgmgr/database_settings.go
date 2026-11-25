package pkgmgr

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type databaseConfiguration struct {
	file, mount, defaults string
}

var databaseConfigurations = map[string]databaseConfiguration{
	"mysql":      {"my.cnf", "/etc/ezpanel-mysql.cnf", ""},
	"postgresql": {"postgresql.conf", "/etc/ezpanel-postgresql.conf", "data_directory = '/var/lib/postgresql/data'\nhba_file = '/var/lib/postgresql/data/pg_hba.conf'\nident_file = '/var/lib/postgresql/data/pg_ident.conf'\nlisten_addresses = '*'\nport = 5432\nmax_connections = 100\nshared_buffers = 128MB\nwork_mem = 4MB\nlogging_collector = on\nlog_directory = '" + ContainerLogDir + "'\nlog_filename = 'postgresql.log'\nlog_min_duration_statement = -1\n"},
	"mongodb":    {"mongod.conf", "/etc/ezpanel-mongod.conf", "storage:\n  dbPath: /data/db\nnet:\n  bindIpAll: true\nsystemLog:\n  destination: file\n  path: " + containerMongoLog + "\n  logAppend: true\n"},
	"redis":      {"redis.conf", "/etc/ezpanel-redis.conf", "dir /data\nappendonly yes\nlogfile " + containerRedisLog + "\n"},
	"memcached":  {"memcached.args", "/etc/ezpanel-memcached.args", "# Memcached 启动参数配置（每行一个参数，修改后重启生效）\n-m 64\n-c 1024\n-t 4\n-U 0\n"},
}

func (m *Manager) databaseSettingsRoot(id, instance string) string {
	return m.databaseInstanceRoot(id, instance)
}

func (m *Manager) databaseConfigPath(id, instance string) string {
	return filepath.Join(m.databaseSettingsRoot(id, instance), "conf", databaseConfigurations[id].file)
}

func configRevision(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (m *Manager) databasePortFile(id, instance string) string {
	return filepath.Join(m.databaseSettingsRoot(id, instance), ".state", "host-port")
}

func (m *Manager) databaseRemoteFile(id, instance string) string {
	return filepath.Join(m.databaseSettingsRoot(id, instance), ".state", "remote-access")
}

func (m *Manager) databaseRemoteAccess(id, instance string) bool {
	if id == "memcached" {
		return false
	}
	raw, _ := os.ReadFile(m.databaseRemoteFile(id, instance))
	v := strings.TrimSpace(string(raw))
	return v == "1" || strings.EqualFold(v, "true")
}

func (m *Manager) databaseBindIP(id, instance string) string {
	if m.databaseRemoteAccess(id, instance) {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

func (m *Manager) SaveDatabaseInstanceRemoteAccess(ctx context.Context, id, instance string, enabled bool) (DatabaseInstance, error) {
	if id == "memcached" && enabled {
		return DatabaseInstance{}, errors.New("Memcached 仅支持本机和容器内网访问")
	}
	var updated DatabaseInstance
	err := m.ApplicationChange(id, func() error {
		row, err := m.DatabaseInstance(ctx, id, instance)
		if err != nil {
			return err
		}
		if row.ContainerID == "" {
			return errors.New("数据库实例不存在")
		}
		remoteFile := m.databaseRemoteFile(id, instance)
		previous, err := os.ReadFile(remoteFile)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(remoteFile), 0750); err != nil {
			return err
		}
		val := "0\n"
		if enabled {
			val = "1\n"
		}
		if err := writeHostConfig(remoteFile, []byte(val)); err != nil {
			return err
		}
		if !row.Running {
			if enabled != row.RemoteAccess {
				row.PendingRemoteAccess = &enabled
			} else {
				row.PendingRemoteAccess = nil
			}
			updated = row
			return nil
		}
		info, exists, err := m.inspectContainer(ctx, row.ContainerID)
		if err == nil && !exists {
			err = errors.New("数据库实例不存在")
		}
		if err == nil {
			_, err = m.applyLegacyDatabaseConfig(ctx, id, instance, row.ContainerHost, info)
		}
		if err != nil {
			return errors.Join(err, writeHostConfig(remoteFile, previous))
		}
		updated, err = m.DatabaseInstance(ctx, id, instance)
		return err
	})
	return updated, err
}

func (m *Manager) databaseHostPort(id, instance, fallback string) string {
	raw, _ := os.ReadFile(m.databasePortFile(id, instance))
	port, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err == nil && port > 0 && port <= 65535 {
		return strconv.Itoa(port)
	}
	return fallback
}

func (m *Manager) SaveDatabaseInstancePort(ctx context.Context, id, instance string, port int) error {
	if port < 1 || port > 65535 {
		return errors.New("端口须在 1–65535 之间")
	}
	return m.ApplicationChange(id, func() error {
		row, err := m.DatabaseInstance(ctx, id, instance)
		if err != nil {
			return err
		}
		if row.ContainerID == "" || row.Port == 0 {
			return errors.New("无法确认该实例的本机端口")
		}
		next := strconv.Itoa(port)
		if port != row.Port {
			listener, err := net.Listen("tcp4", "127.0.0.1:"+next)
			if err != nil {
				return errors.New("该本机端口不可用")
			}
			listener.Close()
		}
		portFile := m.databasePortFile(id, instance)
		if err := os.MkdirAll(filepath.Dir(portFile), 0750); err != nil {
			return err
		}
		return os.WriteFile(portFile, []byte(next+"\n"), 0600)
	})
}

func (m *Manager) databaseConfigCommand(id, instance string) []string {
	switch id {
	case "mysql":
		args := []string{"mysqld"}
		if _, err := os.Stat(m.databaseConfigPath(id, instance)); err == nil {
			args = append(args, "--defaults-file="+databaseConfigurations[id].mount)
		}
		args = append(args, mysqlErrorLog, mysqlAuthLogging, "--skip-name-resolve", "--port=3306", "--datadir=/var/lib/mysql")
		if instance == "default" {
			return append(args, "--socket="+m.MySQLSocket())
		}
		return append(args, "--socket=/var/run/mysqld/mysqld.sock")
	case "postgresql":
		return []string{"postgres", "-c", "config_file=" + databaseConfigurations[id].mount}
	case "mongodb":
		args := []string{"mongod", "--config", databaseConfigurations[id].mount, "--port", "27017", "--dbpath", "/data/db"}
		data, _ := os.ReadFile(m.databaseConfigPath(id, instance))
		var cfg struct {
			Security struct {
				Authorization string
			}
		}
		_ = yaml.Unmarshal(data, &cfg)
		if cfg.Security.Authorization == "disabled" {
			// 初始化仍由官方入口处理，已有数据时才取消入口强制追加的 --auth。
			args = append([]string{"sh", "-c", `if [ -e /data/db/WiredTiger ]; then unset MONGO_INITDB_ROOT_USERNAME MONGO_INITDB_ROOT_PASSWORD; fi; exec docker-entrypoint.sh "$@"`, "mongod"}, args...)
		}
		return args
	case "redis":
		return []string{"sh", "-c", `exec docker-entrypoint.sh redis-server /etc/ezpanel-redis.conf --port 6379 --dir /data --requirepass "$(cat /run/ezpanel-password)"`}
	case "memcached":
		return []string{"sh", "-c", `while IFS= read -r line; do
  case "$line" in ''|'#'*) continue ;; esac
  read -r flag val <<EOF
$line
EOF
  [ -n "$flag" ] && set -- "$@" "$flag"
  [ -n "$val" ] && set -- "$@" "$val"
done < /etc/ezpanel-memcached.args
exec >> ` + containerMemcachedLog + ` 2>&1
printf '[%s] 启动 Memcached\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
exec memcached -v "$@" -p 11211 -U 0`}
	}
	return nil
}

func (m *Manager) databaseConfigMount(id, instance string) []string {
	path, mount := m.databaseConfigPath(id, instance), databaseConfigurations[id].mount
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return []string{"--mount", "type=bind,src=" + path + ",dst=" + mount + ",readonly",
		"--label", "ezpanel.config-revision=" + configRevision(data)}
}

func (m *Manager) prepareDatabaseConfig(id, instance string) error {
	if id == "mysql" {
		return nil
	}
	if id == "memcached" {
		if err := m.prepareMemcachedLog(instance); err != nil {
			return err
		}
	}
	path := m.databaseConfigPath(id, instance)
	if err := ensurePublicTree(filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return os.Chmod(path, 0644)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(path, []byte(databaseConfigurations[id].defaults), 0644); err != nil {
		return err
	}
	return os.Chmod(path, 0644)
}

func (m *Manager) DatabaseInstanceSettings(ctx context.Context, id, instance string) (Settings, error) {
	row, err := m.DatabaseInstance(ctx, id, instance)
	if err != nil {
		return Settings{}, err
	}
	if row.ContainerID == "" {
		return Settings{}, errors.New("数据库实例不存在")
	}
	path := m.databaseConfigPath(id, instance)
	data, err := os.ReadFile(path)
	missing := os.IsNotExist(err)
	if missing {
		if id == "mysql" {
			data, err = m.mysqlMainConfig(ctx, row.ContainerID)
			if err != nil {
				return Settings{}, err
			}
		} else {
			data = []byte(databaseConfigurations[id].defaults)
		}
	} else if err != nil {
		return Settings{}, err
	}
	if len(data) > 1<<20 {
		return Settings{}, errors.New("配置文件超过 1 MiB")
	}
	return Settings{Path: path, Content: string(data), Revision: configRevision(data), Missing: missing}, nil
}

// 已停止的实例同样可以读取镜像的主配置。
func (m *Manager) mysqlMainConfig(ctx context.Context, container string) ([]byte, error) {
	for _, path := range []string{"/etc/my.cnf", "/etc/mysql/my.cnf"} {
		result, err := m.docker(ctx, 8*time.Second, "cp", "-L", container+":"+path, "-")
		if err != nil {
			continue
		}
		reader := tar.NewReader(strings.NewReader(result.Stdout))
		header, err := reader.Next()
		if err != nil || header.Typeflag != tar.TypeReg || result.Truncated {
			return nil, errors.New("无法读取 MySQL 主配置文件")
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, errors.New("该 MySQL 实例未找到 /etc/my.cnf 或 /etc/mysql/my.cnf")
}

func (m *Manager) SaveDatabaseInstanceSettings(ctx context.Context, id, instance, content, revision string) error {
	return m.changeDatabaseInstanceSettings(ctx, id, instance, revision, func(string) (string, error) {
		return content, nil
	})
}

func (m *Manager) changeDatabaseInstanceSettings(ctx context.Context, id, instance, revision string, edit func(string) (string, error)) error {
	return m.ApplicationChange(id, func() error {
		current, err := m.DatabaseInstanceSettings(ctx, id, instance)
		if err != nil {
			return err
		}
		if current.Revision != revision {
			return errors.New("配置已变更，请刷新后重试")
		}
		content, err := edit(current.Content)
		if err != nil {
			return err
		}
		if len(content) > 1<<20 || strings.ContainsRune(content, 0) {
			return errors.New("配置内容无效或超过 1 MiB")
		}
		if id == "mongodb" {
			var document map[string]any
			if err := yaml.Unmarshal([]byte(content), &document); err != nil || len(document) == 0 {
				return errors.New("MongoDB 配置须为有效的 YAML 对象")
			}
		}
		if id == "memcached" && content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		dir := filepath.Dir(current.Path)
		if err := ensurePublicTree(dir); err != nil {
			return err
		}
		file, err := os.CreateTemp(dir, ".database-config-")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		_, err = file.WriteString(content)
		if err == nil {
			err = file.Chmod(0644)
		}
		if err = errors.Join(err, file.Close()); err != nil {
			return err
		}
		return os.Rename(file.Name(), current.Path)
	})
}

// 旧容器仅在启动或重启应用已保存的配置时替换，原容器用于失败回退。
func (m *Manager) applyLegacyDatabaseConfig(ctx context.Context, id, instance, name string, info containerInfo) (bool, error) {
	port := info.databasePort(id)
	nextPort := m.databaseHostPort(id, instance, port)
	if nextPort != port {
		if err := m.prepareDatabaseConfig(id, instance); err != nil {
			return false, err
		}
	}
	config := m.databaseConfigMount(id, instance)
	appliedRevision := info.Config.Labels["ezpanel.config-revision"]
	configUnchanged := len(config) == 0 || config[3] == "ezpanel.config-revision="+appliedRevision
	remote := m.databaseRemoteAccess(id, instance)
	remoteChanged := info.databaseRemoteAccess(id) != remote
	if port == nextPort && configUnchanged && !remoteChanged {
		return false, nil
	}
	var args []string
	if instance == "default" {
		var err error
		args, err = m.serviceContainerArgs(id, nextPort)
		if err != nil {
			return false, err
		}
	} else {
		if nextPort == "" {
			return false, errors.New("无法确认该实例的本机端口")
		}
		var err error
		args, err = m.databaseExtraBindings(id, instance, info.Config.Labels["ezpanel.instance-name"], nextPort)
		if err != nil {
			return false, err
		}
	}
	args = append(args, config...)
	image, err := m.docker(ctx, 5*time.Second, "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", info.ImageID)
	if err != nil {
		return false, err
	}
	wasRunning := info.State.Running && info.State.Status != "restarting"
	previous := name + "-before-config"
	if _, exists, err := m.inspectContainer(ctx, previous); err != nil {
		return false, err
	} else if exists {
		if _, err := m.docker(ctx, 20*time.Second, "rm", "-f", previous); err != nil {
			return false, err
		}
	}
	if info.State.Running {
		if _, err := m.docker(ctx, 30*time.Second, "stop", info.ID); err != nil {
			return false, err
		}
	}
	recovery := context.WithoutCancel(ctx)
	if _, err = m.docker(ctx, 10*time.Second, "rename", info.ID, previous); err != nil {
		if wasRunning {
			_, restartErr := m.docker(recovery, 30*time.Second, "start", info.ID)
			err = errors.Join(err, restartErr)
		}
		return false, err
	}
	err = m.createServiceContainer(ctx, []string{"run", "-d"}, name, info.Config.Labels["ezpanel.version"], strings.TrimSpace(image.Stdout), args, info.ImageID, m.databaseConfigCommand(id, instance))
	if err == nil {
		err = m.waitDatabaseAfterConfig(ctx, id, instance, name)
	}
	if err == nil && (remote || remoteChanged) && m.syncDBPort != nil {
		actualPort, _ := strconv.Atoi(nextPort)
		if syncErr := m.syncDBPort(ctx, id, instance, actualPort, remote); syncErr != nil {
			err = fmt.Errorf("同步数据库防火墙规则失败：%w", syncErr)
		}
	}
	if err != nil {
		if current, exists, inspectErr := m.inspectContainer(recovery, name); inspectErr != nil {
			err = errors.Join(err, inspectErr)
		} else if exists {
			_, removeErr := m.docker(recovery, 30*time.Second, "rm", "-f", current.ID)
			err = errors.Join(err, removeErr)
		}
		_, rollback := m.docker(recovery, 10*time.Second, "rename", info.ID, name)
		if wasRunning && rollback == nil {
			_, rollback = m.docker(recovery, 30*time.Second, "start", info.ID)
		}
		return true, errors.Join(err, rollback)
	}
	if _, err := m.docker(recovery, 30*time.Second, "rm", info.ID); err != nil {
		slog.Warn("新数据库实例已生效，清理旧容器失败", "instance", name, "error", err)
	}
	return true, nil
}

// 等待目标实例能回应本地客户端，确认新容器可用后才移除旧容器。
func (m *Manager) waitDatabaseAfterConfig(parent context.Context, id, instance, name string) error {
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	var probe []string
	switch id {
	case "mysql":
		socket := "/var/run/mysqld/mysqld.sock"
		if instance == "default" {
			socket = m.MySQLSocket()
		}
		probe = []string{"mysqladmin", "--no-defaults", "--socket=" + socket, "ping", "--silent"}
	case "postgresql":
		probe = []string{"pg_isready", "-U", "postgres", "-h", "127.0.0.1", "-p", "5432"}
	case "mongodb":
		probe = []string{"sh", "-c", `[ "$(cat /proc/1/comm)" = mongod ] && exec mongosh --quiet --port 27017 --eval 'quit(db.adminCommand({ping:1}).ok === 1 ? 0 : 1)'`}
	case "redis":
		probe = []string{"redis-cli", "-p", "6379", "ping"}
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return errors.New("应用配置后数据库未就绪，请检查配置")
		}
		info, exists, err := m.inspectContainer(ctx, name)
		if err != nil {
			return err
		}
		if !exists || info.State.Status == "restarting" || info.State.Status == "exited" {
			return errors.New("应用配置后容器启动失败，请检查配置")
		}
		if !info.State.Running {
			continue
		}
		if id == "memcached" {
			for _, binding := range info.HostConfig.PortBindings["11211/tcp"] {
				conn, err := net.DialTimeout("tcp", net.JoinHostPort(binding.HostIP, binding.HostPort), time.Second)
				if err == nil {
					conn.Close()
					return nil
				}
			}
			continue
		}
		out, err := m.docker(ctx, 5*time.Second, append([]string{"exec", info.ID}, probe...)...)
		if err == nil && (id != "redis" || strings.Contains(out.Stdout, "PONG") || strings.Contains(out.Stdout+out.Stderr, "NOAUTH")) {
			return nil
		}
	}
}
