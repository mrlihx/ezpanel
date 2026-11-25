package toolbox

import (
	"context"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"ezpanel/internal/task"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const swapPath = "/var/lib/ezpanel-toolbox/swapfile"
const ramPath = "/mnt/ezpanel-ramdisk"

func updateFstab(key, line string) error {
	b, e := os.ReadFile("/etc/fstab")
	if e != nil {
		return e
	}
	lines := []string{}
	marker := "# ezpanel-" + key
	for _, old := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if !strings.HasSuffix(strings.TrimSpace(old), marker) {
			lines = append(lines, old)
		}
	}
	if line != "" {
		lines = append(lines, line+" "+marker)
	}
	return writeConfig("/etc/fstab", []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
func (m *Module) saveDNS(ctx context.Context, servers []string) error {
	status, _ := command(ctx, "systemctl", "is-active", "systemd-resolved.service")
	if strings.TrimSpace(status) == "active" {
		path := "/etc/systemd/resolved.conf.d/90-ezpanel.conf"
		previous, e := os.ReadFile(path)
		exists := e == nil
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if e = writeConfig(path, []byte("[Resolve]\nDNS="+strings.Join(servers, " ")+"\nDomains=~.\n"), 0644); e != nil {
			return e
		}
		if _, e = command(ctx, "systemctl", "restart", "systemd-resolved.service"); e != nil {
			if exists {
				os.WriteFile(path, previous, 0644)
			} else {
				os.Remove(path)
			}
			command(ctx, "systemctl", "restart", "systemd-resolved.service")
			return e
		}
		return nil
	}
	info, e := os.Lstat("/etc/resolv.conf")
	if e != nil {
		return e
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("DNS 由其他网络服务管理，请在对应服务中配置；不会覆盖其符号链接")
	}
	b, e := os.ReadFile("/etc/resolv.conf")
	if e != nil {
		return e
	}
	lines := []string{}
	for _, line := range strings.Split(string(b), "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] != "nameserver" {
			lines = append(lines, line)
		}
	}
	for _, ip := range servers {
		lines = append(lines, "nameserver "+ip)
	}
	return writeConfig("/etc/resolv.conf", []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
func (m *Module) saveSwap(ctx context.Context, size int) error {
	b, e := os.ReadFile("/proc/swaps")
	if e != nil {
		return e
	}
	active := false
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && f[0] == swapPath {
			active = true
		}
	}
	if size == 0 {
		if active {
			if _, e = command(ctx, "swapoff", swapPath); e != nil {
				return e
			}
		}
		if e = updateFstab("swap", ""); e != nil {
			return e
		}
		if e = os.Remove(swapPath); e != nil && !os.IsNotExist(e) {
			return e
		}
		return nil
	}
	if e = os.MkdirAll("/var/lib/ezpanel-toolbox", 0700); e != nil {
		return e
	}
	next := swapPath + ".next"
	defer os.Remove(next)
	f, e := os.OpenFile(next, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	f.Close()
	if _, e = command(ctx, "dd", "if=/dev/zero", "of="+next, "bs=1M", "count="+strconv.Itoa(size), "conv=fsync", "status=none"); e != nil {
		return e
	}
	if _, e = command(ctx, "mkswap", next); e != nil {
		return e
	}
	if active {
		if _, e = command(ctx, "swapoff", swapPath); e != nil {
			return e
		}
	}
	backup := swapPath + ".previous"
	exists := false
	if _, e = os.Stat(swapPath); e == nil {
		if e = os.Rename(swapPath, backup); e != nil {
			return e
		}
		exists = true
	} else if !os.IsNotExist(e) {
		return e
	}
	rollback := func() {
		os.Remove(swapPath)
		if exists {
			os.Rename(backup, swapPath)
		}
		if active {
			command(ctx, "swapon", swapPath)
		}
	}
	if e = os.Rename(next, swapPath); e != nil {
		rollback()
		return e
	}
	if _, e = command(ctx, "swapon", swapPath); e != nil {
		rollback()
		return e
	}
	if e = updateFstab("swap", swapPath+" none swap sw,nofail 0 0"); e != nil {
		command(ctx, "swapoff", swapPath)
		rollback()
		return e
	}
	if exists {
		return os.Remove(backup)
	}
	return nil
}
func (m *Module) saveRAM(ctx context.Context, size int) error {
	b, e := os.ReadFile("/proc/mounts")
	if e != nil {
		return e
	}
	mounted := false
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) > 2 && f[1] == ramPath {
			if f[2] != "tmpfs" || f[0] != "ezpanel-tmpfs" {
				return errors.New("目标不是内存盘，不允许接管")
			}
			mounted = true
		}
	}
	if size == 0 {
		if mounted {
			if _, e = command(ctx, "umount", ramPath); e != nil {
				return e
			}
		}
		return updateFstab("ramdisk", "")
	}
	if e = os.MkdirAll(ramPath, 0755); e != nil {
		return e
	}
	if mounted {
		_, e = command(ctx, "mount", "-o", "remount,size="+strconv.Itoa(size)+"M", ramPath)
	} else {
		entries, err := os.ReadDir(ramPath)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return errors.New("挂载目录非空，不能遮盖已有文件")
		}
		_, e = command(ctx, "mount", "-t", "tmpfs", "-o", "size="+strconv.Itoa(size)+"M,mode=0755,nodev,nosuid", "ezpanel-tmpfs", ramPath)
	}
	if e != nil {
		return e
	}
	return updateFstab("ramdisk", "ezpanel-tmpfs "+ramPath+" tmpfs size="+strconv.Itoa(size)+"M,mode=0755,nodev,nosuid,nofail 0 0")
}

