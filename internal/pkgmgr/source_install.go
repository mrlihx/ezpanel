package pkgmgr

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"ezpanel/internal/executor"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type sourceInstall struct {
	DownloadSource    SourceSelection   `json:"downloadSource"`
	UpdateFailed      bool              `json:"updateFailed,omitempty"`
	Prefix            string            `json:"prefix,omitempty"`
	PreviousPrefix    string            `json:"previousPrefix,omitempty"`
	PreviousProcesses []oldProcess      `json:"previousProcesses,omitempty"`
	ID                string            `json:"id"`
	Version           string            `json:"version"`
	URL               string            `json:"url"`
	SHA256            string            `json:"sha256"`
	Links             map[string]string `json:"links"`
	Unit              string            `json:"unit"`
}

func (m *Manager) sourceRecord(id string) (sourceInstall, error) {
	var record sourceInstall
	b, e := os.ReadFile(filepath.Join(m.installRoot(id), ".panel-source.json"))
	if e != nil {
		return record, e
	}
	e = json.Unmarshal(b, &record)
	return record, e
}
func (m *Manager) sourceStatus(ctx context.Context, id string) (State, bool, error) {
	record, e := m.sourceRecord(id)
	if os.IsNotExist(e) {
		return State{}, false, nil
	}
	if e != nil {
		return State{}, true, e
	}
	a, _ := app(id)
	state := State{ID: id, Name: a.name, Category: a.category, Description: a.description, Installed: true, Version: &record.Version, PkgManager: m.repository(id).label, Manageable: true, Uninstallable: true, Unit: record.Unit}
	state.InstallDir = m.installRoot(id)
	if id == "mysql" || id == "mariadb" || id == "redis" || id == "mongodb" {
		state.DataDir = m.serviceDataDir(id)
	}
	if id == "mysql" {
		state.InitialPasswordPath = filepath.Join(m.serviceStateDir(id), "root-password")
	}
	for _, target := range record.Links {
		if _, e = os.Stat(target); e != nil {
			state.Reason = "安装文件缺失，请查看日志后卸载重装"
			state.Manageable = false
		}
	}
	if id == "phpmyadmin" {
		for _, name := range []string{"index.php", "config.inc.php"} {
			info, err := os.Stat(filepath.Join(m.PHPMyAdminRoot(), name))
			if err != nil || !info.Mode().IsRegular() {
				state.Reason = "phpMyAdmin安装文件缺失，请卸载重装"
				state.Manageable = false
			}
		}
	}
	if record.Unit != "" {
		r, _ := m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "is-active", record.Unit)
		running := strings.TrimSpace(r.Stdout) == "active"
		state.Running = &running
	}
	state.Updateable = true
	state.UpdateNotice = m.VersionInfo(id).Notice
	for _, release := range m.VersionInfo(id).List {
		if series(release.Version) == series(record.Version) && newer(release.Version, record.Version) {
			state.UpdateVersion = release.Version
			break
		}
	}
	if record.PreviousPrefix != "" {
		state.UpdateNotice = "新版本已接管，等待旧连接退出后清理旧程序"
	}
	if record.UpdateFailed {
		state.Reason = "上次更新切换未完成，请检查日志后启动或重启新版本；备份及旧程序保留"
	}
	return state, true, nil
}

