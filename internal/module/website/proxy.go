package website

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"ezpanel/internal/store"
)

var proxyDirectory = regexp.MustCompile(`^/[A-Za-z0-9._~/-]*$`)
var proxyHeaderName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var quoteProxyHeader = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

func validateProxyURL(value string) (*url.URL, error) {
	if strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(";{}#$\\\"'", r)
	}) >= 0 {
		return nil, errors.New("目标 URL 包含不允许的字符")
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("目标 URL 须为无账号、查询参数及片段的 HTTP 或 HTTPS 地址")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("代理端口须为 1 到 65535")
		}
	}
	if net.ParseIP(u.Hostname()) == nil {
		if _, err := domain(u.Hostname()); err != nil || strings.HasPrefix(u.Hostname(), "*.") {
			return nil, errors.New("代理目标主机格式无效")
		}
	}
	return u, nil
}

func validateProxyRoutes(input []store.ProxyRoute) ([]store.ProxyRoute, error) {
	if len(input) > 50 {
		return nil, errors.New("最多添加 50 条反向代理规则")
	}
	routes := make([]store.ProxyRoute, 0, len(input))
	seen := map[string]bool{}
	for _, route := range input {
		route.Name = strings.TrimSpace(route.Name)
		if route.Name == "" || len(route.Name) > 150 || strings.IndexFunc(route.Name, unicode.IsControl) >= 0 {
			return nil, errors.New("请填写有效的代理名称")
		}
		route.Path = strings.TrimSpace(route.Path)
		if !proxyDirectory.MatchString(route.Path) || len(route.Path) > 255 {
			return nil, errors.New("代理目录须以 / 开头，仅允许字母、数字和 ._~/-")
		}
		route.Path = path.Clean(route.Path)
		if route.Path != "/" {
			route.Path += "/"
		}
		if strings.HasPrefix(route.Path, "/.well-known/acme-challenge/") || seen[route.Path] {
			return nil, fmt.Errorf("代理目录 %s 已占用", route.Path)
		}
		seen[route.Path] = true
		target, err := validateProxyURL(strings.TrimSpace(route.Target))
		if err != nil {
			return nil, fmt.Errorf("%s：%w", route.Name, err)
		}
		route.Target = target.String()
		route.Host = strings.TrimSpace(route.Host)
		if route.Host != "" && route.Host != "$host" {
			host, err := validateProxyURL("http://" + route.Host)
			if err != nil || host.Path != "" {
				return nil, fmt.Errorf("%s：发送域名须为主机名或 IP，可带端口", route.Name)
			}
		}
		if err := validateProxyAdvanced(route); err != nil {
			return nil, fmt.Errorf("%s：%w", route.Name, err)
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func validateProxyAdvanced(route store.ProxyRoute) error {
	if route.CacheTTL < 0 || route.CacheTTL > 86400 {
		return errors.New("缓存时长须为 0 到 86400 秒")
	}
	if limit := route.RateLimit; limit != nil && (limit.RequestsPerSecond < 1 || limit.RequestsPerSecond > 10000 || limit.Burst < 0 || limit.Burst > 10000) {
		return errors.New("限速须为每秒 1 到 10000 个请求，突发量须为 0 到 10000")
	}
	if len(route.Headers) > 20 {
		return errors.New("最多设置 20 个自定义 Header")
	}
	type headerKey struct{ direction, name string }
	seen := make(map[headerKey]bool, len(route.Headers))
	for _, header := range route.Headers {
		if (header.Direction != "request" && header.Direction != "response") || !proxyHeaderName.MatchString(header.Name) {
			return errors.New("请填写有效的 Header 类型和名称")
		}
		if len(header.Value) > 2048 || strings.IndexFunc(header.Value, unicode.IsControl) >= 0 || strings.ContainsRune(header.Value, '$') {
			return errors.New("Header 值最多 2048 字节，不能包含控制字符或 Nginx 变量")
		}
		name := strings.ToLower(header.Name)
		switch name {
		case "connection", "content-length", "keep-alive", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade":
			return fmt.Errorf("%s 由代理协议管理", header.Name)
		case "host", "x-real-ip", "x-forwarded-for", "x-forwarded-proto":
			if header.Direction == "request" {
				return fmt.Errorf("%s 由面板管理", header.Name)
			}
		}
		key := headerKey{header.Direction, name}
		if seen[key] {
			return fmt.Errorf("%s 在同一方向重复", header.Name)
		}
		seen[key] = true
	}
	return nil
}

func proxyZone(prefix, siteID, routePath string) string {
	hash := sha256.Sum256([]byte(siteID + "\x00" + routePath))
	return fmt.Sprintf("panel_proxy_%s_%x", prefix, hash[:8])
}

// 缓存路径和共享限速区必须位于 http 块，不能随每个监听端口重复生成。
func writeProxyZones(b *strings.Builder, row store.Website) {
	if row.RedirectEnabled {
		return
	}
	cache := false
	for _, route := range row.ProxyRoutes {
		if !route.Enabled {
			continue
		}
		cache = cache || route.CacheTTL > 0
		if limit := route.RateLimit; limit != nil {
			fmt.Fprintf(b, "limit_req_zone $binary_remote_addr zone=%s:1m rate=%dr/s;\n", proxyZone("rate", row.ID, route.Path), limit.RequestsPerSecond)
		}
	}
	if cache {
		zone := proxyZone("cache", row.ID, "")
		// OpenResty 的配置挂载为只读，缓存属于容器内可丢弃的数据。
		fmt.Fprintf(b, "proxy_cache_path /tmp/%s levels=1:2 keys_zone=%s:1m max_size=256m inactive=1d use_temp_path=off;\n", zone, zone)
	}
}

func writeProxyRoute(b *strings.Builder, siteID string, route store.ProxyRoute, rewrite string) {
	host := route.Host
	if host == "" {
		host = "$proxy_host"
	}
	fmt.Fprintf(b, "    location ^~ %s {\n", route.Path)
	if rewrite != "" {
		fmt.Fprintf(b, "        %s\n", rewrite)
	}
	fmt.Fprintf(b, "        proxy_pass %s;\n        proxy_http_version 1.1;\n        proxy_set_header Host %s;\n", route.Target, host)
	if strings.HasPrefix(route.Target, "https://") {
		b.WriteString("        proxy_ssl_server_name on;\n")
	}
	b.WriteString("        proxy_set_header X-Real-IP $remote_addr;\n        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n        proxy_set_header X-Forwarded-Proto $scheme;\n")
	if route.WebSocket == nil || *route.WebSocket {
		b.WriteString("        proxy_set_header Upgrade $http_upgrade;\n        proxy_set_header Connection $panel_connection_upgrade;\n")
	} else {
		b.WriteString("        proxy_set_header Upgrade \"\";\n        proxy_set_header Connection \"\";\n")
	}
	if route.CacheTTL > 0 {
		key := sha256.Sum256(fmt.Appendf(nil, "%q %q %q %q %d", route.Path, route.Target, route.Host, route.Headers, route.CacheTTL))
		fmt.Fprintf(b, "        proxy_buffering on;\n        proxy_cache %s;\n        proxy_cache_key \"%x|$scheme|$host|$server_port|$request_uri\";\n        proxy_cache_valid 200 %ds;\n        proxy_cache_bypass $panel_proxy_cache_skip;\n        proxy_no_cache $panel_proxy_cache_skip;\n", proxyZone("cache", siteID, ""), key[:8], route.CacheTTL)
	}
	if limit := route.RateLimit; limit != nil {
		fmt.Fprintf(b, "        limit_req zone=%s", proxyZone("rate", siteID, route.Path))
		if limit.Burst > 0 {
			fmt.Fprintf(b, " burst=%d nodelay", limit.Burst)
		}
		b.WriteString(";\n        limit_req_status 429;\n")
	}
	for _, header := range route.Headers {
		value := quoteProxyHeader.Replace(header.Value)
		if header.Direction == "request" {
			fmt.Fprintf(b, "        proxy_set_header %s \"%s\";\n", header.Name, value)
		} else {
			fmt.Fprintf(b, "        proxy_hide_header %s;\n        add_header %s \"%s\" always;\n", header.Name, header.Name, value)
		}
	}
	b.WriteString("    }\n")
}
