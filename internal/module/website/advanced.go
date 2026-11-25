package website

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	settingDefaultSiteID = "website.default_site_id"
	settingTLSProtocols  = "website.tls_protocols"
)

var validTLSProtocols = []string{"TLSv1", "TLSv1.1", "TLSv1.2", "TLSv1.3"}
var defaultTLSProtocols = []string{"TLSv1.2", "TLSv1.3"}

var defaultPageFiles = map[string]string{
	"index":     "default_index.html",
	"404":       "404.html",
	"not_found": "not_found.html",
	"stopped":   "stopped.html",
}

var defaultPageTemplates = map[string]string{
	"index": `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>站点创建成功 - EzPanel</title>
  <style>
    body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f7fa; color: #1f2937; display: flex; min-height: 100vh; align-items: center; justify-content: center; }
    .card { background: #fff; padding: 40px 48px; border-radius: 14px; box-shadow: 0 8px 30px rgba(15, 23, 42, 0.08); max-width: 520px; text-align: center; }
    h1 { margin: 0 0 12px; font-size: 24px; color: #1677ff; }
    p { margin: 8px 0; line-height: 1.7; color: #4b5563; font-size: 14px; }
  </style>
</head>
<body>
  <div class="card">
    <h1>恭喜，站点创建成功！</h1>
    <p>这是系统自动生成的默认首页（index.html），您可以将网站程序上传至站点根目录覆盖此文件。</p>
    <p>Powered by EzPanel</p>
  </div>
</body>
</html>
`,
	"404": `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>404 Not Found</title>
  <style>
    body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f7fa; color: #1f2937; display: flex; min-height: 100vh; align-items: center; justify-content: center; }
    .card { background: #fff; padding: 40px 48px; border-radius: 14px; box-shadow: 0 8px 30px rgba(15, 23, 42, 0.08); max-width: 480px; text-align: center; }
    h1 { margin: 0 0 10px; font-size: 48px; color: #1677ff; }
    p { margin: 8px 0; color: #6b7280; font-size: 14px; }
  </style>
</head>
<body>
  <div class="card">
    <h1>404</h1>
    <p>您访问的页面不存在或已被移除。</p>
  </div>
</body>
</html>
`,
	"not_found": `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>没有找到站点</title>
  <style>
    body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f7fa; color: #1f2937; display: flex; min-height: 100vh; align-items: center; justify-content: center; }
    .card { background: #fff; padding: 40px 48px; border-radius: 14px; box-shadow: 0 8px 30px rgba(15, 23, 42, 0.08); max-width: 500px; text-align: center; }
    h1 { margin: 0 0 12px; font-size: 22px; color: #dc2626; }
    p { margin: 8px 0; line-height: 1.7; color: #4b5563; font-size: 14px; }
  </style>
</head>
<body>
  <div class="card">
    <h1>没有找到站点</h1>
    <p>您访问的域名或 IP 尚未绑定到任何网站，请联系站点管理员检查域名解析与站点配置。</p>
  </div>
</body>
</html>
`,
	"stopped": `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>站点已停用</title>
  <style>
    body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #f5f7fa; color: #1f2937; display: flex; min-height: 100vh; align-items: center; justify-content: center; }
    .card { background: #fff; padding: 40px 48px; border-radius: 14px; box-shadow: 0 8px 30px rgba(15, 23, 42, 0.08); max-width: 500px; text-align: center; }
    h1 { margin: 0 0 12px; font-size: 22px; color: #d97706; }
    p { margin: 8px 0; line-height: 1.7; color: #4b5563; font-size: 14px; }
  </style>
</head>
<body>
  <div class="card">
    <h1>站点已停用</h1>
    <p>当前站点已被管理员暂停访问，请稍后再试或联系管理员。</p>
  </div>
</body>
</html>
`,
}

func (m *Module) pagesDir() string {
	return filepath.Join(filepath.Dir(m.confDir()), "pages")
}

func (m *Module) defaultServerConfPath() string {
	return filepath.Join(m.confDir(), "00-ezpanel-default.conf")
}

