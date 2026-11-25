package plugin

import (
	"encoding/json"
	"errors"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (m *Module) candidate(mf Manifest, old map[string]string, values map[string]string, required bool) (map[string]string, error) {
	out := map[string]string{}
	allowed := map[string]Env{}
	for _, v := range mf.Env {
		allowed[v.Key] = v
		value, exists := old[v.Key]
		if !exists {
			value = v.Default
			if v.Secret && value != "" {
				var e error
				value, e = store.Encrypt(m.cfg.AESKey, value)
				if e != nil {
					return nil, e
				}
			}
		}
		out[v.Key] = value
	}
	for key, value := range values {
		v, ok := allowed[key]
		if !ok || len(value) > 8192 || strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
			return nil, errors.New("未知配置键或值无效")
		}
		if v.Secret && value != "" {
			var e error
			value, e = store.Encrypt(m.cfg.AESKey, value)
			if e != nil {
				return nil, e
			}
		}
		out[key] = value
	}
	if required {
		for _, v := range mf.Env {
			if v.Required && out[v.Key] == "" {
				return nil, errors.New("缺少必填配置：" + v.Key)
			}
		}
	}
	return out, nil
}
func (m *Module) configure(c *gin.Context) {
	var in struct {
		Values map[string]string `json:"values"`
	}
	if !response.Bind(c, &in) {
		return
	}
	p, ok := m.row(c)
	if !ok {
		return
	}
	release, e := m.tasks.Acquire("plugin:" + p.ID)
	if e != nil {
		response.Fail(c, response.Conflict, "插件忙")
		return
	}
	defer release()
	var mf Manifest
	var old map[string]string
	if json.Unmarshal([]byte(p.Manifest), &mf) != nil || json.Unmarshal([]byte(p.Config), &old) != nil {
		response.Fail(c, 5000, "")
		return
	}
	next, e := m.candidate(mf, old, in.Values, true)
	if e != nil {
		response.Fail(c, response.Invalid, e.Error())
		return
	}
	cfg, _ := json.Marshal(next)
	before := p.Config
	running := p.Enabled
	if running {
		m.stop(p.ID)
	}
	p.Config = string(cfg)
	if e = m.db.Model(&p).Updates(map[string]any{"config": p.Config, "updated_at": time.Now().UTC()}).Error; e == nil && running {
		e = m.start(p)
	}
	if e != nil {
		m.stop(p.ID)
		p.Config = before
		restore := m.db.Model(&p).Update("config", before).Error
		if restore == nil && running {
			restore = m.start(p)
		}
		if restore != nil {
			m.setStatus(p.ID, "failed")
			response.Fail(c, 3003, "配置更新失败，旧配置恢复失败")
			return
		}
		response.Fail(c, 3003, "配置更新失败，已恢复旧配置")
		return
	}
	response.OK(c, gin.H{"runtimeStatus": m.status(p)})
}
func (m *Module) logs(c *gin.Context) {
	p, ok := m.row(c)
	if !ok {
		return
	}
	lines := 200
	if q, exists := c.GetQuery("lines"); exists {
		var e error
		lines, e = strconv.Atoi(q)
		if e != nil || lines < 1 || lines > 1000 {
			response.Fail(c, response.Invalid, "lines 必须为 1-1000")
			return
		}
	}
	path := filepath.Join(m.private(), p.ID, "logs/plugin.log")
	f, e := os.Open(path)
	if os.IsNotExist(e) {
		response.OK(c, gin.H{"content": "", "truncated": false})
		return
	}
	if e != nil {
		response.Fail(c, 5000, "")
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		response.Fail(c, 5000, "")
		return
	}
	offset := max(info.Size()-(5<<20), 0)
	if _, e = f.Seek(offset, 0); e != nil {
		response.Fail(c, 5000, "")
		return
	}
	b, e := io.ReadAll(io.LimitReader(f, 5<<20))
	if e != nil {
		response.Fail(c, 5000, "")
		return
	}
	parts := strings.Split(string(b), "\n")
	truncated := offset > 0 || len(parts) > lines
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	secrets := []string{m.cfg.JWTSecret, m.cfg.AESKey, m.cfg.Entry}
	var mf Manifest
	var vals map[string]string
	if json.Unmarshal([]byte(p.Manifest), &mf) != nil || json.Unmarshal([]byte(p.Config), &vals) != nil {
		response.Fail(c, 5000, "")
		return
	}
	for _, v := range mf.Env {
		if v.Secret && vals[v.Key] != "" {
			value, err := store.Decrypt(m.cfg.AESKey, vals[v.Key])
			if err != nil {
				response.Fail(c, 5000, "")
				return
			}
			secrets = append(secrets, value)
		}
	}
	m.mu.Lock()
	if r := m.runs[p.ID]; r != nil {
		secrets = append(secrets, r.token, r.proxy)
	}
	m.mu.Unlock()
	response.OK(c, gin.H{"content": redact(strings.Join(parts, "\n"), secrets), "truncated": truncated})
}
