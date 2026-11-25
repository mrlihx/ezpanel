package toolbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Module struct {
	installDir string
	db         *gorm.DB
	tasks      *task.Manager
	auth       *auth.Service
	mu         sync.Mutex
	ipMu       sync.Mutex
	ipTry      *ipTrial
}

func New(db *gorm.DB, tasks *task.Manager, a *auth.Service, installDir string) *Module {
	return &Module{db: db, tasks: tasks, auth: a, installDir: installDir}
}
func (m *Module) Name() string { return "toolbox" }
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/toolbox")
	r.GET("/:section", m.read)
	r.PUT("/:section", m.save)
	r.GET("/ip", m.readIP)
	r.GET("/disks", m.readDisks)
	r.PUT("/disks", m.changeMount)
	r.PUT("/ip", m.tryIP)
	r.POST("/ip/:action", m.finishIP)
	r.POST("/dns/test", m.testDNS)
	r.POST("/mirrors/test", m.testMirrors)
	r.POST("/time/sync", m.syncTime)
	r.POST("/audit/setup", m.setupAudit)
}
func fail(c *gin.Context, e error) { response.Fail(c, response.System, response.Summary(e.Error())) }
func command(ctx context.Context, name string, args ...string) (string, error) {
	bin, e := executor.LookPath(name)
	if e != nil {
		return "", fmt.Errorf("系统未提供 %s，请先安装对应工具", name)
	}
	timeout := 30 * time.Second
	if name == "dd" || name == "swapoff" {
		timeout = 10 * time.Minute
	}
	r, e := executor.Run(ctx, timeout, bin, args...)
	if e != nil {
		return r.Stdout, fmt.Errorf("%s 操作失败：%s", name, strings.TrimSpace(r.Stderr))
	}
	return r.Stdout, nil
}
func revision(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// 写入前保留文件备份，使用同目录临时文件，保留现有权限和所有者。
func writeConfig(path string, b []byte, mode os.FileMode) error {
	old, e := os.ReadFile(path)
	exists := e == nil
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	uid, gid := -1, -1
	if info, e := os.Stat(path); e == nil {
		mode = info.Mode().Perm()
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(stat.Uid), int(stat.Gid)
		}
	}
	if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	if exists {
		if e = os.WriteFile(path+".panel-backup", old, 0600); e != nil {
			return e
		}
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".ezpanel-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(mode); e != nil {
		f.Close()
		return e
	}
	if uid >= 0 {
		if e = f.Chown(uid, gid); e != nil {
			f.Close()
			return e
		}
	}
	_, e = f.Write(b)
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func memory() map[string]int64 {
	out := map[string]int64{}
	b, _ := os.ReadFile("/proc/meminfo")
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 {
			n, _ := strconv.ParseInt(fields[1], 10, 64)
			out[strings.TrimSuffix(fields[0], ":")] = n * 1024
		}
	}
	return out
}
func (m *Module) read(c *gin.Context) {
	ctx := c.Request.Context()
	var data any
	var e error
	switch c.Param("section") {
	case "dns":
		b, err := os.ReadFile("/etc/resolv.conf")
		if err != nil {
			fail(c, err)
			return
		}
		servers := []string{}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && f[0] == "nameserver" {
				servers = append(servers, f[1])
			}
		}
		managed, _ := os.ReadFile("/etc/systemd/resolved.conf.d/90-ezpanel.conf")
		for _, line := range strings.Split(string(managed), "\n") {
			if strings.HasPrefix(line, "DNS=") {
				servers = strings.Fields(strings.TrimPrefix(line, "DNS="))
			}
		}
		data = gin.H{"servers": servers, "content": string(b), "providers": dnsProviders}
	case "swap":
		b, err := os.ReadFile("/proc/swaps")
		if err != nil {
			fail(c, err)
			return
		}
		ownMB := int64(0)
		if info, err := os.Stat(swapPath); err == nil {
			ownMB = info.Size() >> 20
		}
		mem := memory()
		data = gin.H{"total": mem["SwapTotal"], "used": mem["SwapTotal"] - mem["SwapFree"], "free": mem["SwapFree"], "ownMB": ownMB, "entries": string(b), "path": swapPath}
	case "time":
		var status timeStatus
		status, e = readTimeStatus(ctx)
		if e == nil {
			var zones string
			zones, e = command(ctx, "timedatectl", "list-timezones")
			data = gin.H{"now": time.Now().UTC().Format(time.RFC3339), "zone": status.Zone, "zones": strings.Fields(zones), "synchronized": status.Synchronized, "service": status.Service, "serviceActive": status.ServiceActive}
		}
	case "password":
		b, err := os.ReadFile("/etc/passwd")
		if err != nil {
			fail(c, err)
			return
		}
		users := []string{}
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Split(line, ":")
			if len(f) == 7 && (f[0] == "root" || (!strings.HasSuffix(f[6], "nologin") && !strings.HasSuffix(f[6], "false"))) {
				users = append(users, f[0])
			}
		}
		data = gin.H{"users": users}
	case "ramdisk":
		b, err := os.ReadFile("/proc/mounts")
		if err != nil {
			fail(c, err)
			return
		}
		mounted := false
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) > 2 && f[1] == ramPath && f[2] == "tmpfs" {
				mounted = true
			}
		}
		var stat syscall.Statfs_t
		var size uint64
		if mounted && syscall.Statfs(ramPath, &stat) == nil {
			size = stat.Blocks * uint64(stat.Bsize)
		}
		data = gin.H{"path": ramPath, "mounted": mounted, "sizeMB": size >> 20, "memory": memory()["MemTotal"]}
	case "hosts":
		b, err := os.ReadFile("/etc/hosts")
		e = err
		data = gin.H{"content": string(b), "revision": revision(b), "path": "/etc/hosts"}
	case "mirrors":
		data, e = m.readMirrors()
	case "commands":
		var row store.Setting
		err := m.db.Where("key = ?", "toolbox.commands").First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			data = gin.H{"list": []savedCommand{}}
		} else if err != nil {
			e = err
		} else {
			var rows []savedCommand
			e = json.Unmarshal([]byte(row.Value), &rows)
			data = gin.H{"list": rows}
		}
	case "kernel":
		rows := []gin.H{}
		for _, p := range kernelParams {
			b, err := os.ReadFile("/proc/sys/" + strings.ReplaceAll(p.Key, ".", "/"))
			rows = append(rows, gin.H{"key": p.Key, "label": p.Label, "min": p.Min, "max": p.Max, "value": strings.TrimSpace(string(b)), "available": err == nil})
		}
		data = gin.H{"list": rows}
	case "audit":
		page, size, ok := response.Page(c, 100, 20)
		if !ok {
			return
		}
		rows, total := store.QueryOpLogs(m.db, store.OpLogFilter{
			Modules: []string{"terminal", "toolbox"},
			Page:    page,
			Size:    size,
		})
		installed := false
		if _, err := executor.LookPath("auditctl"); err == nil {
			installed = true
		}
		rules, _ := command(ctx, "auditctl", "-l")
		content := ""
		if installed {
			content, _ = command(ctx, "ausearch", "-k", "ezpanel-command", "-ts", "today", "-i")
		}
		data = gin.H{"list": rows, "total": total, "installed": installed, "enabled": strings.Contains(rules, "ezpanel-command"), "rules": rules, "content": content}
	default:
		response.Fail(c, response.NotFound, "工具不存在")
		return
	}
	if e != nil {
		fail(c, e)
		return
	}
	response.OK(c, data)
}

