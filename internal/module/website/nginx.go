package website

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"gorm.io/gorm"
)

const proxyMaps = `map $http_upgrade $panel_connection_upgrade {
    default upgrade;
    '' close;
}
map "$http_authorization:$http_cookie:$http_upgrade" $panel_proxy_cache_skip {
    "::" 0;
    default 1;
}
`
const rotation = siteLogRoot + "/*/*.log {\n    size 10M\n    rotate 5\n    missingok\n    notifempty\n    compress\n    delaycompress\n    copytruncate\n}\n"

type fileChange struct {
	content []byte
	mode    os.FileMode
	exists  bool
}

func (m *Module) certPaths(id string) (string, string) {
	dir := filepath.Join(m.cfg.DataDir(), "certs", id)
	return filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "private.key")
}
func (m *Module) confDir() string { return m.cfg.Layout().OpenRestyConfDir() }
func (m *Module) confPath(id string, enabled bool) string {
	p := filepath.Join(m.confDir(), "panel-"+id+".conf")
	if !enabled {
		p += ".disabled"
	}
	return p
}

type siteServer struct {
	Port  int
	SSL   bool
	Hosts []string
}

// 已保存的绑定格式在API入口校验；此处按端口分组，避免域名跨端口串用。
func siteServers(row store.Website) []siteServer {
	ports := map[int][]string{}
	tlsHosts := map[string]bool{}
	for _, value := range websiteDomains(row) {
		host, port := splitBinding(value)
		if !row.SSLEnabled || port != 443 {
			ports[port] = append(ports[port], host)
		}
		if row.SSLEnabled {
			tlsHosts[host] = true
		}
	}
	if row.SSLEnabled {
		for host := range tlsHosts {
			ports[443] = append(ports[443], host)
		}
		sort.Strings(ports[443])
	}
	numbers := make([]int, 0, len(ports))
	for port := range ports {
		numbers = append(numbers, port)
	}
	sort.Ints(numbers)
	servers := make([]siteServer, 0, len(numbers))
	for _, port := range numbers {
		servers = append(servers, siteServer{port, row.SSLEnabled && port == 443, ports[port]})
	}
	return servers
}
func (m *Module) render(row store.Website) string {
	return m.renderWithOptions(row, row.Enabled && row.ID == m.defaultSiteID())
}

