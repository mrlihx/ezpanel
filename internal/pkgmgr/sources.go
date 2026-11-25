package pkgmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const softwareUserAgent = "ezpanel-software/1.0"

type Release struct {
	Version           string `json:"version"`
	Source            string `json:"source"`
	Method            string `json:"method"`
	URL               string `json:"url"`
	SHA256            string `json:"sha256,omitempty"`
	Platform          string `json:"platform,omitempty"`
	UnavailableReason string `json:"unavailableReason,omitempty"`
}
type versionCache struct {
	List     []Release `json:"list"`
	SyncedAt time.Time `json:"syncedAt"`
	Notice   string    `json:"notice"`
}
type repository struct {
	key, label, index, base string
	mysqlTags               bool
}

type SourceSelection struct {
	Source string `json:"source"`
	Mirror string `json:"mirror,omitempty"`
}
type SourceOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

func SourceOptions(id string) []SourceOption {
	rows := []SourceOption{{"official", "官方源"}}
	if isHostTool(id) {
		return []SourceOption{{"official", "系统软件源"}}
	}
	if UsesDocker(id) {
		return []SourceOption{{"official", "Docker Hub（引擎默认源）"}, {"custom", "镜像加速地址"}, {"registry", "指定镜像仓库"}}
	}
	if id == "phpmyadmin" {
		return rows
	}
	switch id {
	case "nginx", "redis":
		rows = append(rows, SourceOption{"huawei", "华为云镜像"})
	case "mariadb", "docker":
		rows = append(rows, SourceOption{"aliyun", "阿里云镜像"})
	}
	return append(rows, SourceOption{"custom", "自定义下载目录"})
}
func (m *Manager) InstalledSource(id string) SourceSelection {
	if record, e := m.sourceRecord(id); e == nil && record.DownloadSource.Source != "" {
		return record.DownloadSource
	}
	return SourceSelection{Source: "official"}
}

// 下载源在请求边界校验一次，版本查询与后台安装共用同一份选择。
func (m *Manager) SelectSource(id string, in SourceSelection) (SourceSelection, error) {
	if in.Source == "" {
		in = m.InstalledSource(id)
	}
	found := false
	for _, row := range SourceOptions(id) {
		found = found || row.Value == in.Source
	}
	if !found {
		return in, errors.New("此软件不支持所选下载源")
	}
	if in.Source == "registry" {
		in.Mirror = strings.TrimPrefix(strings.TrimSpace(in.Mirror), "http://")
		if !strings.Contains(in.Mirror, "://") {
			in.Mirror = "https://" + in.Mirror
		}
	} else if in.Source != "custom" {
		in.Mirror = ""
		return in, nil
	}
	if UsesDocker(id) {
		mirror, err := dockerMirror(in.Mirror)
		in.Mirror = mirror
		return in, err
	}
	in.Mirror = strings.TrimRight(strings.TrimSpace(in.Mirror), "/")
	u, e := url.Parse(in.Mirror)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return in, errors.New("自定义下载源须为无账号和查询参数的HTTPS目录")
	}
	return in, nil
}

