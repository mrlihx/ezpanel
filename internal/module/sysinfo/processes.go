package sysinfo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v3/process"
)

type processIdentity struct {
	Process
	exe        string
	panel      bool
	unreadable bool
}

type processFailure struct {
	code    int
	message string
}

func (e *processFailure) Error() string { return e.message }

func readIdentity(ctx context.Context, pr *process.Process) processIdentity {
	v := processIdentity{Process: Process{PID: pr.Pid}}
	var e error
	v.Name, e = pr.NameWithContext(ctx)
	v.unreadable = e != nil || v.Name == ""
	v.PPID, e = pr.PpidWithContext(ctx)
	v.unreadable = v.unreadable || e != nil
	v.Started, e = pr.CreateTimeWithContext(ctx)
	v.unreadable = v.unreadable || e != nil || v.Started <= 0
	v.exe, e = pr.ExeWithContext(ctx)
	v.unreadable = v.unreadable || e != nil || v.exe == ""
	// /proc 的可执行文件链接即使带有 deleted 标记，仍可按 inode 识别同一面板程序。
	ownPath, ownErr := os.Executable()
	targetPath := v.exe
	if runtime.GOOS == "linux" {
		ownPath, targetPath, ownErr = "/proc/self/exe", fmt.Sprintf("/proc/%d/exe", pr.Pid), nil
	}
	own, ownStatErr := os.Stat(ownPath)
	target, targetErr := os.Stat(targetPath)
	if ownErr != nil || ownStatErr != nil || targetErr != nil {
		v.unreadable = true
	} else {
		v.panel = os.SameFile(own, target)
	}
	return v
}

func protection(v processIdentity, identities map[int32]processIdentity) string {
	if v.PID <= 2 {
		return "系统基础进程不可终止"
	}
	if v.PID == int32(os.Getpid()) || v.panel {
		return "面板及同一可执行程序不可终止"
	}
	if v.PPID == 2 && v.exe == "" {
		return "内核线程不可终止"
	}
	if v.unreadable {
		return "进程身份无法完整读取，禁止终止"
	}
	for _, name := range []string{strings.ToLower(v.Name), strings.ToLower(filepath.Base(v.exe))} {
		if strings.HasPrefix(name, "systemd") || strings.HasPrefix(name, "containerd-shim") {
			return "系统关键守护进程不可终止"
		}
		switch name {
		case "init", "sshd", "ssh", "sshd-session", "dbus-daemon", "dbus-broker", "dbus-broker-launch", "networkmanager", "networkd-dispatcher", "dhclient", "dhcpcd", "dhcpcd-run-hooks", "wpa_supplicant", "iwd", "connmand", "udhcpc", "udevd", "syslogd", "syslog-ng", "rsyslogd", "klogd", "journald", "auditd", "audispd", "cron", "crond", "anacron", "containerd", "dockerd", "docker-proxy", "polkitd":
			return "系统关键守护进程不可终止"
		}
	}
	seen := map[int32]bool{v.PID: true}
	for parent := v.PPID; parent > 2; {
		if parent == int32(os.Getpid()) {
			return "面板子进程不可终止"
		}
		ancestor, ok := identities[parent]
		if !ok || ancestor.unreadable || seen[parent] {
			return "进程祖先身份无法确认，禁止终止"
		}
		if ancestor.panel {
			return "面板子进程不可终止"
		}
		seen[parent] = true
		parent = ancestor.PPID
	}
	return ""
}

func processPID(c *gin.Context) (int32, bool) {
	n, e := strconv.ParseInt(c.Param("pid"), 10, 32)
	if e != nil || n <= 0 {
		response.Fail(c, response.Invalid, "PID无效")
		return 0, false
	}
	return int32(n), true
}

func liveIdentity(ctx context.Context, pid int32) (processIdentity, map[int32]processIdentity, error) {
	pr, e := process.NewProcessWithContext(ctx, pid)
	if e != nil {
		return processIdentity{}, nil, &processFailure{response.NotFound, "进程已退出或无法读取"}
	}
	v := readIdentity(ctx, pr)
	identities := map[int32]processIdentity{pid: v}
	for parent := v.PPID; parent > 2 && parent != int32(os.Getpid()); {
		if _, seen := identities[parent]; seen {
			break
		}
		ancestor, e := process.NewProcessWithContext(ctx, parent)
		if e != nil {
			break
		}
		identity := readIdentity(ctx, ancestor)
		identities[parent] = identity
		if identity.unreadable {
			break
		}
		parent = identity.PPID
	}
	return v, identities, nil
}

