package pkgmgr

import (
	"context"
	"errors"
	"ezpanel/internal/config"
	"ezpanel/internal/executor"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrUnsupported = errors.New("系统或软件不支持受控管理")
var ErrBusy = errors.New("软件安装、卸载或服务操作正在运行")

type System struct {
	ID      string `json:"id"`
	Manager string `json:"manager"`
	Command string `json:"-"`
}
type State struct {
	RunningInstances    int      `json:"runningInstances,omitempty"`
	InstanceCount       int      `json:"instanceCount"`
	InstallDir          string   `json:"installDir,omitempty"`
	DataDir             string   `json:"dataDir,omitempty"`
	InitialPasswordPath string   `json:"initialPasswordPath,omitempty"`
	Updateable          bool     `json:"updateable"`
	UpdateVersion       string   `json:"updateVersion"`
	UpdateNotice        string   `json:"updateNotice"`
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Installed           bool     `json:"installed"`
	ComposeMissing      bool     `json:"composeMissing,omitempty"`
	Running             *bool    `json:"running"`
	Version             *string  `json:"version"`
	InstalledVersions   []string `json:"installedVersions,omitempty"`
	PkgManager          string   `json:"pkgManager"`
	Manageable          bool     `json:"manageable"`
	Category            string   `json:"category"`
	Description         string   `json:"description"`
	Installable         bool     `json:"installable"`
	Uninstallable       bool     `json:"uninstallable"`
	UsedBySites         []string `json:"usedBySites,omitempty"`
	Reason              string   `json:"reason"`
	Unit                string   `json:"-"`
	rsyncDetached       bool
	packages            []string
}
type application struct {
	id, name, category, description string
	binaries, units, apt, rpm       []string
}

var applications = []application{
	{id: "frp", name: "Frp服务", category: "tools", description: "提供内网穿透与反向代理，将内网的 TCP、UDP 和 Web 服务映射到外部网络"},
	{id: "openresty", name: "OpenResty", category: "web", description: "网站统一入口，支持 PHP 和反向代理"},
	{id: "python", name: "Python", category: "web", description: "在网站页部署 Python 项目"},
	{id: "nodejs", name: "Node.js", category: "web", description: "在网站页部署 Node.js 项目"},
	{id: "go", name: "Go", category: "web", description: "在网站页部署 Go 项目"},
	{id: "phpmyadmin", name: "phpMyAdmin", category: "tools", description: "通过面板私有网关管理MySQL，使用已安装的兼容PHP运行环境"},
	{"ftp", "Pure-FTPd", "tools", "宿主机 FTP 文件传输服务", []string{"pure-ftpd"}, []string{"pure-ftpd.service"}, []string{"pure-ftpd"}, []string{"pure-ftpd"}},
	{"fail2ban", "Fail2ban", "tools", "根据宿主机登录与服务日志执行封禁规则", []string{"fail2ban-client"}, []string{"fail2ban.service"}, []string{"fail2ban"}, []string{"fail2ban-server", "fail2ban"}},
	{"supervisor", "Supervisor", "tools", "管理宿主机进程、启动命令与重启策略", []string{"supervisord"}, []string{"supervisor.service", "supervisord.service"}, []string{"supervisor"}, []string{"supervisor"}},
	{"clamav", "ClamAV", "tools", "使用宿主机病毒库扫描建站目录", []string{"clamd"}, []string{"clamav-daemon.service", "clamd@scan.service"}, []string{"clamav-daemon", "clamav", "clamav-freshclam", "clamdscan"}, []string{"clamd", "clamav", "clamav-update"}},
	{"rsync", "Rsync", "tools", "管理宿主机文件同步模块和访问权限", []string{"rsync"}, []string{"rsync.service", "rsyncd.service"}, []string{"rsync"}, []string{"rsync-daemon", "rsync"}},
	{"mongodb", "MongoDB", "database", "MongoDB文档数据库，默认仅本机监听", []string{"mongod"}, []string{"mongod.service"}, []string{"mongodb-org-server"}, []string{"mongodb-org-server"}},
	{"postgresql", "PostgreSQL", "database", "PostgreSQL 关系型数据库，默认仅本机监听", []string{"postgres"}, []string{"postgresql.service"}, []string{"postgresql"}, []string{"postgresql-server"}},
	{"memcached", "Memcached", "database", "Memcached内存缓存，默认关闭UDP并仅本机监听", []string{"memcached"}, []string{"memcached.service"}, []string{"memcached"}, []string{"memcached"}},
	{"nginx", "Nginx", "web", "HTTP 与反向代理服务器", []string{"nginx"}, []string{"nginx.service"}, []string{"nginx", "nginx-core", "nginx-full", "nginx-light", "nginx-extras", "nginx-common", "libnginx-mod-http-geoip", "libnginx-mod-http-geoip2", "libnginx-mod-http-image-filter", "libnginx-mod-http-xslt-filter", "libnginx-mod-mail", "libnginx-mod-stream", "libnginx-mod-stream-geoip", "libnginx-mod-stream-geoip2", "libnginx-mod-http-perl", "libnginx-mod-http-auth-pam", "libnginx-mod-http-dav-ext", "libnginx-mod-http-echo", "libnginx-mod-http-fancyindex", "libnginx-mod-http-headers-more-filter", "libnginx-mod-http-lua", "libnginx-mod-http-ndk", "libnginx-mod-http-subs-filter", "libnginx-mod-http-upstream-fair", "libnginx-mod-nchan"}, []string{"nginx", "nginx-core", "nginx-filesystem"}},
	{"mysql", "MySQL", "database", "MySQL 数据库服务器；与 MariaDB 互斥", []string{"mysqld"}, []string{"mysql.service", "mysqld.service"}, []string{"mysql-server", "mysql-server-8.0", "mysql-server-8.4", "mysql-server-core-8.0", "mysql-server-core-8.4", "mysql-community-server", "mysql-community-server-core"}, []string{"mysql-server", "community-mysql-server", "mysql-community-server"}},
	{"mariadb", "MariaDB", "database", "MariaDB 数据库服务器；与 MySQL 互斥", []string{"mariadbd", "mysqld"}, []string{"mariadb.service", "mysql.service", "mysqld.service"}, []string{"mariadb-server", "mariadb-server-core", "mariadb-server-10.11", "mariadb-server-core-10.11", "mariadb-server-10.6", "mariadb-server-core-10.6", "mariadb-server-10.5", "mariadb-server-core-10.5", "mariadb-plugin-provider-bzip2", "mariadb-plugin-provider-lz4", "mariadb-plugin-provider-lzma", "mariadb-plugin-provider-lzo", "mariadb-plugin-provider-snappy"}, []string{"mariadb-server", "MariaDB-server"}},
	{"redis", "Redis", "database", "Redis 内存数据服务", []string{"redis-server"}, []string{"redis-server.service", "redis.service"}, []string{"redis-server", "redis-tools"}, []string{"redis"}},
	{"mysql-client", "MySQL 客户端", "tools", "系统仓库提供的 MySQL 协议客户端，具体提供者取决于发行版", []string{"mysql", "mariadb"}, nil, []string{"default-mysql-client", "mysql-client", "mysql-client-8.0", "mysql-client-8.4", "mysql-client-core-8.0", "mysql-client-core-8.4", "mysql-community-client", "mysql-community-client-core", "mariadb-client", "mariadb-client-core", "mariadb-client-10.11", "mariadb-client-core-10.11", "mariadb-client-10.6", "mariadb-client-core-10.6", "mariadb-client-10.5", "mariadb-client-core-10.5"}, []string{"mariadb", "mysql", "community-mysql", "mysql-community-client", "MariaDB-client"}},
	{"docker", "Docker", "web", "Docker 容器引擎", []string{"dockerd"}, []string{"docker.service"}, []string{"docker.io"}, []string{"moby-engine", "docker"}},
}

func app(id string) (application, bool) {
	for _, a := range applications {
		if a.id == id {
			return a, true
		}
	}
	return application{}, false
}
func ValidID(id string) bool { _, ok := app(id); return ok }
func Name(id string) string  { a, _ := app(id); return a.name }

func ApplicationNames() map[string]string {
	names := make(map[string]string, len(applications))
	for _, app := range applications {
		names[app.id] = app.name
	}
	return names
}
func (a application) packagesFor(sys System) []string {
	if sys.Manager == "apt-get" {
		return a.apt
	}
	return a.rpm
}
func contains(values []string, value string) bool {
	return slices.Contains(values, value)
}

type Manager struct {
	cfg           config.Config
	sourcesMu     sync.Mutex
	sourceCache   map[string]versionCache
	refreshMu     sync.Mutex
	refreshLocks  map[string]*sync.Mutex
	ReadFile      func(string) ([]byte, error)
	LookPath      func(string) (string, error)
	Run           func(context.Context, time.Duration, []string, string, ...string) (executor.Result, error)
	Stream        func(context.Context, time.Duration, []string, func(string), string, ...string) (executor.Result, error)
	mu            sync.Mutex
	installing    map[string]bool
	packageMu     sync.Mutex
	lastAptUpdate time.Time
	detectMu      sync.Mutex
	detectCached  bool
	detectSys     System
	services      map[string]bool
	syncDBPort    func(context.Context, string, string, int, bool) error
}

func New() *Manager {
	return &Manager{ReadFile: os.ReadFile, LookPath: executor.LookPath, Run: executor.RunEnv, Stream: executor.Stream, services: map[string]bool{}, installing: map[string]bool{}, sourceCache: map[string]versionCache{}, refreshLocks: map[string]*sync.Mutex{}}
}
func (m *Manager) Configure(c config.Config) { m.cfg = c }
func (m *Manager) SetDatabasePortSync(sync func(context.Context, string, string, int, bool) error) {
	m.syncDBPort = sync
}
func (m *Manager) Detect() (System, error) {
	m.detectMu.Lock()
	if m.detectCached {
		sys := m.detectSys
		m.detectMu.Unlock()
		return sys, nil
	}
	m.detectMu.Unlock()
	b, e := m.ReadFile("/etc/os-release")
	if e != nil {
		return System{}, ErrUnsupported
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[k] = strings.Trim(v, "\"'")
		}
	}
	id := values["ID"]
	var candidates []string
	switch id {
	case "debian", "ubuntu":
		candidates = []string{"apt-get"}
	case "rhel", "centos", "rocky", "almalinux", "fedora", "ol", "amzn":
		candidates = []string{"dnf", "yum"}
	default:
		return System{}, ErrUnsupported
	}
	pid, e := m.ReadFile("/proc/1/comm")
	if e != nil || strings.TrimSpace(string(pid)) != "systemd" {
		return System{}, ErrUnsupported
	}
	for _, name := range candidates {
		if path, e := m.LookPath(name); e == nil {
			sys := System{ID: id, Manager: name, Command: path}
			m.detectMu.Lock()
			m.detectSys = sys
			m.detectCached = true
			m.detectMu.Unlock()
			return sys, nil
		}
	}
	return System{}, ErrUnsupported
}

