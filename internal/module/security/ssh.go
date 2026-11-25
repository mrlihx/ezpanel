package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

const sshMain = "/etc/ssh/sshd_config"
const sshManaged = "/etc/ssh/ezpanel.conf"

// systemd停止SSH后会清理RuntimeDirectory，独立配置检查也需要该目录。
func sshdCommand(ctx context.Context, args ...string) (string, error) {
	if err := os.Mkdir("/run/sshd", 0755); err == nil {
		if err = os.Chmod("/run/sshd", 0755); err != nil {
			return "", err
		}
		if err = os.Chown("/run/sshd", 0, 0); err != nil {
			return "", err
		}
	} else if !os.IsExist(err) {
		return "", err
	}
	return run(ctx, "sshd", args...)
}
func sshValues(ctx context.Context) (map[string]string, error) {
	text, err := sshdCommand(ctx, "-T")
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, " ")
		if ok {
			if key == "port" && values[key] != "" {
				values[key] += "," + value
			} else {
				if key == "permitrootlogin" && value == "without-password" {
					value = "prohibit-password"
				}
				values[key] = value
			}
		}
	}
	return values, nil
}
func sshPorts(ctx context.Context) ([]int, error) {
	values, err := sshValues(ctx)
	if err != nil {
		return nil, err
	}
	ports := []int{}
	for _, value := range strings.Split(values["port"], ",") {
		port, err := strconv.Atoi(value)
		if err != nil {
			return nil, err
		}
		ports = append(ports, port)
	}
	return ports, nil
}
func sshUnit(ctx context.Context) (string, error) {
	for _, unit := range []string{"ssh.service", "sshd.service"} {
		text, _ := run(ctx, "systemctl", "show", unit, "--property=LoadState", "--value")
		if strings.TrimSpace(text) == "loaded" {
			return unit, nil
		}
	}
	return "", errors.New("系统未安装OpenSSH服务")
}
func sshRevision() string {
	main, _ := os.ReadFile(sshMain)
	managed, _ := os.ReadFile(sshManaged)
	sum := sha256.Sum256(append(append(main, 0), managed...))
	return hex.EncodeToString(sum[:])
}
func (m *Module) ssh(c *gin.Context) {
	values, err := sshValues(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	unit, err := sshUnit(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	active, _ := run(c.Request.Context(), "systemctl", "is-active", unit)
	socket, _ := run(c.Request.Context(), "systemctl", "is-active", "ssh.socket")
	response.OK(c, gin.H{"values": values, "revision": sshRevision(), "unit": unit, "running": strings.TrimSpace(active) == "active" || strings.TrimSpace(socket) == "active", "socketActivated": strings.TrimSpace(socket) == "active", "timezone": m.cfg.Current().Timezone})
}
func (m *Module) saveSSH(c *gin.Context) {
	var in struct {
		Password  bool   `json:"password"`
		Pubkey    bool   `json:"pubkey"`
		Port      int    `json:"port"`
		RootLogin string `json:"rootLogin"`
		Revision  string `json:"revision"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Port < 1 || in.Port > 65535 || len(in.Revision) != 64 || (!in.Password && !in.Pubkey) || (in.RootLogin != "yes" && in.RootLogin != "no" && in.RootLogin != "prohibit-password" && in.RootLogin != "forced-commands-only") {
		response.Fail(c, response.Invalid, "SSH配置无效，至少保留一种登录方式")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if in.Revision != sshRevision() {
		response.Fail(c, response.Conflict, "SSH配置已变化，请刷新后再保存")
		return
	}
	ctx := c.Request.Context()
	oldValues, err := sshValues(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	keyGenerated := false
	if in.Pubkey {
		_, keyGenerated, err = m.ensureSSHKey("root", false)
		if err != nil {
			fail(c, err)
			return
		}
	}
	// 关闭密码前至少确认root已有公钥，避免最常见的自锁。
	if !in.Password {
		keys, err := readUserKeys("root")
		if err != nil || strings.TrimSpace(keys) == "" {
			response.Fail(c, response.Invalid, "请先为root配置并验证SSH公钥，再关闭密码登录")
			return
		}
	}
	main, err := os.ReadFile(sshMain)
	if err != nil {
		fail(c, err)
		return
	}
	oldManaged, managedErr := os.ReadFile(sshManaged)
	yesNo := func(value bool) string {
		if value {
			return "yes"
		}
		return "no"
	}
	content := fmt.Sprintf("# ezpanel managed SSH settings\nPasswordAuthentication %s\nKbdInteractiveAuthentication %s\nPubkeyAuthentication %s\nPort %d\nPermitRootLogin %s\n", yesNo(in.Password), yesNo(in.Password), yesNo(in.Pubkey), in.Port, in.RootLogin)
	include := "Include " + sshManaged + "\n"
	newMain := string(main)
	if !strings.HasPrefix(newMain, include) {
		newMain = include + newMain
	}
	// Port允许多次出现。仅替换主文件全局段的Port，其余Include由sshd -T核对。
	lines := strings.Split(newMain, "\n")
	matched := false
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.EqualFold(fields[0], "Match") {
			matched = true
		}
		if !matched && strings.EqualFold(fields[0], "Port") {
			lines[i] = "# ezpanel: " + line
		}
	}
	restore := func() {
		_ = write(sshMain, main, 0600)
		if managedErr == nil {
			_ = write(sshManaged, oldManaged, 0600)
		} else {
			_ = os.Remove(sshManaged)
		}
	}
	if err = write(sshManaged, []byte(content), 0600); err == nil {
		err = write(sshMain, []byte(strings.Join(lines, "\n")), 0600)
	}
	if err == nil {
		_, err = sshdCommand(ctx, "-t")
	}
	if err == nil {
		values, e := sshValues(ctx)
		err = e
		if err == nil && (values["port"] != strconv.Itoa(in.Port) || values["passwordauthentication"] != yesNo(in.Password) || values["pubkeyauthentication"] != yesNo(in.Pubkey) || values["permitrootlogin"] != in.RootLogin) {
			err = errors.New("其他SSH Include覆盖或追加了配置，请先整理冲突配置")
		}
	}
	if err != nil {
		restore()
		fail(c, err)
		return
	}
	unit, err := sshUnit(ctx)
	if err != nil {
		restore()
		fail(c, err)
		return
	}
	socket, _ := run(ctx, "systemctl", "is-active", "ssh.socket")
	serviceStatus, _ := run(ctx, "systemctl", "is-active", unit)
	socketEnabled, _ := run(ctx, "systemctl", "is-enabled", "ssh.socket")
	socketManaged := strings.TrimSpace(socket) == "active" || strings.TrimSpace(socketEnabled) == "enabled"
	running := strings.TrimSpace(serviceStatus) == "active" || strings.TrimSpace(socket) == "active"
	firewallChanged := false
	changed := oldValues["port"] != strconv.Itoa(in.Port)
	if changed {
		state, e := m.readFirewall()
		if e != nil {
			restore()
			fail(c, e)
			return
		}
		// 受管防火墙在SSH重启前读取新配置并放行新端口；原规则保持。
		if state.Enabled {
			if e = m.applyFirewall(ctx, state, m.auth.ClientIP(c)); e != nil {
				restore()
				fail(c, e)
				return
			}
			firewallChanged = true
		}
	}
	socketPath := "/etc/systemd/system/ssh.socket.d/ezpanel.conf"
	oldSocket, socketErr := os.ReadFile(socketPath)
	if changed && socketManaged {
		err = write(socketPath, []byte(fmt.Sprintf("[Socket]\nListenStream=\nListenStream=%d\n", in.Port)), 0644)
		if err == nil {
			_, err = run(ctx, "systemctl", "daemon-reload")
		}
		if err == nil && running {
			_, err = run(ctx, "systemctl", "stop", unit)
		}
		if err == nil && running {
			_, err = run(ctx, "systemctl", "restart", "ssh.socket")
		}
		if err == nil && running {
			_, err = run(ctx, "systemctl", "start", unit)
		}
	} else if running {
		_, err = run(ctx, "systemctl", "reload-or-restart", unit)
	}
	if err != nil {
		restore()
		var recoveryErr error
		if socketErr == nil {
			recoveryErr = write(socketPath, oldSocket, 0644)
		} else if e := os.Remove(socketPath); !os.IsNotExist(e) {
			recoveryErr = e
		}
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, e := run(recovery, "systemctl", "daemon-reload")
		recoveryErr = errors.Join(recoveryErr, e)
		if running {
			if strings.TrimSpace(socket) == "active" {
				_, e = run(recovery, "systemctl", "stop", unit)
				recoveryErr = errors.Join(recoveryErr, e)
				_, e = run(recovery, "systemctl", "restart", "ssh.socket")
				recoveryErr = errors.Join(recoveryErr, e)
			}
			_, e = run(recovery, "systemctl", "restart", unit)
			recoveryErr = errors.Join(recoveryErr, e)
		}
		if firewallChanged {
			state, e := m.readFirewall()
			if e == nil {
				e = m.applyFirewall(recovery, state, m.auth.ClientIP(c))
			}
			recoveryErr = errors.Join(recoveryErr, e)
		}
		fail(c, errors.Join(err, recoveryErr))
		return
	}

	response.OK(c, gin.H{"saved": true, "port": in.Port, "keyGenerated": keyGenerated})
}
func (m *Module) sshService(c *gin.Context) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !response.Bind(c, &in) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx := c.Request.Context()
	unit, err := sshUnit(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	if in.Enabled {
		if _, err = sshdCommand(ctx, "-t"); err == nil {
			_, err = run(ctx, "systemctl", "enable", "--now", unit)
		}
	} else {
		state, _ := run(ctx, "systemctl", "is-active", "ssh.socket")
		if strings.TrimSpace(state) == "active" {
			_, err = run(ctx, "systemctl", "disable", "--now", "ssh.socket")
		}
		if err == nil {
			_, err = run(ctx, "systemctl", "disable", "--now", unit)
		}
	}
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"saved": true})
}
func userHome(name string) (string, error) {
	text, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(text), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 7 && fields[0] == name && filepath.IsAbs(fields[5]) {
			return fields[5], nil
		}
	}
	return "", errors.New("系统账号不存在")
}
