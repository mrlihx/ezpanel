package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/executor"
	"ezpanel/internal/module/security"
	"ezpanel/internal/version"
	"golang.org/x/crypto/bcrypt"
)

const panelUnit = "ezpanel.service"

type adminOptions struct {
	port                int
	entry, username, ip string
	accessMode          string
	follow              bool
}

type panelCommand struct {
	name, options, description string
	usesConfig                 bool
}

var panelCommands = []panelCommand{
	{"help", "[command]", "列出所有命令，或查看指定命令的帮助", false},
	{"info", "", "查看版本、管理员账号、访问地址和监听端口", true},
	{"start", "", "启动面板服务", false},
	{"stop", "", "停止面板服务", false},
	{"restart", "", "重启面板服务", false},
	{"status", "", "查看面板服务状态", false},
	{"logs", "[--follow]", "查看最近 100 行面板日志，--follow 可持续跟踪", false},
	{"reset-port", "--port <port>", "设置面板监听端口（1–65535）", true},
	{"external-access", "--mode <on|off>", "开启或关闭非回环网卡的面板直连", true},
	{"reset-entry", "[--entry <entry>]", "设置安全入口，至少 5 位字母或数字，省略时随机生成", true},
	{"reset-username", "--username <name>", "修改管理员用户名并注销旧会话", true},
	{"reset-password", "", "交互式重置管理员密码并注销旧会话", true},
	{"unlock-login", "--ip <address>", "清除指定来源 IP 的登录锁定", true},
	{"ready", "", "检查面板入口和匿名会话拦截是否就绪", true},
	{"upgrade-check", "", "检查面板是否有阻止升级的活动任务", true},
	{"version", "", "查看程序版本", false},
	{"server", "[--init]", "运行面板进程，--init 交互初始化后退出", true},
}

func printHelp() {
	fmt.Println("用法：ezpanel <命令> [选项]")
	fmt.Println()
	for _, command := range panelCommands {
		usage := strings.TrimSpace(command.name + " " + command.options)
		fmt.Printf("  %-35s %s\n", usage, command.description)
	}
	fmt.Println("\n运行 ezpanel help <命令> 或 ezpanel <命令> --help 查看详情。")
	fmt.Println("在终端直接运行 ezpanel 可打开数字操作菜单。")
	fmt.Println("读取面板配置的命令可用 --install-dir <path> 指定安装目录，默认为 /opt/ezpanel。")
}

func (command panelCommand) printHelp() {
	usage := strings.TrimSpace(command.name + " " + command.options)
	if command.usesConfig {
		usage += " [--install-dir <path>] [--dev]"
	}
	fmt.Printf("用法：ezpanel %s\n\n%s\n", usage, command.description)
	if command.usesConfig {
		fmt.Println("\n  --install-dir <path>  安装目录，默认为 /opt/ezpanel")
		fmt.Println("  --dev                 开发模式，保留认证与来源校验")
	}
}

func printPanelInfo(cfg config.Config) error {
	db, err := openPanelDatabase(cfg, "ro")
	if err != nil {
		return err
	}
	defer db.Close()
	var username string
	if err := db.QueryRow("SELECT username FROM users ORDER BY id LIMIT 1").Scan(&username); err != nil {
		return fmt.Errorf("无法读取面板管理员：%w", err)
	}
	printPanelAddress(cfg)
	fmt.Printf("管理员: %s\n版本: %s\n安装目录: %s\n", username, version.Version, cfg.InstallDir)
	return nil
}

func printPanelAddress(cfg config.Config) {
	addr := net.JoinHostPort(cfg.ListenAddr, strconv.Itoa(cfg.Port))
	origin := cfg.PanelOrigin("")
	if cfg.DirectIPAccess() {
		fmt.Printf("外网直连: %s://<服务器公网IP>:%d/%s\n", origin.Scheme, cfg.Port, cfg.Entry)
	}
	fmt.Printf("访问地址: %s/%s\n监听: %s\nTLS: %t\n", origin, cfg.Entry, addr, cfg.TLSCert != "")
}

