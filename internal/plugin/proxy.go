package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

func (m *Module) ui(c *gin.Context) {
	p, ok := m.row(c)
	if !ok {
		return
	}
	if !p.Enabled || m.status(p) != "running" {
		response.Fail(c, 3004, "插件未运行")
		return
	}
	if c.Param("version") != p.Version {
		response.Fail(c, response.NotFound, "版本不存在")
		return
	}
	name := strings.TrimPrefix(c.Param("asset"), "/")
	if !packagePath(name) {
		response.Fail(c, response.NotFound, "资源不存在")
		return
	}
	root := filepath.Join(m.packages(), p.ID, "web")
	realRoot, e := filepath.EvalSymlinks(root)
	if e != nil {
		response.Fail(c, response.NotFound, "资源不存在")
		return
	}
	real, e := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.FromSlash(name)))
	if e != nil || !within(real, realRoot) {
		response.Fail(c, response.NotFound, "资源不存在")
		return
	}
	f, e := os.Open(real)
	if e != nil {
		response.Fail(c, response.NotFound, "资源不存在")
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		response.Fail(c, response.NotFound, "资源不存在")
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(real))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), f)
}
func (m *Module) proxy(c *gin.Context) {
	p, ok := m.row(c)
	if !ok {
		return
	}
	rest := strings.TrimPrefix(c.Param("rest"), "/")
	if rest != "" && !packagePath(rest) {
		response.Fail(c, response.Invalid, "代理路径无效")
		return
	}
	m.mu.Lock()
	r := m.runs[p.ID]
	ready := r != nil && m.statuses[p.ID] == "running"
	m.mu.Unlock()
	if !p.Enabled || p.Type != "process" || !ready {
		response.Fail(c, 3004, "插件未运行")
		return
	}
	session := auth.Current(c)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	timer := time.AfterFunc(60*time.Second, cancel)
	defer timer.Stop()

	go func() {
		tick := time.NewTicker(auth.SessionRecheckInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.ctx.Done():
				cancel()
				return
			case <-tick.C:
				if _, e := m.auth.CheckSession(session.Token); e != nil {
					cancel()
					return
				}
			}
		}
	}()
	target := "127.0.0.1:" + strconv.Itoa(r.port)
	prefix := "/plugin/" + p.ID + "/api/"
	proxy := &httputil.ReverseProxy{}
	proxy.Transport = &http.Transport{Proxy: nil, ResponseHeaderTimeout: 60 * time.Second, DisableKeepAlives: true}
	proxy.FlushInterval = -1
	proxy.Rewrite = func(pReq *httputil.ProxyRequest) {
		req := pReq.Out
		req.URL.Scheme = "http"
		req.URL.Host = target
		req.Host = target
		req.URL.Path = "/api/" + rest
		req.URL.RawPath = ""
		for name := range req.Header {
			lower := strings.ToLower(name)
			if lower == "cookie" || lower == "authorization" || lower == "forwarded" || strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-panel-") || lower == "x-plugin-token" || lower == "x-real-ip" {
				req.Header.Del(name)
			}
		}
		req.Header.Set("X-Panel-User", session.User.Username)
		req.Header.Set("X-Panel-Plugin-Id", p.ID)
		req.Header.Set("X-Panel-Proxy-Token", r.proxy)
	}
	proxy.ModifyResponse = func(res *http.Response) error {
		res.Header.Del("Set-Cookie")
		if res.StatusCode == http.StatusSwitchingProtocols || strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
			timer.Stop()
		}
		location := res.Header.Get("Location")
		if location != "" {
			u, e := url.Parse(location)
			if e != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(location, "//") || strings.ContainsAny(location, "\\\r\n") {
				return errors.New("插件重定向被拒绝")
			}
			if strings.HasPrefix(u.Path, "/api/") {
				u.Path = prefix + strings.TrimPrefix(u.Path, "/api/")
				u.RawPath = ""
			} else if !strings.HasPrefix(u.Path, prefix) {
				return errors.New("插件重定向越界")
			}
			if path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") {
				return errors.New("插件重定向路径非规范")
			}
			decoded := u.Path
			for _, part := range strings.Split(decoded, "/") {
				if part == ".." || part == "." {
					return errors.New("插件重定向越界")
				}
			}
			res.Header.Set("Location", u.String())
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, e error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(map[string]any{"code": 3004, "msg": "插件代理中断或响应不受允许", "data": nil})
	}
	req := c.Request.WithContext(ctx)
	req.Body = http.MaxBytesReader(c.Writer, req.Body, 100<<20)
	proxy.ServeHTTP(c.Writer, req)
}
