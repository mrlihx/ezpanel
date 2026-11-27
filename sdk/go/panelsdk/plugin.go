// Package panelsdk 实现可信插件的面板代理身份与 Host v1 客户端。
package panelsdk

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

const SDKVersion = 1

type Plugin struct {
	ID, DataDir string
	Host        *HostClient
	port        int
	proxy       string
	mux         *http.ServeMux
}

func New() (*Plugin, error) {
	id := os.Getenv("PANEL_PLUGIN_ID")
	port, e := strconv.Atoi(os.Getenv("PANEL_PLUGIN_PORT"))
	data := os.Getenv("PANEL_PLUGIN_DATA")
	host, err := url.Parse(os.Getenv("PANEL_HOST_URL"))
	hostToken := os.Getenv("PANEL_PLUGIN_TOKEN")
	proxy := os.Getenv("PANEL_PROXY_TOKEN")
	if e != nil || err != nil || !regexp.MustCompile(`^[a-z][a-z0-9-]{2,31}$`).MatchString(id) || port < 20000 || port > 29999 || !filepath.IsAbs(data) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hostToken) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(proxy) || host.Scheme != "http" || host.Hostname() != "127.0.0.1" || host.Port() == "" || host.User != nil || host.Path != "" || host.RawQuery != "" || host.Fragment != "" {
		return nil, errors.New("插件启动环境无效，请通过面板安装并启用")
	}
	return &Plugin{ID: id, DataDir: data, port: port, proxy: proxy, mux: http.NewServeMux(), Host: &HostClient{base: host.String() + "/host/v1", token: hostToken, client: &http.Client{Timeout: 310 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}}, nil
}
func (p *Plugin) HandleFunc(path string, handler http.HandlerFunc) error {
	if len(path) < 5 || path[:5] != "/api/" {
		return errors.New("插件处理器只允许 /api/ 路径")
	}
	p.mux.HandleFunc(path, handler)
	return nil
}
func (p *Plugin) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return p.RunContext(ctx)
}
func (p *Plugin) RunContext(ctx context.Context) error {
	defer p.Host.client.CloseIdleConnections()
	l, e := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", p.port))
	if e != nil {
		return e
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Panel-Proxy-Token")), []byte(p.proxy)) != 1 {
			http.Error(w, "forbidden", 403)
			return
		}
		if r.URL.Path == "/health" {
			w.WriteHeader(200)
			return
		}
		if r.Header.Get("X-Panel-Plugin-Id") != p.ID || r.Header.Get("X-Panel-User") == "" {
			http.Error(w, "forbidden", 403)
			return
		}
		p.mux.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.Serve(l) }()
	select {
	case e = <-done:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return e
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		e = server.Shutdown(shutdown)
		if e != nil {
			server.Close()
		}
		<-done
		return e
	}
}
