package terminal

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

type Module struct {
	ctx    context.Context
	auth   *auth.Service
	db     *gorm.DB
	key    string
	mu     sync.Mutex
	active map[string]int
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed bool
}

func New(ctx context.Context, db *gorm.DB, a *auth.Service, key string) *Module {
	ctx, cancel := context.WithCancel(ctx)
	return &Module{ctx: ctx, cancel: cancel, db: db, auth: a, key: key, active: make(map[string]int)}
}
func (m *Module) Close()       { m.mu.Lock(); m.closed = true; m.cancel(); m.mu.Unlock(); m.wg.Wait() }
func (m *Module) Name() string { return "terminal" }
func (m *Module) Routes(g *gin.RouterGroup) {
	g.GET("/ws/terminal", m.socket)
	g.GET("/api/terminal/hosts", m.listHosts)
	g.POST("/api/terminal/hosts", m.saveHost)
	g.PUT("/api/terminal/hosts/:id", m.saveHost)
	g.DELETE("/api/terminal/hosts/:id", m.deleteHost)
}

type terminalIO struct {
	input    io.Writer
	output   io.Reader
	resize   func(uint16, uint16) error
	close    func()
	done     <-chan struct{}
	exitCode func() int
	warning  string
}

// 只读取服务器账号配置，浏览器不能选择程序或传入 shell 参数。
func loginShell() string {
	data, err := os.ReadFile("/etc/passwd")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) == 7 && fields[0] == "root" && filepath.IsAbs(fields[6]) {
				if info, e := os.Stat(fields[6]); e == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
					return fields[6]
				}
			}
		}
	}
	return "/bin/bash"
}
func (m *Module) socket(c *gin.Context) {
	q, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		response.Fail(c, 1001, "终端参数无效")
		return
	}
	cols, rows := 80, 24
	if q.Has("cols") {
		cols, err = strconv.Atoi(q.Get("cols"))
	}
	if err != nil {
		response.Fail(c, 1001, "终端参数无效")
		return
	}
	if q.Has("rows") {
		rows, err = strconv.Atoi(q.Get("rows"))
	}
	if err != nil || cols < 1 || cols > 500 || rows < 1 || rows > 300 {
		response.Fail(c, 1001, "终端尺寸无效")
		return
	}
	cwd, hostID := q.Get("cwd"), q.Get("hostId")
	if hostID != "" {
		if _, err := hex.DecodeString(hostID); len(hostID) != 32 || err != nil || cwd != "" {
			response.Fail(c, 1001, "SSH 主机参数无效")
			return
		}
	} else {
		if cwd == "" {
			cwd = "/root"
		}
		info, statErr := os.Stat(cwd)
		if !filepath.IsAbs(cwd) || statErr != nil || !info.IsDir() {
			response.Fail(c, 1001, "终端工作目录无效（默认目录 /root 必须存在）")
			return
		}
		cwd, err = filepath.EvalSymlinks(cwd)
		if err != nil {
			response.Fail(c, 1001, "工作目录不可用")
			return
		}
	}
	session := auth.Current(c)
	username := session.User.Username
	m.mu.Lock()
	if m.closed || m.active[username] >= 5 {
		m.mu.Unlock()
		response.Fail(c, 1005, "最多同时连接5个终端")
		return
	}
	m.active[username]++
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		m.active[username]--
		if m.active[username] == 0 {
			delete(m.active, username)
		}
		m.mu.Unlock()
	}()
	// 来源在全局 SourceGuard 按配置验证。
	conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	reason := "连接断开"
	ip := m.auth.ClientIP(c)
	targetDesc := "连接本机终端"
	if hostID != "" {
		targetDesc = "连接 SSH 终端[" + hostID + "]"
	}
	audit := func(phase string, success bool) {
		m.db.Create(&store.OpLog{Username: &username, IP: ip, Module: "terminal", Action: phase, Target: targetDesc, Phase: phase, Success: success, CreatedAt: time.Now().UTC()})
	}
	var terminal terminalIO
	if hostID != "" {
		terminal, err = m.startSSH(ctx, session.User.ID, hostID, cols, rows)
	} else {
		var pty *executor.PTY
		pty, err = executor.StartPTY(ctx, loginShell(), cwd, uint16(cols), uint16(rows))
		if err == nil {
			terminal = terminalIO{
				input: pty.File, output: pty.File, resize: pty.Resize, done: pty.Done(),
				close:    func() { pty.Close(); pty.Wait() },
				exitCode: func() int { code, _ := pty.ExitCode(); return code },
			}
		} else {
			err = errors.New("无法启动登录 Shell")
		}
	}
	if err != nil {
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(1011, err.Error()), time.Now().Add(time.Second))
		return
	}
	audit("start", true)
	defer func() {
		terminal.close()
	}()
	if hostID != "" {
		readyFrame, _ := json.Marshal(struct {
			Type    string `json:"type"`
			Warning string `json:"warning,omitempty"`
		}{Type: "ready", Warning: terminal.warning})
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, readyFrame); err != nil {
			return
		}
	}
	conn.SetReadLimit(64 * 1024)
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	var writeMu sync.Mutex
	write := func(kind int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(kind, data)
	}
	closeSocket := func(code int, msg string) {
		conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, msg), time.Now().Add(time.Second))
		conn.Close()
	}
	readDone := make(chan string, 1)
	go func() {
		for {
			kind, data, e := conn.ReadMessage()
			if e != nil {
				readDone <- "连接断开"
				return
			}
			var frame struct {
				Type string  `json:"type"`
				Data *string `json:"data"`
				Cols *int    `json:"cols"`
				Rows *int    `json:"rows"`
			}
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.DisallowUnknownFields()
			if kind != websocket.TextMessage || decoder.Decode(&frame) != nil || decoder.Decode(new(any)) != io.EOF {
				readDone <- "终端协议无效"
				return
			}
			switch frame.Type {
			case "input":
				if frame.Data == nil || frame.Cols != nil || frame.Rows != nil {
					readDone <- "输入帧无效"
					return
				}
				last.Store(time.Now().UnixNano())
				if _, e = io.WriteString(terminal.input, *frame.Data); e != nil {
					readDone <- "终端输入失败"
					return
				}
			case "resize":
				if frame.Data != nil || frame.Cols == nil || frame.Rows == nil || *frame.Cols < 1 || *frame.Cols > 500 || *frame.Rows < 1 || *frame.Rows > 300 {
					readDone <- "终端尺寸无效"
					return
				}
				last.Store(time.Now().UnixNano())
				if terminal.resize(uint16(*frame.Cols), uint16(*frame.Rows)) != nil {
					readDone <- "调整终端失败"
					return
				}
			case "ping":
				if frame.Data != nil || frame.Cols != nil || frame.Rows != nil {
					readDone <- "心跳帧无效"
					return
				}
			default:
				readDone <- "未知终端帧"
				return
			}
		}
	}()
	outputDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, e := terminal.output.Read(buf)
			if n > 0 {
				if err := write(websocket.BinaryMessage, buf[:n]); err != nil {
					outputDone <- err
					return
				}
			}
			if e != nil {
				outputDone <- nil
				return
			}
		}
	}()
	recheck := time.NewTicker(auth.SessionRecheckInterval)
	defer recheck.Stop()
	idle := time.NewTicker(time.Second)
	defer idle.Stop()
	for {
		select {
		case <-ctx.Done():
			reason = "服务器关闭"
			closeSocket(1001, reason)
			return
		case reason = <-readDone:
			closeSocket(1008, reason)
			return
		case e := <-outputDone:
			if e != nil {
				reason = "终端输出连接中断"
				closeSocket(1001, reason)
				return
			}
			outputDone = nil
		case <-terminal.done:
			// 退出前排空缓冲输出，超时则关闭终端以解除读取等待。
			if outputDone != nil {
				select {
				case <-outputDone:
				case <-time.After(10 * time.Second):
					terminal.close()
					<-outputDone
				case <-ctx.Done():
					terminal.close()
					<-outputDone
				}
			}
			data, _ := json.Marshal(gin.H{"type": "exit", "code": terminal.exitCode()})
			_ = write(websocket.TextMessage, data)
			reason = "Shell 已退出"
			closeSocket(1000, reason)
			return
		case <-recheck.C:
			if _, e := m.auth.CheckSession(session.Token); e != nil {
				reason = "会话已失效"
				closeSocket(4001, reason)
				return
			}
		case <-idle.C:
			if time.Since(time.Unix(0, last.Load())) >= 30*time.Minute {
				reason = "终端空闲超过30分钟"
				closeSocket(1000, reason)
				return
			}
		}
	}
}