func (m *Module) processDetail(c *gin.Context) {
	pid, ok := processPID(c)
	if !ok {
		return
	}
	started, e := strconv.ParseInt(c.Query("started"), 10, 64)
	if e != nil || started <= 0 {
		response.Fail(c, response.Invalid, "须提供列表中的有效创建时间")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	identity, ancestors, e := liveIdentity(ctx, pid)
	if e != nil {
		response.Fail(c, response.NotFound, e.Error())
		return
	}
	if identity.Started != started {
		response.Fail(c, response.Conflict, "进程身份已变化，请刷新列表")
		return
	}
	item := identity.Process
	item.ProtectedReason = protection(identity, ancestors)
	item.Protected = item.ProtectedReason != ""
	m.mu.RLock()
	for _, sampled := range m.processes["cpu"] {
		if sampled.PID == pid && sampled.Started == started {
			item.CPU, item.Mem = sampled.CPU, sampled.Mem
			break
		}
	}
	m.mu.RUnlock()
	pr, e := process.NewProcessWithContext(ctx, pid)
	if e != nil {
		response.Fail(c, response.NotFound, "进程已退出或无法读取")
		return
	}
	item.User, _ = pr.UsernameWithContext(ctx)
	item.Status, _ = pr.StatusWithContext(ctx)
	if memory, e := pr.MemoryInfoWithContext(ctx); e == nil {
		item.MemoryBytes = &memory.RSS
	}
	command, _ := pr.CmdlineWithContext(ctx)
	cwd, _ := pr.CwdWithContext(ctx)
	var threads *int32
	if count, e := pr.NumThreadsWithContext(ctx); e == nil {
		threads = &count
	}
	// 使用新对象读取创建时间，避免 gopsutil 的对象缓存掩盖 PID 重用。
	check := &process.Process{Pid: pid}
	if current, e := check.CreateTimeWithContext(ctx); e != nil || current != started {
		response.Fail(c, response.Conflict, "进程已退出或身份已变化，请刷新列表")
		return
	}
	startedAt := time.UnixMilli(started).UTC()
	response.OK(c, struct {
		Process
		Exe       string    `json:"exe"`
		Command   string    `json:"command"`
		Cwd       string    `json:"cwd"`
		StartedAt time.Time `json:"startedAt"`
		Threads   *int32    `json:"threads"`
	}{item, identity.exe, command, cwd, startedAt, threads})
}

func (m *Module) terminate(c *gin.Context) {
	pid, ok := processPID(c)
	if !ok {
		return
	}
	var input struct {
		Started int64 `json:"started"`
	}
	if !response.Bind(c, &input) {
		return
	}
	if input.Started <= 0 {
		response.Fail(c, response.Invalid, "须提供列表中的有效创建时间")
		return
	}
	defer func() {
		if m.db == nil {
			return
		}
		session := auth.Current(c)
		var username *string
		if session.User.ID != 0 {
			username = &session.User.Username
		}
		code := c.GetInt("panel.errorCode")
		phase := "rejected"
		if c.Writer.Status() < 400 {
			phase = "signal_sent"
		}
		// 审计只保存身份与信号结果，不保存命令及参数。
		if m.db.Create(&store.OpLog{Username: username, IP: m.auth.ClientIP(c), Module: "process", Action: "terminate", Target: fmt.Sprintf("终止进程[pid=%d]", pid), Phase: phase, Success: code == 0, ErrorCode: code, CreatedAt: time.Now().UTC()}).Error == nil {
			c.Set("panel.auditRecorded", true)
		}
	}()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if e := terminatePID(ctx, pid, input.Started); e != nil {
		if failure, ok := e.(*processFailure); ok {
			response.Fail(c, failure.code, failure.message)
		} else {
			response.Fail(c, response.System, e.Error())
		}
		return
	}
	response.OK(c, gin.H{"message": "已发送SIGTERM，请刷新确认进程是否退出"})
}
