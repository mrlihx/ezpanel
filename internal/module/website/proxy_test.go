package website

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"ezpanel/internal/store"
)

func TestProxyRoutesRenderForPHPAndProxySites(t *testing.T) {
	routes, err := validateProxyRoutes([]store.ProxyRoute{
		{Name: "API", Path: "/api", Target: "https://api.example.com/v1/", Host: "upstream.example.com", Enabled: true},
		{Name: "资源", Path: "/assets/", Target: "http://127.0.0.1:8080", Enabled: true},
		{Name: "停用", Path: "/off/", Target: "http://127.0.0.1:8081"},
	})
	if err != nil {
		t.Fatal(err)
	}
	module := &Module{}
	site := store.Website{Name: "example.com", Domains: `["example.com"]`, Type: "static", Root: "/www/wwwroot/example.com", PHPVersion: "8.5", ProxyRoutes: routes}
	check := func(text string, wanted ...string) {
		t.Helper()
		for _, item := range wanted {
			if !strings.Contains(text, item) {
				t.Errorf("配置缺少 %q", item)
			}
		}
		if strings.Contains(text, "location ^~ /off/") {
			t.Error("停用规则被写入配置")
		}
	}
	check(module.render(site), "location ^~ /api/", "location ^~ /assets/", "proxy_pass https://api.example.com/v1/;", "proxy_set_header Host upstream.example.com;", "proxy_ssl_server_name on;", "fastcgi_pass unix:", "location / {")
	site.Type = "proxy"
	check(module.render(site), "location ^~ /api/", "location ^~ /assets/", "location / { return 404; }")
}

func TestProxyRoutesRejectDuplicateAndReservedDirectories(t *testing.T) {
	route := store.ProxyRoute{Name: "API", Path: "/api", Target: "http://127.0.0.1:8080", Enabled: true}
	for _, value := range []string{"/api/", "/.well-known/acme-challenge/"} {
		other := route
		other.Path = value
		if _, err := validateProxyRoutes([]store.ProxyRoute{route, other}); err == nil {
			t.Errorf("错误接受目录 %s", value)
		}
	}
}

func TestProxyAdvancedRulesUseHTTPZonesAndPreserveLegacyWebSocket(t *testing.T) {
	off := false
	routes, err := validateProxyRoutes([]store.ProxyRoute{
		{Name: "旧规则", Path: "/legacy", Target: "http://127.0.0.1:8080", Enabled: true},
		{Name: "API", Path: "/api", Target: "http://127.0.0.1:8080", Enabled: true, WebSocket: &off,
			CacheTTL: 60, RateLimit: &store.ProxyRateLimit{RequestsPerSecond: 5, Burst: 10},
			Headers: []store.ProxyHeader{{Direction: "request", Name: "X-Application", Value: "example"}, {Direction: "response", Name: "X-Test", Value: `quoted "value"; next`}}},
		{Name: "停用", Path: "/off", Target: "http://127.0.0.1:8080", RateLimit: &store.ProxyRateLimit{RequestsPerSecond: 9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	site := store.Website{ID: "site", Name: "example.com", Domains: `["example.com:8080","example.com:8081"]`, Type: "proxy", ProxyRoutes: routes}
	config := (&Module{}).render(site)
	http, servers, found := strings.Cut(config, "server {")
	if !found || strings.Count(http, "proxy_cache_path ") != 1 || strings.Count(http, "limit_req_zone ") != 1 || strings.Contains(http, "rate=9r/s") {
		t.Fatalf("invalid http directives: %s", http)
	}
	if strings.Contains(servers, "proxy_cache_path ") || strings.Contains(servers, "limit_req_zone ") {
		t.Fatalf("http directives leaked into a server: %s", servers)
	}
	for _, want := range []string{`proxy_set_header Upgrade "";`, "proxy_cache_valid 200 60s;", "proxy_cache_bypass $panel_proxy_cache_skip;", "burst=10 nodelay;", "limit_req_status 429;", `proxy_set_header X-Application "example";`, `add_header X-Test "quoted \"value\"; next" always;`} {
		if strings.Count(servers, want) != 2 {
			t.Errorf("%q should appear in both server blocks", want)
		}
	}
	if strings.Count(servers, "proxy_set_header Upgrade $http_upgrade;") != 2 {
		t.Fatal("legacy rules lost their WebSocket configuration")
	}
}

func TestProxyHeaderValidationRejectsInvalidNamesValuesAndDuplicates(t *testing.T) {
	for _, headers := range [][]store.ProxyHeader{
		{{Direction: "request", Name: "X-Test;", Value: "value"}},
		{{Direction: "request", Name: "X-Test", Value: "a\nb"}},
		{{Direction: "request", Name: "X-Test", Value: "$request_uri"}},
		{{Direction: "request", Name: "hOst", Value: "example"}},
		{{Direction: "response", Name: "Transfer-Encoding", Value: "chunked"}},
		{{Direction: "request", Name: "X-Test", Value: "a"}, {Direction: "request", Name: "x-test", Value: "b"}},
	} {
		if _, err := validateProxyRoutes([]store.ProxyRoute{{Name: "API", Path: "/", Target: "http://127.0.0.1:8080", Headers: headers}}); err == nil {
			t.Errorf("invalid headers accepted: %+v", headers)
		}
	}
}

func TestWebsiteBindingsUseExplicitPorts(t *testing.T) {
	values := []string{"example.com:8080", "192.0.2.1:8443", "[2001:db8::1]:9443", "other.example.com:8080"}
	for i, value := range values {
		bound, err := binding(value)
		if err != nil {
			t.Fatal(err)
		}
		values[i] = bound
	}
	domains, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	var ports []int
	for _, server := range siteServers(store.Website{Domains: string(domains)}) {
		ports = append(ports, server.Port)
	}
	if !slices.Equal(ports, []int{8080, 8443, 9443}) {
		t.Fatalf("ports = %v", ports)
	}
}
