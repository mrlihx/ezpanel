package cron

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/response"
	"ezpanel/internal/task"

	"github.com/gin-gonic/gin"
)

type urlParameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type urlSettings struct {
	URL       string         `json:"url,omitempty"`
	Method    string         `json:"urlMethod,omitempty"`
	UserAgent string         `json:"userAgent,omitempty"`
	Params    []urlParameter `json:"urlParams,omitempty"`
}

func (settings *urlSettings) validate() error {
	settings.URL = strings.TrimSpace(settings.URL)
	if _, err := parseURL(settings.URL); err != nil {
		return err
	}
	switch settings.Method {
	case "", http.MethodGet:
		settings.Method = http.MethodGet
		settings.Params = nil
	case http.MethodPost:
		size := 0
		for i := range settings.Params {
			p := &settings.Params[i]
			p.Name = strings.TrimSpace(p.Name)
			if p.Name == "" {
				return errors.New("POST 参数名不能为空")
			}
			size += len(p.Name) + len(p.Value)
		}
		if len(settings.Params) > 100 || size > 64<<10 {
			return errors.New("POST 参数最多 100 项，合计不超过 64 KiB")
		}
	default:
		return errors.New("请求方式只支持 GET 或 POST")
	}
	settings.UserAgent = strings.TrimSpace(settings.UserAgent)
	if len(settings.UserAgent) > 1024 || strings.IndexFunc(settings.UserAgent, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return errors.New("User-Agent 须为 1024 字节以内的单行文本")
	}
	return nil
}

type urlResult struct {
	StatusCode    int    `json:"statusCode"`
	DurationMs    int64  `json:"durationMs"`
	ResponseBytes int64  `json:"responseBytes"`
	Body          string `json:"body,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (m *Module) testURL(c *gin.Context) {
	var settings urlSettings
	if !response.Bind(c, &settings) {
		return
	}
	if err := settings.validate(); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	result, err := m.requestURL(c.Request.Context(), settings, true)
	if err != nil {
		result.Error = err.Error()
	}
	response.OK(c, result)
}

func parseURL(raw string) (*url.URL, error) {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 8192 || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.Fragment != "" || strings.Contains(u.Hostname(), "%") {
		return nil, errors.New("URL须为无账号密码的HTTP/HTTPS地址")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return nil, errors.New("URL端口无效")
		}
	}
	return u, nil
}
func safeURL(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return "URL任务"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
func (m *Module) visitURL(ctx context.Context, settings urlSettings, log *task.Log) (any, error) {
	result, err := m.requestURL(ctx, settings, false)
	if result.StatusCode != 0 {
		log.Line(fmt.Sprintf("HTTP %d，耗时 %d ms，响应 %d 字节", result.StatusCode, result.DurationMs, result.ResponseBytes))
	}
	if err == nil && (result.StatusCode < 200 || result.StatusCode >= 300) {
		err = errors.New("URL返回非2xx状态")
	}
	return result, err
}

func (m *Module) requestURL(parent context.Context, settings urlSettings, preview bool) (result urlResult, err error) {
	u, e := parseURL(settings.URL)
	if e != nil {
		return result, e
	}
	started := time.Now()
	defer func() { result.DurationMs = time.Since(started).Milliseconds() }()
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	dialer := net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 30 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, portText, e := net.SplitHostPort(address)
		if e != nil {
			return nil, errors.New("URL目标地址无效")
		}
		port, e := strconv.Atoi(portText)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil || len(ips) == 0 {
			return nil, errors.New("URL域名解析失败")
		}
		addrs, e := net.InterfaceAddrs()
		if e != nil {
			return nil, errors.New("本机地址读取失败，拒绝未知管理目标")
		}
		local := []net.IP{}
		for _, a := range addrs {
			ip, _, e := net.ParseCIDR(a.String())
			if e == nil {
				local = append(local, ip)
			}
		}
		m.mu.Lock()
		portsFn := m.blockedPorts
		m.mu.Unlock()
		currentCfg := m.cfg.Current()
		ports := append([]int{currentCfg.Port}, portsFn()...)
		if public, e := url.Parse(currentCfg.PublicURL); e == nil && public.Host != "" {
			port := 443
			if public.Scheme == "http" {
				port = 80
			}
			if value := public.Port(); value != "" {
				if n, e := strconv.Atoi(value); e == nil {
					port = n
				}
			}
			ports = append(ports, port)
		}
		for _, resolved := range ips {
			ip := resolved.IP
			if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.Equal(net.ParseIP("100.100.100.200")) || ip.Equal(net.ParseIP("fd00:ec2::254")) {
				return nil, errors.New("禁止访问链路本地或云元数据地址")
			}
			isLocal := ip.IsLoopback()
			for _, own := range local {
				if ip.Equal(own) {
					isLocal = true
					break
				}
			}
			if isLocal {
				for _, p := range ports {
					if port == p {
						return nil, errors.New("禁止访问本机面板、Host API或插件监听端口")
					}
				}
			}
		}
		var last error
		for _, resolved := range ips {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), portText))
			if e == nil {
				return conn, nil
			}
			last = e
		}
		return nil, last
	}}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("重定向超过5次")
		}
		_, e := parseURL(req.URL.String())
		if e == nil && req.Method == http.MethodPost && (req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme) {
			return http.ErrUseLastResponse
		}
		return e
	}}
	method := settings.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if method == http.MethodPost {
		values := url.Values{}
		for _, p := range settings.Params {
			values.Add(p.Name, p.Value)
		}
		body = strings.NewReader(values.Encode())
	}
	request, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return result, errors.New("URL请求创建失败")
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if settings.UserAgent != "" {
		request.Header.Set("User-Agent", settings.UserAgent)
	}
	res, e := client.Do(request)
	if e != nil {
		return result, errors.New("URL访问失败，请检查地址、安全限制、TLS或超时")
	}
	defer res.Body.Close()
	result.StatusCode = res.StatusCode
	if preview {
		const limit = 64 << 10
		content, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
		result.ResponseBytes = int64(len(content))
		result.Truncated = len(content) > limit
		result.Body = strings.ToValidUTF8(string(content[:min(len(content), limit)]), "�")
		e = err
	} else {
		result.ResponseBytes, e = io.Copy(io.Discard, res.Body)
	}
	if e != nil {
		return result, errors.New("响应未完整读取或已超时")
	}
	return result, nil
}
