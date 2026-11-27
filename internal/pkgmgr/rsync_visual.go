package pkgmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"unicode"

	"ezpanel/internal/pathsafe"
)

const rsyncStart = "# BEGIN EZPANEL RECEIVERS"
const rsyncEnd = "# END EZPANEL RECEIVERS"
const rsyncSecrets = "/etc/rsyncd.ezpanel.secrets"

var rsyncName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var rsyncConfigValue = regexp.MustCompile(`^[^\r\n\x00\\]*$`)

type RsyncGlobal struct {
	Address        string `json:"address"`
	Port           int    `json:"port"`
	MaxConnections int    `json:"maxConnections"`
	Timeout        int    `json:"timeout"`
	DontCompress   string `json:"dontCompress"`
	Allow          string `json:"allow"`
}

type RsyncReceiver struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Enabled  bool   `json:"enabled"`
	Allow    string `json:"allow"`
}

type RsyncVisual struct {
	RsyncGlobal
	Revision  string          `json:"revision"`
	Receivers []RsyncReceiver `json:"receivers"`
}

func (m *Manager) RsyncVisual(ctx context.Context) (RsyncVisual, error) {
	raw, err := m.Settings(ctx, "rsync")
	if err != nil {
		return RsyncVisual{}, err
	}
	return parseRsyncVisual(raw)
}

func parseRsyncVisual(raw Settings) (RsyncVisual, error) {
	out := RsyncVisual{
		RsyncGlobal: RsyncGlobal{Address: "0.0.0.0", Port: 873},
		Revision:    raw.Revision, Receivers: []RsyncReceiver{},
	}
	inModule := false
	for line := range strings.SplitSeq(raw.Content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inModule = !strings.EqualFold(line, "[global]")
		}
		key, value, ok := strings.Cut(line, "=")
		if inModule || !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "address":
			out.Address = value
		case "port":
			if port, err := strconv.Atoi(value); err == nil {
				out.Port = port
			}
		case "max connections":
			out.MaxConnections, _ = strconv.Atoi(value)
		case "timeout":
			out.Timeout, _ = strconv.Atoi(value)
		case "dont compress":
			out.DontCompress = value
		case "hosts allow":
			out.Allow = strings.Join(strings.Fields(strings.ReplaceAll(value, ",", " ")), "\n")
		}
	}
	if _, managed, ok := strings.Cut(raw.Content, rsyncStart+"\n"); ok {
		body, _, _ := strings.Cut(managed, rsyncEnd)
		for line := range strings.SplitSeq(body, "\n") {
			if encoded, ok := strings.CutPrefix(line, "# Receiver "); ok {
				var row RsyncReceiver
				if err := json.Unmarshal([]byte(encoded), &row); err != nil {
					return out, errors.New("Rsync 接收任务配置无法读取")
				}
				out.Receivers = append(out.Receivers, row)
			}
		}
	}
	return out, nil
}

func (m *Manager) SaveRsyncVisual(ctx context.Context, in RsyncVisual) error {
	return m.ApplicationChange("rsync", func() error {
		raw, err := m.Settings(ctx, "rsync")
		if err != nil {
			return err
		}
		if raw.Revision != in.Revision {
			return errors.New("配置已修改，请刷新后保存")
		}
		existing, err := m.Settings(ctx, "rsync", "receiver-secrets")
		if err != nil {
			return err
		}
		next, credentials, err := m.renderRsyncVisual(raw.Content, existing.Content, in)
		if err != nil {
			return err
		}
		previous, err := parseRsyncVisual(raw)
		if err != nil {
			return err
		}
		restart := false
		if previous.Address != in.Address || previous.Port != in.Port {
			state, err := m.Status(ctx, "rsync")
			if err != nil {
				return err
			}
			restart = state.Running != nil && *state.Running
		}
		restore := func(kind string, prior Settings, written string) error {
			hash := sha256.Sum256([]byte(written))
			return m.saveSettingsUnlocked(context.WithoutCancel(ctx), "rsync", prior.Content, hex.EncodeToString(hash[:]), false, kind)
		}
		if err := m.prepareRsync(); err != nil {
			return err
		}
		if err := m.saveSettingsUnlocked(ctx, "rsync", credentials, existing.Revision, false, "receiver-secrets"); err != nil {
			return err
		}
		if err := m.saveSettingsUnlocked(ctx, "rsync", next, raw.Revision, false); err != nil {
			return errors.Join(err, restore("receiver-secrets", existing, credentials))
		}
		if restart {
			if _, err := m.Service(ctx, "rsync", "restart"); err != nil {
				rollbackErr := errors.Join(restore("", raw, next), restore("receiver-secrets", existing, credentials))
				if rollbackErr != nil {
					return errors.Join(err, rollbackErr)
				}
				_, restartErr := m.Service(context.WithoutCancel(ctx), "rsync", "restart")
				return errors.Join(fmt.Errorf("监听更新失败，已恢复原配置：%w", err), restartErr)
			}
		}
		return nil
	})
}

func (m *Manager) RsyncReceiverPassword(ctx context.Context, name string) (string, error) {
	config, err := m.RsyncVisual(ctx)
	if err != nil {
		return "", err
	}
	username := ""
	for _, row := range config.Receivers {
		if row.Name == name {
			username = row.Username
			break
		}
	}
	if username == "" {
		return "", errors.New("接收任务不存在")
	}
	secrets, err := m.Settings(ctx, "rsync", "receiver-secrets")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(secrets.Content, "\n") {
		if user, password, ok := strings.Cut(line, ":"); ok && user == username {
			return password, nil
		}
	}
	return "", errors.New("接收任务密码不可用")
}

