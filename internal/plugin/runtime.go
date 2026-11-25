package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

type running struct {
	process      *executor.Process
	token, proxy string
	port         int
	cancel       context.CancelFunc
	ctx          context.Context
	log          *pluginLog
	budget       float64
	lastRequest  time.Time
}
type pluginLog struct {
	mu      sync.Mutex
	path    string
	secrets []string
	pending []byte
	discard bool
}

func (l *pluginLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range b {
		if c == '\n' {
			if e := l.flush(); e != nil {
				return 0, e
			}
		} else if !l.discard {
			if len(l.pending) >= 64<<10 {
				l.pending = nil
				l.discard = true
			} else {
				l.pending = append(l.pending, c)
			}
		}
	}
	return len(b), nil
}
func (l *pluginLog) flush() error {
	msg := "[插件日志行超过 64KiB，已丢弃]"
	if !l.discard {
		msg = redact(string(l.pending), l.secrets)
	}
	l.pending = nil
	l.discard = false
	msg += "\n"
	if f, e := os.Stat(l.path); e == nil && f.Size()+int64(len(msg)) > 5<<20 {
		if e = os.Remove(l.path + ".2"); e != nil && !os.IsNotExist(e) {
			return e
		}
		if e = os.Rename(l.path+".1", l.path+".2"); e != nil && !os.IsNotExist(e) {
			return e
		}
		if e = os.Rename(l.path, l.path+".1"); e != nil {
			return e
		}
	}
	f, e := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.WriteString(msg)
	closeErr := f.Close()
	if e != nil {
		return e
	}
	return closeErr
}
func (l *pluginLog) finish() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.pending) > 0 || l.discard {
		l.flush()
	}
}
func token() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func (m *Module) setStatus(id, s string) { m.mu.Lock(); m.statuses[id] = s; m.mu.Unlock() }
func (m *Module) start(p store.Plugin) error {
	select {
	case <-m.closed:
		return errors.New("插件管理器已关闭")
	default:
	}
	if p.Type == "static" {
		m.setStatus(p.ID, "running")
		return nil
	}
	var mf Manifest
	var values map[string]string
	if json.Unmarshal([]byte(p.Manifest), &mf) != nil || json.Unmarshal([]byte(p.Config), &values) != nil {
		return errors.New("插件存储配置损坏")
	}
	for _, v := range mf.Env {
		if v.Required && values[v.Key] == "" {
			return errors.New("缺少必填配置")
		}
	}
	uid, gid := os.Getuid(), os.Getgid()
	if runtime.GOOS == "linux" {
		u, e := user.Lookup("panel-plugin")
		if e != nil {
			return errors.New("缺少 panel-plugin 无登录用户，请修复安装环境")
		}
		uid, e = strconv.Atoi(u.Uid)
		if e != nil {
			return e
		}
		gid, e = strconv.Atoi(u.Gid)
		if e != nil {
			return e
		}
	} else if !m.cfg.Dev {
		return errors.New("非 Linux 只支持 dev")
	}
	data := filepath.Join(m.private(), p.ID)
	if e := os.MkdirAll(filepath.Join(data, "logs"), 0700); e != nil {
		return e
	}
	if runtime.GOOS == "linux" {
		for _, dir := range []string{data, filepath.Join(data, "logs")} {
			if e := os.Chown(dir, uid, gid); e != nil {
				return e
			}
			if e := os.Chmod(dir, 0700); e != nil {
				return e
			}
		}
	}
	hostToken, e := token()
	if e != nil {
		return e
	}
	proxy, e := token()
	if e != nil {
		return e
	}
	port := 0
	var seed [2]byte
	if _, e = rand.Read(seed[:]); e != nil {
		return e
	}
	offset := (int(seed[0])<<8 | int(seed[1])) % 10000
	for i := 0; i < 10000; i++ {
		candidate := 20000 + (offset+i)%10000
		l, e := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", candidate))
		if e == nil {
			l.Close()
			port = candidate
			break
		}
	}
	if port == 0 {
		return errors.New("没有可用插件端口")
	}
	env := append(executor.DefaultEnv(), "HOME="+data, "PANEL_PLUGIN_ID="+p.ID, "PANEL_PLUGIN_PORT="+strconv.Itoa(port), "PANEL_PLUGIN_DATA="+data, "PANEL_HOST_URL="+m.hostURL, "PANEL_PLUGIN_TOKEN="+hostToken, "PANEL_PROXY_TOKEN="+proxy)
	secrets := []string{m.cfg.JWTSecret, m.cfg.AESKey, m.cfg.Entry, hostToken, proxy}
	for _, v := range mf.Env {
		value := values[v.Key]
		if v.Secret && value != "" {
			value, e = store.Decrypt(m.cfg.AESKey, value)
			if e != nil {
				return e
			}
			secrets = append(secrets, value)
		}
		env = append(env, v.Key+"="+value)
	}
	logger := &pluginLog{path: filepath.Join(data, "logs/plugin.log"), secrets: secrets}
	arch := "linux-" + runtime.GOARCH
	if m.cfg.Dev {
		arch = runtime.GOOS + "-" + runtime.GOARCH
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{token: hostToken, proxy: proxy, port: port, cancel: cancel, ctx: ctx, log: logger, budget: 20, lastRequest: time.Now()}
	m.mu.Lock()
	if m.runs[p.ID] != nil {
		m.mu.Unlock()
		cancel()
		return errors.New("插件已有进程")
	}
	m.generations[p.ID]++
	m.runs[p.ID] = r
	m.statuses[p.ID] = "starting"
	m.mu.Unlock()
	proc, e := executor.StartProcess(executor.ProcessOptions{Path: filepath.Join(m.packages(), p.ID, mf.Entry[arch]), Dir: data, Env: env, UID: uint32(uid), GID: uint32(gid), Dev: m.cfg.Dev, Output: logger})
	if e != nil {
		cancel()
		m.mu.Lock()
		delete(m.runs, p.ID)
		m.statuses[p.ID] = "failed"
		m.mu.Unlock()
		return errors.New("插件进程启动失败")
	}
	m.mu.Lock()
	if m.runs[p.ID] != r {
		m.mu.Unlock()
		proc.Stop()
		return errors.New("插件启动已取消")
	}
	r.process = proc
	m.mu.Unlock()
	client := &http.Client{Timeout: 400 * time.Millisecond, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-proc.Done():
			m.stop(p.ID)
			return errors.New("插件就绪前退出")
		case <-deadline.C:
			m.stop(p.ID)
			return errors.New("插件健康检查超时")
		case <-tick.C:
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/health", port), nil)
			req.Header.Set("X-Panel-Proxy-Token", proxy)
			res, err := client.Do(req)
			if err == nil {
				io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
				res.Body.Close()
				if res.StatusCode == 200 {
					m.mu.Lock()
					stillCurrent := m.runs[p.ID] == r
					if stillCurrent {
						m.statuses[p.ID] = "running"
					}
					m.mu.Unlock()
					if !stillCurrent {
						return errors.New("插件启动过程中已被停止")
					}
					go m.watch(p.ID, r)
					return nil
				}
			}
		}
	}
}
func (m *Module) stop(id string) {
	m.mu.Lock()
	r := m.runs[id]
	m.generations[id]++
	delete(m.runs, id)
	m.statuses[id] = "stopping"
	m.mu.Unlock()
	if r != nil {
		r.cancel()
		m.mu.Lock()
		proc := r.process
		m.mu.Unlock()
		if proc != nil {
			proc.Stop()
		}
		r.log.finish()
	}
	m.setStatus(id, "disabled")
}
func (m *Module) watch(id string, r *running) {
	select {
	case <-r.process.Done():
	case <-m.closed:
		return
	}
	m.mu.Lock()
	if m.runs[id] != r {
		m.mu.Unlock()
		return
	}
	r.cancel()
	generation := m.generations[id]
	delete(m.runs, id)
	m.statuses[id] = "crashed"
	m.mu.Unlock()
	r.process.Stop()
	r.log.finish()
	for {
		m.mu.Lock()
		now := time.Now()
		history := m.crashes[id][:0]
		for _, t := range m.crashes[id] {
			if now.Sub(t) < 10*time.Minute {
				history = append(history, t)
			}
		}
		history = append(history, now)
		m.crashes[id] = history
		attempt := len(history)
		m.mu.Unlock()
		if attempt >= 5 {
			m.setStatus(id, "failed")
			return
		}
		timer := time.NewTimer(min(time.Second*time.Duration(1<<(attempt-1)), 30*time.Second))
		select {
		case <-m.closed:
			timer.Stop()
			return
		case <-timer.C:
		}
		release, e := m.tasks.Acquire("plugin:" + id)
		if e != nil {
			return
		}
		m.mu.Lock()
		valid := m.generations[id] == generation && m.runs[id] == nil
		m.mu.Unlock()
		if !valid {
			release()
			return
		}
		var p store.Plugin
		if m.db.First(&p, "id = ?", id).Error != nil || !p.Enabled {
			release()
			return
		}
		e = m.start(p)
		// 仍持有资源锁，只接续本次失败启动产生的代次；外部操作之后的代次不接受。
		if e != nil {
			m.mu.Lock()
			generation = m.generations[id]
			m.mu.Unlock()
		}
		release()
		if e == nil {
			return
		}
		m.setStatus(id, "crashed")
	}
}
func (m *Module) Close() {
	m.closeOnce.Do(func() {
		close(m.closed)
		if m.host != nil {
			m.host.Close()
		}
		m.mu.Lock()
		ids := make([]string, 0, len(m.runs))
		for id := range m.runs {
			ids = append(ids, id)
		}
		m.mu.Unlock()
		for _, id := range ids {
			m.stop(id)
		}
	})
}
func (m *Module) lifecycle(c *gin.Context, action string) {
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
	if action == "restart" && (p.Type != "process" || !p.Enabled) {
		response.Fail(c, response.Invalid, "只有已启用 process 可以重启")
		return
	}
	if action == "disable" {
		m.stop(p.ID)
		if m.db.Model(&p).Updates(map[string]any{"enabled": false, "updated_at": time.Now().UTC()}).Error != nil {
			response.Fail(c, 5000, "")
			return
		}
		response.OK(c, gin.H{"runtimeStatus": "disabled"})
		return
	}
	var mf Manifest
	var values map[string]string
	if json.Unmarshal([]byte(p.Manifest), &mf) != nil || json.Unmarshal([]byte(p.Config), &values) != nil {
		response.Fail(c, 5000, "")
		return
	}
	for _, v := range mf.Env {
		if v.Required && values[v.Key] == "" {
			response.Fail(c, response.Invalid, "缺少必填配置："+v.Key)
			return
		}
	}
	m.stop(p.ID)
	if e = m.start(p); e != nil {
		if action == "enable" {
			m.db.Model(&p).Update("enabled", false)
		}
		m.setStatus(p.ID, "failed")
		response.Fail(c, 3003, e.Error())
		return
	}
	if e = m.db.Model(&p).Updates(map[string]any{"enabled": true, "updated_at": time.Now().UTC()}).Error; e != nil {
		m.stop(p.ID)
		response.Fail(c, 5000, "")
		return
	}
	m.mu.Lock()
	delete(m.crashes, p.ID)
	m.mu.Unlock()
	response.OK(c, gin.H{"runtimeStatus": "running"})
}

func redact(s string, secrets []string) string {
	for _, v := range secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
			for _, line := range strings.Split(v, "\n") {
				line = strings.TrimSuffix(line, "\r")
				if line != "" {
					s = strings.ReplaceAll(s, line, "[REDACTED]")
				}
			}
		}
	}
	return strings.ToValidUTF8(s, "�")
}
