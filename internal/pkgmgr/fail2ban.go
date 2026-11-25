package pkgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const fail2banConfig = "/etc/fail2ban/jail.d/zz-ezpanel.local"
const fail2banBlacklist = "/etc/fail2ban/ezpanel-blacklist.nft"
const fail2banBlacklistUnit = "/etc/systemd/system/fail2ban.service.d/ezpanel-blacklist.conf"
const fail2banUnit = "fail2ban.service"
const systemctlPath = "/usr/bin/systemctl"

var fail2banJails = map[string]string{"sshd": "sshd", "ftpd": "pure-ftpd", "mysql": "mysqld-auth"}
var fail2banPorts = map[string]int{"sshd": 22, "ftpd": 21, "mysql": 3306}
var mysqlAuthLogPath = regexp.MustCompile(`^/[a-zA-Z0-9_./-]+$`)

type Fail2banRule struct {
	Service   string `json:"service"`
	Enabled   bool   `json:"enabled"`
	MaxRetry  int    `json:"maxRetry"`
	FindTime  int    `json:"findTime"`
	BanTime   int    `json:"banTime"`
	Port      int    `json:"port"`
	LogPath   string `json:"logPath,omitempty"`
	inherited bool
}

type Fail2banSettings struct {
	Installed bool                    `json:"installed"`
	Running   bool                    `json:"running"`
	Rules     map[string]Fail2banRule `json:"rules"`
	Blacklist []string                `json:"blacklist"`
	Whitelist []string                `json:"whitelist"`
}

func (m *Manager) Fail2banSettings(ctx context.Context) (Fail2banSettings, error) {
	out := Fail2banSettings{Rules: map[string]Fail2banRule{}, Blacklist: []string{}, Whitelist: []string{}}
	content, err := m.ReadFile(fail2banConfig)
	if err == nil {
		first, _, _ := strings.Cut(string(content), "\n")
		if err = json.Unmarshal([]byte(strings.TrimPrefix(first, "# ")), &out); err != nil {
			return out, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return out, err
	}

	state, err := m.Status(ctx, "fail2ban")
	if err != nil {
		return out, err
	}
	out.Installed = state.Installed && state.Manageable
	out.Running = state.Running != nil && *state.Running
	if !out.Installed || !out.Running {
		return out, nil
	}
	// 识别发行版已开启的 SSH jail，首次使用快捷入口时可直接关闭或编辑。
	if _, exists := out.Rules["sshd"]; !exists {
		if _, err := m.fail2banClient(ctx, "status", "sshd"); err == nil {
			rule := Fail2banRule{Service: "sshd", Enabled: true, Port: 22, MaxRetry: 5, FindTime: 600, BanTime: 600, inherited: true}
			for name, target := range map[string]*int{"maxretry": &rule.MaxRetry, "findtime": &rule.FindTime, "bantime": &rule.BanTime} {
				if value, err := m.fail2banClient(ctx, "get", "sshd", name); err == nil {
					if number, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
						*target = number
					}
				}
			}
			if sshd, err := m.LookPath("sshd"); err == nil {
				result, _ := m.Run(ctx, 3*time.Second, nil, sshd, "-T")
				for line := range strings.SplitSeq(result.Stdout, "\n") {
					if port, ok := strings.CutPrefix(line, "port "); ok {
						if value, err := strconv.Atoi(strings.TrimSpace(port)); err == nil {
							rule.Port = value
						}
						break
					}
				}
			}
			out.Rules["sshd"] = rule
		}
	}
	return out, nil
}

func ValidateFail2banRule(rule *Fail2banRule) error {
	if fail2banPorts[rule.Service] == 0 || rule.Port < 1 || rule.Port > 65535 ||
		rule.MaxRetry < 1 || rule.MaxRetry > 999 || rule.FindTime < 1 || rule.BanTime < 1 {
		return errors.New("保护服务、端口或时间参数无效")
	}
	rule.LogPath = strings.TrimSpace(rule.LogPath)
	if rule.Service == "mysql" && (rule.Enabled || rule.LogPath != "") {
		if !mysqlAuthLogPath.MatchString(rule.LogPath) || filepath.Clean(rule.LogPath) != rule.LogPath {
			return errors.New("请填写 MySQL 认证失败日志的绝对路径")
		}
		if rule.Enabled {
			if info, err := os.Stat(rule.LogPath); err != nil || !info.Mode().IsRegular() {
				return errors.New("找不到 MySQL 日志文件，请先启用 MySQL 认证失败日志")
			}
		}
	}
	return nil
}

func ParseFail2banIPs(value string) ([]string, error) {
	rows := []string{}
	seen := map[string]bool{}
	for line := range strings.SplitSeq(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(line); err == nil {
			line = prefix.Masked().String()
		} else if address, err := netip.ParseAddr(line); err == nil && address.Zone() == "" {
			line = address.Unmap().String()
		} else {
			return nil, fmt.Errorf("无效的 IP 或网段：%s", line)
		}
		if !seen[line] {
			rows = append(rows, line)
			seen[line] = true
		}
	}
	return rows, nil
}

