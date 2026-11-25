package pkgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
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
	ReadOnly bool   `json:"readOnly"`
	Exclude  string `json:"exclude"`
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
	out := RsyncVisual{
		RsyncGlobal: RsyncGlobal{Port: 873},
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
	raw, err := m.Settings(ctx, "rsync")
	if err != nil {
		return err
	}
	if raw.Revision != in.Revision {
		return errors.New("配置已修改，请刷新后保存")
	}
	if in.Address != "" {
		if ip, err := netip.ParseAddr(in.Address); err != nil || ip.Zone() != "" {
			return errors.New("监听地址须为有效 IP")
		}
	}
	if in.Port < 1 || in.Port > 65535 || in.MaxConnections < 0 || in.MaxConnections > 10000 || in.Timeout < 0 || in.Timeout > 86400 ||
		!rsyncConfigValue.MatchString(in.DontCompress) || len(in.Receivers) > 100 {
		return errors.New("Rsync 全局配置无效")
	}
	allow, err := ParseFail2banIPs(in.Allow)
	if err != nil {
		return err
	}
	existing, err := m.Settings(ctx, "rsync", "receiver-secrets")
	if err != nil {
		return err
	}
	previousPasswords := map[string]string{}
	for line := range strings.SplitSeq(existing.Content, "\n") {
		if user, password, ok := strings.Cut(line, ":"); ok {
			previousPasswords[user] = password
		}
	}

	before, managed, found := strings.Cut(raw.Content, rsyncStart+"\n")
	if found {
		_, after, ok := strings.Cut(managed, rsyncEnd)
		if !ok {
			return errors.New("Rsync 接收任务配置标记不完整")
		}
		before += after
	}
	global := map[string]string{
		"address": in.Address, "port": strconv.Itoa(in.Port),
		"max connections": strconv.Itoa(in.MaxConnections), "timeout": strconv.Itoa(in.Timeout),
		"dont compress": strings.Join(strings.Fields(strings.ReplaceAll(in.DontCompress, ",", " ")), " "),
		"hosts allow":   strings.Join(allow, " "),
	}
	keys := []string{"address", "port", "max connections", "timeout", "dont compress", "hosts allow"}
	var next, credentials strings.Builder
	for _, key := range keys {
		if value := global[key]; value != "" {
			fmt.Fprintf(&next, "%s = %s\n", key, value)
		}
	}
	names := map[string]bool{}
	users := map[string]bool{}
	insideModule := false
	for line := range strings.SplitSeq(before, "\n") {
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
		return err
	}
	next.WriteString("\n" + rsyncStart + "\n")
	for _, row := range in.Receivers {
		var filters []string
		for line := range strings.SplitSeq(row.Exclude, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				if !rsyncConfigValue.MatchString(line) {
					return errors.New("接收任务排除规则格式无效")
				}
				filters = append(filters, "- "+strings.ReplaceAll(line, "%", "%%"))
			}
		}
		if !rsyncName.MatchString(row.Name) || !rsyncName.MatchString(row.Username) || names[row.Name] || users[row.Username] ||
			!rsyncConfigValue.MatchString(row.Path) || len(row.Exclude) > 8192 ||
			strings.IndexFunc(row.Password, unicode.IsControl) >= 0 {
			return errors.New("接收任务名称、认证用户或配置无效，名称与账号不能重复")
		}
		real, err := policy.Check(row.Path, true)
		if err != nil || policy.CheckTree(real) != nil {
			return errors.New("接收目录无效或指向受保护目录")
		}
		if info, err := os.Stat(real); err != nil || !info.IsDir() {
			return errors.New("接收目录必须是已存在的目录")
		}
		row.Path = real
		password := row.Password
		if password == "" {
			password = previousPasswords[row.Username]
		}
		if password == "" || len(password) > 256 {
			return errors.New("请设置接收任务密码")
		}
		names[row.Name], users[row.Username] = true, true
		fmt.Fprintf(&credentials, "%s:%s\n", row.Username, password)
		row.Password = ""
		data, _ := json.Marshal(row)
		fmt.Fprintf(&next, "# Receiver %s\n", data)
		if !row.Enabled {
			continue
		}
		fmt.Fprintf(&next, "[%s]\npath = %s\nuid = 65534\ngid = 65534\nuse chroot = yes\nmunge symlinks = yes\nlist = no\nread only = %t\nwrite only = %t\nauth users = %s\nsecrets file = %s\nrefuse options = delete\n", row.Name, strings.ReplaceAll(row.Path, "%", "%%"), row.ReadOnly, !row.ReadOnly, row.Username, rsyncSecrets)
		if len(filters) != 0 {
			fmt.Fprintf(&next, "filter = %s\n", strings.Join(filters, " "))
		}
		next.WriteByte('\n')
	}
	next.WriteString(rsyncEnd + "\n")
	if err := m.SaveSettings(ctx, "rsync", credentials.String(), existing.Revision, "receiver-secrets"); err != nil {
		return err
	}
	if err := m.SaveSettings(ctx, "rsync", next.String(), raw.Revision); err != nil {
		current, readErr := m.Settings(context.WithoutCancel(ctx), "rsync", "receiver-secrets")
		if readErr != nil {
			return errors.Join(err, readErr)
		}
		return errors.Join(err, m.SaveSettings(context.WithoutCancel(ctx), "rsync", existing.Content, current.Revision, "receiver-secrets"))
	}
	return nil
}
