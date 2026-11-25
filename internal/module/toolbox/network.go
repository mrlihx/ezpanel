package toolbox

import (
	"context"
	"errors"
	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type dnsProvider struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Primary   string `json:"primary"`
	Secondary string `json:"secondary"`
}

var dnsProviders = []dnsProvider{{"ali", "阿里 AliDNS", "223.5.5.5", "223.6.6.6"}, {"114", "114 DNS", "114.114.114.114", "114.114.115.115"}, {"tencent", "腾讯 DNSPod", "119.29.29.29", "119.28.28.28"}, {"baidu", "百度 DNS", "180.76.76.76", ""}, {"cnnic", "CNNIC SDNS", "1.2.4.8", "210.2.4.8"}, {"cloudflare", "Cloudflare", "1.1.1.1", "1.0.0.1"}, {"google", "Google", "8.8.8.8", "8.8.4.4"}, {"quad9", "Quad9", "9.9.9.9", "149.112.112.112"}}

type dnsResult struct {
	dnsProvider
	Latency   *float64 `json:"latency"`
	ResolveMS *float64 `json:"resolveMs"`
	Success   int      `json:"success"`
	Total     int      `json:"total"`
	Errors    []string `json:"errors"`
}

func (m *Module) testDNS(c *gin.Context) {
	var in struct {
		Servers   []string `json:"servers"`
		Recommend bool     `json:"recommend"`
	}
	if !response.Bind(c, &in) {
		return
	}
	providers := dnsProviders
	if !in.Recommend {
		if len(in.Servers) < 1 || len(in.Servers) > 2 {
			response.Fail(c, response.Invalid, "请输入1至2个 DNS IP")
			return
		}
		providers = []dnsProvider{}
		for _, ip := range in.Servers {
			if net.ParseIP(ip) == nil {
				response.Fail(c, response.Invalid, "DNS 必须是有效IP")
				return
			}
			providers = append(providers, dnsProvider{ID: ip, Name: "自定义 DNS", Primary: ip})
		}
	}
	rows := make([]dnsResult, len(providers))
	var wg sync.WaitGroup
	for i, p := range providers {
		wg.Add(1)
		go func(i int, p dnsProvider) {
			defer wg.Done()
			row := dnsResult{dnsProvider: p, Errors: []string{}}
			begin := time.Now()
			conn, e := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(c.Request.Context(), "tcp", net.JoinHostPort(p.Primary, "53"))
			if e == nil {
				n := float64(time.Since(begin).Microseconds()) / 1000
				row.Latency = &n
				conn.Close()
			}
			var total time.Duration
			for _, ip := range []string{p.Primary, p.Secondary} {
				if ip == "" {
					continue
				}
				resolver := net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, "53"))
				}}
				for _, domain := range []string{"www.baidu.com", "www.qq.com", "www.aliyun.com", "example.com"} {
					ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
					started := time.Now()
					_, err := resolver.LookupHost(ctx, domain)
					cancel()
					row.Total++
					if err == nil {
						row.Success++
						total += time.Since(started)
					} else {
						row.Errors = append(row.Errors, ip+" / "+domain+"："+err.Error())
					}
				}
			}
			if row.Success > 0 {
				n := float64(total.Microseconds()) / float64(row.Success) / 1000
				row.ResolveMS = &n
			}
			rows[i] = row
		}(i, p)
	}
	wg.Wait()
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Success*b.Total != b.Success*a.Total {
			return a.Success*b.Total > b.Success*a.Total
		}
		if a.ResolveMS == nil {
			return false
		}
		if b.ResolveMS == nil {
			return true
		}
		return *a.ResolveMS < *b.ResolveMS
	})
	response.OK(c, gin.H{"list": rows})
}

type mirrorProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	PIP  string `json:"pip"`
}