func (m *Manager) repository(id string, selected ...SourceSelection) repository {
	choice := m.InstalledSource(id)
	if len(selected) != 0 {
		choice = selected[0]
	}
	source := choice.Source
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	r := repository{key: source}
	switch id {
	case "phpmyadmin":
		r.label = "phpMyAdmin官方源"
		r.index = "https://www.phpmyadmin.net/home_page/version.json"
	case "mongodb":
		r.label = "MongoDB官方源"
		r.index = "https://downloads.mongodb.org/current.json"
	case "memcached":
		r.label = "Memcached官方源"
		r.index = "https://memcached.org/downloads"
		r.base = "https://memcached.org/files/"
	case "nginx":
		r.label = "Nginx官方源"
		r.index = "https://nginx.org/download/"
		if source == "huawei" {
			r.label = "华为云Nginx镜像"
			r.index = "https://mirrors.huaweicloud.com/repository/toolkit/nginx/"
		}
	case "redis":
		r.label = "Redis官方源"
		r.index = "https://download.redis.io/releases/"
		if source == "huawei" {
			r.label = "华为云Redis镜像"
			r.index = "https://mirrors.huaweicloud.com/redis/"
		}
	case "mariadb":
		r.label = "MariaDB官方源"
		r.index = "https://archive.mariadb.org/"
		if source == "aliyun" {
			r.label = "阿里云MariaDB镜像"
			r.index = "https://mirrors.aliyun.com/mariadb/"
		}
	case "mysql", "mysql-client":
		r.label = "MySQL官方源"
		r.index = "https://api.github.com/repos/mysql/mysql-server/tags?per_page=100"
		r.base = "https://dev.mysql.com/get/Downloads/MySQL-8.4/"
		r.mysqlTags = true

	case "docker":
		r.label = "Docker官方源"
		if arch != "" {
			r.index = "https://download.docker.com/linux/static/stable/" + arch + "/"
			if source == "aliyun" {
				r.label = "阿里云Docker镜像"
				r.index = "https://mirrors.aliyun.com/docker-ce/linux/static/stable/" + arch + "/"
			}
		}
	}
	if branch := phpBranch(id); branch != "" {
		r.label = "PHP官方源"
		r.index = "https://www.php.net/releases/?json&version=" + branch
		r.base = "https://www.php.net/distributions/"
	}
	if source == "custom" {
		r.label = "自定义下载源"
		r.index = choice.Mirror + "/"
		r.base = r.index
		r.mysqlTags = false
		hash := sha256.Sum256([]byte(r.index))
		r.key = "custom-" + hex.EncodeToString(hash[:6])
	}
	if r.base == "" {
		r.base = r.index
	}
	return r
}
func (m *Manager) fileURL(address string) string {
	u, e := url.Parse(address)
	if e != nil {
		return address
	}
	prefix := m.cfg.Current().GitHubProxy
	if prefix != "" && (u.Hostname() == "github.com" || u.Hostname() == "raw.githubusercontent.com") {
		return strings.TrimRight(prefix, "/") + "/" + address
	}
	return address
}
func (m *Manager) sourceRequest(ctx context.Context, address string, limit int64, downloadTimeout ...time.Duration) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", m.fileURL(address), nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", softwareUserAgent)
	timeout := 15 * time.Second
	if len(downloadTimeout) > 0 {
		timeout = downloadTimeout[0]
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || r.URL.Scheme != "https" {
			return errors.New("软件下载仅允许HTTPS重定向")
		}
		return nil
	}}
	res, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("下载源返回HTTP %d", res.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if e == nil && int64(len(b)) > limit {
		e = errors.New("下载源目录超过大小限制")
	}
	return b, e
}
func series(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}
func newer(a, b string) bool {
	x, y := strings.Split(a, "."), strings.Split(b, ".")
	if len(x) == 2 {
		x = append(x, "0")
	}
	if len(y) == 2 {
		y = append(y, "0")
	}
	if len(x) != 3 || len(y) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		left, _ := strconv.Atoi(x[i])
		right, _ := strconv.Atoi(y[i])
		if left != right {
			return left > right
		}
	}
	return false
}
func (m *Manager) cachePath(id string, r repository) string {
	if m.cfg.InstallDir == "" {
		return ""
	}
	return filepath.Join(m.cfg.DataDir(), "source-catalog", id+"-"+r.key+".json")
}
func (m *Manager) VersionInfo(id string, selected ...SourceSelection) versionCache {
	if UsesDocker(id) && !IsProjectRuntime(id) {
		return m.containerVersionInfo(id)
	}
	r := m.repository(id, selected...)
	if isHostTool(id) {
		r.key = "system"
	}
	m.sourcesMu.Lock()
	defer m.sourcesMu.Unlock()
	return m.sourceCache[id+":"+r.key]
}
func (m *Manager) Releases(ctx context.Context, id string, refresh bool, selected ...SourceSelection) ([]Release, error) {
	if isHostTool(id) {
		sys, err := m.Detect()
		if err != nil {
			return nil, err
		}
		if refresh && sys.Manager == "apt-get" {
			m.packageMu.Lock()
			if time.Since(m.lastAptUpdate) >= time.Minute {
				_, err = m.Run(ctx, time.Minute, nil, sys.Command, "update")
				if err == nil {
					m.lastAptUpdate = time.Now()
				}
			}
			m.packageMu.Unlock()
			if err != nil {
				return nil, fmt.Errorf("刷新系统软件源失败：%w", err)
			}
		}
		a, _ := app(id)
		version, err := m.hostToolVersion(ctx, sys, a.packagesFor(sys)[0])
		if err != nil {
			return nil, err
		}
		rows := []Release{{Version: version, Source: "系统软件源", Method: "package"}}
		m.sourcesMu.Lock()
		m.sourceCache[id+":system"] = versionCache{List: rows, SyncedAt: time.Now()}
		m.sourcesMu.Unlock()
		return rows, nil
	}
	if UsesDocker(id) {
		rows, err := m.publishedContainerReleases(ctx, id, refresh)
		if err != nil {
			return nil, err
		}
		if id == "mysql" && runtime.GOARCH != "amd64" {
			status, _ := m.ReadFile("/proc/sys/fs/binfmt_misc/qemu-x86_64")
			if !strings.HasPrefix(string(status), "enabled\n") {
				for i := range rows {
					if rows[i].Platform == "linux/amd64" {
						rows[i].UnavailableReason = "需要宿主机启用 x86-64 仿真"
					}
				}
			}
		}
		if len(selected) != 0 && selected[0].Mirror != "" {
			for i := range rows {
				rows[i].Source = strings.TrimPrefix(selected[0].Mirror, "https://")
				if !strings.Contains(rows[i].URL, "/") {
					rows[i].URL = "library/" + rows[i].URL
				}
				rows[i].URL = rows[i].Source + "/" + rows[i].URL
			}
		}
		return rows, nil
	}
	r := m.repository(id, selected...)
	if id == "phpmyadmin" && r.key != "official" {
		return nil, errors.New("phpMyAdmin仅支持官方源")
	}
	if r.index == "" {
		return nil, ErrUnsupported
	}
	key := id + ":" + r.key
	m.sourcesMu.Lock()
	cached := m.sourceCache[key]
	if len(cached.List) == 0 {
		if path := m.cachePath(id, r); path != "" {
			b, e := os.ReadFile(path)
			if e == nil {
				_ = json.Unmarshal(b, &cached)
			}
		}
		m.sourceCache[key] = cached
	}
	m.sourcesMu.Unlock()
	if !refresh && len(cached.List) > 0 && time.Since(cached.SyncedAt) < 30*time.Minute {
		return cached.List, nil
	}
	b, e := m.sourceRequest(ctx, r.index, 8<<20)
	text := string(b)
	if e == nil && r.mysqlTags {
		var tags []struct {
			Name string `json:"name"`
		}
		e = json.Unmarshal(b, &tags)
		if e == nil {
			names := []string{}
			for _, t := range tags {
				names = append(names, t.Name)
			}
			text = strings.Join(names, "\n")
		}
	}
	if r.mysqlTags && (e != nil || !strings.Contains(text, "mysql-8.4.")) {
		var fallback string
		fallback, e = m.mysqlFileVersion(ctx)
		if e == nil {
			text = "mysql-" + fallback
			r.mysqlTags = false
			r.base = "https://github.com/mysql/mysql-server/archive/refs/tags/"
		}
	}
	rows := []Release{}
	phpHashes := map[string]string{}
	phpSeries := phpBranch(id)
	if e == nil && phpSeries != "" && r.key == "official" {
		var data struct {
			Version string `json:"version"`
			Museum  bool   `json:"museum"`
			Source  []struct {
				Filename string `json:"filename"`
				SHA256   string `json:"sha256"`
			} `json:"source"`
		}
		e = json.Unmarshal(b, &data)
		if e == nil {
			if series(data.Version) != phpSeries || !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(data.Version) {
				e = errors.New("PHP上游未返回所选分支的正式版本")
			} else {
				text = "php-" + data.Version + ".tar.gz"
				for _, source := range data.Source {
					if source.Filename == text {
						phpHashes[data.Version] = source.SHA256
					}
				}
				if data.Museum {
					r.base = "https://museum.php.net/php" + phpSeries[:1] + "/"
				}
			}
		}
	}
	if e == nil && id == "phpmyadmin" {
		rows, e = phpMyAdminReleases(b)
	}
	if e == nil && id != "phpmyadmin" {
		name := id
		if id == "mysql-client" {
			name = "mysql"
		}
		if phpSeries != "" {
			name = "php"
		}
		pattern := regexp.MustCompile(regexp.QuoteMeta(name+"-") + `([0-9]+\.[0-9]+\.[0-9]+)(?:\.tar\.gz|\.tgz|/|["\s]|$)`)
		latest := map[string]Release{}
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			v := match[1]
			if phpSeries != "" && series(v) != phpSeries {
				continue
			}
			parts := strings.Split(v, ".")
			major, _ := strconv.Atoi(parts[0])
			minor, _ := strconv.Atoi(parts[1])
			if (id == "nginx" && (major != 1 || minor < 26)) || (id == "redis" && (major < 7 || major == 7 && minor < 2)) || (id == "mariadb" && (major < 10 || major == 10 && minor < 11)) || ((id == "mysql" || id == "mysql-client") && (major != 8 || minor != 4)) || (id == "docker" && major < 25) {
				continue
			}
			row := Release{Version: v, Source: r.label, Method: "compile", SHA256: phpHashes[v]}
			if phpSeries != "" {
				row.URL = r.base + "php-" + v + ".tar.gz"
			}
			switch id {
			case "nginx", "redis", "mysql", "mysql-client", "memcached":
				row.URL = r.base + name + "-" + v + ".tar.gz"
			case "mongodb":
				row.URL = r.base + "mongodb-" + v + ".tgz"
				row.Method = "binary"
			case "mariadb":
				row.URL = r.base + "mariadb-" + v + "/source/mariadb-" + v + ".tar.gz"
			case "docker":
				row.URL = r.base + "docker-" + v + ".tgz"
				row.Method = "binary"
			}
			branch := series(v)
			if previous, ok := latest[branch]; !ok || newer(v, previous.Version) {
				latest[branch] = row
			}
		}
		for _, row := range latest {
			rows = append(rows, row)
		}
		sort.Slice(rows, func(i, j int) bool { return newer(rows[i].Version, rows[j].Version) })
		if len(rows) == 0 {
			e = errors.New("当前下载源没有本安装配方支持的正式版本")
		}
	}
	if e != nil {
		if len(cached.List) == 0 {
			return nil, e
		}
		cached.Notice = "更新版本目录失败，显示上次同步结果：" + e.Error()
		m.sourcesMu.Lock()
		m.sourceCache[key] = cached
		m.sourcesMu.Unlock()
		return cached.List, nil
	}
	cached = versionCache{List: rows, SyncedAt: time.Now().UTC()}
	path := m.cachePath(id, r)
	if path != "" {
		if e = os.MkdirAll(filepath.Dir(path), 0700); e == nil {
			var out []byte
			out, e = json.Marshal(cached)
			if e == nil {
				var f *os.File
				f, e = os.CreateTemp(filepath.Dir(path), ".catalog-")
				if e == nil {
					_, e = f.Write(out)
					closed := f.Close()
					if e == nil {
						e = closed
					}
					if e == nil {
						e = os.Rename(f.Name(), path)
					}
					os.Remove(f.Name())
				}
			}
		}
		if e != nil {
			return nil, fmt.Errorf("版本目录缓存保存失败: %w", e)
		}
	}
	m.sourcesMu.Lock()
	m.sourceCache[key] = cached
	m.sourcesMu.Unlock()
	return rows, nil
}