func (m *Manager) UpdateFail2ban(ctx context.Context, enable bool, update func(*Fail2banSettings)) error {
	if err := m.lock("fail2ban"); err != nil {
		return err
	}
	defer m.unlock("fail2ban")
	settings, err := m.Fail2banSettings(ctx)
	if err != nil {
		return err
	}
	if !settings.Installed {
		return errors.New("请先在应用商店安装 Fail2ban")
	}
	// 白名单、静态封禁和 jail 配置通过同一入口更新。
	update(&settings)
	if settings.Rules["mysql"].Enabled {
		if _, err := m.LookPath("nft"); err != nil {
			return errors.New("MySQL 保护需要宿主机安装 nftables")
		}
	}
	if settings.Rules["sshd"].inherited {
		delete(settings.Rules, "sshd")
	}
	old, err := os.ReadFile(fail2banConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	saved, err := json.Marshal(Fail2banSettings{Rules: settings.Rules, Blacklist: settings.Blacklist, Whitelist: settings.Whitelist})
	if err != nil {
		return err
	}
	var config strings.Builder
	fmt.Fprintf(&config, "# %s\n", saved)
	if len(settings.Whitelist) > 0 {
		fmt.Fprintf(&config, "\n[DEFAULT]\nignoreip = 127.0.0.0/8 ::1 %s\n", strings.Join(settings.Whitelist, " "))
	}
	for _, name := range []string{"sshd", "ftpd", "mysql"} {
		rule, exists := settings.Rules[name]
		if !exists {
			continue
		}
		fmt.Fprintf(&config, "\n[%s]\nenabled = %t\n", fail2banJails[name], rule.Enabled)
		if !rule.Enabled && rule.MaxRetry == 0 {
			continue
		}
		fmt.Fprintf(&config, "port = %d\nmaxretry = %d\nfindtime = %d\nbantime = %d\nusedns = no\n", rule.Port, rule.MaxRetry, rule.FindTime, rule.BanTime)
		if name == "mysql" && rule.LogPath != "" {
			fmt.Fprintf(&config, "backend = auto\nlogpath = %s\naction = nftables[name=mysqld-auth, port=%d]\n         nftables[name=mysqld-forward, chain=ezpanel-forward, chain_hook=forward, type=custom, rule_match-custom=meta l4proto tcp ct original proto-dst %d]\n", rule.LogPath, rule.Port, rule.Port)
		} else {
			config.WriteString("backend = systemd\n")
		}
	}

	if err = writeHostConfig(fail2banConfig, []byte(config.String())); err != nil {
		return err
	}
	if _, err = m.fail2banClient(ctx, "-t"); err == nil {
		err = m.applyFail2banBlacklist(ctx, settings, settings.Running)
	}
	if err == nil {
		if settings.Running {
			_, err = m.fail2banClient(ctx, "reload")
		} else if enable {
			_, err = m.Run(ctx, 25*time.Second, nil, systemctlPath, "enable", "--now", fail2banUnit)
		}
	}
	if err != nil {
		rollback := context.WithoutCancel(ctx)
		restoreErr := writeHostConfig(fail2banConfig, old)
		if restoreErr == nil {
			var previous Fail2banSettings
			if len(old) != 0 {
				first, _, _ := strings.Cut(string(old), "\n")
				_ = json.Unmarshal([]byte(strings.TrimPrefix(first, "# ")), &previous)
			}
			restoreErr = m.applyFail2banBlacklist(rollback, previous, settings.Running)
		}
		if settings.Running {
			_, _ = m.fail2banClient(rollback, "reload")
		}
		return errors.Join(err, restoreErr)
	}
	return nil
}

func (m *Manager) applyFail2banBlacklist(ctx context.Context, settings Fail2banSettings, apply bool) error {
	_, previous := os.Stat(fail2banBlacklistUnit)
	if len(settings.Blacklist) == 0 && errors.Is(previous, os.ErrNotExist) {
		return nil
	}
	nft, err := m.LookPath("nft")
	if err != nil {
		return errors.New("IP 黑名单需要宿主机安装 nftables")
	}
	var rules strings.Builder
	rules.WriteString("add table inet ezpanel_fail2ban\ndelete table inet ezpanel_fail2ban\n")
	if len(settings.Blacklist) != 0 {
		rules.WriteString("table inet ezpanel_fail2ban {\n")
		for _, list := range []struct {
			name string
			rows []string
		}{{"allow", settings.Whitelist}, {"deny", settings.Blacklist}} {
			for _, family := range []string{"ipv4", "ipv6"} {
				var addresses []string
				for _, row := range list.rows {
					if strings.Contains(row, ":") == (family == "ipv6") {
						addresses = append(addresses, row)
					}
				}
				fmt.Fprintf(&rules, "set %s_%s { type %s_addr; flags interval; auto-merge;", list.name, family, family)
				if len(addresses) > 0 {
					fmt.Fprintf(&rules, " elements = { %s };", strings.Join(addresses, ", "))
				}
				rules.WriteString(" }\n")
			}
		}
		rules.WriteString("chain sources {\n ip saddr @allow_ipv4 return\n ip6 saddr @allow_ipv6 return\n ip saddr @deny_ipv4 drop\n ip6 saddr @deny_ipv6 drop\n}\n")
		rules.WriteString("chain incoming { type filter hook input priority -5; policy accept; jump sources; }\n")
		rules.WriteString("chain forwarded { type filter hook forward priority -5; policy accept; jump sources; }\n}\n")
	}
	if err = writeHostConfig(fail2banBlacklist, []byte(rules.String())); err != nil {
		return err
	}
	args := []string{"-c", "-f", fail2banBlacklist}
	if apply {
		args = args[1:]
	}
	if result, err := m.Run(ctx, 8*time.Second, nil, nft, args...); err != nil {
		return fmt.Errorf("应用 IP 黑名单失败: %s", strings.TrimSpace(result.Stderr))
	}
	if previous == nil {
		return nil
	}
	unit := fmt.Sprintf("[Service]\nExecStartPre=%s -f %s\nExecStopPost=-%s delete table inet ezpanel_fail2ban\n", nft, fail2banBlacklist, nft)
	if err = writeHostConfig(fail2banBlacklistUnit, []byte(unit)); err != nil {
		return err
	}
	_, err = m.Run(ctx, 10*time.Second, nil, systemctlPath, "daemon-reload")
	return err
}

func writeHostConfig(path string, content []byte) error {
	if content == nil {
		if err := os.Remove(path); !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ezpanel-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(content)
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (m *Manager) fail2banClient(ctx context.Context, args ...string) (string, error) {
	client, err := m.LookPath("fail2ban-client")
	if err != nil {
		return "", err
	}
	result, err := m.Run(ctx, 20*time.Second, nil, client, args...)
	if err != nil {
		return "", fmt.Errorf("Fail2ban 操作失败: %s", strings.TrimSpace(result.Stderr+" "+result.Stdout))
	}
	return result.Stdout, nil
}

type Fail2banStats struct {
	TotalBanned     int `json:"totalBanned"`
	TotalFailed     int `json:"totalFailed"`
	CurrentlyBanned int `json:"currentlyBanned"`
	CurrentlyFailed int `json:"currentlyFailed"`
}

type Fail2banLogView struct {
	Log   string         `json:"log"`
	IPs   []string       `json:"ips"`
	Stats *Fail2banStats `json:"stats,omitempty"`
}

func (m *Manager) Fail2banLogs(ctx context.Context, service string) (Fail2banLogView, error) {
	var view Fail2banLogView
	jail := fail2banJails[service]
	if jail == "" {
		return view, ErrUnsupported
	}
	if service == "sshd" {
		status, err := m.fail2banClient(ctx, "status", jail)
		if err != nil {
			return view, nil
		}
		stats := Fail2banStats{}
		counters := map[string]*int{
			"Total banned": &stats.TotalBanned, "Total failed": &stats.TotalFailed,
			"Currently banned": &stats.CurrentlyBanned, "Currently failed": &stats.CurrentlyFailed,
		}
		for line := range strings.SplitSeq(status, "\n") {
			key, value, _ := strings.Cut(strings.TrimLeft(line, " |`-\t"), ":")
			if key == "Banned IP list" {
				view.IPs = strings.Fields(value)
			} else if counter, ok := counters[key]; ok {
				count, err := strconv.Atoi(strings.TrimSpace(value))
				if err == nil {
					*counter = count
					delete(counters, key)
				}
			}
		}
		if len(counters) != 0 {
			return view, errors.New("无法读取 SSH 防护统计")
		}
		view.Stats = &stats
		return view, nil
	}
	result, _ := m.Run(ctx, 5*time.Second, nil, "/usr/bin/tail", "-n", "800", "/var/log/fail2ban.log")
	if result.Stdout == "" {
		result, _ = m.Run(ctx, 5*time.Second, nil, "/usr/bin/journalctl", "-u", fail2banUnit, "-n", "800", "--no-pager")
	}
	var lines []string
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		if strings.Contains(line, "["+jail+"]") {
			lines = append(lines, line)
		}
	}
	bans, _ := m.fail2banClient(ctx, "get", jail, "banip")
	view.Log = strings.Join(lines, "\n")
	view.IPs = strings.Fields(bans)
	return view, nil
}

func (m *Manager) UnbanFail2banIP(ctx context.Context, service string, ip netip.Addr) error {
	jail := fail2banJails[service]
	if jail == "" {
		return ErrUnsupported
	}
	_, err := m.fail2banClient(ctx, "set", jail, "unbanip", ip.String())
	return err
}