var mirrorProviders = []mirrorProvider{{"official", "官方源", "", "https://pypi.org/simple"}, {"tencent", "腾讯源", "https://mirrors.tencent.com", "https://mirrors.cloud.tencent.com/pypi/simple"}, {"ali", "阿里源", "https://mirrors.aliyun.com", "https://mirrors.aliyun.com/pypi/simple"}, {"ustc", "中科大源", "https://mirrors.ustc.edu.cn", "https://mirrors.ustc.edu.cn/pypi/simple"}, {"huawei", "华为源", "https://repo.huaweicloud.com", "https://repo.huaweicloud.com/repository/pypi/simple"}, {"tuna", "清华源", "https://mirrors.tuna.tsinghua.edu.cn", "https://pypi.tuna.tsinghua.edu.cn/simple"}}

func systemInfo() (string, string, error) {
	b, e := os.ReadFile("/etc/os-release")
	if e != nil {
		return "", "", e
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[k] = strings.Trim(v, "\"'")
		}
	}
	return values["ID"], values["VERSION_CODENAME"], nil
}
func mirrorURL(p mirrorProvider, id string) string {
	if id == "ubuntu" {
		if p.ID == "official" {
			if runtime.GOARCH == "arm64" {
				return "https://ports.ubuntu.com/ubuntu-ports"
			}
			return "https://archive.ubuntu.com/ubuntu"
		}
		if runtime.GOARCH == "arm64" {
			return p.URL + "/ubuntu-ports"
		}
		return p.URL + "/ubuntu"
	}
	if id == "debian" {
		if p.ID == "official" {
			return "https://deb.debian.org/debian"
		}
		return p.URL + "/debian"
	}
	return ""
}
func aptFiles() ([]string, error) {
	out := []string{}
	if _, e := os.Stat("/etc/apt/sources.list"); e == nil {
		out = append(out, "/etc/apt/sources.list")
	}
	entries, e := os.ReadDir("/etc/apt/sources.list.d")
	if os.IsNotExist(e) {
		return out, nil
	}
	if e != nil {
		return nil, e
	}
	for _, entry := range entries {
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".sources") || strings.HasSuffix(entry.Name(), ".list")) {
			out = append(out, filepath.Join("/etc/apt/sources.list.d", entry.Name()))
		}
	}
	return out, nil
}
func (m *Module) readMirrors() (any, error) {
	id, code, e := systemInfo()
	if e != nil {
		return nil, e
	}
	supported := id == "ubuntu" || id == "debian"
	var files []string
	if supported {
		files, e = aptFiles()
	} else {
		files, e = filepath.Glob("/etc/yum.repos.d/*.repo")
	}
	if e != nil {
		return nil, e
	}
	rows := []gin.H{}
	sources := []string{}
	for _, path := range files {
		b, e := os.ReadFile(path)
		if e != nil {
			return nil, e
		}
		rows = append(rows, gin.H{"path": path, "content": string(b)})
		for _, address := range repoSources(path, string(b)) {
			if !slices.Contains(sources, address) {
				sources = append(sources, address)
			}
		}
	}
	pip, _ := os.ReadFile("/etc/pip.conf")
	pipIndex, global := mirrorProviders[0].PIP, false
	for line := range strings.SplitSeq(string(pip), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			global = line == "[global]"
		}
		key, value, ok := strings.Cut(line, "=")
		if global && ok && strings.TrimSpace(key) == "index-url" {
			pipIndex = strings.TrimSpace(value)
		}
	}
	providerID := ""
	if supported && len(sources) > 0 {
		for _, provider := range mirrorProviders {
			target := strings.TrimPrefix(mirrorURL(provider, id), "https://")
			all, found := true, false
			for _, source := range sources {
				source = strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(source, "http://"), "https://"), "/")
				matches := source == target
				if provider.ID == "official" {
					matches = matches || source == "security.ubuntu.com/ubuntu" || source == "ports.ubuntu.com/ubuntu-ports" ||
						source == "archive.ubuntu.com/ubuntu" || source == "security.debian.org/debian-security" || source == "deb.debian.org/debian-security"
				} else if id == "debian" {
					matches = matches || source == target+"-security"
				}
				all, found = all && matches, found || matches
			}
			if all && found {
				providerID = provider.ID
				break
			}
		}
	}
	return gin.H{"os": id, "codename": code, "supported": supported, "files": rows, "pip": string(pip), "pipIndex": pipIndex,
		"systemSources": sources, "systemProvider": providerID, "providers": mirrorProviders}, nil
}