// 只读取所选文件的发布方校验清单；缺少清单时明确记录HTTPS来源模式。
func (m *Manager) releaseChecksum(ctx context.Context, r Release) string {
	if r.SHA256 != "" {
		return r.SHA256
	}
	u, _ := url.Parse(r.URL)
	base := r.URL[:strings.LastIndex(r.URL, "/")+1]
	filename := filepath.Base(u.Path)
	for _, address := range []string{r.URL + ".sha256", base + "sha256sums.txt", base + "sha256sum.txt"} {
		b, e := m.sourceRequest(ctx, address, 2<<20)
		if e != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(fields[0]) && (len(fields) == 1 || filepath.Base(strings.TrimPrefix(fields[len(fields)-1], "*")) == filename) {
				return strings.ToLower(fields[0])
			}
		}
	}
	return ""
}

// API不可达时，通过可加速的官方版本文件寻找已存在的8.4发布标签。
func (m *Manager) mysqlFileVersion(ctx context.Context) (string, error) {
	b, e := m.sourceRequest(ctx, "https://raw.githubusercontent.com/mysql/mysql-server/8.4/MYSQL_VERSION", 64<<10)
	if e != nil {
		return "", e
	}
	patch := -1
	for _, line := range strings.Split(string(b), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "MYSQL_VERSION_PATCH" {
			patch, e = strconv.Atoi(strings.TrimSpace(value))
			if e != nil {
				return "", errors.New("官方MySQL补丁版本格式无效")
			}
		}
	}
	if patch < 0 {
		return "", errors.New("无法从官方版本文件解析MySQL版本")
	}
	for tries := 0; tries < 5 && patch >= 0; tries++ {
		v := "8.4." + strconv.Itoa(patch)
		address := "https://github.com/mysql/mysql-server/archive/refs/tags/mysql-" + v + ".tar.gz"
		req, e := http.NewRequestWithContext(ctx, "HEAD", m.fileURL(address), nil)
		if e != nil {
			return "", e
		}
		req.Header.Set("User-Agent", softwareUserAgent)
		client := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) > 5 || r.URL.Scheme != "https" {
				return errors.New("GitHub下载重定向无效")
			}
			return nil
		}}
		res, e := client.Do(req)
		if e == nil {
			valid := res.StatusCode == 200 && !strings.Contains(res.Header.Get("Content-Type"), "text/html")
			res.Body.Close()
			if valid {
				return v, nil
			}
		}
		patch--
	}
	return "", errors.New("官方MySQL API与可加速的发布文件均不可用，请配置可访问的下载源或GitHub文件加速地址")
}