func (m *Module) ensureDefaultPages() error {
	dir := m.pagesDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0755)
	for kind, filename := range defaultPageFiles {
		path := filepath.Join(dir, filename)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if err := atomicWrite(path, []byte(defaultPageTemplates[kind]), 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Module) readDefaultPage(kind string) string {
	filename, ok := defaultPageFiles[kind]
	if !ok {
		return ""
	}
	path := filepath.Join(m.pagesDir(), filename)
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	return defaultPageTemplates[kind]
}

func (m *Module) defaultSiteID() string {
	if m.db == nil {
		return ""
	}
	var row store.Setting
	if err := m.db.First(&row, "key = ?", settingDefaultSiteID).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(row.Value)
}

func normalizeTLSProtocols(list []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(validTLSProtocols))
	for _, item := range list {
		v := strings.TrimSpace(item)
		if !slices.Contains(validTLSProtocols, v) {
			return nil, errors.New("不支持的 TLS 协议版本：" + v)
		}
		seen[v] = true
	}
	for _, proto := range validTLSProtocols {
		if seen[proto] {
			out = append(out, proto)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("至少保留一个 TLS 协议版本")
	}
	return out, nil
}

func (m *Module) tlsProtocols() []string {
	if m.db == nil {
		return append([]string{}, defaultTLSProtocols...)
	}
	var row store.Setting
	if err := m.db.First(&row, "key = ?", settingTLSProtocols).Error; err != nil {
		return append([]string{}, defaultTLSProtocols...)
	}
	if list, err := normalizeTLSProtocols(strings.Fields(row.Value)); err == nil {
		return list
	}
	return append([]string{}, defaultTLSProtocols...)
}

func (m *Module) tlsProtocolsString() string {
	return strings.Join(m.tlsProtocols(), " ")
}

func (m *Module) resolveDefaultSite(overrideID *string, excludedID string) *store.Website {
	targetID := m.defaultSiteID()
	if overrideID != nil {
		targetID = strings.TrimSpace(*overrideID)
	}
	if targetID == "" || targetID == excludedID {
		return nil
	}
	var site store.Website
	if err := m.db.First(&site, "id = ?", targetID).Error; err != nil || !site.Enabled {
		return nil
	}
	return &site
}

func (m *Module) renderDefaultServer(defaultSite *store.Website) string {
	hasDefault80 := false
	hasDefault443 := false
	if defaultSite != nil && defaultSite.Enabled {
		for _, srv := range siteServers(*defaultSite) {
			if srv.Port == 80 && !srv.SSL {
				hasDefault80 = true
			}
			if srv.Port == 443 && srv.SSL {
				hasDefault443 = true
			}
		}
	}
	var b strings.Builder
	b.WriteString("# 由 ezpanel 管理：默认站点兜底与 HTTPS 防窜站，请勿手工修改。\n")
	if !hasDefault80 {
		fmt.Fprintf(&b, `server {
    listen 80 default_server;
    server_name _;
    root %s;
    location / {
        return 404;
    }
    error_page 404 /not_found.html;
    location = /not_found.html {
        internal;
        root %s;
    }
}
`, m.pagesDir(), m.pagesDir())
	}
	if !hasDefault443 {
		b.WriteString(`server {
    listen 443 ssl http2 default_server;
    server_name _;
    ssl_reject_handshake on;
}
`)
	}
	return b.String()
}

func (m *Module) renderStopped(row store.Website) string {
	var b strings.Builder
	b.WriteString("# 由 ezpanel 管理（站点已停用），请通过面板修改。\n")
	tlsStr := m.tlsProtocolsString()
	for _, server := range siteServers(row) {
		fmt.Fprintf(&b, "server {\n    listen %d", server.Port)
		if server.SSL {
			b.WriteString(" ssl http2")
		}
		if slices.ContainsFunc(server.Hosts, func(host string) bool { return strings.HasPrefix(host, "[") }) {
			b.WriteString(";\n    listen [::]:" + strconv.Itoa(server.Port))
			if server.SSL {
				b.WriteString(" ssl http2")
			}
		}
		fmt.Fprintf(&b, ";\n    server_name %s;\n", strings.Join(server.Hosts, " "))
		if server.SSL {
			cert, key := m.certPaths(row.ID)
			fmt.Fprintf(&b, "    ssl_certificate \"%s\";\n    ssl_certificate_key \"%s\";\n    ssl_protocols %s;\n", cert, key, tlsStr)
		}
		fmt.Fprintf(&b, "    root %s;\n    location / {\n        return 503;\n    }\n    error_page 503 /stopped.html;\n    location = /stopped.html {\n        internal;\n        root %s;\n    }\n}\n", m.pagesDir(), m.pagesDir())
	}
	return b.String()
}

func (m *Module) applyAllConfigs(ctx context.Context, defaultIDOverride *string, commit func() error) error {
	return m.packages.ApplicationChange("openresty", func() error {
		state, e := m.packages.Status(ctx, "openresty")
		if e != nil {
			return e
		}
		if !state.Installed || !state.Manageable {
			return pkgmgr.ErrUnsupported
		}
		if err := m.ensureDefaultPages(); err != nil {
			return errors.New("默认页面目录初始化失败")
		}
		var rows []store.Website
		if err := m.db.Order("id ASC").Find(&rows).Error; err != nil {
			return errStorage
		}
		defaultSite := m.resolveDefaultSite(defaultIDOverride, "")
		defID := ""
		if defaultSite != nil {
			defID = defaultSite.ID
		}
		changes := map[string]fileChange{
			m.defaultServerConfPath(): {[]byte(m.renderDefaultServer(defaultSite)), 0644, true},
		}
		for _, row := range rows {
			changes[m.confPath(row.ID, false)] = fileChange{}
			if row.Enabled {
				changes[m.confPath(row.ID, true)] = fileChange{[]byte(m.renderWithOptions(row, row.ID == defID)), 0640, true}
			} else {
				changes[m.confPath(row.ID, true)] = fileChange{[]byte(m.renderStopped(row)), 0640, true}
			}
		}
		paths := make([]string, 0, len(changes))
		old := make(map[string]fileChange, len(changes))
		for path := range changes {
			paths = append(paths, path)
			if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
				if data, err := os.ReadFile(path); err == nil {
					old[path] = fileChange{data, info.Mode().Perm(), true}
				}
			}
		}
		sort.Strings(paths)
		running := state.Running != nil && *state.Running
		restore := func(cause error) error {
			for _, p := range paths {
				_ = applyFile(p, old[p])
			}
			recovery, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if running {
				_ = m.reload(recovery)
			}
			return cause
		}
		for _, p := range paths {
			if err := applyFile(p, changes[p]); err != nil {
				return restore(errors.New("写入 OpenResty 配置失败"))
			}
		}
		if err := m.openRestyTest(ctx); err != nil {
			return restore(err)
		}
		if err := commit(); err != nil {
			return restore(err)
		}
		if running {
			if err := m.reload(ctx); err != nil {
				return restore(err)
			}
		}
		return nil
	})
}