func (m *Manager) renderRsyncVisual(content, secrets string, in RsyncVisual) (string, string, error) {
	if in.Address != "" {
		if ip, err := netip.ParseAddr(in.Address); err != nil || ip.Zone() != "" {
			return "", "", errors.New("监听地址须为有效 IP")
		}
	}
	if in.Port < 1 || in.Port > 65535 || in.MaxConnections < 0 || in.MaxConnections > 10000 || in.Timeout < 0 || in.Timeout > 86400 ||
		!rsyncConfigValue.MatchString(in.DontCompress) || len(in.Receivers) > 100 {
		return "", "", errors.New("Rsync 全局配置无效")
	}
	allow, err := ParseFail2banIPs(in.Allow)
	if err != nil {
		return "", "", err
	}
	previousPasswords := map[string]string{}
	for line := range strings.SplitSeq(secrets, "\n") {
		if user, password, ok := strings.Cut(line, ":"); ok {
			previousPasswords[user] = password
		}
	}

	before, managed, found := strings.Cut(content, rsyncStart+"\n")
	if found {
		_, after, ok := strings.Cut(managed, rsyncEnd)
		if !ok {
			return "", "", errors.New("Rsync 接收任务配置标记不完整")
		}
		before += after
	}
	global := map[string]string{
		"address": in.Address, "port": strconv.Itoa(in.Port),
		"max connections": strconv.Itoa(in.MaxConnections), "timeout": strconv.Itoa(in.Timeout),
		"dont compress": strings.Join(strings.Fields(strings.ReplaceAll(in.DontCompress, ",", " ")), " "),
		"hosts allow":   strings.Join(allow, " "),
		"log file":      m.layout().RsyncLog(),
	}
	keys := []string{"address", "port", "max connections", "timeout", "dont compress", "hosts allow", "log file"}
	var next, credentials strings.Builder
	for _, key := range keys {
		if value := global[key]; value != "" {
			fmt.Fprintf(&next, "%s = %s\n", key, value)
		}
	}
	names := map[string]bool{}
	users := map[string]bool{}
	insideModule := false
	for line := range strings.SplitSeq(strings.TrimSpace(before), "\n") {
		text := strings.TrimSpace(line)
		if name, ok := strings.CutPrefix(text, "["); ok {
			insideModule = !strings.EqualFold(text, "[global]")
			if insideModule {
				names[strings.TrimSpace(strings.TrimSuffix(name, "]"))] = true
			}
		}
		if key, _, ok := strings.Cut(text, "="); !insideModule && ok {
			if _, managed := global[strings.TrimSpace(key)]; managed {
				continue
			}
		}
		next.WriteString(line + "\n")
	}
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		return "", "", err
	}
	next.WriteString("\n" + rsyncStart + "\n")
	for _, row := range in.Receivers {
		if row.Name == "" {
			row.Name = row.Username
		}
		receiverAllow, err := ParseFail2banIPs(row.Allow)
		if err != nil {
			return "", "", err
		}
		row.Allow = strings.Join(receiverAllow, "\n")
		if !rsyncName.MatchString(row.Name) || !rsyncName.MatchString(row.Username) || names[row.Name] || users[row.Username] ||
			!rsyncConfigValue.MatchString(row.Path) ||
			strings.IndexFunc(row.Password, unicode.IsControl) >= 0 {
			return "", "", errors.New("接收任务名称、认证用户或配置无效，名称与账号不能重复")
		}
		real, err := policy.Check(row.Path, true)
		if err != nil || policy.CheckTree(real) != nil {
			return "", "", errors.New("接收目录无效或指向受保护目录")
		}
		info, err := os.Stat(real)
		if err != nil || !info.IsDir() {
			return "", "", errors.New("接收目录必须是已存在的目录")
		}
		owner := info.Sys().(*syscall.Stat_t)
		row.Path = real
		password := row.Password
		if password == "" {
			password = previousPasswords[row.Username]
		}
		if password == "" || len(password) > 256 {
			return "", "", errors.New("请设置接收任务密码")
		}
		moduleNames := []string{row.Name}
		if row.Username != row.Name {
			if names[row.Username] {
				return "", "", errors.New("接收用户名与已有任务标识冲突")
			}
			moduleNames = append(moduleNames, row.Username)
		}
		users[row.Username] = true
		fmt.Fprintf(&credentials, "%s:%s\n", row.Username, password)
		row.Password = ""
		data, _ := json.Marshal(row)
		fmt.Fprintf(&next, "# Receiver %s\n", data)
		for _, name := range moduleNames {
			names[name] = true
			if !row.Enabled {
				continue
			}
			// 以目录归属用户写入，避免 nobody 无法写入默认网站目录。
			fmt.Fprintf(&next, "[%s]\npath = %s\nuid = %d\ngid = %d\nuse chroot = yes\nmunge symlinks = yes\nlist = no\nread only = false\nwrite only = true\nauth users = %s\nsecrets file = %s\n", name, strings.ReplaceAll(row.Path, "%", "%%"), owner.Uid, owner.Gid, row.Username, rsyncSecrets)
			if len(receiverAllow) > 0 {
				fmt.Fprintf(&next, "hosts allow = %s\nhosts deny = *\n", strings.Join(receiverAllow, " "))
			}
			next.WriteByte('\n')
		}
	}

	next.WriteString(rsyncEnd + "\n")
	return next.String(), credentials.String(), nil
}
