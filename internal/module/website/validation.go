package website

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ezpanel/internal/pathsafe"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/store"
)

type input struct {
	RuntimeEnvironment    string `json:"runtimeEnvironment"`
	RuntimeVersion        string `json:"runtimeVersion"`
	RuntimeLaunch         string `json:"runtimeLaunch"`
	RuntimeEntry          string `json:"runtimeEntry"`
	RuntimeCommand        string `json:"runtimeCommand"`
	RuntimePort           int    `json:"runtimePort"`
	RuntimeWorkers        int    `json:"runtimeWorkers"`
	RuntimeThreads        int    `json:"runtimeThreads"`
	RuntimeTimeout        int    `json:"runtimeTimeout"`
	RuntimeFile           string `json:"runtimeFile"`
	RuntimeAutoStart      bool   `json:"runtimeAutoStart"`
	RuntimePackageManager string `json:"runtimePackageManager"`
	RuntimeScript         string `json:"runtimeScript"`
	RuntimeEnv            string `json:"runtimeEnv"`

	ProjectType string             `json:"projectType"`
	PHPVersion  string             `json:"phpVersion"`
	Name        string             `json:"name"`
	Domains     []string           `json:"domains"`
	Type        string             `json:"type"`
	Root        string             `json:"root"`
	ProxyRoutes []store.ProxyRoute `json:"proxyRoutes"`
	Remark      string             `json:"remark"`
}

