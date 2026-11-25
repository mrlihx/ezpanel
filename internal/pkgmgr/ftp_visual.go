package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const ftpConfDir = "/etc/pure-ftpd/conf"
const ftpConfig = "/etc/pure-ftpd/pure-ftpd.conf"
const ftpDefaultDB = "/etc/pure-ftpd/pureftpd.pdb"

type FTPVisual struct {
	Address      string `json:"address"`
	Port         int    `json:"port"`
	PassiveStart int    `json:"passiveStart"`
	PassiveEnd   int    `json:"passiveEnd"`
	Logging      bool   `json:"logging"`
}

func (m *Manager) ftpValues(state State) (map[string]string, error) {
	values := map[string]string{}
	if state.PkgManager == "apt-get" {
		for _, key := range []string{"Bind", "PassivePortRange", "AltLog", "PureDB"} {
			if b, err := os.ReadFile(filepath.Join(ftpConfDir, key)); err == nil {
				values[key] = strings.TrimSpace(string(b))
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		return values, nil
	}
	data, err := os.ReadFile(ftpConfig)
	if err != nil {
		return nil, err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && !strings.HasPrefix(fields[0], "#") {
			values[fields[0]] = strings.Join(fields[1:], " ")
		}
	}
	return values, nil
}

func (m *Manager) FTPSettings(ctx context.Context) (FTPVisual, error) {
	state, err := m.Status(ctx, "ftp")
	if err != nil {
		return FTPVisual{}, err
	}
	if !state.Manageable {
		return FTPVisual{}, ErrUnsupported
	}
	values, err := m.ftpValues(state)
	out := FTPVisual{Port: 21, Logging: values["AltLog"] != ""}
	if addr, port, ok := strings.Cut(values["Bind"], ","); ok {
		out.Address = addr
		if n, e := strconv.Atoi(strings.TrimSpace(port)); e == nil {
			out.Port = n
		}
	} else if n, e := strconv.Atoi(strings.TrimSpace(values["Bind"])); e == nil {
		out.Port = n
	}
	parts := strings.Fields(values["PassivePortRange"])
	if len(parts) == 2 {
		out.PassiveStart, _ = strconv.Atoi(parts[0])
		out.PassiveEnd, _ = strconv.Atoi(parts[1])
	}
	return out, err
}

func (m *Manager) SaveFTPSettings(ctx context.Context, in FTPVisual) error {
	if in.Address != "" {
		if ip, err := netip.ParseAddr(in.Address); err != nil || ip.Zone() != "" {
			return errors.New("请输入有效的监听 IP，留空表示全部地址")
		}
	}
	if in.Port < 1 || in.Port > 65535 || in.PassiveStart < 0 || in.PassiveEnd > 65535 ||
		(in.PassiveStart == 0) != (in.PassiveEnd == 0) || in.PassiveEnd < in.PassiveStart {
		return errors.New("FTP 端口或被动端口范围无效")
	}
	state, err := m.Status(ctx, "ftp")
	if err != nil {
		return err
	}
	if !state.Manageable {
		return ErrUnsupported
	}
	values, err := m.ftpValues(state)
	if err != nil {
		return err
	}
	bind := strconv.Itoa(in.Port)
	if in.Address != "" {
		bind = in.Address + "," + bind
	}
	update := map[string]string{"Bind": bind, "PassivePortRange": "", "AltLog": ""}
	if in.PassiveStart > 0 {
		update["PassivePortRange"] = fmt.Sprintf("%d %d", in.PassiveStart, in.PassiveEnd)
	}
	if in.Logging {
		if values["AltLog"] != "" {
			update["AltLog"] = values["AltLog"]
		} else {
			if err := ensurePublicTree(m.layout().Root(), m.layout().LogDir(), m.layout().PureFTPdLogDir()); err != nil {
				return err
			}
			update["AltLog"] = "clf:" + m.layout().PureFTPdLog()
		}
	}
	return m.writeFTPValues(ctx, state, update)
}

func (m *Manager) writeFTPValues(ctx context.Context, state State, values map[string]string) error {
	files := map[string][]byte{}
	before := map[string][]byte{}
	if state.PkgManager == "apt-get" {
		for key, value := range values {
			path := filepath.Join(ftpConfDir, key)
			if value == "" {
				files[path] = nil
			} else {
				files[path] = []byte(value + "\n")
			}
		}
	} else {
		old, err := os.ReadFile(ftpConfig)
		if err != nil {
			return err
		}
		var content strings.Builder
		for line := range strings.SplitSeq(strings.TrimSuffix(string(old), "\n"), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				if _, ok := values[fields[0]]; ok {
					continue
				}
			}
			content.WriteString(line + "\n")
		}
		for key, value := range values {
			if value != "" {
				fmt.Fprintf(&content, "%s %s\n", key, value)
			}
		}
		files[ftpConfig] = []byte(content.String())
	}
	for path := range files {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		before[path] = data
	}
	rollback := func() error {
		var err error
		for path, data := range before {
			err = errors.Join(err, writeHostConfig(path, data))
		}
		return err
	}
	for path, data := range files {
		if err := writeHostConfig(path, data); err != nil {
			return errors.Join(err, rollback())
		}
	}
	if state.Running != nil && *state.Running {
		if result, err := m.Run(ctx, 45*time.Second, nil, systemctlPath, "restart", state.Unit); err != nil {
			err = errors.Join(fmt.Errorf("Pure-FTPd 重启失败: %s", strings.TrimSpace(result.Stderr)), rollback())
			_, restore := m.Run(context.WithoutCancel(ctx), 45*time.Second, nil, systemctlPath, "restart", state.Unit)
			return errors.Join(err, restore)
		}
	}
	return nil
}

func (m *Manager) ftpAuthPath(state State) (string, error) {
	values, err := m.ftpValues(state)
	if err != nil {
		return "", err
	}
	db := values["PureDB"]
	if db == "" {
		db = ftpDefaultDB
	}
	if !filepath.IsAbs(db) {
		return "", errors.New("PureDB 路径必须是绝对路径")
	}
	return db, nil
}
