package website

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/store"
)

func (m *Module) acmeRoot() string { return m.cfg.Layout().ACMEDir() }
func (m *Module) acmeLocation() string {
	return "    # ezpanel ACME\n    location ^~ /.well-known/acme-challenge/ {\n        root " + m.acmeRoot() + ";\n        default_type text/plain;\n        try_files $uri =404;\n    }\n"
}

// 只在申请HTTP证书时应用验证路径，不启动OpenResty或改动站点其他配置。
// 当 id 为空时，自动写入临时 Nginx 站点配置用于 HTTP-01 验证（不写入网站数据库），并在验证结束后通过返回的清理函数移除。
func (m *Module) PrepareACME(ctx context.Context, id string, domains []string) (string, func(), error) {
	if id == "" {
		return m.prepareTempACME(ctx, domains)
	}
	release, err := m.tasks.Acquire("website:" + id)
	if err != nil {
		return "", nil, err
	}
	defer release()
	var site store.Website
	if err = m.db.First(&site, "id = ?", id).Error; err != nil {
		return "", nil, err
	}
	if !site.Enabled {
		return "", nil, errors.New("HTTP验证需要已启用的网站")
	}
	bound := map[string]bool{}
	for _, server := range siteServers(site) {
		if server.Port == 80 {
			for _, host := range server.Hosts {
				bound[host] = true
			}
		}
	}
	for _, domain := range domains {
		if !bound[domain] {
			return "", nil, fmt.Errorf("域名%s未在所选网站监听80端口，请先绑定或使用DNS验证", domain)
		}
	}
	if site.RedirectEnabled {
		return "", nil, errors.New("所选网站配置了整站重定向，请临时关闭或使用DNS验证")
	}
	acmeRoot := m.acmeRoot()
	for _, dir := range []string{acmeRoot, filepath.Join(acmeRoot, ".well-known"), filepath.Join(acmeRoot, ".well-known", "acme-challenge")} {
		if err = os.MkdirAll(dir, 0755); err != nil {
			return "", nil, err
		}
		if err = os.Chmod(dir, 0755); err != nil {
			return "", nil, err
		}
	}
	err = m.packages.ApplicationChange("openresty", func() error {
		state, e := m.packages.Status(ctx, "openresty")
		if e != nil {
			return e
		}
		if !state.Manageable {
			return pkgmgr.ErrUnsupported
		}
		if state.Running == nil || !*state.Running {
			return errors.New("HTTP验证需要OpenResty正在运行")
		}
		path := m.confPath(site.ID, true)
		previous, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if strings.Contains(strings.ToLower(string(previous)), "# ezpanel acme") {
			return nil
		}
		servers := regexp.MustCompile(`(?m)^\s*server\s*\{`)
		if !servers.Match(previous) {
			return errors.New("站点配置未找到server块，请使用DNS验证")
		}
		next := servers.ReplaceAllStringFunc(string(previous), func(string) string {
			return "server {\n" + m.acmeLocation()
		})
		if e = atomicWrite(path, []byte(next), 0644); e != nil {
			return e
		}
		restore := func(cause error) error {
			recovery := atomicWrite(path, previous, 0644)
			if recovery == nil {
				recovery = m.reload(context.WithoutCancel(ctx))
			}
			return errors.Join(cause, recovery)
		}
		if e = m.openRestyTest(ctx); e != nil {
			return restore(e)
		}
		if e = m.reload(ctx); e != nil {
			return restore(e)
		}
		return nil
	})
	return acmeRoot, nil, err
}

func (m *Module) prepareTempACME(ctx context.Context, domains []string) (string, func(), error) {
	if len(domains) == 0 {
		return "", nil, errors.New("HTTP验证需要至少一个域名")
	}
	acmeRoot := m.acmeRoot()
	for _, dir := range []string{acmeRoot, filepath.Join(acmeRoot, ".well-known"), filepath.Join(acmeRoot, ".well-known", "acme-challenge")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", nil, err
		}
		if err := os.Chmod(dir, 0755); err != nil {
			return "", nil, err
		}
	}
	tempPath := filepath.Join(filepath.Dir(m.confPath("tmp", true)), "_ezpanel_acme_"+store.ID()+".conf")
	content := "server {\n    listen 80;\n    server_name " + strings.Join(domains, " ") + ";\n" + m.acmeLocation() + "    location / { return 404; }\n}\n"
	err := m.packages.ApplicationChange("openresty", func() error {
		state, e := m.packages.Status(ctx, "openresty")
		if e != nil {
			return e
		}
		if !state.Manageable {
			return pkgmgr.ErrUnsupported
		}
		if state.Running == nil || !*state.Running {
			return errors.New("HTTP验证需要OpenResty正在运行")
		}
		if e = atomicWrite(tempPath, []byte(content), 0644); e != nil {
			return e
		}
		cleanup := func() {
			_ = os.Remove(tempPath)
			_ = m.reload(context.WithoutCancel(ctx))
		}
		if e = m.openRestyTest(ctx); e != nil {
			cleanup()
			return e
		}
		if e = m.reload(ctx); e != nil {
			cleanup()
			return e
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("清理 ACME 临时站点失败", "error", err)
			return
		}
		reloadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			err := m.packages.ApplicationChange("openresty", func() error { return m.reload(reloadCtx) })
			if !errors.Is(err, pkgmgr.ErrBusy) {
				if err != nil {
					slog.Warn("ACME 临时配置已删除，重载 OpenResty 失败", "error", err)
				}
				return
			}
			select {
			case <-reloadCtx.Done():
				slog.Warn("ACME 临时配置已删除，等待 OpenResty 操作结束超时")
				return
			case <-ticker.C:
			}
		}
	}
	return acmeRoot, cleanup, nil
}
