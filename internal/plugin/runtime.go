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
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
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
	lastNotice   time.Time
	backupSlots  chan struct{}
	jobLogs      map[string]*task.Log
}
type pluginLog struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	size    int64
	secrets []string
	pending []byte
	discard bool
}

func (l *pluginLog) secretValues() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.secrets)
}

func (l *pluginLog) addSecret(value string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !slices.Contains(l.secrets, value) {
		l.secrets = append(l.secrets, value)
	}
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
	if l.file == nil {
		if info, e := os.Stat(l.path); e == nil {
			l.size = info.Size()
		} else {
			l.size = 0
		}
	}
	if l.size+int64(len(msg)) > 5<<20 {
		if l.file != nil {
			_ = l.file.Close()
			l.file = nil
		}
		if e := os.Remove(l.path + ".2"); e != nil && !os.IsNotExist(e) {
			return e
		}
		if e := os.Rename(l.path+".1", l.path+".2"); e != nil && !os.IsNotExist(e) {
			return e
		}
		if e := os.Rename(l.path, l.path+".1"); e != nil && !os.IsNotExist(e) {
			return e
		}
		l.size = 0
	}
	if l.file == nil {
		f, e := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		l.file = f
	}
	n, e := l.file.WriteString(msg)
	l.size += int64(n)
	if e != nil {
		_ = l.file.Close()
		l.file = nil
		return e
	}
	return nil
}
func (l *pluginLog) finish() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.pending) > 0 || l.discard {
		_ = l.flush()
	}
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}
func token() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func (m *Module) setStatus(id, s string) { m.bootStatus(id, s, nil) }
func (m *Module) start(p store.Plugin) error {
	select {
	case <-m.closed:
		return errors.New("插件管理器已关闭")
	default:
	}
	var mf Manifest
	var values map[string]string
	if json.Unmarshal([]byte(p.Manifest), &mf) != nil || json.Unmarshal([]byte(p.Config), &values) != nil {
		return errors.New("插件存储配置损坏")
	}
	if err := m.checkDependencies(mf, true); err != nil {
		return err
	}
	if p.Type == "static" {
		m.setStatus(p.ID, "running")
		return nil
	}
	for _, v := range mf.Env {
		if v.Required && values[v.Key] == "" {
			return errors.New("缺少必填配置")
		}
	}
	uid, gid := os.Getuid(), os.Getgid()
	if mf.Sandbox != nil && runtime.GOOS != "linux" {
		return errors.New("插件沙箱仅支持 Linux systemd")
	}
	if runtime.GOOS == "linux" {
		u, e := user.Lookup("panel-plugin")
		if e != nil {
			return errors.New("缺少 panel-plugin 无登录用户，请修复安装环境")
		}
		uid, e = strconv.Atoi(u.Uid)
		if e != nil {
			return e
		}
		if mf.Sandbox != nil {
			uid, e = m.sandboxUID(p.ID)
			if e != nil {
				return e
			}
		}
		gid, e = strconv.Atoi(u.Gid)
		if e != nil {
			return e
		}
		if mf.Sandbox != nil {
			gid = uid
		}
	} else if !m.cfg.Dev {
		return errors.New("非 Linux 只支持 dev")
	}
	data := filepath.Join(m.private(), p.ID)
	if runtime.GOOS == "linux" {
		if e := preparePluginData(data, uid, gid); e != nil {
			return e
		}
	} else if e := os.MkdirAll(data, 0700); e != nil {
		return e
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
	logger := &pluginLog{path: m.logPath(p.ID), secrets: secrets}
	arch := "linux-" + runtime.GOARCH
	if m.cfg.Dev {
		arch = runtime.GOOS + "-" + runtime.GOARCH
	}
	options := executor.ProcessOptions{Path: filepath.Join(m.packages(), p.ID, mf.Entry[arch]), Dir: data, Env: env, UID: uint32(uid), GID: uint32(gid), Dev: m.cfg.Dev, Output: logger}
	if mf.Sandbox != nil {
		var cleanup func()
		options, cleanup, e = m.sandboxOptions(p.ID, port, *mf.Sandbox, options)
		if e != nil {
			return e
		}
		defer cleanup()
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{token: hostToken, proxy: proxy, port: port, cancel: cancel, ctx: ctx, log: logger, budget: 20, lastRequest: time.Now()}
	if mf.BackupProvider != nil {
		r.backupSlots = make(chan struct{}, 2)
	}
	m.mu.Lock()
	select {
	case <-m.closed:
		m.mu.Unlock()
		cancel()
		return errors.New("插件管理器已关闭")
	default:
	}
	if m.runs[p.ID] != nil {
		m.mu.Unlock()
		cancel()
		return errors.New("插件已有进程")
	}
	m.generations[p.ID]++
	m.runs[p.ID] = r
	m.statuses[p.ID] = "starting"
	m.mu.Unlock()
	proc, e := executor.StartProcess(options)
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
		case <-ctx.Done():
			return errors.New("插件启动已取消")
		case <-proc.Done():
			m.stop(p.ID)
			if err := proc.Wait(); err != nil {
				return fmt.Errorf("插件就绪前退出：%w", err)
			}
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
						delete(m.startupErrors, p.ID)
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
	m.statuses[id] = "crashed"
	m.mu.Unlock()
	r.process.Stop()
	r.log.finish()
	m.mu.Lock()
	if m.runs[id] != r {
		m.mu.Unlock()
		return
	}
	generation := m.generations[id]
	delete(m.runs, id)
	m.mu.Unlock()
	for {
		m.mu.Lock()
		if m.generations[id] != generation {
			m.mu.Unlock()
			return
		}
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
		if attempt >= 5 {
			m.statuses[id] = "failed"
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		release, e := m.waitRestartLock(id, min(time.Second*time.Duration(1<<(attempt-1)), 30*time.Second))
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
		if m.db.First(&p, "id = ?", id).Error != nil || !p.Enabled || p.Operation != nil {
			release()
			return
		}
		e = m.start(p)
		// 仍持有资源锁，只接续本次失败启动产生的代次；外部操作之后的代次不接受。
		if e != nil {
			m.mu.Lock()
			generation = m.generations[id]
			m.statuses[id] = "crashed"
			m.mu.Unlock()
		}
		release()
		if e == nil {
			m.notifyStartup()
			return
		}
	}
}

func (m *Module) waitRestartLock(id string, delay time.Duration) (func(), error) {
	for {
		timer := time.NewTimer(delay)
		select {
		case <-m.closed:
			timer.Stop()
			return nil, errors.New("插件管理器已关闭")
		case <-timer.C:
		}
		release, err := m.tasks.Acquire("plugin:" + id)
		if !errors.Is(err, task.ErrBusy) {
			return release, err
		}
		delay = time.Second
	}
}
func (m *Module) Close() {
	m.closeOnce.Do(func() {
		close(m.closed)
		m.scheduleMu.Lock()
		for _, stop := range m.schedules {
			stop()
		}
		clear(m.schedules)
		m.scheduleMu.Unlock()
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
		m.background.Wait()
	})
}
func (m *Module) lifecycle(c *gin.Context, action string) {
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
	if action == "enable" && p.Enabled && m.status(p) == "running" {
		response.OK(c, gin.H{"runtimeStatus": "running"})
		return
	}
	if action == "restart" && (p.Type != "process" || !p.Enabled) {
		response.Fail(c, response.Invalid, "只有已启用 process 可以重启")
		return
	}
	if action == "disable" {
		if err := m.checkDependents(p.ID, ""); err != nil {
			response.Fail(c, response.Conflict, err.Error())
			return
		}
		if m.db.Model(&p).Updates(map[string]any{"enabled": false, "updated_at": time.Now().UTC()}).Error != nil {
			response.Fail(c, 5000, "")
			return
		}
		m.stop(p.ID)
		m.notifyStartup()
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
	m.notifyStartup()
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
