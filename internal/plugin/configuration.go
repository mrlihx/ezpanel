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
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func validateEnvField(v Env) error {
	switch v.Type {
	case "", "text", "password", "number", "boolean", "file", "directory":
	case "select":
		if len(v.Options) == 0 || len(v.Options) > 50 {
			return errors.New("下拉字段需要 1 至 50 个选项：" + v.Key)
		}
	default:
		return errors.New("未知配置字段类型：" + v.Key)
	}
	if len(v.Help) > 512 || v.Secret && v.Type != "" && v.Type != "text" && v.Type != "password" {
		return errors.New("配置字段说明或秘密类型无效：" + v.Key)
	}
	return validateEnvValue(v, v.Default)
}

func validateEnvValue(v Env, value string) error {
	if value == "" {
		return nil
	}
	valid := true
	switch v.Type {
	case "number":
		_, err := strconv.ParseInt(value, 10, 64)
		valid = err == nil
	case "boolean":
		valid = value == "true" || value == "false"
	case "select":
		valid = slices.Contains(v.Options, value)
	case "file", "directory":
		valid = filepath.IsAbs(value) && filepath.Clean(value) == value
	}
	if !valid {
		return errors.New("配置字段值不符合类型要求：" + v.Key)
	}
	return nil
}

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
		if err := validateEnvValue(v, value); err != nil {
			return nil, err
		}
		if key == webhookSecretKey && value != "" && len(value) < 32 && slices.Contains(mf.Permissions, "jobs.webhook") {
			return nil, errors.New("Webhook 密钥至少需要 32 字节")
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
	release, e := m.tasks.Acquire("plugin:" + c.Param("id"))
	if e != nil {
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
	if string(cfg) == before {
		response.OK(c, gin.H{"runtimeStatus": m.status(p)})
		return
	}
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
	path := m.logPath(p.ID)
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
	content := string(b)
	parts := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	truncated := offset > 0 || len(parts) > lines
	if len(parts) > lines {
		tail := strings.Join(parts[len(parts)-lines:], "\n")
		if strings.HasSuffix(content, "\n") {
			tail += "\n"
		}
		content = tail
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
	response.OK(c, gin.H{"content": redact(content, secrets), "truncated": truncated})
}