type savedCommand struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}
type settingInput struct {
	Servers  []string       `json:"servers"`
	SizeMB   int            `json:"sizeMB"`
	Zone     string         `json:"zone"`
	Username string         `json:"username"`
	Password string         `json:"password"`
	Content  string         `json:"content"`
	Revision string         `json:"revision"`
	List     []savedCommand `json:"list"`
	Values   map[string]int `json:"values"`
	Kind     string         `json:"kind"`
	Provider string         `json:"provider"`
	Enabled  bool           `json:"enabled"`
}

func (m *Module) save(c *gin.Context) {
	var in settingInput
	if !response.Bind(c, &in) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx := c.Request.Context()
	var e error
	switch c.Param("section") {
	case "dns":
		if len(in.Servers) < 1 || len(in.Servers) > 2 {
			response.Fail(c, response.Invalid, "请输入主 DNS 和可选备用 DNS")
			return
		}
		for _, v := range in.Servers {
			if net.ParseIP(v) == nil {
				response.Fail(c, response.Invalid, "DNS 必须是有效 IP 地址")
				return
			}
		}
		e = m.saveDNS(ctx, in.Servers)
	case "swap":
		if in.SizeMB < 0 || in.SizeMB > 16384 {
			response.Fail(c, response.Invalid, "Swap 大小须为0至16384 MB")
			return
		}
		e = m.saveSwap(ctx, in.SizeMB)
	case "time":
		zones, err := command(ctx, "timedatectl", "list-timezones")
		if err != nil {
			e = err
			break
		}
		valid := false
		for _, zone := range strings.Fields(zones) {
			if zone == in.Zone {
				valid = true
				break
			}
		}
		if !valid {
			response.Fail(c, response.Invalid, "请选择有效的系统时区")
			return
		}
		_, e = command(ctx, "timedatectl", "set-timezone", in.Zone)
	case "password":
		if !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(in.Username) || len(in.Password) < 12 || len(in.Password) > 256 || strings.ContainsAny(in.Password, "\x00\r\n") {
			response.Fail(c, response.Invalid, "密码须为12至256字符，不含换行或空字符")
			return
		}
		b, err := os.ReadFile("/etc/passwd")
		if err != nil {
			e = err
			break
		}
		valid := false
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Split(line, ":")
			if len(f) == 7 && f[0] == in.Username && (f[0] == "root" || (!strings.HasSuffix(f[6], "nologin") && !strings.HasSuffix(f[6], "false"))) {
				valid = true
			}
		}
		if !valid {
			response.Fail(c, response.Invalid, "只能修改已有登录用户的密码")
			return
		}
		bin, err := executor.LookPath("chpasswd")
		if err != nil {
			e = err
			break
		}
		_, e = executor.Pipe(ctx, 30*time.Second, nil, strings.NewReader(in.Username+":"+in.Password+"\n"), nil, bin)
	case "ramdisk":
		if in.SizeMB < 0 || int64(in.SizeMB) > (memory()["MemTotal"]/2)>>20 {
			response.Fail(c, response.Invalid, "内存盘容量不能超过物理内存的一半")
			return
		}
		e = m.saveRAM(ctx, in.SizeMB)
	case "hosts":
		if len(in.Content) > 1<<20 || strings.ContainsRune(in.Content, 0) {
			response.Fail(c, response.Invalid, "hosts 内容过长或含空字符")
			return
		}
		for _, line := range strings.Split(in.Content, "\n") {
			line, _, _ = strings.Cut(line, "#")
			f := strings.Fields(line)
			if len(f) == 0 {
				continue
			}
			if len(f) < 2 || net.ParseIP(f[0]) == nil {
				response.Fail(c, response.Invalid, "hosts 每行须为IP和主机名")
				return
			}
		}
		b, err := os.ReadFile("/etc/hosts")
		if err != nil {
			e = err
			break
		}
		if revision(b) != in.Revision {
			response.Fail(c, response.Conflict, "hosts 已修改，请重新加载")
			return
		}
		e = writeConfig("/etc/hosts", []byte(in.Content), 0644)
	case "mirrors":
		e = m.saveMirror(ctx, in.Kind, in.Provider)
	case "commands":
		if len(in.List) > 100 {
			response.Fail(c, response.Invalid, "最多保存100条命令")
			return
		}
		for _, row := range in.List {
			if strings.TrimSpace(row.Name) == "" || len(row.Name) > 64 || strings.TrimSpace(row.Command) == "" || len(row.Command) > 4096 || strings.ContainsRune(row.Command, 0) {
				response.Fail(c, response.Invalid, "命令名称或内容无效")
				return
			}
		}
		var b []byte
		b, e = json.Marshal(in.List)
		if e == nil {
			e = m.db.Save(&store.Setting{Key: "toolbox.commands", Value: string(b)}).Error
		}
	case "kernel":
		e = m.saveKernel(ctx, in.Values)
	case "audit":
		e = m.saveAudit(ctx, in.Enabled)
	default:
		response.Fail(c, response.NotFound, "工具不存在")
		return
	}
	if e != nil {
		fail(c, e)
		return
	}
	response.OK(c, gin.H{"saved": true})
}
