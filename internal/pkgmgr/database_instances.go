package pkgmgr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/executor"
)

var databasePorts = map[string]string{"mysql": "3306", "postgresql": "5432", "mongodb": "27017", "redis": "6379", "memcached": "11211"}

func IsDatabaseContainer(id string) bool { return databasePorts[id] != "" }

type DatabaseInstance struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Version             string `json:"version"`
	UpdateVersion       string `json:"updateVersion,omitempty"`
	Port                int    `json:"port"`
	PendingPort         int    `json:"pendingPort,omitempty"`
	RemoteAccess        bool   `json:"remoteAccess"`
	PendingRemoteAccess *bool  `json:"pendingRemoteAccess,omitempty"`
	Running             bool   `json:"running"`
	Status              string `json:"-"`
	DataDir             string `json:"dataDir"`
	ErrorLogPath        string `json:"errorLogPath,omitempty"`
	ConfigPath          string `json:"configPath,omitempty"`
	ContainerHost       string `json:"containerHost"`
	ContainerPort       int    `json:"containerPort"`
	NetworkReady        bool   `json:"networkReady"`
	ContainerID         string `json:"-"`
	ContainerIP         string `json:"-"`
	StartedAt           string `json:"-"`
	LogFile             string `json:"-"`
	PID                 int32  `json:"-"`
	MemoryLimit         uint64 `json:"-"`
}

type databaseContainer struct {
	name, version, state string
}

func (m *Manager) databaseContainers(ctx context.Context, id string) ([]databaseContainer, error) {
	result, err := m.docker(ctx, 5*time.Second, "ps", "-a", "--filter", "label=ezpanel.managed=true", "--filter", "label=ezpanel.database="+id,
		"--format", `{{.Names}}	{{.Label "ezpanel.version"}}	{{.State}}`)
	if err != nil {
		return nil, err
	}
	rows := []databaseContainer{}
	for line := range strings.SplitSeq(strings.TrimSpace(result.Stdout), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 3 && databaseInstanceID.MatchString(strings.TrimPrefix(fields[0], "ezpanel-"+id+"-")) {
			rows = append(rows, databaseContainer{name: fields[0], version: fields[1], state: fields[2]})
		}
	}
	return rows, nil
}

func (m *Manager) DatabaseInstances(ctx context.Context, id string) ([]DatabaseInstance, error) {
	rows, err := m.DatabaseInstanceSummaries(ctx, id)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].UpdateVersion = m.containerUpdateVersion(ctx, id, rows[i].Version)
	}
	return rows, nil
}