func (m *Module) renderWithOptions(row store.Website, isDefaultSite bool) string {
	var b strings.Builder
	b.WriteString("# 由 ezpanel 管理，请通过面板修改。\n")
	writeProxyZones(&b, row)
	rootProxy := slices.ContainsFunc(row.ProxyRoutes, func(route store.ProxyRoute) bool {
		return route.Enabled && route.Path == "/"
	})
	tlsStr := m.tlsProtocolsString()
	for _, server := range siteServers(row) {
		defaultFlag := ""
		if isDefaultSite && (server.Port == 80 || (server.Port == 443 && server.SSL)) {
			defaultFlag = " default_server"
		}
		fmt.Fprintf(&b, "server {\n    listen %d", server.Port)
		if server.SSL {
			b.WriteString(" ssl http2")
		}
		b.WriteString(defaultFlag)
		if slices.ContainsFunc(server.Hosts, func(host string) bool { return strings.HasPrefix(host, "[") }) {
			b.WriteString(";\n    listen [::]:" + strconv.Itoa(server.Port))
			if server.SSL {
				b.WriteString(" ssl http2")
			}
			b.WriteString(defaultFlag)
		}
		fmt.Fprintf(&b, ";\n    server_name %s;\n", strings.Join(server.Hosts, " "))
		b.WriteString(m.acmeLocation())
		fmt.Fprintf(&b, "    access_log %q;\n    error_log %q;\n", siteLogPath(siteLogDirectory(row), "access"), siteLogPath(siteLogDirectory(row), "error"))
		if server.SSL {
			cert, key := m.certPaths(row.ID)
			fmt.Fprintf(&b, "    ssl_certificate \"%s\";\n    ssl_certificate_key \"%s\";\n    ssl_protocols %s;\n", cert, key, tlsStr)
		} else if row.SSLEnabled && row.ForceHTTPS {
			b.WriteString("    location / { return 301 https://$host$request_uri; }\n}\n")
			continue
		}
		if row.RedirectEnabled {
			target := row.RedirectTarget
			if row.RedirectKeepURI {
				target = strings.TrimRight(target, "/") + "$request_uri"
			}
			fmt.Fprintf(&b, "    location / { return %d \"%s\"; }\n}\n", row.RedirectCode, target)
			continue
		}
		fmt.Fprintf(&b, "    set $ezwaf_site_id %q;\n", row.ID)
		if row.Type == "static" {
			fmt.Fprintf(&b, "    root %s;\n", row.Root)
			if row.PHPVersion == "" {
				b.WriteString("    index index.html index.htm;\n    location ~* \\.php(?:/|$) { return 404; }\n")
			} else {
				baseDir := strings.TrimRight(row.Root, "/")
				b.WriteString("    index index.php index.html index.htm;\n")
				fmt.Fprintf(&b, "    location ~ \\.php$ {\n        try_files $uri =404;\n        include /usr/local/openresty/nginx/conf/fastcgi_params;\n        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;\n        fastcgi_param HTTP_PROXY \"\";\n        fastcgi_param PHP_ADMIN_VALUE \"open_basedir=%s/:/tmp/:/proc/:/etc/ssl/certs/\";\n        fastcgi_pass unix:%s;\n    }\n", baseDir, pkgmgr.PHPSocket(m.cfg.Layout(), row.PHPVersion))
				b.WriteString("    location ~* \\.php(?:/|$) { return 404; }\n")
			}
			if !rootProxy {
				fmt.Fprintf(&b, "    error_page 404 /_ezpanel_404.html;\n    location = /_ezpanel_404.html {\n        internal;\n        root %s;\n        try_files /404.html =404;\n    }\n", m.pagesDir())
				defaultRoot := "try_files $uri $uri/ =404;"
				if row.PHPVersion != "" {
					defaultRoot = "try_files $uri $uri/ /index.php?$query_string;"
				}
				rule := row.RewriteRule
				switch {
				case rule == "":
					fmt.Fprintf(&b, "    location / {\n        %s\n    }\n", defaultRoot)
				case hasRootLocation(rule):
					fmt.Fprintf(&b, "    %s\n", rule)
				case hasAnyLocation(rule):
					fmt.Fprintf(&b, "    %s\n    location / {\n        %s\n    }\n", rule, defaultRoot)
				default:
					fmt.Fprintf(&b, "    location / {\n        %s\n    }\n", rule)
				}
			}
		} else if row.RuntimeVersion != "" && row.ProxyTarget != "" {
			writeProxyRoute(&b, row.ID, store.ProxyRoute{Path: "/", Target: row.ProxyTarget, Host: "$host"}, row.RewriteRule)
		} else if !rootProxy {
			b.WriteString("    location / { return 404; }\n")
		}
		for _, route := range row.ProxyRoutes {
			if route.Enabled {
				writeProxyRoute(&b, row.ID, route, "")
			}
		}
		b.WriteString("}\n")
	}
	return b.String()
}
func atomicWrite(path string, content []byte, mode os.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".panel-*.tmp")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(content)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func applyFile(path string, value fileChange) error {
	if !value.exists {
		e := os.Remove(path)
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	return atomicWrite(path, value.content, value.mode)
}

// change 只在命令成功后短事务提交数据库；任何中间失败均恢复旧文件。
func (m *Module) change(ctx context.Context, row store.Website, remove bool, cert, key *string, commit func() error) error {
	return m.packages.ApplicationChange("openresty", func() error {
		state, e := m.packages.Status(ctx, "openresty")
		if e != nil {
			return e
		}
		if !state.Installed || !state.Manageable {
			return pkgmgr.ErrUnsupported
		}
		if e = m.inspectConfiguration(row, remove); e != nil {
			return e
		}
		var previous store.Website
		if err := m.db.Select("name,log_directory,enabled,domains,ssl_enabled").First(&previous, "id = ?", row.ID).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		oldLogDir, newLogDir := filepath.Join(siteLogRoot, siteLogDirectory(previous)), filepath.Join(siteLogRoot, siteLogDirectory(row))
		movedLogs := false
		if err := m.ensureDefaultPages(); err != nil {
			return errors.New("默认页面目录初始化失败")
		}
		clearDefaultSite := (remove || !row.Enabled) && row.ID == m.defaultSiteID()
		var defaultSite *store.Website
		if !clearDefaultSite {
			if row.Enabled && !remove && row.ID == m.defaultSiteID() {
				copyRow := row
				defaultSite = &copyRow
			} else {
				defaultSite = m.resolveDefaultSite(nil, row.ID)
			}
		}
		changes := map[string]fileChange{
			m.confPath(row.ID, true): {}, m.confPath(row.ID, false): {},
			m.defaultServerConfPath():                          {[]byte(m.renderDefaultServer(defaultSite)), 0644, true},
			filepath.Join(m.confDir(), "panel-websocket.conf"): {[]byte(proxyMaps), 0644, true},
			"/etc/logrotate.d/panel-nginx":                     {[]byte(rotation), 0644, true},
		}
		if !remove {
			if row.Enabled {
				isDef := defaultSite != nil && defaultSite.ID == row.ID
				changes[m.confPath(row.ID, true)] = fileChange{[]byte(m.renderWithOptions(row, isDef)), 0640, true}
			} else {
				changes[m.confPath(row.ID, true)] = fileChange{[]byte(m.renderStopped(row)), 0640, true}
			}
		}
		certPath, keyPath := m.certPaths(row.ID)
		if cert != nil {
			changes[certPath] = fileChange{[]byte(*cert), 0600, true}
			changes[keyPath] = fileChange{[]byte(*key), 0600, true}
		} else if !row.SSLEnabled || remove {
			changes[certPath] = fileChange{}
			changes[keyPath] = fileChange{}
		}
		paths := make([]string, 0, len(changes))
		old := make(map[string]fileChange, len(changes))
		for path := range changes {
			paths = append(paths, path)
			info, e := os.Lstat(path)
			if e == nil {
				if !info.Mode().IsRegular() {
					return errors.New("面板配置路径不是普通文件，需人工检查")
				}
				data, e := os.ReadFile(path)
				if e != nil {
					return errors.New("旧配置快照读取失败")
				}
				old[path] = fileChange{data, info.Mode().Perm(), true}
			} else if os.IsNotExist(e) {
				old[path] = fileChange{}
			} else {
				return errors.New("旧配置快照读取失败")
			}
		}
		sort.Strings(paths)
		// 安装目录来自可信配置，但仍用 OpenResty 引号安全的路径，避免模板受配置值影响。
		if strings.ContainsAny(certPath, "\"\\\r\n$") {
			return errors.New("面板证书目录包含 OpenResty 不支持的字符")
		}
		if cert != nil {
			if e = os.MkdirAll(filepath.Dir(certPath), 0700); e != nil {
				return errors.New("证书目录创建失败")
			}
		}
		if e = os.MkdirAll("/etc/logrotate.d", 0755); e != nil {
			return errors.New("日志轮转目录不可写")
		}
		running := state.Running != nil && *state.Running
		restore := func(cause error) error {
			failed := false
			if movedLogs && os.Rename(newLogDir, oldLogDir) != nil {
				failed = true
			}
			for _, p := range paths {
				if applyFile(p, old[p]) != nil {
					failed = true
				}
			}
			// 取消请求后仍给恢复旧配置独立的短时窗口。
			recovery, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if e := m.openRestyTest(recovery); e != nil {
				failed = true
			} else if running {
				if e = m.reload(recovery); e != nil {
					failed = true
				}
			}
			if !old[certPath].exists && !old[keyPath].exists {
				_ = os.Remove(filepath.Dir(certPath))
			}
			if failed {
				return errors.New("配置恢复失败，需人工检查；" + cause.Error())
			}
			return fmt.Errorf("变更失败，已恢复旧配置：%w", cause)
		}
		if !remove && previous.Name != "" && siteLogDirectory(previous) != siteLogDirectory(row) {
			if _, err := os.Lstat(newLogDir); err == nil {
				return errors.New("新网站名称对应的日志目录已存在，请更换名称或先整理日志目录")
			} else if !os.IsNotExist(err) {
				return err
			}
			if _, err := os.Lstat(oldLogDir); err == nil {
				if e = os.Rename(oldLogDir, newLogDir); e != nil {
					return e
				}
				movedLogs = true
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		if e = ensureSiteLogDir(siteLogDirectory(row)); e != nil {
			return restore(errors.New("站点日志目录不可写"))
		}
		for _, p := range paths {
			if e = applyFile(p, changes[p]); e != nil {
				return restore(errors.New("配置或证书写入失败，请检查权限和磁盘空间"))
			}
		}
		if e = m.openRestyTest(ctx); e != nil {
			return restore(e)
		}
		if running {
			if e = m.reload(ctx); e != nil {
				return restore(e)
			}
		}
		newPorts := []int{}
		if !remove && (row.Enabled || previous.Name == "") {
			current := map[int]bool{}
			if previous.Enabled {
				for _, server := range siteServers(previous) {
					current[server.Port] = true
				}
			}
			for _, server := range siteServers(row) {
				if !current[server.Port] {
					newPorts = append(newPorts, server.Port)
				}
			}
		}
		e = m.firewall.WithWebsitePorts(ctx, newPorts, func() error {
			if err := commit(); err != nil {
				return errors.New("数据库提交失败")
			}
			if clearDefaultSite {
				_ = m.db.Save(&store.Setting{Key: settingDefaultSiteID, Value: ""}).Error
			}
			return nil
		})
		if e != nil {
			return restore(e)
		}
		if !row.SSLEnabled || remove {
			_ = os.Remove(filepath.Dir(certPath))
		}
		return nil
	})
}
func (m *Module) openRestyTest(ctx context.Context) error {
	result, e := m.packages.OpenRestyCommand(ctx, "-t")
	if e != nil {
		return errors.New("OpenResty 配置检查失败：" + response.Summary(strings.TrimSpace(result.Stderr+" "+result.Stdout), m.cfg.InstallDir))
	}
	output := strings.ToLower(result.Stderr + " " + result.Stdout)
	if strings.Contains(output, "conflicting server name") {
		return errors.New("OpenResty 检测到域名配置冲突，请人工处理非面板配置")
	}
	return nil
}
func (m *Module) reload(ctx context.Context) error {
	result, e := m.packages.OpenRestyCommand(ctx, "-s", "reload")
	if e != nil {
		return errors.New("OpenResty 重载失败：" + response.Summary(strings.TrimSpace(result.Stderr+" "+result.Stdout), m.cfg.InstallDir))
	}
	return nil
}

var configToken = regexp.MustCompile(`#[^\n]*|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[{};]|[^\s{};#]+`)

// 读取实际 include 链，只接受 http 级 conf.d 包含；不修改主配置或接管现有站点。
func (m *Module) inspectConfiguration(row store.Website, remove bool) error {
	requested := map[int]bool{}
	if !remove && row.Enabled {
		for _, server := range siteServers(row) {
			requested[server.Port] = server.SSL
		}
	}
	var rows []store.Website
	if e := m.db.Find(&rows).Error; e != nil {
		return errStorage
	}
	owned := map[string]bool{
		m.confPath(row.ID, true):  true,
		m.defaultServerConfPath(): true,
	}
	for _, site := range rows {
		owned[m.confPath(site.ID, true)] = true
	}
	included := false
	seen := map[string]bool{}
	var scan func(string, []string) error
	scan = func(path string, parents []string) error {
		if seen[path] {
			return nil
		}
		seen[path] = true
		data, e := os.ReadFile(path)
		if e != nil {
			return errors.New("无法读取 OpenResty 主配置或 include 文件")
		}
		stack := append([]string{}, parents...)
		directive := []string{}
		for _, token := range configToken.FindAllString(string(data), -1) {
			if strings.HasPrefix(token, "#") {
				continue
			}
			switch token {
			case "{":
				if len(directive) > 0 {
					stack = append(stack, directive[0])
				} else {
					stack = append(stack, "")
				}
				directive = nil
			case "}":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				directive = nil
			case ";":
				if len(directive) > 1 && directive[0] == "listen" && !owned[path] && len(stack) > 0 && stack[len(stack)-1] == "server" {
					address := strings.Trim(directive[1], "\"'")
					tls, datagram := false, false
					for _, option := range directive[2:] {
						switch strings.Trim(option, "\"'") {
						case "ssl":
							tls = true
						case "udp", "quic":
							datagram = true
						}
					}
					if !strings.HasPrefix(address, "unix:") && !datagram {
						port := 80
						if colon := strings.LastIndex(address, ":"); colon >= 0 {
							if number, e := strconv.Atoi(address[colon+1:]); e == nil {
								port = number
							}
						} else if number, e := strconv.Atoi(address); e == nil {
							port = number
						}
						if ssl, exists := requested[port]; exists && (!slices.Contains(stack, "http") || ssl != tls) {
							return fmt.Errorf("端口%d在非面板OpenResty配置中的监听协议与站点不一致，请先调整绑定", port)
						}
					}
				}
				if len(directive) > 1 && directive[0] == "include" {
					pattern := strings.Trim(directive[1], "\"'")
					if !filepath.IsAbs(pattern) {
						pattern = filepath.Join(m.cfg.Layout().OpenRestyDir(), pattern)
					}
					if filepath.Clean(pattern) == m.confDir()+"/*.conf" && len(stack) > 0 && stack[len(stack)-1] == "http" {
						included = true
					}
					matches, e := filepath.Glob(pattern)
					if e != nil {
						return errors.New("OpenResty include 格式无效")
					}
					if len(matches) == 0 && !strings.ContainsAny(pattern, "*?[") && !strings.HasPrefix(pattern, "/usr/local/openresty/") {
						return errors.New("OpenResty include 文件不存在")
					}
					for _, file := range matches {
						if e = scan(file, stack); e != nil {
							return e
						}
					}
				}
				directive = nil
			default:
				directive = append(directive, token)
			}
		}
		return nil
	}
	if e := scan(m.cfg.Layout().OpenRestyConfig(), nil); e != nil {
		return e
	}
	if !included {
		return errors.New("OpenResty 主配置未在 http 块包含 应用目录的 conf.d/*.conf，请先人工配置")
	}
	return nil
}

// CheckEnvironment 安装任务复用相同 include 检查；不修改用户的主配置。
func (m *Module) CheckEnvironment() error {
	return m.inspectConfiguration(store.Website{}, true)
}
