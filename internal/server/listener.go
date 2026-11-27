package server

import (
	"context"
	"crypto/tls"
	"errors"
	"ezpanel/internal/config"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Listener 只替换面板HTTP入口，软件任务和其他模块继续运行。
type Listener struct {
	mu       sync.Mutex
	handler  http.Handler
	server   *http.Server
	listener net.Listener
	cfg      config.Config
	Errors   chan error
}

func NewListener(handler http.Handler) *Listener {
	return &Listener{handler: handler, Errors: make(chan error, 1)}
}
func (m *Listener) open(c config.Config, pair *tls.Certificate) (*http.Server, net.Listener, error) {
	var t *tls.Config
	if c.TLSCert != "" {
		var loaded tls.Certificate
		if pair != nil {
			loaded = *pair
		} else {
			var e error
			loaded, e = tls.LoadX509KeyPair(c.TLSCert, c.TLSKey)
			if e != nil {
				return nil, nil, e
			}
		}
		t = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{loaded}}
	}
	ln, e := net.Listen("tcp", net.JoinHostPort(c.ListenAddr, strconv.Itoa(c.Port)))
	if e != nil {
		return nil, nil, e
	}
	srv := &http.Server{Handler: m.handler, ReadHeaderTimeout: 10 * time.Second, TLSConfig: t}
	go func() {
		var e error
		if t != nil {
			e = srv.ServeTLS(ln, "", "")
		} else {
			e = srv.Serve(ln)
		}
		if e != nil && !errors.Is(e, http.ErrServerClosed) && !errors.Is(e, net.ErrClosed) {
			select {
			case m.Errors <- e:
			default:
			}
		}
	}()
	return srv, ln, nil
}
func (m *Listener) Apply(c config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, oldConfig := m.server, m.cfg
	// 证书校验发生在关闭入口前；业务长任务不依赖此监听器。
	var pair *tls.Certificate
	if c.TLSCert != "" {
		loaded, e := tls.LoadX509KeyPair(c.TLSCert, c.TLSKey)
		if e != nil {
			return e
		}
		pair = &loaded
	}
	rebind := m.listener != nil && c.ListenAddr == oldConfig.ListenAddr && c.Port == oldConfig.Port
	if rebind {
		m.listener.Close()
	}
	srv, ln, e := m.open(c, pair)
	if e != nil {
		if rebind {
			var restore error
			m.server, m.listener, restore = m.open(oldConfig, nil)
			e = errors.Join(e, restore)
		}
		return e
	}
	if m.listener != nil && !rebind {
		m.listener.Close()
	}
	m.server, m.listener, m.cfg = srv, ln, c
	if old != nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = old.Shutdown(ctx)
		}()
	}
	return nil
}
func (m *Listener) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.server != nil {
		return m.server.Shutdown(ctx)
	}
	return nil
}