var hostname = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var rootChars = regexp.MustCompile(`^/[a-zA-Z0-9/._-]*$`)
var executableChars = regexp.MustCompile(`^[a-zA-Z0-9/._-]+$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var bindingPort = regexp.MustCompile(`^[0-9]{1,5}$`)

func domain(value string) (string, error) {
	value = strings.ToLower(strings.TrimSuffix(value, "."))
	host := strings.TrimPrefix(value, "*.")
	if host == "" || len(host) > 253 || net.ParseIP(host) != nil {
		return "", errors.New("域名必须为合法 ASCII 主机名；国际域名请使用 punycode")
	}
	for _, label := range strings.Split(host, ".") {
		if !hostname.MatchString(label) {
			return "", errors.New("域名格式无效")
		}
	}
	return value, nil
}
func binding(value string) (string, error) {
	value = strings.TrimSpace(value)
	host, rawPort, explicit := value, "", false
	if strings.HasPrefix(value, "[") {
		end := strings.Index(value, "]")
		if end < 0 {
			return "", errors.New("IPv6地址须使用[IPv6]:端口格式")
		}
		host = value[1:end]
		if tail := value[end+1:]; tail != "" {
			if !strings.HasPrefix(tail, ":") {
				return "", errors.New("IP绑定格式无效")
			}
			rawPort, explicit = tail[1:], true
		}
	} else if addr, e := netip.ParseAddr(value); e == nil && addr.Is6() {
		host = addr.String()
	} else {
		host, rawPort, explicit = strings.Cut(value, ":")
	}
	port := 80
	if explicit {
		var e error
		port, e = strconv.Atoi(rawPort)
		if !bindingPort.MatchString(rawPort) || e != nil || port < 1 || port > 65535 {
			return "", errors.New("绑定端口需要1至65535")
		}
	}
	if addr, e := netip.ParseAddr(host); e == nil {
		if addr.Zone() != "" {
			return "", errors.New("站点IPv6不支持网卡作用域")
		}
		host = addr.Unmap().String()
		if addr.Unmap().Is6() {
			host = "[" + host + "]"
		}
	} else {
		var e error
		host, e = domain(host)
		if e != nil {
			return "", e
		}
	}
	if port == 80 {
		return host, nil
	}
	return host + ":" + strconv.Itoa(port), nil
}

// 内部只接收已规范化绑定，IPv6保留方括号供Nginx匹配Host。
func splitBinding(value string) (string, int) {
	host, raw, explicit := value, "", false
	if strings.HasPrefix(value, "[") {
		end := strings.Index(value, "]")
		host = value[:end+1]
		if len(value) > end+1 {
			raw, explicit = value[end+2:], true
		}
	} else {
		host, raw, explicit = strings.Cut(value, ":")
	}
	port := 80
	if explicit {
		port, _ = strconv.Atoi(raw)
	}
	return host, port
}
func (m *Module) validate(in input, old store.Website) (store.Website, error) {
	if in.ProjectType == "" {
		if old.Type == in.Type {
			in.ProjectType = old.ProjectType
		}
		if in.ProjectType == "" {
			in.ProjectType = "php"
			if in.Type == "proxy" {
				in.ProjectType = "proxy"
			}
		}
	}
	if (in.ProjectType != "php" && in.ProjectType != "proxy" && !pkgmgr.IsProjectRuntime(in.ProjectType)) || (in.ProjectType == "php" && in.Type != "static") || (in.ProjectType != "php" && in.Type != "proxy") {
		return old, errors.New("项目分类与站点类型不匹配")
	}
	if old.ProjectType != "" && old.ProjectType != in.ProjectType {
		return old, errors.New("已有站点的项目分类不可更改")
	}
	old.ProjectType = in.ProjectType
	if in.RuntimeEnvironment == "" {
		in.RuntimeEnvironment = in.RuntimeVersion
	}
	managed := pkgmgr.IsProjectRuntime(in.ProjectType) && (in.ProjectType != "go" || in.RuntimeEnvironment != "" || old.RuntimeEnvironment != "")
	previousEntry := old.RuntimeEntry
	previousFile := old.RuntimeFile
	if managed {
		if in.RuntimeEnvironment == "" {
			return old, errors.New("请选择运行环境")
		}
		if err := in.validateRuntime(old); err != nil {
			return old, err
		}
		old.RuntimeFile, old.RuntimeAutoStart = in.RuntimeFile, in.RuntimeAutoStart
		old.RuntimePackageManager, old.RuntimeScript = in.RuntimePackageManager, in.RuntimeScript
		in.RuntimeEnv = strings.TrimSpace(strings.ReplaceAll(in.RuntimeEnv, "\r\n", "\n"))
		if len(in.RuntimeEnv) > 32768 || strings.ContainsAny(in.RuntimeEnv, "\r\x00") {
			return old, errors.New("环境变量内容无效")
		}
		if in.RuntimeEnv != "" {
			for line := range strings.SplitSeq(in.RuntimeEnv, "\n") {
				name, _, ok := strings.Cut(line, "=")
				if line != "" && (!ok || !envName.MatchString(name)) {
					return old, errors.New("环境变量每行填写 NAME=value")
				}
				if name == "PORT" && in.RuntimePort > 0 {
					return old, errors.New("PORT 由项目端口设置，请从环境变量中移除")
				}
				if name == "PROJECT_FILE" && in.RuntimeFile != "" {
					return old, errors.New("PROJECT_FILE 由项目执行文件设置")
				}
			}
		}
		old.RuntimeEnvironment, old.RuntimeCommand, old.RuntimePort = in.RuntimeEnvironment, in.RuntimeCommand, in.RuntimePort
		old.RuntimeLaunch, old.RuntimeEntry = in.RuntimeLaunch, in.RuntimeEntry
		old.RuntimeWorkers, old.RuntimeThreads, old.RuntimeTimeout = in.RuntimeWorkers, in.RuntimeThreads, in.RuntimeTimeout
		if in.RuntimeEnv != "" {
			var err error
			old.RuntimeEnv, err = store.Encrypt(m.cfg.AESKey, in.RuntimeEnv)
			if err != nil {
				return old, err
			}
		} else {
			old.RuntimeEnv = ""
		}
	}
	if !utf8.ValidString(in.Remark) || utf8.RuneCountInString(in.Remark) > 512 {
		return old, errors.New("备注最多512字")
	}
	if len(in.Domains) > 100 || (!managed && len(in.Domains) == 0) {
		return old, errors.New("域名需要1至100项")
	}
	domains := make([]string, 0, len(in.Domains))
	seen := map[string]bool{}
	for _, value := range in.Domains {
		d, e := binding(value)
		if e != nil {
			return old, e
		}
		if !seen[d] {
			domains = append(domains, d)
			seen[d] = true
		}
	}
	in.Name = strings.TrimSpace(in.Name)
	if old.Name == "" && in.Name == "" {
		if len(domains) == 0 {
			return old, errors.New("请输入项目名称")
		}
		host, port := splitBinding(domains[0])
		host = strings.Trim(strings.TrimPrefix(host, "*."), "[]")
		suffix := "_" + strconv.Itoa(port)
		in.Name = host[:min(len(host), 255-len(suffix))] + suffix
	}
	if !utf8.ValidString(in.Name) || len(in.Name) > 255 || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 {
		return old, errors.New("站点名称不能超过255字节或包含控制字符")
	}
	if !validLogName(in.Name) {
		return old, errors.New("请填写有效的站点名称，不能包含路径分隔符、引号或通配符")
	}
	root, target := "", ""
	if in.Type == "proxy" {
		in.PHPVersion = ""
	}
	if in.PHPVersion != "" {
		if !pkgmgr.IsPHP("php-" + in.PHPVersion) {
			return old, errors.New("PHP版本无效")
		}
		state, e := m.packages.Status(context.Background(), "php-"+in.PHPVersion)
		if e != nil || !state.Installed {
			return old, errors.New("请先在应用商店安装所选PHP版本")
		}
	}
	switch {
	case in.Type == "static" || managed:
		if !rootChars.MatchString(in.Root) {
			return old, errors.New("根目录必须为绝对路径，仅允许字母、数字和 /._-")
		}
		var e error
		root, e = m.policy.Check(filepath.Clean(in.Root), true)
		if e != nil || m.policy.CheckTree(root) != nil || !rootChars.MatchString(root) {
			return old, errors.New("根目录受保护、含无效链接或无法解析")
		}
		if info, e := os.Stat(root); e == nil && !info.IsDir() {
			return old, errors.New("根目录不是目录")
		} else if e != nil && !os.IsNotExist(e) {
			return old, errors.New("根目录无法访问")
		}
		if pathsafe.Contains(m.cfg.Layout().AppsDir(), root) || pathsafe.Contains(root, m.cfg.Layout().AppsDir()) {
			return old, errors.New("项目目录不能与应用安装目录重叠")
		}
		if in.Type == "static" && !pathsafe.Contains(m.cfg.Current().SiteRoot, root) {
			return old, errors.New("网站根目录必须位于默认建站目录内")
		}
		if managed {
			target = old.ProxyTarget
			file, label := "", ""
			if in.RuntimeEntry != "" && (in.RuntimeEntry != previousEntry || root != old.Root) {
				module, _, _ := strings.Cut(in.RuntimeEntry, ":")
				file, label = strings.ReplaceAll(module, ".", "/")+".py", "入口文件"
			} else if in.RuntimeFile != "" && (in.RuntimeFile != previousFile || root != old.Root) {
				file, label = in.RuntimeFile, "项目执行文件"
			}
			if file != "" {
				resolved, err := filepath.EvalSymlinks(filepath.Join(root, file))
				if err != nil || !pathsafe.Contains(root, resolved) {
					return old, errors.New(label + "必须位于项目目录内且可访问")
				}
				if info, err := os.Stat(resolved); err != nil || !info.Mode().IsRegular() ||
					(in.ProjectType == "go" && filepath.Ext(file) != ".go" && info.Mode().Perm()&0111 == 0) {
					return old, errors.New(label + "无效，请检查文件类型和执行权限")
				}
			}
		}
	case in.Type == "proxy":
		if len(in.ProxyRoutes) == 0 && old.Name == "" {
			return old, errors.New("请填写至少一条反向代理规则")
		}
	default:
		return old, errors.New("站点类型必须为 static 或 proxy")
	}
	if !managed {
		routes, err := validateProxyRoutes(in.ProxyRoutes)
		if err != nil {
			return old, err
		}
		old.ProxyRoutes = routes
	}

	encoded, _ := json.Marshal(domains)
	old.PHPVersion = in.PHPVersion
	old.LogDirectory = in.Name
	old.Name, old.Domains, old.Type, old.Root, old.ProxyTarget, old.Remark = in.Name, string(encoded), in.Type, root, target, in.Remark
	return old, m.validateSites(old)
}

// 比较实际监听的域名与端口，包括SSL派生的443绑定。
func (m *Module) validateSites(site store.Website) error {
	if err := validateRedirect(site); err != nil {
		return err
	}
	servers := siteServers(site)
	if site.SSLEnabled && len(servers) == 0 {
		return errors.New("请先绑定域名或 IP，再启用 SSL")
	}
	for _, server := range servers {
		if server.Port == m.cfg.Current().Port {
			return errors.New("站点不能绑定面板监听端口")
		}
	}
	var rows []store.Website
	if e := m.db.Select("id, name, root, enabled, domains, ssl_enabled").Find(&rows).Error; e != nil {
		return errStorage
	}
	for _, row := range rows {
		if row.ID == site.ID {
			continue
		}
		if row.Name == site.Name {
			return errors.New("网站名称已存在，请使用其他名称")
		}
		if site.Root != "" && row.Root != "" && (pathsafe.Contains(site.Root, row.Root) || pathsafe.Contains(row.Root, site.Root)) {
			return errors.New("站点根目录不能相同或互相包含")
		}
		for _, existing := range siteServers(row) {
			for _, requested := range servers {
				if existing.Port != requested.Port {
					continue
				}
				if site.Enabled && row.Enabled && existing.SSL != requested.SSL {
					return fmt.Errorf("端口%d已用于其他站点，不能同时绑定HTTP和HTTPS", requested.Port)
				}
				for _, host := range requested.Hosts {
					if slices.Contains(existing.Hosts, host) {
						return fmt.Errorf("域名与端口 %s:%d 已被其他站点使用", host, requested.Port)
					}
				}
			}
		}
	}
	return nil
}
func websiteDomains(row store.Website) []string {
	var ds []string
	_ = json.Unmarshal([]byte(row.Domains), &ds)
	return ds
}
func certificate(cert, key string, domains []string) (*x509.Certificate, error) {
	pair, e := tls.X509KeyPair([]byte(cert), []byte(key))
	if e != nil || len(pair.Certificate) == 0 {
		return nil, errors.New("证书链或私钥无效，或证书与私钥不匹配")
	}
	chain := make([]*x509.Certificate, 0, len(pair.Certificate))
	now := time.Now()
	for _, der := range pair.Certificate {
		item, e := x509.ParseCertificate(der)
		if e != nil {
			return nil, errors.New("证书链解析失败")
		}
		if now.Before(item.NotBefore) || !now.Before(item.NotAfter) {
			return nil, errors.New("证书链包含尚未生效或已过期证书")
		}
		chain = append(chain, item)
	}
	leaf := chain[0]
	if leaf.IsCA {
		return nil, errors.New("首张证书必须为站点证书")
	}
	for i := 0; i+1 < len(chain); i++ {
		if e = chain[i].CheckSignatureFrom(chain[i+1]); e != nil {
			return nil, errors.New("证书链顺序或签名无效")
		}
	}
	if len(uncoveredDomains(leaf, domains)) > 0 {
		return nil, errors.New("证书未覆盖全部站点域名")
	}
	return leaf, nil
}
func uncoveredDomains(leaf *x509.Certificate, domains []string) []string {
	missing := []string{}
	for _, binding := range domains {
		d := binding
		d, _ = splitBinding(d)
		d = strings.Trim(d, "[]")
		if strings.HasPrefix(d, "*.") {
			found := false
			for _, name := range leaf.DNSNames {
				if strings.EqualFold(strings.TrimSuffix(name, "."), d) {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, binding)
			}
		} else if leaf.VerifyHostname(d) != nil {
			missing = append(missing, binding)
		}
	}
	return missing
}
func (m *Module) canDelete(row store.Website) (bool, string) {
	return m.canDeleteWithRoots(row, nil)
}

func (m *Module) canDeleteWithRoots(row store.Website, preloaded []store.Website) (bool, string) {
	if row.Type != "static" && row.RuntimeEnvironment == "" {
		return false, "反向代理没有站点目录"
	}
	if !row.RootCreatedByPanel {
		return false, "目录不是面板为该站点新建，仅可保留目录"
	}
	siteRoot := m.cfg.Current().SiteRoot
	if row.Root == "" || row.Root == siteRoot || !pathsafe.Contains(siteRoot, row.Root) {
		return false, "仅允许删除当前默认建站目录下的授权目录"
	}
	base, e := filepath.EvalSymlinks(siteRoot)
	if e != nil || row.Root == base || !pathsafe.Contains(base, row.Root) {
		return false, "仅允许删除当前默认建站目录下的授权目录"
	}
	real, e := m.policy.Check(row.Root, true)
	info, statErr := os.Lstat(row.Root)
	if e != nil || real != row.Root || statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || m.policy.CheckTree(real) != nil {
		return false, "目录已变更、丢失或不再处于安全范围"
	}
	rows := preloaded
	if rows == nil {
		if m.db.Select("id, root").Where("id <> ? AND root <> ?", row.ID, "").Find(&rows).Error != nil {
			return false, "无法确认目录引用"
		}
	}
	for _, other := range rows {
		if other.ID == row.ID || other.Root == "" {
			continue
		}
		if pathsafe.Contains(other.Root, real) || pathsafe.Contains(real, other.Root) {
			return false, "目录仍被其他站点引用"
		}
	}
	return true, ""
}