// 官方归档也是外部输入；只释放单一顶层目录，链接在普通文件写完后创建。
func extractSource(archive, destination string, pecl ...bool) error {
	f, e := os.Open(archive)
	if e != nil {
		return e
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	type link struct {
		name, target string
		hard         bool
	}
	links := []link{}
	var total int64
	top := ""
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		// GitHub归档含PAX全局元数据，它不是待解压的文件或根目录。
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		clean := filepath.Clean(h.Name)
		if len(pecl) > 0 && pecl[0] && (clean == "package.xml" || clean == "package2.xml") {
			continue
		}
		parts := strings.SplitN(clean, "/", 2)
		if filepath.IsAbs(clean) || parts[0] == ".." {
			return errors.New("软件归档路径无效")
		}
		if top == "" {
			top = parts[0]
		}
		if parts[0] != top {
			return errors.New("软件归档包含多个根目录")
		}
		if len(parts) == 1 {
			continue
		}
		relative := parts[1]
		if relative == ".." || strings.HasPrefix(relative, "../") {
			return errors.New("软件归档越界")
		}
		path := filepath.Join(destination, relative)
		switch h.Typeflag {
		case tar.TypeDir:
			e = os.MkdirAll(path, 0755)
		case tar.TypeReg:
			total += h.Size
			if h.Size < 0 || total > 16<<30 {
				return errors.New("解压软件超过16GiB限制")
			}
			if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
				return e
			}
			var out *os.File
			out, e = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(h.Mode)&0777)
			if e == nil {
				_, e = io.CopyN(out, tr, h.Size)
				closeErr := out.Close()
				if e == nil {
					e = closeErr
				}
				// 保留发行归档时间，避免make误判生成文件过期并调用版本绑定的维护工具。
				if e == nil && !h.ModTime.IsZero() {
					e = os.Chtimes(path, h.ModTime, h.ModTime)
				}
			}
		case tar.TypeSymlink, tar.TypeLink:
			target := h.Linkname
			if h.Typeflag == tar.TypeLink {
				p := strings.SplitN(filepath.Clean(target), "/", 2)
				if len(p) != 2 || p[0] != top {
					return errors.New("软件归档硬链接无效")
				}
				target = filepath.Join(destination, p[1])
			} else {
				target = filepath.Clean(filepath.Join(filepath.Dir(path), target))
			}
			rel, err := filepath.Rel(destination, target)
			if err != nil || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(h.Linkname) {
				return errors.New("软件归档链接越界")
			}
			links = append(links, link{path, target, h.Typeflag == tar.TypeLink})
		default:
			return errors.New("软件归档含不支持的文件类型")
		}
		if e != nil {
			return e
		}
	}
	for _, l := range links {
		if e = os.MkdirAll(filepath.Dir(l.name), 0755); e != nil {
			return e
		}
		if l.hard {
			e = os.Link(l.target, l.name)
		} else {
			var rel string
			rel, e = filepath.Rel(filepath.Dir(l.name), l.target)
			if e == nil {
				e = os.Symlink(rel, l.name)
			}
		}
		if e != nil {
			return e
		}
	}
	return nil
}
func writeInitial(path, content string, mode os.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	if e := os.Chmod(filepath.Dir(path), 0755); e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if os.IsExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	e = f.Chmod(mode)
	if e == nil {
		_, e = f.WriteString(content)
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	return e
}
func (m *Manager) InstallRelease(ctx context.Context, id, version string, onLine func(string), selected ...SourceSelection) (State, error) {
	if isHostTool(id) {
		return m.installPackage(ctx, id, version, onLine)
	}
	if UsesDocker(id) {
		return m.installDocker(ctx, id, version, onLine, selected...)
	}
	return m.installRelease(ctx, id, version, false, false, onLine, selected...)
}
func (m *Manager) UpdateRelease(ctx context.Context, id, version string, onLine func(string), selected ...SourceSelection) (State, error) {
	if isHostTool(id) {
		return m.updateHostTool(ctx, id, version, onLine)
	}
	if IsPHP(id) || id == "openresty" || IsDatabaseContainer(id) {
		return m.changeContainer(ctx, id, version, true, onLine, selected...)
	}
	if UsesDocker(id) {
		return State{}, ErrUnsupported
	}
	return m.installRelease(ctx, id, version, true, false, onLine, selected...)
}
func (m *Manager) installRelease(ctx context.Context, id, version string, updating, switching bool, onLine func(string), selected ...SourceSelection) (State, error) {
	choice := m.InstalledSource(id)
	if len(selected) != 0 {
		choice = selected[0]
	}
	releases, e := m.Releases(ctx, id, false, choice)
	if e != nil {
		return State{}, e
	}
	var release Release
	for _, r := range releases {
		if r.Version == version {
			release = r
			break
		}
	}
	if release.Version == "" {
		return State{}, errors.New("所选版本不在当前下载源目录，请刷新版本列表")
	}
	if e = m.lock(id); e != nil {
		return State{}, e
	}
	defer m.unlock(id)
	state, e := m.Status(ctx, id)
	if e != nil {
		return state, e
	}
	var previous sourceInstall
	if updating {
		previous, e = m.sourceRecord(id)
		if e != nil || !state.Installed {
			return state, errors.New("仅支持更新本商店官方源安装的软件")
		}
		if switching && version == previous.Version {
			return state, errors.New("请选择不同的Nginx版本")
		}
		if !switching && (series(previous.Version) != series(version) || !newer(version, previous.Version)) {
			return state, errors.New("更新目标须为当前版本系列中较新的补丁版本")
		}
		if m.upgradeBusy(ctx, id) {
			return state, errors.New("前次隔离升级进程仍在运行，请先处理该任务")
		}
		if previous.PreviousPrefix != "" {
			return state, errors.New("上次更新的旧进程尚未退出，请待清理完成后再次更新")
		}
	} else if state.Installed {
		return state, errors.New("已有安装，请使用更新入口；不会覆盖现有软件")
	}
	if id == "phpmyadmin" {
		if _, e = m.PHPMyAdminRuntime(ctx); e != nil {
			return state, e
		}
	}

	if id == "mysql" || id == "mariadb" {
		other := "mysql"
		if id == "mysql" {
			other = "mariadb"
		}
		s, e := m.Status(ctx, other)
		if e != nil {
			return state, e
		}
		if s.Installed {
			return state, errors.New("MySQL 与 MariaDB 不能同时安装")
		}
	}
	sys, e := m.Detect()
	if e != nil {
		return state, e
	}
	root := m.installRoot(id)
	prefix := filepath.Join(root, "releases", version)
	dataDir := m.serviceDataDir(id)
	if !updating && (id == "mysql" || id == "mariadb" || id == "mongodb") {
		b, err := os.ReadFile(m.versionFile(id))
		if err != nil && !os.IsNotExist(err) {
			return state, err
		}
		if len(b) > 0 && strings.TrimSpace(string(b)) != version {
			return state, errors.New("检测到其他版本保留的数据库数据；请先迁移数据，不能原地跨版本重装")
		}
	}
	if !updating && (id == "mysql" || id == "mariadb") {
		if _, err := os.Stat(dataDir + "/mysql"); os.IsNotExist(err) {
			entries, readErr := os.ReadDir(dataDir)
			if readErr != nil && !os.IsNotExist(readErr) {
				return state, readErr
			}
			if len(entries) > 0 {
				return state, fmt.Errorf("检测到未完成初始化的数据目录 %s，请先备份并处理后重试；本次未重新编译", dataDir)
			}
		} else if err != nil {
			return state, err
		}
	}
	buildRoot := filepath.Join(m.cfg.DataDir(), "software-build")
	if e = os.MkdirAll(buildRoot, 0700); e != nil {
		return state, e
	}
	work, e := os.MkdirTemp(buildRoot, id+"-")
	if e != nil {
		return state, e
	}
	defer os.RemoveAll(work)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Hour)
	defer cancel()
	buildEnv := append(executor.DefaultEnv(), "TMPDIR="+work, "TMP="+work, "TEMP="+work)
	if IsPHP(id) {
		buildEnv = phpEnvironment(id, prefix, buildEnv)
	}
	onLine("磁盘构建目录：" + work)
	run := func(cwd, name string, args ...string) error {
		bin := name
		var err error
		if !filepath.IsAbs(name) {
			bin, err = m.LookPath(name)
			if err != nil {
				return err
			}
		}
		if updating && (name == "make" || name == "cmake") {
			nice, err := m.LookPath("nice")
			if err != nil {
				return err
			}
			args = append([]string{"-n", "10", bin}, args...)
			bin = nice
		}
		env := buildEnv
		if (id == "mysql" || id == "mariadb") && (strings.HasSuffix(name, "/mysqld") || strings.HasSuffix(name, "/mariadbd") || strings.HasSuffix(name, "/mariadb-install-db")) {
			// 初始化会降权为数据库账号，不能继承只有面板root可访问的构建临时目录。
			env = append(append([]string(nil), buildEnv...), "TMPDIR="+m.serviceTempDir(id), "TMP="+m.serviceTempDir(id), "TEMP="+m.serviceTempDir(id))
		}
		_, err = executor.StreamFixed(ctx, 3*time.Hour, cwd, env, onLine, bin, args...)
		return err
	}
	if release.Method == "compile" || id == "docker" || id == "mongodb" {
		onLine("等待系统依赖事务（其余应用的下载和构建可并行）")
		if e = func() error {
			m.packageMu.Lock()
			defer m.packageMu.Unlock()
			if err := ctx.Err(); err != nil {
				return err
			}
			deps := []string{"gcc", "gcc-c++", "make", "cmake", "bison", "flex", "openssl-devel", "pcre2-devel", "zlib-devel", "ncurses-devel", "libaio-devel", "libtirpc-devel", "libevent-devel", "libudev-devel", "rpcgen", "perl", "pkgconf-pkg-config"}
			if sys.Manager == "apt-get" {
				deps = []string{"build-essential", "cmake", "bison", "flex", "libssl-dev", "libpcre2-dev", "zlib1g-dev", "libncurses-dev", "libaio-dev", "libtirpc-dev", "libevent-dev", "libudev-dev", "rpcsvc-proto", "perl", "pkg-config", "libnuma-dev", "liblz4-dev", "libzstd-dev"}
			}
			if id == "nginx" || id == "redis" {
				deps = []string{"gcc", "make", "openssl-devel", "pcre2-devel", "zlib-devel"}
				if sys.Manager == "apt-get" {
					deps = []string{"build-essential", "libssl-dev", "libpcre2-dev", "zlib1g-dev"}
				}
			}
			if id == "nginx" {
				deps = append(deps, "logrotate")
			}
			if id == "docker" {
				deps = []string{"iptables"}
			}
			if id == "memcached" {
				deps = []string{"gcc", "make", "libevent-devel"}
				if sys.Manager == "apt-get" {
					deps = []string{"build-essential", "libevent-dev"}
				}
			}
			if id == "mongodb" {
				deps = []string{"openssl-libs", "libcurl", "cyrus-sasl-lib"}
				if sys.Manager == "apt-get" {
					deps = []string{"libssl-dev", "libcurl4-openssl-dev", "libsasl2-2", "libgssapi-krb5-2"}
				}
			}
			if IsPHP(id) {
				deps = []string{"gcc", "gcc-c++", "make", "autoconf", "automake", "libtool", "bison", "re2c", "pkgconf-pkg-config", "xz", "perl", "openssl-devel", "sqlite-devel", "oniguruma-devel", "libzip-devel", "zlib-devel"}
				if sys.Manager == "apt-get" {
					deps = []string{"build-essential", "autoconf", "automake", "libtool", "bison", "re2c", "pkg-config", "xz-utils", "perl", "libssl-dev", "libsqlite3-dev", "libonig-dev", "libzip-dev", "zlib1g-dev"}
				}
				if phpBranch(id) >= "8.1" {
					curl := "libcurl-devel"
					if sys.Manager == "apt-get" {
						curl = "libcurl4-openssl-dev"
					}
					deps = append(deps, curl)
				}
			}
			var err error
			deps, err = m.missingPackages(ctx, sys, deps, onLine)
			if err != nil || len(deps) == 0 {
				return err
			}
			onLine("安装缺失依赖（不替换应用软件包）")
			env := executor.DefaultEnv()
			args := []string{"-y", "--setopt=obsoletes=False", "install"}
			if sys.Manager == "apt-get" {
				env = append(env, "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l")
				if _, e = m.Stream(ctx, 15*time.Minute, env, onLine, sys.Command, "update"); e != nil {
					return e
				}
				args = []string{"-y", "--no-remove", "--no-upgrade", "install"}
			}
			if e = m.checkPlan(ctx, sys, "install", deps, nil, onLine); e != nil {
				return e
			}
			if _, e = m.Stream(ctx, 30*time.Minute, env, onLine, sys.Command, append(args, deps...)...); e != nil {
				return e
			}
			return nil
		}(); e != nil {
			return state, e
		}
	}
	onLine("下载所选版本 " + release.Version + "：" + release.URL)
	req, e := http.NewRequestWithContext(ctx, "GET", m.fileURL(release.URL), nil)
	if e != nil {
		return state, e
	}
	// 和版本目录使用相同标识，避免镜像站拦截Go默认User-Agent。
	req.Header.Set("User-Agent", softwareUserAgent)
	client := &http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || r.URL.Scheme != "https" {
			return errors.New("软件下载仅允许 HTTPS")
		}
		return nil
	}}
	res, e := client.Do(req)
	if e != nil {
		return state, e
	}
	if (id == "mysql" || id == "mysql-client") && strings.HasPrefix(release.URL, "https://dev.mysql.com/") && (res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusGone) {
		res.Body.Close()
		release.URL = "https://downloads.mysql.com/archives/get/p/23/file/mysql-" + version + ".tar.gz"
		onLine("从 MySQL 官方归档下载相同版本：" + release.URL)
		req, e = http.NewRequestWithContext(ctx, "GET", m.fileURL(release.URL), nil)
		if e != nil {
			return state, e
		}
		req.Header.Set("User-Agent", softwareUserAgent)
		res, e = client.Do(req)
		if e != nil {
			return state, e
		}
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return state, fmt.Errorf("下载失败 HTTP %d", res.StatusCode)
	}
	expectedHash := m.releaseChecksum(ctx, release)
	if id == "phpmyadmin" && expectedHash == "" {
		res.Body.Close()
		return state, errors.New("无法读取phpMyAdmin官方SHA256校验值，未安装")
	}
	if expectedHash != "" {
		onLine("已读取下载源SHA256校验值")
	} else {
		onLine("下载源未提供可读取的SHA256清单，使用所选HTTPS来源并记录归档摘要")
	}
	archive := filepath.Join(work, "source.tar.gz")
	f, e := os.Create(archive)
	if e != nil {
		res.Body.Close()
		return state, e
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(res.Body, (1<<30)+1))
	res.Body.Close()
	closeErr := f.Close()
	if copyErr != nil {
		return state, copyErr
	}
	if closeErr != nil {
		return state, closeErr
	}
	if n > 1<<30 {
		return state, errors.New("软件安装包超过1GiB限制")
	}
	checksum := hex.EncodeToString(hash.Sum(nil))
	if expectedHash != "" && checksum != expectedHash {
		return state, errors.New("安装包SHA256与下载源清单不一致，已拒绝安装")
	}
	onLine("归档 SHA256：" + checksum)
	src := filepath.Join(work, "source")
	if e = os.MkdirAll(src, 0755); e != nil {
		return state, e
	}
	if e = extractSource(archive, src); e != nil {
		return state, e
	}
	if id == "phpmyadmin" {
		return m.publishPHPMyAdmin(ctx, release, src, prefix, checksum, choice, previous, run, onLine)
	}
	if e = os.RemoveAll(prefix); e != nil {
		return state, e
	}
	if e = EnsureWWWRoot(); e != nil {
		return state, e
	}
	if e = m.ensureAppParents(id); e != nil {
		return state, e
	}
	if e = os.MkdirAll(prefix, 0755); e != nil {
		return state, e
	}
	// 主进程umask为0077，受控程序目录必须显式允许服务账号遍历。
	for _, dir := range []string{m.layout().AppsDir(), filepath.Dir(root), root, root + "/releases"} {
		if e = os.Chmod(dir, 0755); e != nil {
			return state, e
		}
	}
	published := false
	defer func() {
		if !published {
			os.RemoveAll(prefix)
		}
	}()
	links := map[string]string{}
	unit := ""
	service := ""
	config := ""
	configPath := ""
	switch id {
	case "mongodb":
		if e = os.MkdirAll(prefix+"/bin", 0755); e != nil {
			return state, e
		}
		if e = run(work, "cp", src+"/bin/mongod", prefix+"/bin/mongod"); e != nil {
			return state, e
		}
		if _, err := m.Run(ctx, 30*time.Second, nil, prefix+"/bin/mongod", "--version"); err != nil {
			return state, errors.New("官方MongoDB程序无法在当前服务器运行，请检查CPU指令集与系统运行库")
		}
		links["/usr/local/bin/mongod"] = prefix + "/bin/mongod"
		configPath = "/etc/ezpanel-apps/mongodb.yaml"
		config = "storage:\n  dbPath: " + dataDir + "\nnet:\n  bindIp: 127.0.0.1\n  port: 27017\n"
		unit = "mongod.service"
		service = "User=panel-mongodb\nGroup=panel-mongodb\nExecStart=" + prefix + "/bin/mongod --config " + configPath + "\nTimeoutStopSec=120\n"
	case "memcached":
		if e = run(src, "sh", "./configure", "--prefix="+prefix); e != nil {
			return state, e
		}
		if e = run(src, "make", "-j2"); e != nil {
			return state, e
		}
		if e = run(src, "make", "install"); e != nil {
			return state, e
		}
		links["/usr/local/bin/memcached"] = prefix + "/bin/memcached"
		configPath = "/etc/ezpanel-apps/memcached.env"
		config = "OPTIONS=-m 64 -p 11211 -l 127.0.0.1 -U 0\n"
		unit = "memcached.service"
		service = "User=panel-memcached\nGroup=panel-memcached\nEnvironmentFile=" + configPath + "\nExecStart=" + prefix + "/bin/memcached $OPTIONS\n"
	case "nginx":
		if e = run(src, "sh", "./configure", "--prefix="+prefix, "--sbin-path="+prefix+"/sbin/nginx", "--conf-path=/etc/nginx/nginx.conf", "--pid-path=/run/nginx.pid", "--error-log-path=/var/log/nginx/error.log", "--http-log-path=/var/log/nginx/access.log", "--with-http_ssl_module", "--with-http_v2_module", "--with-http_realip_module", "--with-stream", "--with-stream_ssl_module"); e != nil {
			return state, e
		}
		if e = run(src, "make", "-j2"); e != nil {
			return state, e
		}
		// 不运行 make install，避免覆盖卸载后保留的 Nginx 配置。
		if e = os.MkdirAll(prefix+"/sbin", 0755); e != nil {
			return state, e
		}
		b, err := os.ReadFile(src + "/objs/nginx")
		if err != nil {
			return state, err
		}
		if e = os.WriteFile(prefix+"/sbin/nginx", b, 0755); e != nil {
			return state, e
		}
		if !updating {
			for _, name := range []string{"mime.types", "fastcgi_params", "uwsgi_params", "scgi_params"} {
				b, err := os.ReadFile(src + "/conf/" + name)
				if err != nil {
					return state, err
				}
				if e = writeInitial("/etc/nginx/"+name, string(b), 0644); e != nil {
					return state, e
				}
			}
			for _, dir := range []string{"/etc/nginx/conf.d", "/var/log/nginx", prefix + "/html", prefix + "/logs"} {
				if e = os.MkdirAll(dir, 0755); e != nil {
					return state, e
				}
			}
		}
		configPath = "/etc/nginx/nginx.conf"
		config = "worker_processes auto;\npid /run/nginx.pid;\nevents { worker_connections 1024; }\nhttp {\n include /etc/nginx/mime.types;\n default_type application/octet-stream;\n sendfile on;\n include /etc/nginx/conf.d/*.conf;\n}\n"
		links["/usr/local/sbin/nginx"] = prefix + "/sbin/nginx"
		unit = "nginx.service"
		service = "Type=forking\nPIDFile=/run/nginx.pid\nExecStartPre=" + prefix + "/sbin/nginx -t\nExecStart=" + prefix + "/sbin/nginx\nExecReload=" + prefix + "/sbin/nginx -s reload\n"
	case "redis":
		onLine("构建Redis核心程序与TLS支持，不调用扩展模块构建入口")
		if e = run(src, "make", "-C", "src", "-j2", "BUILD_TLS=yes"); e != nil {
			return state, e
		}
		if e = run(src, "make", "-C", "src", "PREFIX="+prefix, "BUILD_TLS=yes", "install"); e != nil {
			return state, e
		}
		links["/usr/local/bin/redis-server"] = prefix + "/bin/redis-server"
		links["/usr/local/bin/redis-cli"] = prefix + "/bin/redis-cli"
		configPath = "/etc/redis/redis.conf"
		config = "bind 127.0.0.1 ::1\nprotected-mode yes\nport 6379\ndaemonize no\ndir " + dataDir + "\nappendonly yes\n"
		unit = "redis-server.service"
		service = "User=panel-redis\nGroup=panel-redis\nExecStart=" + prefix + "/bin/redis-server " + configPath + "\n"
	case "mysql", "mysql-client", "mariadb":
		build := filepath.Join(work, "build")
		args := []string{"-S", src, "-B", build, "-DCMAKE_BUILD_TYPE=Release", "-DCMAKE_INSTALL_PREFIX=" + prefix, "-DWITH_SSL=system", "-DWITH_UNIT_TESTS=OFF", "-DCMAKE_POLICY_VERSION_MINIMUM=3.5"}
		if id != "mariadb" {
			args = append(args, "-DWITH_ROUTER=OFF", "-DWITH_NDB=OFF")
			if id == "mysql-client" {
				args = append(args, "-DWITHOUT_SERVER=ON")
			}
		} else {
			args = append(args, "-DPLUGIN_ROCKSDB=NO", "-DPLUGIN_COLUMNSTORE=NO", "-DINSTALL_SCRIPTDIR=bin", "-DINSTALL_SBINDIR=bin")
		}
		if e = run(work, "cmake", args...); e != nil {
			return state, e
		}
		if e = run(work, "cmake", "--build", build, "--parallel", "1"); e != nil {
			return state, e
		}
		if e = run(work, "cmake", "--install", build); e != nil {
			return state, e
		}
		for _, binary := range []string{"mysql", "mysqldump", "mariadb", "mariadb-dump"} {
			if _, err := os.Stat(prefix + "/bin/" + binary); err == nil {
				if _, err := m.LookPath(binary); err == nil {
					onLine("保留现有客户端入口：" + binary)
					continue
				}
				links["/usr/local/bin/"+binary] = prefix + "/bin/" + binary
			}
		}
		if id != "mysql-client" {
			binary := "mysqld"
			if id == "mariadb" {
				binary = "mariadbd"
			}
			links["/usr/local/sbin/"+binary] = prefix + "/bin/" + binary
			configPath = "/etc/ezpanel-apps/" + id + ".cnf"
			config = "[mysqld]\nbasedir=" + prefix + "\ndatadir=" + dataDir + "\ntmpdir=" + m.serviceTempDir(id) + "\nsocket=/run/mysqld/mysqld.sock\nbind-address=127.0.0.1\nport=3306\n[client]\nsocket=/run/mysqld/mysqld.sock\n"
			unit = id + ".service"
			service = "User=panel-mysql\nGroup=panel-mysql\nRuntimeDirectory=mysqld\nRuntimeDirectoryMode=0755\nExecStart=" + prefix + "/bin/" + binary + " --defaults-file=" + configPath + "\nTimeoutStartSec=600\n"
			if id == "mysql" {
				service = "Type=forking\nPIDFile=/run/mysqld/ezpanel.pid\n" + strings.Replace(service, " --defaults-file="+configPath+"\n", " --defaults-file="+configPath+" --daemonize --pid-file=/run/mysqld/ezpanel.pid\n", 1)
			}
		}
	case "docker":
		binDir := prefix + "/bin"
		if e = os.Rename(src, binDir); e != nil {
			return state, e
		}
		for _, binary := range []string{"docker", "dockerd"} {
			if _, err := os.Stat(binDir + "/" + binary); err == nil {
				links["/usr/local/bin/"+binary] = binDir + "/" + binary
			}
		}
		configPath = "/etc/docker/daemon.json"
		config = "{}\n"
		unit = "docker.service"
		service = "Environment=PATH=" + binDir + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\nExecStart=" + binDir + "/dockerd --config-file=" + configPath + "\nDelegate=yes\nKillMode=process\n"
	default:
		return state, ErrUnsupported
	}
	if e = filepath.WalkDir(prefix, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		mode := os.FileMode(0644)
		if entry.IsDir() {
			mode = 0755
		} else {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().Perm()&0111 != 0 {
				mode = 0755
			}
		}
		return os.Chmod(path, mode)
	}); e != nil {
		return state, e
	}
	service = strings.ReplaceAll(service, prefix, root+"/current")
	config = strings.ReplaceAll(config, "basedir="+prefix, "basedir="+root+"/current")
	for link, target := range links {
		links[link] = strings.Replace(target, prefix, root+"/current", 1)
	}
	if updating {
		result, err := m.updateBuilt(ctx, id, release, prefix, checksum, previous, choice, service, links, onLine)
		published = true
		return result, err
	}

	if configPath != "" {
		if e = writeInitial(configPath, config, 0640); e != nil {
			return state, e
		}
	}
	if id == "redis" || id == "mysql" || id == "mariadb" || id == "mongodb" || id == "memcached" {
		username := "panel-mysql"
		if id == "redis" || id == "mongodb" || id == "memcached" {
			username = "panel-" + id
		}
		if _, err := m.Run(ctx, 30*time.Second, nil, "/usr/bin/id", "-u", username); err != nil {
			if e = run(work, "useradd", "--system", "--user-group", "--no-create-home", "--shell", "/usr/sbin/nologin", username); e != nil {
				return state, e
			}
		}
		if e = os.MkdirAll(filepath.Dir(dataDir), 0755); e != nil {
			return state, e
		}
		if e = os.Chmod(filepath.Dir(dataDir), 0755); e != nil {
			return state, e
		}
		if e = os.MkdirAll(dataDir, 0750); e != nil {
			return state, e
		}
		if e = run(work, "chown", "-R", username+":"+username, dataDir); e != nil {
			return state, e
		}
		if e = run(work, "chown", username+":"+username, configPath); e != nil {
			return state, e
		}
		if id == "mysql" || id == "mariadb" || id == "mongodb" {
			if e = os.MkdirAll(m.serviceStateDir(id), 0700); e != nil {
				return state, e
			}
		}
		if id == "mysql" || id == "mariadb" {
			if e = os.MkdirAll(m.serviceTempDir(id), 0700); e != nil {
				return state, e
			}
			if e = run(work, "chown", username+":"+username, m.serviceTempDir(id)); e != nil {
				return state, e
			}
		}
		if id == "mongodb" {
			if e = os.WriteFile(m.versionFile(id), []byte(version), 0600); e != nil {
				return state, e
			}
		}
		if id == "mysql" || id == "mariadb" {
			if e = os.WriteFile(m.versionFile(id), []byte(version), 0600); e != nil {
				return state, e
			}
			if _, err := os.Stat(dataDir + "/mysql"); os.IsNotExist(err) {
				if id == "mariadb" {
					if e = run(work, prefix+"/bin/mariadb-install-db", "--defaults-file="+configPath, "--user="+username, "--basedir="+prefix, "--datadir="+dataDir, "--auth-root-authentication-method=socket"); e != nil {
						return state, e
					}
				} else {
					if e = run(work, prefix+"/bin/mysqld", "--defaults-file="+configPath, "--initialize-insecure", "--basedir="+prefix, "--user="+username); e != nil {
						return state, e
					}
					random := make([]byte, 24)
					if _, e = rand.Read(random); e != nil {
						return state, e
					}
					password := hex.EncodeToString(random)
					if e = os.WriteFile(filepath.Join(m.serviceStateDir(id), "root-password"), []byte(password), 0600); e != nil {
						return state, e
					}
					init := filepath.Join(m.serviceTempDir(id), "initialize.sql")
					if e = os.WriteFile(init, []byte("ALTER USER 'root'@'localhost' IDENTIFIED BY '"+password+"';\n"), 0600); e != nil {
						return state, e
					}
					if e = run(work, "chown", username+":"+username, init); e != nil {
						return state, e
					}
				}
			} else if err != nil {
				return state, err
			}
			// 首次启动失败后重试仍应用初始化 SQL，启动成功后移除。
			if _, err := os.Stat(filepath.Join(m.serviceTempDir(id), "initialize.sql")); err == nil && id == "mysql" {
				service = strings.Replace(service, " --defaults-file="+configPath, " --defaults-file="+configPath+" --init-file="+filepath.Join(m.serviceTempDir(id), "initialize.sql"), 1)
			} else if err != nil && !os.IsNotExist(err) {
				return state, err
			}
		}
	}
	record := sourceInstall{ID: id, DownloadSource: choice, Prefix: prefix, Version: version, URL: release.URL, SHA256: checksum, Links: links}
	b, e := json.MarshalIndent(record, "", "  ")
	if e != nil {
		return state, e
	}
	if e = m.selectRelease(id, prefix); e != nil {
		return state, e
	}
	if e = m.installAliases(id, prefix); e != nil {
		return state, e
	}
	if e = os.WriteFile(root+"/.panel-source.json", b, 0600); e != nil {
		return state, e
	}
	published = true
	for link, target := range links {
		if old, err := os.Readlink(link); err == nil && old == target {
			continue
		}
		if e = os.Symlink(target, link); e != nil {
			return state, fmt.Errorf("不能创建程序入口 %s（保留现有文件）: %w", link, e)
		}
	}
	if unit != "" {
		text := unitText(id, service)
		if e = os.WriteFile("/etc/systemd/system/"+unit, []byte(text), 0644); e != nil {
			return state, e
		}
		record.Unit = unit
		b, e = json.MarshalIndent(record, "", "  ")
		if e != nil {
			return state, e
		}
		if e = os.WriteFile(root+"/.panel-source.json", b, 0600); e != nil {
			return state, e
		}
	}
	if unit != "" {
		if e = m.ensureGuard(ctx, unit); e != nil {
			return state, e
		}
		if e = run(work, "systemctl", "enable", "--now", unit); e != nil {
			return state, e
		}
		if id == "mysql" && strings.Contains(service, " --init-file=") {
			unitPath := "/etc/systemd/system/" + unit
			b, e := os.ReadFile(unitPath)
			if e != nil {
				return state, e
			}
			text := strings.Replace(string(b), " --init-file="+filepath.Join(m.serviceTempDir(id), "initialize.sql"), "", 1)
			if e = os.WriteFile(unitPath, []byte(text), 0644); e != nil {
				return state, e
			}
			if e = run(work, "systemctl", "daemon-reload"); e != nil {
				return state, e
			}
			if e = os.Remove(filepath.Join(m.serviceTempDir(id), "initialize.sql")); e != nil {
				return state, e
			}
			onLine("MySQL 初始 root 密码保存在 " + filepath.Join(m.serviceStateDir(id), "root-password") + "，请通过终端查看")
		}
	}
	onLine("官方软件安装完成；配置、数据与安装来源已记录")
	actual, e := m.Status(ctx, id)
	if e == nil && actual.Running != nil && !*actual.Running {
		e = errors.New("安装完成但服务未运行，请查看应用日志")
	}
	return actual, e
}
func (m *Manager) uninstallSource(ctx context.Context, id string, log func(string)) (State, error) {
	record, e := m.sourceRecord(id)
	if e != nil {
		return State{}, e
	}
	dataDir := m.serviceDataDir(id)
	if m.upgradeBusy(ctx, id) {
		return State{}, errors.New("隔离升级进程仍在运行，不能卸载")
	}
	if record.Unit != "" {
		if _, e = m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "disable", "--now", record.Unit); e != nil {
			return State{}, e
		}
		if e = os.Remove("/etc/systemd/system/" + record.Unit); e != nil && !os.IsNotExist(e) {
			return State{}, e
		}
	}
	for link, target := range record.Links {
		current, e := os.Readlink(link)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			info, err := os.Lstat(link)
			if err != nil {
				return State{}, err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			return State{}, e
		}
		if current == target {
			if e = os.Remove(link); e != nil {
				return State{}, e
			}
		}
	}
	root := m.installRoot(id)
	// 数据、配置和首次安装凭据留在原目录，仅卸载面板记录的程序。
	e = os.RemoveAll(filepath.Join(root, "releases"))
	if e == nil {
		for _, name := range []string{"bin", "sbin", "lib", "lib64", "include", "share"} {
			path := filepath.Join(root, name)
			if target, err := os.Readlink(path); err == nil && target == "current/"+name {
				if e = os.Remove(path); e != nil {
					return State{}, e
				}
			}
		}
		for _, name := range []string{"current", ".panel-source.json"} {
			if err := os.Remove(filepath.Join(root, name)); err != nil && !os.IsNotExist(err) {
				return State{}, err
			}
		}
	}
	if e != nil {
		return State{}, e
	}
	if record.Unit != "" {
		if _, e = m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
			return State{}, e
		}
	}
	log("已移除本商店安装的程序与服务，保留配置、网站和数据目录：" + dataDir)
	return m.Status(ctx, id)
}