func (m *Module) getAdvanced(c *gin.Context) {
	_ = m.ensureDefaultPages()
	pages := make(map[string]string, len(defaultPageFiles))
	for kind := range defaultPageFiles {
		pages[kind] = m.readDefaultPage(kind)
	}
	var sites []store.Website
	if err := m.db.Select("id", "name", "domains", "enabled", "ssl_enabled").Order("name ASC").Find(&sites).Error; err != nil {
		failure(c, errStorage)
		return
	}
	siteOptions := make([]gin.H, 0, len(sites))
	for _, s := range sites {
		siteOptions = append(siteOptions, gin.H{
			"id":         s.ID,
			"name":       s.Name,
			"enabled":    s.Enabled,
			"sslEnabled": s.SSLEnabled,
		})
	}
	response.OK(c, gin.H{
		"pages":         pages,
		"defaultSiteId": m.defaultSiteID(),
		"tlsProtocols":  m.tlsProtocols(),
		"sites":         siteOptions,
	})
}

func (m *Module) saveDefaultPage(c *gin.Context) {
	kind := c.Param("kind")
	filename, ok := defaultPageFiles[kind]
	if !ok {
		response.Fail(c, response.Invalid, "默认页面类型无效")
		return
	}
	var in struct {
		Content string `json:"content"`
		Reset   bool   `json:"reset"`
	}
	if !response.Bind(c, &in) {
		return
	}
	content := in.Content
	if in.Reset {
		content = defaultPageTemplates[kind]
	}
	if len(content) > 256<<10 || strings.ContainsRune(content, 0) {
		response.Fail(c, response.Invalid, "页面内容不能超过 256KiB 或包含空字符")
		return
	}
	if err := m.ensureDefaultPages(); err != nil {
		failure(c, errors.New("默认页面目录创建失败"))
		return
	}
	if err := atomicWrite(filepath.Join(m.pagesDir(), filename), []byte(content), 0644); err != nil {
		failure(c, errors.New("默认页面保存失败"))
		return
	}
	response.OK(c, gin.H{"kind": kind, "content": content})
}

func (m *Module) saveDefaultSite(c *gin.Context) {
	var in struct {
		SiteID string `json:"siteId"`
	}
	if !response.Bind(c, &in) {
		return
	}
	siteID := strings.TrimSpace(in.SiteID)
	if siteID != "" {
		var site store.Website
		if err := m.db.First(&site, "id = ?", siteID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				response.Fail(c, response.NotFound, "所选站点不存在")
				return
			}
			failure(c, errStorage)
			return
		}
		if !site.Enabled {
			response.Fail(c, response.Invalid, "已停用的站点不能设为默认站点，请先启用该站点")
			return
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.applyAllConfigs(c.Request.Context(), &siteID, func() error {
		return m.db.Save(&store.Setting{Key: settingDefaultSiteID, Value: siteID}).Error
	}); err != nil {
		failure(c, err)
		return
	}
	response.OK(c, gin.H{"defaultSiteId": siteID})
}

func (m *Module) saveTLSProtocols(c *gin.Context) {
	var in struct {
		Protocols []string `json:"protocols"`
	}
	if !response.Bind(c, &in) {
		return
	}
	protocols, err := normalizeTLSProtocols(in.Protocols)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	joined := strings.Join(protocols, " ")
	m.mu.Lock()
	defer m.mu.Unlock()
	oldVal := m.tlsProtocolsString()
	if err := m.db.Save(&store.Setting{Key: settingTLSProtocols, Value: joined}).Error; err != nil {
		failure(c, errStorage)
		return
	}
	if err := m.applyAllConfigs(c.Request.Context(), nil, func() error { return nil }); err != nil {
		_ = m.db.Save(&store.Setting{Key: settingTLSProtocols, Value: oldVal}).Error
		failure(c, err)
		return
	}
	response.OK(c, gin.H{"tlsProtocols": protocols})
}