type kernelParam struct {
	Key, Label string
	Min, Max   int
}

var kernelParams = []kernelParam{{"vm.swappiness", "交换倾向", 0, 200}, {"vm.vfs_cache_pressure", "目录与索引缓存回收倾向", 0, 1000}, {"net.core.somaxconn", "监听队列上限", 128, 65535}, {"net.ipv4.tcp_fin_timeout", "FIN 等待秒数", 5, 600}, {"net.ipv4.tcp_keepalive_time", "TCP 保活空闲秒数", 60, 86400}, {"fs.file-max", "系统文件句柄上限", 1024, 100000000}}

func (m *Module) saveKernel(ctx context.Context, values map[string]int) error {
	if len(values) == 0 || len(values) > len(kernelParams) {
		return errors.New("请选择需要修改的内核参数")
	}
	for key, value := range values {
		valid := false
		for _, p := range kernelParams {
			if p.Key == key && value >= p.Min && value <= p.Max {
				valid = true
			}
		}
		if !valid {
			return fmt.Errorf("内核参数 %s 或取值不支持", key)
		}
	}
	old := map[string]string{}
	for key := range values {
		b, e := os.ReadFile("/proc/sys/" + strings.ReplaceAll(key, ".", "/"))
		if e != nil {
			return e
		}
		old[key] = strings.TrimSpace(string(b))
	}
	rollback := func() {
		for key, value := range old {
			command(ctx, "sysctl", "-w", key+"="+value)
		}
	}
	for key, value := range values {
		if _, e := command(ctx, "sysctl", "-w", key+"="+strconv.Itoa(value)); e != nil {
			rollback()
			return e
		}
	}
	// 保留本工具之前保存但本次未修改的条目。
	existing, _ := os.ReadFile("/etc/sysctl.d/90-ezpanel.conf")
	saved := map[string]string{}
	for _, line := range strings.Split(string(existing), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			saved[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	for key, value := range values {
		saved[key] = strconv.Itoa(value)
	}
	lines := []string{}
	for _, p := range kernelParams {
		if value, ok := saved[p.Key]; ok {
			lines = append(lines, p.Key+" = "+value)
		}
	}
	if e := writeConfig("/etc/sysctl.d/90-ezpanel.conf", []byte(strings.Join(lines, "\n")+"\n"), 0644); e != nil {
		rollback()
		return e
	}
	return nil
}
func (m *Module) saveAudit(ctx context.Context, enabled bool) error {
	args := []string{"-a", "always,exit", "-F", "arch=b64", "-S", "execve,execveat", "-k", "ezpanel-command"}
	text := ""
	if enabled {
		text = strings.Join(args, " ") + "\n"
	} else {
		args[0] = "-d"
		rules, e := command(ctx, "auditctl", "-l")
		if e != nil {
			return e
		}
		if !strings.Contains(rules, "ezpanel-command") {
			return writeConfig("/etc/audit/rules.d/90-ezpanel.rules", nil, 0600)
		}
	}
	if _, e := command(ctx, "auditctl", args...); e != nil {
		return e
	}
	return writeConfig("/etc/audit/rules.d/90-ezpanel.rules", []byte(text), 0600)
}
func (m *Module) setupAudit(c *gin.Context) {
	session := auth.Current(c)
	id, e := m.tasks.Submit(task.Meta{Module: "toolbox", Action: "audit-install", Target: "auditd", ResourceID: "auditd", LockKey: "software-install", Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		var name string
		var args []string
		if bin, err := executor.LookPath("apt-get"); err == nil {
			name = bin
			args = []string{"-y", "--no-remove", "install", "auditd"}
			if _, e := executor.Stream(ctx, 15*time.Minute, append(executor.DefaultEnv(), "DEBIAN_FRONTEND=noninteractive"), log.Line, bin, "update"); e != nil {
				return nil, e
			}
		} else if bin, err := executor.LookPath("dnf"); err == nil {
			name = bin
			args = []string{"-y", "install", "audit"}
		} else {
			return nil, errors.New("当前系统不支持自动安装 auditd")
		}
		_, e := executor.Stream(ctx, 15*time.Minute, append(executor.DefaultEnv(), "DEBIAN_FRONTEND=noninteractive"), log.Line, name, args...)
		if e != nil {
			return nil, e
		}
		_, e = command(ctx, "systemctl", "enable", "--now", "auditd.service")
		return nil, e
	})
	if e != nil {
		fail(c, e)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
