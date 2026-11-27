package plugin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

const frameImportMap = `{"imports":{"react":"/shared/v1/0.js","react/jsx-runtime":"/shared/v1/1.js","react/jsx-dev-runtime":"/shared/v1/2.js","react-dom":"/shared/v1/3.js","react-dom/client":"/shared/v1/4.js","antd":"/shared/v1/5.js","@ant-design/icons":"/shared/v1/6.js"}}`
const isolatedDocument = "sandbox; default-src 'none'; frame-ancestors 'none'"

func (m *Module) FrameRoutes(g *gin.Engine) {
	g.GET(auth.PluginFramePath, m.frame)
	g.HEAD(auth.PluginFramePath, m.frame)
}

func (m *Module) frameMAC(id, version, expires string) []byte {
	mac := hmac.New(sha256.New, []byte(m.cfg.JWTSecret))
	_, _ = fmt.Fprintf(mac, "plugin-frame\n%s\n%s\n%s", id, version, expires)
	return mac.Sum(nil)
}

func (m *Module) frameSession(c *gin.Context) {
	p, ok := m.row(c)
	if !ok {
		return
	}
	var manifest Manifest
	if !p.Enabled || p.Operation != nil || m.status(p) != "running" || json.Unmarshal([]byte(p.Manifest), &manifest) != nil || manifest.UIMode == "legacy" {
		response.Fail(c, response.Conflict, "插件隔离页面暂不可用")
		return
	}
	expires := strconv.FormatInt(time.Now().Add(12*time.Hour).Unix(), 10)
	key := hex.EncodeToString(m.frameMAC(p.ID, p.Version, expires))
	response.OK(c, gin.H{"url": fmt.Sprintf("/plugin-frame/%s/%s/%s/%s/", p.ID, p.Version, expires, key)})
}

func (m *Module) frame(c *gin.Context) {
	id, version, expires := c.Param("id"), c.Param("version"), c.Param("expires")
	deadline, err := strconv.ParseInt(expires, 10, 64)
	key, keyErr := hex.DecodeString(c.Param("signature"))
	if err != nil || keyErr != nil || deadline <= time.Now().Unix() || !hmac.Equal(key, m.frameMAC(id, version, expires)) {
		c.Status(http.StatusNotFound)
		return
	}
	var p store.Plugin
	var manifest Manifest
	if m.db.Select("version, manifest").Take(&p, "id = ? AND enabled = ? AND operation IS NULL", id, true).Error != nil ||
		p.Version != version || m.statusByID(id) != "running" || json.Unmarshal([]byte(p.Manifest), &manifest) != nil || manifest.UIMode == "legacy" {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	asset := c.Param("asset")
	if name, ok := strings.CutPrefix(asset, "/assets/"); ok {
		c.Header("Access-Control-Allow-Origin", "null")
		c.Header("Content-Security-Policy", isolatedDocument)
		m.serveAsset(c, id, name)
		return
	}
	if asset != "/" {
		c.Status(http.StatusNotFound)
		return
	}
	hash := sha256.Sum256([]byte(frameImportMap))
	c.Header("Content-Security-Policy", fmt.Sprintf("sandbox allow-scripts; default-src 'none'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'; script-src 'self' 'sha256-%s'; style-src 'self' 'unsafe-inline'; font-src 'self' data:; img-src 'self' data: blob:", base64.StdEncoding.EncodeToString(hash[:])))
	page := `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>插件</title>` +
		`<script type="importmap">` + frameImportMap + `</script></head><body style="margin:0">` +
		`<div id="root" data-plugin="` + html.EscapeString(id) + `" data-entry="` + html.EscapeString(strings.TrimPrefix(manifest.UI, "web/")) + `"></div>` +
		`<script type="module" src="/assets/plugin-frame.js" crossorigin="anonymous"></script></body></html>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(page))
}

func (m *Module) setUIIsolation(c *gin.Context) {
	release, err := m.tasks.Acquire("plugin:" + c.Param("id"))
	if err != nil {
		response.Fail(c, response.Conflict, "插件忙")
		return
	}
	defer release()
	p, ok := m.row(c)
	if !ok {
		return
	}
	if p.Operation != nil {
		response.Fail(c, response.Conflict, "插件存在未恢复的操作记录")
		return
	}
	var in struct {
		Isolated bool `json:"isolated"`
	}
	if !response.Bind(c, &in) {
		return
	}
	var manifest Manifest
	if json.Unmarshal([]byte(p.Manifest), &manifest) != nil {
		response.Fail(c, response.System, "插件清单无效")
		return
	}
	if in.Isolated {
		manifest.UIMode = "isolated"
	} else {
		manifest.UIMode = "legacy"
	}
	data, _ := json.Marshal(manifest)
	if err := m.db.Model(&p).Update("manifest", string(data)).Error; err != nil {
		response.Fail(c, response.System, "保存隔离设置失败")
		return
	}
	response.OK(c, gin.H{"uiMode": manifest.UIMode})
}

func (m *Module) frameTask(c *gin.Context) {
	var row store.Task
	if err := m.db.Select("id").Take(&row, "id = ? AND module = ?", c.Param("task"), "plugin:"+c.Param("id")).Error; err != nil {
		response.Fail(c, response.NotFound, "插件任务不存在")
		return
	}
	response.OK(c, nil)
}