func (m *Manager) DatabaseInstanceSummaries(ctx context.Context, id string) ([]DatabaseInstance, error) {
	rows := []DatabaseInstance{}
	if _, err := m.LookPath("docker"); err != nil {
		return rows, nil
	}
	containers, err := m.databaseContainers(ctx, id)
	if err != nil {
		return nil, err
	}
	ids := []string{"default"}
	prefix := "ezpanel-" + id + "-"
	for _, item := range containers {
		if instance, ok := strings.CutPrefix(item.name, prefix); ok && databaseInstanceID.MatchString(instance) {
			ids = append(ids, instance)
		}
	}
	for _, instance := range ids {
		row, err := m.DatabaseInstance(ctx, id, instance)
		if err != nil {
			return nil, err
		}
		if row.ContainerID != "" {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func (m *Manager) DatabaseInstance(ctx context.Context, id, instance string) (DatabaseInstance, error) {
	var row DatabaseInstance
	if !IsDatabaseContainer(id) || (instance != "default" && !databaseInstanceID.MatchString(instance)) {
		return row, errors.New("数据库实例无效")
	}
	name := "ezpanel-" + id
	if instance != "default" {
		name += "-" + instance
	}
	info, exists, err := m.inspectContainer(ctx, name)
	if err != nil || !exists {
		return row, err
	}
	if instance != "default" && info.Config.Labels["ezpanel.database"] != id {
		return row, errors.New("数据库实例归属无效")
	}
	row = DatabaseInstance{ID: instance, Name: Name(id), Version: info.Config.Labels["ezpanel.version"], Running: info.State.Running && info.State.Status != "restarting", Status: info.State.Status,
		ContainerHost: name, ContainerID: info.ID, PID: info.State.Pid, ContainerIP: info.managedAddress(), StartedAt: info.State.StartedAt, MemoryLimit: info.HostConfig.Memory}
	row.ContainerPort, _ = strconv.Atoi(databasePorts[id])
	_, row.NetworkReady = info.NetworkSettings.Networks[ManagedNetwork]
	if id == "memcached" {
		row.LogFile = info.memcachedLog()
	}
	if id != "memcached" {
		row.DataDir = filepath.Join(m.databaseInstanceRoot(id, instance), "data")
	}
	if instance != "default" {
		row.Name = info.Config.Labels["ezpanel.instance-name"]
	}
	if id == "mysql" {
		row.ErrorLogPath = filepath.Join(m.layout().AppInstanceLogDir("mysql", instance), "error.log")
	}
	row.ConfigPath = m.databaseConfigPath(id, instance)
	row.Port, _ = strconv.Atoi(info.databasePort(id))
	row.RemoteAccess = info.databaseRemoteAccess(id)
	if desired := m.databaseRemoteAccess(id, instance); desired != row.RemoteAccess {
		row.PendingRemoteAccess = &desired
	}
	if pending, _ := strconv.Atoi(m.databaseHostPort(id, instance, "")); pending != row.Port {
		row.PendingPort = pending
	}
	return row, nil
}

// 连接已登记实例前核对端口，避免实例移除后使用被复用的端口。
func (m *Manager) RunningDatabaseInstance(ctx context.Context, engine, instance string, port int) (DatabaseInstance, error) {
	row, err := m.DatabaseInstance(ctx, engine, instance)
	if err != nil {
		return row, err
	}
	if row.ContainerID == "" || !row.Running || row.Port != port || port == 0 {
		return row, errors.New("所选数据库实例未运行或连接端口已变更，请刷新实例列表")
	}
	return row, nil
}

func (m *Manager) databaseExtraBindings(id, instance, name, port string) ([]string, error) {
	root := m.databaseInstanceRoot(id, instance)
	logDir, err := m.ensureContainerLogDir(id, instance)
	if err != nil {
		return nil, err
	}
	args := []string{
		"--label", "ezpanel.database=" + id,
		"--label", "ezpanel.instance-name=" + name,
		"-p", m.databaseBindIP(id, instance) + ":" + port + ":" + databasePorts[id],
		"--mount", "type=bind,src=" + logDir + ",dst=" + ContainerLogDir,
	}
	data := filepath.Join(root, "data")
	state := filepath.Join(root, ".state")
	secret := "type=bind,src=" + filepath.Join(state, "root-password") + ",dst=/run/ezpanel-password,readonly"
	switch id {
	case "mysql":
		return append(args, "--mount", "type=bind,src="+data+",dst=/var/lib/mysql", "--mount", secret,
			"-e", "MYSQL_ROOT_PASSWORD_FILE=/run/ezpanel-password", "-e", "MYSQL_ROOT_HOST=%"), nil
	case "postgresql":
		return append(args, "--mount", "type=bind,src="+data+",dst=/var/lib/postgresql/data", "--mount", secret,
			"-e", "PGDATA=/var/lib/postgresql/data", "-e", "POSTGRES_PASSWORD_FILE=/run/ezpanel-password"), nil
	case "mongodb":
		return append(args, "--mount", "type=bind,src="+data+",dst=/data/db", "--env-file", filepath.Join(state, "mongo.env")), nil
	case "redis":
		return append(args, "--mount", "type=bind,src="+data+",dst=/data", "--mount", secret), nil
	}
	return args, nil
}

func (m *Manager) InstallDatabaseInstance(ctx context.Context, id, name, version string, log func(string), selected SourceSelection) error {
	release, err := m.dockerRelease(ctx, id, version, selected)
	if err != nil {
		return err
	}
	return m.ApplicationChange(id, func() error {
		platform := releasePlatform(release)
		reference, err := m.pullContainer(ctx, release.URL, platform, log)
		if err != nil {
			return err
		}
		binding, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return err
		}
		port := binding.Addr().(*net.TCPAddr).Port
		_ = binding.Close()

		if err := m.ensureAppParents(id); err != nil {
			return err
		}
		prefix := "ezpanel-" + id + "-"
		var container, instanceID, root string
		for {
			container, err = m.freeContainerName(ctx, prefix)
			if err != nil {
				return err
			}
			instanceID = strings.TrimPrefix(container, prefix)
			root = m.databaseInstanceRoot(id, instanceID)
			if err = os.Mkdir(root, 0755); !errors.Is(err, os.ErrExist) {
				if err != nil {
					return err
				}
				break
			}
		}
		installed := false
		defer func() {
			if !installed {
				_ = os.RemoveAll(root)
			}
		}()
		if err := os.Chmod(root, 0755); err != nil {
			return err
		}
		if name == "" {
			name = Name(id) + " " + strconv.Itoa(port)
		}
		if id != "memcached" {
			state := filepath.Join(root, ".state")
			for _, dir := range []string{filepath.Join(root, "data"), state} {
				if err := os.MkdirAll(dir, 0750); err != nil {
					return err
				}
			}
			password := hex.EncodeToString([]byte(rand.Text()))
			if err := os.WriteFile(filepath.Join(state, "root-password"), []byte(password), 0600); err != nil {
				return err
			}
			if id == "mongodb" {
				if err := os.WriteFile(filepath.Join(state, "mongo.env"), []byte("MONGO_INITDB_ROOT_USERNAME=panelAdmin\nMONGO_INITDB_ROOT_PASSWORD="+password+"\n"), 0600); err != nil {
					return err
				}
			}
		}
		if err := m.prepareDatabaseConfig(id, instanceID); err != nil {
			return err
		}
		bindings, err := m.databaseExtraBindings(id, instanceID, name, strconv.Itoa(port))
		if err != nil {
			return err
		}
		args := append(bindings, m.databaseConfigMount(id, instanceID)...)
		if err := m.createServiceContainer(ctx, []string{"create"}, container, release.Version, platform, args, reference, m.databaseConfigCommand(id, instanceID)); err != nil {
			return err
		}
		if _, err := m.docker(ctx, time.Minute, "start", container); err != nil {
			_, _ = m.docker(context.WithoutCancel(ctx), 20*time.Second, "rm", "-f", container)
			return err
		}
		if id == "mongodb" {
			if err := m.waitDatabaseAfterConfig(ctx, id, instanceID, container); err != nil {
				_, _ = m.docker(context.WithoutCancel(ctx), 20*time.Second, "rm", "-fv", container)
				return err
			}
		}
		installed = true
		log(fmt.Sprintf("已创建 %s，监听 127.0.0.1:%d", name, port))
		return nil
	})
}

var databaseInstanceID = regexp.MustCompile(`^(?:[a-z2-7]{6}|[a-z2-7]{26})$`)

func (m *Manager) DatabaseInstanceAction(ctx context.Context, id, instanceID, action string) error {
	if !IsDatabaseContainer(id) || (instanceID != "default" && !databaseInstanceID.MatchString(instanceID)) {
		return errors.New("数据库实例不存在")
	}
	if instanceID == "default" && action == "remove" {
		_, err := m.uninstallDocker(ctx, id)
		return err
	}
	return m.ApplicationChange(id, func() error {
		name := "ezpanel-" + id
		if instanceID != "default" {
			name += "-" + instanceID
		}
		info, exists, err := m.inspectContainer(ctx, name)
		if err != nil {
			return err
		}
		if !exists || (instanceID != "default" && info.Config.Labels["ezpanel.database"] != id) {
			return errors.New("数据库实例不存在")
		}
		launch := action == "restart" || action == "start" && (!info.State.Running || info.State.Status == "restarting")
		if launch {
			if id == "mongodb" {
				if err := os.Chmod(m.databaseConfigPath(id, instanceID), 0644); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			if applied, err := m.applyLegacyDatabaseConfig(ctx, id, instanceID, name, info); applied || err != nil {
				return err
			}
			if info.databaseRemoteAccess(id) && m.syncDBPort != nil {
				port, _ := strconv.Atoi(info.databasePort(id))
				if err := m.syncDBPort(ctx, id, instanceID, port, true); err != nil {
					return err
				}
			}
			if err := m.connectManagedNetwork(ctx, info); err != nil {
				return err
			}
		}
		args := []string{action, info.ID}
		if action == "remove" {
			if err := m.checkDatabaseInstanceEmpty(ctx, id, instanceID, info); err != nil {
				return err
			}
			args = []string{"rm", "-f", info.ID}
		}
		_, err = m.docker(ctx, 60*time.Second, args...)
		if err == nil && action == "remove" && m.syncDBPort != nil {
			err = m.syncDBPort(ctx, id, instanceID, 0, false)
		}
		if err == nil && launch && id == "mongodb" {
			err = m.waitDatabaseAfterConfig(ctx, id, instanceID, name)
			if err != nil {
				_, _ = m.docker(context.WithoutCancel(ctx), 20*time.Second, "stop", info.ID)
			}
		}
		return err
	})
}

func (m *Manager) checkDatabaseInstanceEmpty(ctx context.Context, engine, instanceID string, info containerInfo) error {
	if engine == "memcached" {
		return nil
	}
	if !info.State.Running || info.State.Status == "restarting" || info.State.Status == "paused" {
		return errors.New("请先启动实例，确认其中没有数据库后再卸载")
	}
	failed := errors.New("无法检查实例中的数据库，请确认实例运行正常且管理凭据有效后重试")
	args := []string{"exec"}
	var probe []string
	var env []string
	switch engine {
	case "mysql":
		socket := "/var/run/mysqld/mysqld.sock"
		if instanceID == "default" {
			socket = m.MySQLSocket()
		}
		password, err := m.DatabaseInstancePassword(engine, instanceID)
		if err != nil {
			return failed
		}
		// 读取宿主机的最新凭据，避免密码文件被替换后容器仍挂载旧文件。
		env = append(executor.DefaultEnv(), "MYSQL_PWD="+password)
		args = append(args, "-e", "MYSQL_PWD")
		probe = []string{"mysql", "--no-defaults", "--socket=" + socket, "-uroot", "-NBe",
			`SELECT count(*) FROM information_schema.schemata WHERE BINARY schema_name NOT IN ('mysql','information_schema','performance_schema','sys')`}
	case "postgresql":
		probe = []string{"psql", "-XAtw", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-c",
			`SELECT count(*) FROM pg_catalog.pg_database WHERE NOT datistemplate AND datname <> 'postgres'`}
	case "mongodb":
		probe = []string{"mongosh", "--quiet", "--norc", "--eval", `
const admin = db.getSiblingDB('admin');
if (process.env.MONGO_INITDB_ROOT_USERNAME) {
  admin.auth(process.env.MONGO_INITDB_ROOT_USERNAME, process.env.MONGO_INITDB_ROOT_PASSWORD);
}
const result = admin.runCommand({ listDatabases: 1, nameOnly: true, authorizedDatabases: false });
print(result.databases.filter(({name}) => !['admin', 'config', 'local'].includes(name)).length);`}
	case "redis":
		probe = []string{"sh", "-ec", `REDISCLI_AUTH="$(cat /run/ezpanel-password)"
export REDISCLI_AUTH
exec redis-cli --raw -e INFO keyspace`}
	}
	docker, err := m.LookPath("docker")
	if err != nil {
		return failed
	}
	out, err := m.Run(ctx, 15*time.Second, env, docker, append(append(args, info.ID), probe...)...)
	if err == nil {
		result := strings.TrimSpace(out.Stdout)
		if engine == "redis" {
			if result == "# Keyspace" {
				return nil
			}
			if strings.HasPrefix(result, "# Keyspace\r\n") || strings.HasPrefix(result, "# Keyspace\n") {
				return errors.New("该实例仍有 Redis 数据，请先清空所有逻辑库中的键再卸载")
			}
		} else if count, err := strconv.Atoi(result); err == nil && count >= 0 {
			if count == 0 {
				return nil
			}
			return fmt.Errorf("该实例仍有 %d 个用户数据库，请先删除全部数据库再卸载实例", count)
		}
	}
	return failed
}

func (m *Manager) DatabaseInstancePassword(id, instanceID string) (string, error) {
	if instanceID == "" {
		instanceID = "default"
	}
	if !IsDatabaseContainer(id) || (instanceID != "default" && !databaseInstanceID.MatchString(instanceID)) || id == "memcached" {
		return "", errors.New("此实例没有可读取的初始密码")
	}
	path := filepath.Join(m.databaseInstanceRoot(id, instanceID), ".state", "root-password")
	data, err := os.ReadFile(path)
	return string(data), err
}

// SetDatabaseInstancePassword 原地覆写密码文件，保持 Docker 单文件 bind-mount 的 inode 不变，
// 使容器内 /run/ezpanel-password 立即同步最新密码，重启或升级后仍保持一致。
func (m *Manager) SetDatabaseInstancePassword(id, instanceID, password string) error {
	if instanceID == "" {
		instanceID = "default"
	}
	if !IsDatabaseContainer(id) || (instanceID != "default" && !databaseInstanceID.MatchString(instanceID)) || id == "memcached" {
		return errors.New("此实例不支持修改密码文件")
	}
	dir := filepath.Join(m.databaseInstanceRoot(id, instanceID), ".state")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "root-password"), []byte(password), 0600)
}

func (m *Manager) UpdateDatabaseInstance(ctx context.Context, id, instance, version string, log func(string), selected SourceSelection) error {
	if !IsDatabaseContainer(id) || (instance != "default" && !databaseInstanceID.MatchString(instance)) {
		return errors.New("数据库实例不存在")
	}
	release, err := m.dockerRelease(ctx, id, version, selected)
	if err != nil {
		return err
	}
	return m.ApplicationChange(id, func() error {
		row, err := m.DatabaseInstance(ctx, id, instance)
		if err != nil {
			return err
		}
		if row.ContainerID == "" {
			return errors.New("数据库实例不存在")
		}
		info, exists, err := m.inspectContainer(ctx, row.ContainerID)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("数据库实例不存在")
		}
		return m.upgradeContainerInstance(ctx, id, instance, release, info, log)
	})
}