func repoSources(path, content string) []string {
	var sources, group []string
	enabled, uris := true, false
	flush := func() {
		if enabled {
			sources = append(sources, group...)
		}
		group, enabled, uris = nil, true, false
	}
	for raw := range strings.SplitSeq(content, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		switch filepath.Ext(path) {
		case ".repo":
			if strings.HasPrefix(line, "[") {
				flush()
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch strings.TrimSpace(key) {
			case "enabled":
				enabled = strings.TrimSpace(value) != "0"
			case "baseurl", "mirrorlist", "metalink":
				group = append(group, strings.Fields(value)...)
			}
		case ".sources":
			if line == "" {
				flush()
			}
			key, value, ok := strings.Cut(line, ":")
			switch {
			case uris && len(raw) > 0 && (raw[0] == ' ' || raw[0] == '\t'):
				group = append(group, strings.Fields(line)...)
			case ok && strings.EqualFold(key, "Enabled"):
				enabled = !strings.EqualFold(strings.TrimSpace(value), "no")
				uris = false
			default:
				uris = ok && strings.EqualFold(key, "URIs")
				if uris {
					group = append(group, strings.Fields(value)...)
				}
			}
		default:
			if strings.HasPrefix(line, "deb ") || strings.HasPrefix(line, "deb-src ") {
				if address := regexp.MustCompile(`(?:https?|file)://[^\s#]+`).FindString(line); address != "" {
					sources = append(sources, address)
				}
			}
		}
	}
	flush()
	return sources
}
func (m *Module) testMirrors(c *gin.Context) {
	var in struct {
		Kind string `json:"kind"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Kind != "apt" && in.Kind != "pip" {
		response.Fail(c, response.Invalid, "镜像类型无效")
		return
	}
	id, code, e := systemInfo()
	if e != nil {
		fail(c, e)
		return
	}
	if in.Kind == "apt" && (id != "ubuntu" && id != "debian") {
		response.Fail(c, response.Invalid, "此系统暂不支持 Apt 镜像切换")
		return
	}
	rows := make([]gin.H, len(mirrorProviders))
	var wg sync.WaitGroup
	for i, p := range mirrorProviders {
		wg.Add(1)
		go func(i int, p mirrorProvider) {
			defer wg.Done()
			url := p.PIP
			target := url + "/pip/"
			if in.Kind == "apt" {
				url = mirrorURL(p, id)
				target = url + "/dists/" + code + "/InRelease"
			}
			ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", target, nil)
			client := &http.Client{Timeout: 10 * time.Second}
			begin := time.Now()
			res, e := client.Do(req)
			row := gin.H{"id": p.ID, "name": p.Name, "url": url, "reachable": false, "complete": false, "latency": nil, "speed": nil, "error": ""}
			if e == nil {
				defer res.Body.Close()
				headers := time.Since(begin)
				data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
				elapsed := time.Since(begin)
				valid := res.StatusCode == 200 && err == nil
				if valid {
					row["reachable"] = true
					row["latency"] = float64(headers.Microseconds()) / 1000
					row["speed"] = float64(len(data)) / elapsed.Seconds()
					if in.Kind == "apt" {
						row["complete"] = strings.Contains(string(data), "-----BEGIN PGP SIGNED MESSAGE-----") && strings.Contains(string(data), "SHA256:")
					} else {
						row["complete"] = strings.Contains(string(data), "href=")
					}
				} else {
					row["error"] = fmt.Sprintf("HTTP %d，读取错误：%v", res.StatusCode, err)
				}
			} else {
				row["error"] = e.Error()
			}
			rows[i] = row
		}(i, p)
	}
	wg.Wait()
	response.OK(c, gin.H{"list": rows, "notice": "仓库检查表示发行版索引或 pip 包索引可读取；不是全仓库校验，Apt 保存时仍由 apt 验证签名和索引。"})
}
func (m *Module) saveMirror(ctx context.Context, kind, provider string) error {
	var selected mirrorProvider
	for _, p := range mirrorProviders {
		if p.ID == provider {
			selected = p
		}
	}
	if selected.ID == "" {
		return errors.New("请选择内置镜像源")
	}
	if kind == "pip" {
		old, e := os.ReadFile("/etc/pip.conf")
		if e != nil && !os.IsNotExist(e) {
			return e
		}

		lines := strings.Split(strings.TrimRight(string(old), "\n"), "\n")
		out := []string{}
		inGlobal, found, hasGlobal := false, false, false
		for _, line := range lines {
			trim := strings.TrimSpace(line)
			if strings.HasPrefix(trim, "[") {
				if inGlobal && !found {
					out = append(out, "index-url = "+selected.PIP)
					found = true
				}
				inGlobal = trim == "[global]"
				hasGlobal = hasGlobal || inGlobal
			}
			key, _, hasKey := strings.Cut(trim, "=")
			if inGlobal && hasKey && strings.TrimSpace(key) == "index-url" {
				line = "index-url = " + selected.PIP
				found = true
			}
			out = append(out, line)
		}
		if !found {
			if !hasGlobal {
				out = append(out, "[global]")
			}
			out = append(out, "index-url = "+selected.PIP)
		}

		return writeConfig("/etc/pip.conf", []byte(strings.Join(out, "\n")+"\n"), 0644)
	}
	if kind != "apt" {
		return errors.New("镜像类型无效")
	}
	id, _, e := systemInfo()
	if e != nil {
		return e
	}
	if id != "ubuntu" && id != "debian" {
		return errors.New("当前仅支持 Ubuntu/Debian 的系统镜像源切换")
	}
	target := mirrorURL(selected, id)
	files, e := aptFiles()
	if e != nil {
		return e
	}
	original := map[string][]byte{}
	changed := map[string][]byte{}
	for _, path := range files {
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		text := string(b)
		next := text
		origins := []string{"http://archive.ubuntu.com/ubuntu", "https://archive.ubuntu.com/ubuntu", "http://security.ubuntu.com/ubuntu", "https://security.ubuntu.com/ubuntu", "http://ports.ubuntu.com/ubuntu-ports", "https://ports.ubuntu.com/ubuntu-ports"}
		if id == "debian" {
			origins = []string{"http://deb.debian.org/debian-security", "https://deb.debian.org/debian-security", "http://security.debian.org/debian-security", "https://security.debian.org/debian-security", "http://deb.debian.org/debian", "https://deb.debian.org/debian"}
		}
		for _, p := range mirrorProviders {
			if p.URL != "" {
				if id == "debian" {
					origins = append(origins, p.URL+"/debian-security")
				}
				origins = append(origins, mirrorURL(p, id))
			}
		}

		replacements := map[string]string{}
		for _, origin := range origins {
			replacement := target
			if strings.Contains(origin, "debian-security") {
				if selected.ID == "official" {
					replacement = "https://security.debian.org/debian-security"
				} else {
					replacement = selected.URL + "/debian-security"
				}
			}
			replacements[origin] = replacement
		}
		// 只替换完整发行版仓库 URL，不改第三方地址或重新匹配新地址。
		next = regexp.MustCompile(`https?://[^\s#]+`).ReplaceAllStringFunc(text, func(value string) string {
			if replacement, ok := replacements[strings.TrimRight(value, "/")]; ok {
				if strings.HasSuffix(value, "/") {
					return replacement + "/"
				}
				return replacement
			}
			return value
		})

		if next != text {
			original[path] = b
			changed[path] = []byte(next)
		}
	}
	if len(changed) == 0 {
		return errors.New("未发现可替换的系统源，或当前已是该镜像；自定义源需要手工维护")
	}
	rollback := func() error {
		var errs []error
		for path, b := range original {
			errs = append(errs, writeConfig(path, b, 0644))
		}
		return errors.Join(errs...)
	}
	for path, b := range changed {
		if e = writeConfig(path, b, 0644); e != nil {
			return errors.Join(e, rollback())
		}
	}
	bin, e := executor.LookPath("apt-get")
	if e != nil {
		return errors.Join(e, rollback())
	}
	_, e = executor.RunEnv(ctx, 10*time.Minute, append(executor.DefaultEnv(), "DEBIAN_FRONTEND=noninteractive"), bin, "update", "--error-on=any")
	if e != nil {
		return errors.Join(e, rollback())
	}
	return nil
}