var versionPattern = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)+(?:[-+][a-zA-Z0-9.]+)?`)

func (m *Manager) Status(ctx context.Context, id string) (State, error) {
	if id == "frp" {
		return m.frpStatus(ctx)
	}
	if UsesDocker(id) {
		return m.dockerStatus(ctx, id)
	}
	a, ok := app(id)
	if !ok {
		return State{}, ErrUnsupported
	}
	if state, owned, err := m.sourceStatus(ctx, id); owned {
		return state, err
	}
	state := State{ID: id, Name: a.name, Category: a.category, Description: a.description}
	if id == "phpmyadmin" {
		return state, nil
	}
	sys, e := m.Detect()
	if e != nil {
		state.Reason = e.Error()
		return state, e
	}
	state.PkgManager = sys.Manager
	aliases := a.packagesFor(sys)
	hostTool := isHostTool(id)
	// 同一应用的拆分包一次查询，避免商店轮询逐别名启动命令。
	var r executor.Result
	if sys.Manager == "apt-get" {
		format := "${binary:Package}\t${db:Status-Status}"
		if hostTool {
			format += "\t${Version}"
		}
		args := append([]string{"-W", "-f=" + format + "\n"}, aliases...)
		r, e = m.Run(ctx, 30*time.Second, nil, "/usr/bin/dpkg-query", args...)
	} else if hostTool {
		args := append([]string{"-q", "--qf", "%{NAME} installed %{VERSION}-%{RELEASE}\n"}, aliases...)
		r, e = m.Run(ctx, 30*time.Second, nil, "/usr/bin/rpm", args...)
	} else {
		r, e = m.Run(ctx, 30*time.Second, nil, "/usr/bin/rpm", "-qa", "--qf", "%{NAME}\n")
	}
	// 不存在的候选包可令整次查询退出1，已安装部分仍在标准输出。
	if r.Truncated || (e != nil && ((sys.Manager != "apt-get" && !hostTool) || r.ExitCode != 1)) {
		state.Reason = "读取系统软件包状态失败"
		return state, errors.New(state.Reason)
	}
	if ctx.Err() != nil {
		return state, ctx.Err()
	}
	for _, line := range strings.Split(r.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || ((sys.Manager == "apt-get" || hostTool) && (len(fields) < 2 || fields[1] != "installed")) {
			continue
		}
		name, _, _ := strings.Cut(fields[0], ":")
		if contains(aliases, name) {
			state.packages = append(state.packages, name)
			if hostTool && name == aliases[0] && len(fields) == 3 {
				state.Version = &fields[2]
			}
		}
	}
	for _, pkg := range state.packages {
		if hostTool && pkg != aliases[0] {
			continue
		}
		if pkg != "nginx-common" && pkg != "nginx-filesystem" && pkg != "redis-tools" && !strings.HasPrefix(pkg, "libnginx-mod-") && !strings.HasPrefix(pkg, "mariadb-plugin-provider-") {
			state.Installed = true
		}
	}
	unmanaged := false
	for _, binary := range a.binaries {
		path, err := m.LookPath(binary)
		if err != nil {
			continue
		}
		arg := "--version"
		if id == "nginx" {
			arg = "-v"
		}
		if id == "memcached" {
			arg = "-h"
		}
		if !hostTool {
			r, err := m.Run(ctx, 30*time.Second, nil, path, arg)
			if err != nil {
				state.Reason = "无法读取程序版本，不能确认程序归属"
				unmanaged = true
				state.Installed = true
				continue
			}
			output := r.Stdout + " " + r.Stderr
			// mysqld 可能是 MariaDB 的兼容入口，必须按版本提供者区分。
			maria := strings.Contains(strings.ToLower(output), "mariadb")
			if (id == "mysql" && maria) || (id == "mariadb" && !maria) {
				continue
			}
			if v := versionPattern.FindString(output); v != "" {
				state.Version = &v
			}
		}
		ownerPath := path
		if real, err := filepath.EvalSymlinks(path); err == nil {
			ownerPath = real
		}
		var owner executor.Result
		if sys.Manager == "apt-get" {
			owner, err = m.Run(ctx, 30*time.Second, nil, "/usr/bin/dpkg-query", "-S", ownerPath)
		} else {
			owner, err = m.Run(ctx, 30*time.Second, nil, "/usr/bin/rpm", "-qf", "--qf", "%{NAME}\n", ownerPath)
		}
		owned := false
		if err == nil {
			for _, line := range strings.Split(owner.Stdout, "\n") {
				name := strings.TrimSpace(line)
				if sys.Manager == "apt-get" {
					name, _, _ = strings.Cut(name, ":")
				}
				if contains(aliases, name) && contains(state.packages, name) {
					owned = true
				}
			}
		}
		if !owned {
			unmanaged = true
			state.Reason = "检测到非本商店可接管的程序，不支持覆盖安装或卸载"
		}
		if !hostTool || !owned {
			state.Installed = true
		}
	}
	if state.Installed && !unmanaged {
		for _, unit := range a.units {
			r, err := m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "show", unit, "--property=LoadState", "--value")
			if err == nil && strings.TrimSpace(r.Stdout) == "loaded" {
				state.Unit = unit
				break
			}
		}
		if state.Unit != "" {
			r, _ := m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "is-active", state.Unit)
			running := strings.TrimSpace(r.Stdout) == "active"
			state.Running = &running
		}
	}
	state.Uninstallable = state.Installed && len(state.packages) > 0 && !unmanaged
	state.Manageable = state.Uninstallable && (len(a.units) == 0 || state.Unit != "")
	if hostTool && state.Manageable && state.Version != nil && sys.Manager == "apt-get" {
		state.Updateable = true
		m.sourcesMu.Lock()
		cache := m.sourceCache[id+":system"]
		m.sourcesMu.Unlock()
		if len(cache.List) != 0 && m.newerDebPackage(ctx, cache.List[0].Version, *state.Version) {
			state.UpdateVersion = cache.List[0].Version
		}
	}
	if state.Installed && !unmanaged && len(a.units) > 0 && state.Unit == "" && state.Reason == "" {
		state.Reason = "未检测到可管理的服务；可卸载仓库包后重新安装"
	}
	if state.Installed && !state.Uninstallable && state.Reason == "" {
		state.Reason = "软件不属于可管理的系统仓库包"
	}
	if id == "rsync" && state.Manageable && state.Running != nil && !*state.Running {
		if _, err := os.Stat(m.rsyncDetachedPath()); err == nil {
			state.Installed, state.Manageable, state.Uninstallable, state.Updateable = false, false, false, false
			state.Running = nil
			state.rsyncDetached = true
			state.Reason = "系统 Rsync 包已保留，安装将恢复接收服务"
		}
	}
	return state, nil
}

// candidate 仅查询当前系统已配置仓库，不添加源或切换数据库提供者。
func (m *Manager) candidate(ctx context.Context, sys System, a application) (string, error) {
	packages := a.packagesFor(sys)
	// 二进制拆分包仅用于识别/卸载，安装只使用发行版入口包。
	var candidates []string
	switch a.id {
	case "nginx":
		candidates = []string{"nginx"}
	case "mysql":
		candidates = []string{"mysql-server", "community-mysql-server"}
	case "mariadb":
		candidates = []string{"mariadb-server"}
	case "redis":
		candidates = packages[:1]
	case "mysql-client":
		if sys.Manager == "apt-get" {
			candidates = []string{"default-mysql-client", "mysql-client", "mariadb-client"}
		} else {
			candidates = []string{"mariadb", "mysql", "community-mysql"}
		}
	case "docker":
		candidates = packages
	}
	for _, pkg := range candidates {
		if sys.Manager == "apt-get" {
			path, e := m.LookPath("apt-cache")
			if e != nil {
				return "", e
			}
			r, e := m.Run(ctx, 30*time.Second, nil, path, "policy", pkg)
			if e != nil {
				return "", fmt.Errorf("读取软件源候选包失败: %w", e)
			}
			for _, line := range strings.Split(r.Stdout, "\n") {
				value, ok := strings.CutPrefix(strings.TrimSpace(line), "Candidate:")
				if ok && strings.TrimSpace(value) != "" && strings.TrimSpace(value) != "(none)" {
					return pkg, nil
				}
			}
		} else {
			r, e := m.Run(ctx, 30*time.Second, nil, sys.Command, "-q", "list", "--available", pkg)
			if e == nil && strings.Contains(r.Stdout, pkg+".") {
				return pkg, nil
			}
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	return "", errors.New("当前系统仓库没有可用安装包；不会添加第三方源")
}
func (m *Manager) Catalog(ctx context.Context) []State {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	targets := make([]application, 0, len(applications))
	for _, a := range applications {
		if a.id == "mysql-client" || a.id == "mariadb" || a.id == "nginx" {
			continue
		}
		targets = append(targets, a)
	}
	_, detectErr := m.Detect()
	rows := make([]State, len(targets))
	var wg sync.WaitGroup
	for i, a := range targets {
		wg.Add(1)
		go func(idx int, app application) {
			defer wg.Done()
			state, e := m.Status(ctx, app.id)
			if e != nil {
				state.Reason = e.Error()
			}
			if e == nil && detectErr == nil && !state.Installed && !UsesDocker(app.id) {
				state.Installable = true
			}
			if e == nil && app.id == "docker" && state.Installed && state.Uninstallable {
				if docker, err := m.LookPath("docker"); err == nil {
					_, available := m.dockerComposeStatus(ctx, docker)
					state.ComposeMissing = !available
				}
			}
			rows[idx] = state
		}(i, a)
	}
	wg.Wait()
	return rows
}

func resourceKey(id string) string {
	if id == "mysql" || id == "mariadb" {
		return "database-server"
	}
	return id
}
func (m *Manager) lock(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := resourceKey(id)
	if m.installing[key] || m.services[key] {
		return ErrBusy
	}
	m.installing[key] = true
	return nil
}
func (m *Manager) unlock(id string) {
	m.mu.Lock()
	delete(m.installing, resourceKey(id))
	m.mu.Unlock()
}

type packageDependencyError string

func (e packageDependencyError) Error() string {
	return fmt.Sprintf("操作会连带移除其他软件包 %s，已拒绝", string(e))
}

// checkPlan 拒绝跨应用删除。RPM 的 --assumeno 正常也会非零退出，因此以完整事务摘要确认计划已解析。
func (m *Manager) checkPlan(ctx context.Context, sys System, action string, packages, allowed []string, log func(string)) error {
	var args []string
	if sys.Manager == "apt-get" {
		args = []string{"--simulate", action}
		args = append(args, packages...)
	} else {
		args = []string{"--assumeno", "--setopt=clean_requirements_on_remove=False", "--setopt=obsoletes=False", action}
		args = append(args, packages...)
	}
	r, e := m.Run(ctx, 2*time.Minute, nil, sys.Command, args...)
	output := r.Stdout + "\n" + r.Stderr
	if log != nil {
		for _, line := range strings.Split(output, "\n") {
			if strings.TrimSpace(line) != "" {
				log(line)
			}
		}
	}
	if r.Truncated {
		return errors.New("包管理器计划输出不完整，已拒绝操作")
	}
	if sys.Manager == "apt-get" {
		if e != nil {
			return fmt.Errorf("包管理器预检失败: %w", e)
		}
		removals := 0
		for _, line := range strings.Split(output, "\n") {
			fields := strings.Fields(line)
			if len(fields) > 1 && fields[0] == "Remv" {
				name, _, _ := strings.Cut(fields[1], ":")
				if !contains(allowed, name) {
					return packageDependencyError(name)
				}
				removals++
			}
		}
		if action == "remove" && removals == 0 {
			return errors.New("未识别到卸载计划，已拒绝操作")
		}
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !strings.Contains(output, "Transaction Summary") || (!strings.Contains(output, "Operation aborted") && !strings.Contains(output, "Exiting on user command")) {
		return errors.New("无法确认 RPM 事务预检，已拒绝操作")
	}
	summaryPattern := regexp.MustCompile(`(?m)^\s*(?:Remove|Removing|Erase)\s+(\d+)\s+Package`)
	expectedRemovals := 0
	for _, match := range summaryPattern.FindAllStringSubmatch(output, -1) {
		n, _ := strconv.Atoi(match[1])
		expectedRemovals += n
	}
	removing, removals := false, 0
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "Transaction Summary" {
			break
		}
		if strings.HasSuffix(trimmed, ":") {
			removing = strings.HasPrefix(trimmed, "Removing") || strings.HasPrefix(trimmed, "Erasing") || strings.HasPrefix(trimmed, "Replacing") || strings.HasPrefix(trimmed, "Obsoleting")
			continue
		}
		if !removing || trimmed == "" || strings.HasPrefix(trimmed, "=") || strings.HasPrefix(trimmed, "-") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			return errors.New("无法解析 RPM 删除计划，已拒绝操作")
		}
		if !contains(allowed, fields[0]) {
			return packageDependencyError(fields[0])
		}
		removals++
	}
	if removals != expectedRemovals {
		return errors.New("RPM 删除计划与事务摘要不一致，已拒绝操作")
	}
	if action == "remove" && removals == 0 {
		return errors.New("未识别到卸载计划，已拒绝操作")
	}
	return nil
}

func (m *Manager) Install(ctx context.Context, id string, onLine func(string)) (State, error) {
	if id == "frp" {
		return m.installFRP(ctx, "", []string{"frps"}, false, onLine)
	}
	if UsesDocker(id) {
		return m.installDocker(ctx, id, "", onLine)
	}
	if id == "phpmyadmin" {
		return State{}, errors.New("请选择phpMyAdmin官方版本后安装")
	}
	return m.installPackage(ctx, id, "", onLine)
}

func (m *Manager) installPackage(ctx context.Context, id, version string, onLine func(string)) (State, error) {
	a, ok := app(id)
	if !ok {
		return State{}, ErrUnsupported
	}
	if e := m.lock(id); e != nil {
		return State{}, e
	}
	defer m.unlock(id)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if onLine == nil {
		onLine = func(string) {}
	}
	state, e := m.Status(ctx, id)
	if e != nil {
		return state, e
	}
	if id == "rsync" && (state.rsyncDetached || state.Manageable && state.Running != nil && !*state.Running) {
		if e = m.prepareRsync(); e != nil {
			return state, e
		}
		if e = m.ensureGuard(ctx, state.Unit); e != nil {
			return state, e
		}
		if e = m.enableRsync(ctx, state.Unit); e != nil {
			return state, e
		}
		return m.Status(ctx, id)
	}
	if state.Installed {
		if !state.Uninstallable {
			return state, ErrUnsupported
		}
		if id == "docker" {
			docker, err := m.LookPath("docker")
			if err != nil {
				return state, err
			}
			return state, m.ensureDockerCompose(ctx, docker, onLine)
		}
		if id == "clamav" && state.Manageable && (state.Running == nil || !*state.Running) {
			if e = m.prepareClamAV(ctx, state, onLine); e != nil {
				return state, e
			}
			_, e = m.Run(ctx, 5*time.Minute, nil, "/usr/bin/systemctl", "enable", "--now", state.Unit)
			if e != nil {
				return state, e
			}
			state, e = m.Status(ctx, id)
			if e == nil && (state.Running == nil || !*state.Running) {
				e = errors.New("ClamAV 病毒库已更新，但扫描服务未运行，请检查运行日志")
			}
			return state, e
		}
		return state, nil
	}
	if id == "mariadb" {
		conflict, err := m.Status(ctx, "mysql")
		if err != nil {
			return state, err
		}
		if conflict.Installed {
			return state, fmt.Errorf("MySQL 与 MariaDB 互斥，不能安装到已有 %s 的系统", conflict.Name)
		}
	}
	m.packageMu.Lock()
	defer m.packageMu.Unlock()
	if e := ctx.Err(); e != nil {
		return state, e
	}
	sys, e := m.Detect()
	if e != nil {
		return state, e
	}
	if onLine == nil {
		onLine = func(string) {}
	}
	env := executor.DefaultEnv()
	if sys.Manager == "apt-get" {
		env = append(env, "DEBIAN_FRONTEND=noninteractive")
		if _, e = m.Stream(ctx, 15*time.Minute, env, onLine, sys.Command, "update"); e != nil {
			return state, fmt.Errorf("软件源索引更新失败，未继续安装（不修改系统源）: %w", e)
		}
	}
	var packages []string
	if isHostTool(id) {
		aliases := a.packagesFor(sys)
		candidate, err := m.hostToolVersion(ctx, sys, aliases[0])
		if err != nil {
			return state, err
		}
		if version != "" && version != candidate {
			return state, errors.New("系统软件源的候选版本已变化，请重新选择版本")
		}
		if sys.Manager == "apt-get" {
			packages = []string{aliases[0] + "=" + candidate}
		} else {
			packages = []string{aliases[0] + "-" + candidate}
		}
		if id == "clamav" {
			packages = append(packages, aliases[1:]...)
		}
	} else {
		pkg, err := m.candidate(ctx, sys, a)
		if err != nil {
			return state, err
		}
		packages = []string{pkg}
	}
	if e = m.checkPlan(ctx, sys, "install", packages, nil, onLine); e != nil {
		return state, e
	}
	args := []string{"-y", "--setopt=obsoletes=False", "install"}
	if sys.Manager == "apt-get" {
		args = []string{"-y", "--no-remove", "--no-upgrade", "-o", "Dpkg::Options::=--force-confold", "install"}
	}
	_, operationErr := m.Stream(ctx, 15*time.Minute, env, onLine, sys.Command, append(args, packages...)...)
	actual, probeErr := m.Status(ctx, id)
	if probeErr == nil {
		state = actual
	}
	if operationErr != nil {
		return state, fmt.Errorf("软件包安装失败（保留系统锁文件，请查看日志）: %w", operationErr)
	}
	if probeErr != nil {
		return state, probeErr
	}
	if !state.Installed || !state.Uninstallable {
		return state, errors.New("安装后未检测到受控软件包，请检查任务日志")
	}
	if id == "docker" {
		docker, err := m.LookPath("docker")
		if err == nil {
			err = m.ensureDockerCompose(ctx, docker, onLine)
		}
		if err != nil {
			return state, fmt.Errorf("Docker 引擎已安装，补装 Compose 失败：%w", err)
		}
	}
	if len(a.units) > 0 {
		if !state.Manageable {
			return state, errors.New("安装后未检测到受控服务")
		}
		if e = m.ensureGuard(ctx, state.Unit); e != nil {
			return state, e
		}
		timeout := 30 * time.Second
		if id == "clamav" {
			if e = m.prepareClamAV(ctx, state, onLine); e != nil {
				return state, e
			}
			timeout = 5 * time.Minute
		}
		if id == "ftp" && state.PkgManager == "apt-get" {
			const dontResolve = "/etc/pure-ftpd/conf/DontResolve"
			if _, err := os.Stat(dontResolve); os.IsNotExist(err) {
				if err = writeInitial(dontResolve, "yes\n", 0644); err != nil {
					return state, err
				}
				if _, err = m.Run(ctx, timeout, nil, "/usr/bin/systemctl", "restart", state.Unit); err != nil {
					return state, err
				}
			}
		}
		if id == "supervisor" {
			path := hostToolConfigs[id][""][0]
			if state.PkgManager != "apt-get" {
				path = hostToolConfigs[id][""][1]
			}
			if _, e = m.supervisorCall(ctx, path, "initialize", nil); e != nil {
				return state, e
			}
			if _, e = m.Run(ctx, timeout, nil, "/usr/bin/systemctl", "restart", state.Unit); e != nil {
				return state, e
			}
		}
		if id == "rsync" {
			if e = m.prepareRsync(); e != nil {
				return state, e
			}
			if e = m.enableRsync(ctx, state.Unit); e != nil {
				return state, e
			}
			return m.Status(ctx, id)
		}
		_, activationErr := m.Run(ctx, timeout, nil, "/usr/bin/systemctl", "enable", "--now", state.Unit)
		state, e = m.Status(ctx, id)
		if activationErr != nil {
			return state, activationErr
		}
		if e != nil {
			return state, e
		}
		if state.Running == nil || !*state.Running {
			return state, errors.New("安装完成但服务未运行，请检查任务日志")
		}
	}
	return state, nil
}

func (m *Manager) Uninstall(ctx context.Context, id string, onLine func(string)) (State, error) {
	if id == "frp" {
		return m.removeFRP(ctx)
	}
	if UsesDocker(id) {
		return m.uninstallDocker(ctx, id)
	}
	a, ok := app(id)
	if !ok {
		return State{}, ErrUnsupported
	}
	if e := m.lock(id); e != nil {
		return State{}, e
	}
	defer m.unlock(id)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if onLine == nil {
		onLine = func(string) {}
	}
	state, e := m.Status(ctx, id)
	if e != nil {
		return state, e
	}
	if !state.Installed || !state.Uninstallable {
		return state, ErrUnsupported
	}
	if _, err := m.sourceRecord(id); err == nil {
		if onLine == nil {
			onLine = func(string) {}
		}
		return m.uninstallSource(ctx, id, onLine)
	}
	m.packageMu.Lock()
	defer m.packageMu.Unlock()
	if e := ctx.Err(); e != nil {
		return state, e
	}
	sys, e := m.Detect()
	if e != nil {
		return state, e
	}
	if onLine == nil {
		onLine = func(string) {}
	}
	packages, allowed := state.packages, a.packagesFor(sys)
	if id == "rsync" && sys.Manager != "apt-get" {
		packages = []string{"rsync-daemon"}
		allowed = packages
	}
	if e = m.checkPlan(ctx, sys, "remove", packages, allowed, onLine); e != nil {
		var dependency packageDependencyError
		if id == "rsync" && state.Manageable && errors.As(e, &dependency) {
			return m.detachRsync(ctx, state, string(dependency), onLine)
		}
		return state, e
	}
	configs, e := m.preserveConfigs(ctx, sys, packages)
	if e != nil {
		return state, e
	}
	if state.Unit != "" {
		if _, e = m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "stop", state.Unit); e != nil {
			return state, fmt.Errorf("停止服务失败，未继续卸载: %w", e)
		}
	}
	onLine("仅移除本应用的系统仓库包；不清理配置、数据库数据和网站目录")
	args := []string{"-y", "--setopt=clean_requirements_on_remove=False", "--setopt=obsoletes=False", "remove"}
	env := executor.DefaultEnv()
	if sys.Manager == "apt-get" {
		args = []string{"-y", "remove"}
		env = append(env, "DEBIAN_FRONTEND=noninteractive")
	}
	args = append(args, packages...)
	_, operationErr := m.Stream(ctx, 15*time.Minute, env, onLine, sys.Command, args...)
	configErr := restoreConfigs(configs)
	actual, probeErr := m.Status(ctx, id)
	if probeErr == nil {
		state = actual
	}
	if configErr != nil {
		return state, configErr
	}
	if operationErr != nil {
		return state, fmt.Errorf("软件包卸载失败，请查看任务日志: %w", operationErr)
	}
	if probeErr != nil {
		return state, probeErr
	}
	if state.Installed || slices.ContainsFunc(state.packages, func(pkg string) bool { return contains(packages, pkg) }) {
		return state, errors.New("卸载后仍检测到软件或受控包，不能确认卸载完成")
	}
	return state, nil
}

func (m *Manager) Service(ctx context.Context, id, action string) (State, error) {
	if action == "reload" && !IsPHP(id) && !contains([]string{"openresty", "nginx", "fail2ban", "supervisor", "docker"}, id) {
		return State{}, ErrUnsupported
	}
	if UsesDocker(id) {
		if !contains([]string{"start", "stop", "restart", "reload"}, action) || IsProjectRuntime(id) {
			return State{}, ErrUnsupported
		}
		return m.dockerService(ctx, id, action)
	}
	a, ok := app(id)
	if !ok || len(a.units) == 0 {
		return State{}, ErrUnsupported
	}
	if !contains([]string{"start", "stop", "restart", "reload"}, action) {
		return State{}, ErrUnsupported
	}
	m.mu.Lock()
	if m.installing[resourceKey(id)] || m.services[resourceKey(id)] {
		m.mu.Unlock()
		return State{}, ErrBusy
	}
	m.services[resourceKey(id)] = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.services, resourceKey(id)); m.mu.Unlock() }()
	state, e := m.Status(ctx, id)
	if e != nil {
		return state, e
	}
	if !state.Installed || !state.Manageable {
		return state, ErrUnsupported
	}
	if action == "reload" && (state.Running == nil || !*state.Running) {
		return state, errors.New("请先启动服务再重载配置")
	}
	if action != "stop" && m.upgradeBusy(ctx, id) {
		return state, errors.New("隔离升级进程仍在运行，不能同时启动公开服务")
	}
	if action == "start" || action == "restart" {
		if id == "rsync" {
			if e = m.prepareRsync(); e != nil {
				return state, e
			}
		}
		if e = m.ensureGuard(ctx, state.Unit); e != nil {
			return state, e
		}
	}
	if action == "reload" && id == "nginx" {
		bin, err := m.LookPath("nginx")
		if err != nil {
			return state, err
		}
		result, err := m.Run(ctx, 30*time.Second, nil, bin, "-t")
		if err != nil {
			return state, fmt.Errorf("Nginx配置检查失败：%s", strings.TrimSpace(result.Stderr+result.Stdout))
		}
	}
	if action == "reload" && id == "fail2ban" {
		if _, e = m.fail2banClient(ctx, "-t"); e != nil {
			return state, e
		}
		_, e = m.fail2banClient(ctx, "reload")
		return state, e
	}
	if action == "reload" && id == "supervisor" {
		bin, err := m.LookPath("supervisorctl")
		if err != nil {
			return state, err
		}
		path := hostToolConfigs[id][""][0]
		if state.PkgManager != "apt-get" {
			path = hostToolConfigs[id][""][1]
		}
		result, err := m.Run(ctx, 60*time.Second, nil, bin, "-c", path, "update")
		if err != nil {
			return state, fmt.Errorf("Supervisor 重载失败：%s", strings.TrimSpace(result.Stderr+" "+result.Stdout))
		}
		return state, nil
	}
	timeout := 30 * time.Second
	if id == "clamav" && action != "stop" {
		if e = m.prepareClamAV(ctx, state, nil); e != nil {
			return state, e
		}
		timeout = 5 * time.Minute
	}
	args := []string{action, state.Unit}
	if action == "stop" {
		// 同时关闭关联的激活单元，避免 socket 等在服务停止后再次拉起它。
		triggers, err := m.Run(ctx, timeout, nil, systemctlPath, "show", state.Unit, "--property=TriggeredBy", "--value")
		if err != nil {
			return state, fmt.Errorf("读取服务激活单元失败：%w", err)
		}
		args = append([]string{"disable", "--now", state.Unit}, strings.Fields(triggers.Stdout)...)
	}
	result, e := m.Run(ctx, timeout, nil, systemctlPath, args...)
	if e != nil {
		return state, fmt.Errorf("服务操作失败：%s", strings.TrimSpace(result.Stderr+" "+result.Stdout))
	}
	actual, e := m.Status(ctx, id)
	if e != nil {
		return actual, e
	}
	expected := action != "stop"
	if actual.Running == nil || *actual.Running != expected {
		return actual, errors.New("服务命令已执行，但状态尚未达到预期，请刷新或检查日志")
	}
	if action == "start" {
		if err := m.enableSystemdStartup(ctx, timeout, state.Unit); err != nil {
			return actual, err
		}
	}
	if action != "stop" {
		if record, err := m.sourceRecord(id); err == nil && record.UpdateFailed {
			if err = m.waitService(ctx, record.Unit, record.Prefix, 30*time.Second); err != nil {
				return actual, err
			}
			record.UpdateFailed = false
			if id == "mysql" || id == "mariadb" || id == "mongodb" {
				if err = os.WriteFile(m.versionFile(id), []byte(record.Version), 0600); err != nil {
					return actual, err
				}
			}
			if err = m.writeRecord(record); err != nil {
				return actual, err
			}
			_ = m.cleanPrevious(&record)
		}
	}
	return actual, nil
}

// ApplicationChange与该应用的安装、服务控制共用互斥。
func (m *Manager) ApplicationChange(id string, fn func() error) error {
	if !ValidID(id) {
		return ErrUnsupported
	}
	if err := m.lock(id); err != nil {
		return err
	}
	defer m.unlock(id)
	return fn()
}