func panelServiceCommand(action string, follow bool) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("面板服务管理需要 Linux 本机 root")
	}
	name, args := "systemctl", []string{action, "--no-pager", panelUnit}
	if action == "logs" {
		name, args = "journalctl", []string{"-u", panelUnit, "-n", "100", "--no-pager"}
		if follow {
			args = append(args, "--follow")
		}
	}
	path, err := executor.LookPath(name)
	if err != nil {
		return fmt.Errorf("找不到 %s：%w", name, err)
	}
	cmd := exec.Command(path, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func resetPanelAccess(command string, old config.Config, options adminOptions) error {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("修改面板监听或入口需要 Linux 本机 root")
	}
	next := old
	switch command {
	case "external-access":
		switch options.accessMode {
		case "on":
			next.ListenAddr = "0.0.0.0"
			// 旧版面板设置可能保存过默认回环地址，切换直连时解除这一限制。
			if old.BoundDomain == "" && old.TLSCert == "" && len(old.TrustedProxies) == 0 {
				if u, err := url.Parse(old.PublicURL); err == nil && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()) {
					next.PublicURL = ""
				}
			}
		case "off":
			next.ListenAddr = "127.0.0.1"
		default:
			return errors.New("请使用 --mode on 开启，或 --mode off 关闭")
		}
	case "reset-port":
		next.Port = options.port
		if len(old.TrustedProxies) == 0 {
			next.PublicURL = replaceDirectPort(old.PublicURL, old.Port, next.Port)
			next.UnboundURL = replaceDirectPort(old.UnboundURL, old.Port, next.Port)
		}
	case "reset-entry":
		next.Entry = options.entry
		if next.Entry == "" {
			entry, err := config.NewEntry()
			if err != nil {
				return err
			}
			next.Entry = entry
		}
	}
	if err := config.Validate(next); err != nil {
		return err
	}
	portChanged := next.Port != old.Port
	if !portChanged && next.Entry == old.Entry && next.ListenAddr == old.ListenAddr && next.PublicURL == old.PublicURL {
		printPanelAddress(old)
		return nil
	}
	if portChanged {
		listener, err := net.Listen("tcp", net.JoinHostPort(next.ListenAddr, strconv.Itoa(next.Port)))
		if err != nil {
			return fmt.Errorf("新端口无法监听：%w", err)
		}
		listener.Close()
	}

	path, err := executor.LookPath("systemctl")
	if err != nil {
		return err
	}
	output, err := exec.Command(path, "show", "-p", "WorkingDirectory", "-p", "ActiveState", panelUnit).Output()
	if err != nil {
		return fmt.Errorf("无法查询面板服务：%w", err)
	}
	active := false
	for _, line := range strings.Split(string(output), "\n") {
		if dir, ok := strings.CutPrefix(line, "WorkingDirectory="); ok && dir != "" && dir != old.InstallDir {
			return fmt.Errorf("面板服务使用安装目录 %s，请用 --install-dir 指定", dir)
		}
		switch line {
		case "ActiveState=active":
			active = true
		case "ActiveState=activating", "ActiveState=deactivating", "ActiveState=reloading":
			return errors.New("面板服务正在切换状态，请稍后重试")
		}
	}
	if active {
		if err = upgradeCheck(old); err != nil {
			return err
		}
		if err = panelServiceCommand("stop", false); err != nil {
			return err
		}
	}
	if err = config.Save(next); err != nil {
		if active {
			err = errors.Join(err, panelServiceCommand("start", false))
		}
		return err
	}

	refreshFirewall := func(cfg config.Config) error {
		return security.New(cfg, nil).RefreshPanelPort(context.Background())
	}
	if portChanged {
		err = refreshFirewall(next)
	}
	if err == nil && active {
		err = panelServiceCommand("start", false)
		if err == nil {
			for range 10 {
				err = readyCheck(next)
				if err == nil {
					break
				}
				time.Sleep(time.Second)
			}
		}
	}
	if err != nil {
		restoreErr := config.Save(old)
		if restoreErr != nil {
			return errors.Join(err, fmt.Errorf("恢复原面板配置失败：%w", restoreErr))
		}
		if portChanged {
			restoreErr = refreshFirewall(old)
		}
		if active {
			restoreErr = errors.Join(restoreErr, panelServiceCommand("restart", false))
		}
		if restoreErr != nil {
			return errors.Join(err, fmt.Errorf("恢复原面板运行状态失败：%w", restoreErr))
		}
		return fmt.Errorf("修改失败，已恢复原面板配置：%w", err)
	}
	printPanelAddress(next)
	return nil
}

func replaceDirectPort(raw string, oldPort, newPort int) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	if port != strconv.Itoa(oldPort) {
		return raw
	}
	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(newPort))
	return u.String()
}

func resetPanelAccount(command string, cfg config.Config, options adminOptions) error {
	if os.Geteuid() != 0 {
		return errors.New("管理面板账号需要本机 root")
	}
	var ip netip.Addr
	column, value := "username", options.username
	switch command {
	case "unlock-login":
		var err error
		ip, err = netip.ParseAddr(options.ip)
		if err != nil || ip.Zone() != "" {
			return errors.New("请通过 --ip 指定有效的来源 IP")
		}
		ip = ip.Unmap()
	case "reset-username":
		if err := auth.ValidateUsername(value); err != nil {
			return err
		}
	case "reset-password":
		password, err := readPassword("新密码: ")
		if err != nil {
			return err
		}
		confirm, err := readPassword("确认密码: ")
		if err != nil {
			return err
		}
		if password != confirm {
			return errors.New("两次密码不一致")
		}
		if err = auth.ValidatePassword(password); err != nil {
			return err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		column, value = "password_hash", string(hash)
	}

	db, err := openPanelDatabase(cfg, "rw")
	if err != nil {
		return err
	}
	defer db.Close()
	if command == "unlock-login" {
		if _, err = db.Exec("DELETE FROM login_attempts WHERE ip = ?", ip.String()); err != nil {
			return err
		}
		fmt.Printf("已清除 %s 的登录失败记录\n", ip)
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	if err = tx.QueryRow("SELECT id FROM users ORDER BY id LIMIT 1").Scan(&id); err != nil {
		return fmt.Errorf("无法读取面板管理员：%w", err)
	}
	if _, err = tx.Exec("UPDATE users SET "+column+" = ?, auth_version = auth_version + 1 WHERE id = ?", value, id); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM auth_sessions WHERE user_id = ?", id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	fmt.Println("管理员凭据已更新，请重新登录")
	return nil
}
