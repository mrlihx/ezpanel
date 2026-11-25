package config

import (
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

type runtimeConfig struct {
	mu    sync.Mutex
	value atomic.Pointer[Config]
}

// Shared 让模块已有的 Config 副本共享明确的运行时快照，无全局配置变量。
func Shared(c Config) Config { c.runtime = &runtimeConfig{}; c.runtime.value.Store(&c); return c }
func (c Config) Current() Config {
	if c.runtime != nil {
		return *c.runtime.value.Load()
	}
	return c
}
func (c Config) Apply(change func(*Config) error) (Config, error) {
	if c.runtime == nil {
		return c, errors.New("运行时配置尚未初始化")
	}
	c.runtime.mu.Lock()
	defer c.runtime.mu.Unlock()
	next := c.Current()
	if next.TrustedProxies != nil {
		next.TrustedProxies = append([]string(nil), next.TrustedProxies...)
	}
	if next.SoftwareSources != nil {
		cloned := make(map[string]string, len(next.SoftwareSources))
		for k, v := range next.SoftwareSources {
			cloned[k] = v
		}
		next.SoftwareSources = cloned
	}
	if next.SoftwareMirrors != nil {
		cloned := make(map[string]string, len(next.SoftwareMirrors))
		for k, v := range next.SoftwareMirrors {
			cloned[k] = v
		}
		next.SoftwareMirrors = cloned
	}
	if e := change(&next); e != nil {
		return next, e
	}
	if e := Save(next); e != nil {
		return next, e
	}
	c.runtime.value.Store(&next)
	return next, nil
}
func validatePanel(c Config) error {
	switch c.PHPMyAdminPHPVersion {
	case "", "7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5":
	default:
		return errors.New("phpMyAdmin请选择兼容的PHP 7.4至8.5版本")
	}
	if c.TerminalFontSize < 12 || c.TerminalFontSize > 20 {
		return errors.New("终端字号须为12至20")
	}
	if !utf8.ValidString(c.Alias) || utf8.RuneCountInString(c.Alias) > 32 || strings.IndexFunc(c.Alias, unicode.IsControl) >= 0 {
		return errors.New("面板别名最多32字，不能包含控制字符")
	}
	if c.IdleMinutes < 1 || c.IdleMinutes > 720 {
		return errors.New("空闲超时须为1至720分钟")
	}
	for _, path := range []string{c.SiteRoot, c.BackupRoot} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || !regexp.MustCompile(`^/[a-zA-Z0-9/._-]+$`).MatchString(path) {
			return errors.New("默认目录须为规范绝对路径，仅允许字母、数字和/._-")
		}
	}
	if c.BoundDomain != "" {
		if len(c.BoundDomain) > 253 || net.ParseIP(c.BoundDomain) != nil {
			return errors.New("绑定域名须为有效ASCII域名，不含协议或端口")
		}
		for _, part := range strings.Split(c.BoundDomain, ".") {
			if !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`).MatchString(part) {
				return errors.New("绑定域名格式无效")
			}
		}
	}
	if c.GitHubProxy != "" {
		u, e := url.Parse(c.GitHubProxy)
		if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return errors.New("GitHub加速地址须为无账号和查询参数的HTTPS地址")
		}
	}

	return nil
}
