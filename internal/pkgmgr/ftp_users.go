package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"ezpanel/internal/executor"
	"ezpanel/internal/pathsafe"
)

var ftpUsername = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,63}$`)

type FTPUser struct {
	Name     string `json:"name"`
	Password string `json:"password,omitempty"`
	Root     string `json:"root"`
	Remark   string `json:"remark"`
	Enabled  bool   `json:"enabled"`
}

func (m *Manager) FTPUsers(ctx context.Context) ([]FTPUser, error) {
	state, err := m.Status(ctx, "ftp")
	if err != nil {
		return nil, err
	}
	if !state.Manageable {
		return nil, ErrUnsupported
	}
	db, err := m.ftpAuthPath(state)
	if err != nil {
		return nil, err
	}
	return readFTPUsers(db)
}

func ftpUserFiles(db string) (string, string) {
	base := strings.TrimSuffix(db, filepath.Ext(db))
	return base + ".passwd", base + ".disabled"
}

func readFTPUsers(db string) ([]FTPUser, error) {
	passwd, disabledPath := ftpUserFiles(db)
	data, err := os.ReadFile(passwd)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	disabled, err := os.ReadFile(disabledPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	off := map[string]bool{}
	for _, name := range strings.Fields(string(disabled)) {
		off[name] = true
	}
	rows := []FTPUser{}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.SplitN(line, ":", 7)
		if len(fields) < 6 || !ftpUsername.MatchString(fields[0]) {
			continue
		}
		root := strings.TrimSuffix(strings.TrimSuffix(fields[5], "/."), "/")
		rows = append(rows, FTPUser{Name: fields[0], Root: root, Remark: fields[4], Enabled: !off[fields[0]]})
	}
	return rows, nil
}

func (m *Manager) ChangeFTPUser(ctx context.Context, action string, in FTPUser) error {
	if !ftpUsername.MatchString(in.Name) || len(in.Remark) > 256 || strings.Contains(in.Remark, ":") ||
		strings.IndexFunc(in.Remark+in.Password, unicode.IsControl) >= 0 {
		return errors.New("FTP 用户名、备注或密码格式无效")
	}
	if (action == "create" || in.Password != "") && (len(in.Password) < 10 || len(in.Password) > 128) {
		return errors.New("请输入 10 到 128 字节的 FTP 密码")
	}
	state, err := m.Status(ctx, "ftp")
	if err != nil {
		return err
	}
	if !state.Manageable {
		return ErrUnsupported
	}
	db, err := m.ftpAuthPath(state)
	if err != nil {
		return err
	}
	rows, err := readFTPUsers(db)
	if err != nil {
		return err
	}
	present := false
	off := map[string]bool{}
	for _, row := range rows {
		off[row.Name] = !row.Enabled
		present = present || row.Name == in.Name
	}
	if action == "create" && present {
		return errors.New("FTP 用户已存在")
	}
	if action != "create" && !present {
		return errors.New("FTP 用户不存在")
	}
	bin, err := m.LookPath("pure-pw")
	if err != nil {
		return errors.New("找不到 Pure-FTPd 虚拟用户管理工具")
	}
	passwd, disabledPath := ftpUserFiles(db)
	stage, err := os.MkdirTemp(filepath.Dir(db), ".ezpanel-ftp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	draft := filepath.Join(stage, "passwd")
	if old, err := os.ReadFile(passwd); err == nil {
		if err = os.WriteFile(draft, old, 0600); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	run := func(password string, args ...string) error {
		result, err := executor.Pipe(ctx, 20*time.Second, nil, strings.NewReader(password+"\n"+password+"\n"), nil, bin, args...)
		if err != nil {
			message := strings.TrimSpace(result.Stderr)
			if password != "" {
				message = strings.ReplaceAll(message, password, "***")
			}
			return fmt.Errorf("Pure-FTPd 用户操作失败: %s", message)
		}
		return nil
	}
	switch action {
	case "create", "edit":
		policy, err := pathsafe.New(m.cfg.InstallDir)
		if err != nil {
			return err
		}
		root, err := policy.Check(in.Root, true)
		if err != nil || policy.CheckTree(root) != nil || strings.ContainsAny(root, ":\r\n") {
			return errors.New("FTP 根目录无效或受到保护")
		}
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			return errors.New("FTP 根目录必须是已存在的目录")
		}
		args := []string{"usermod", in.Name, "-f", draft, "-d", root, "-c", in.Remark}
		if action == "create" {
			args[0] = "useradd"
			args = append(args, "-u", "65534", "-g", "65534")
		}
		if err = run(in.Password, args...); err != nil {
			return err
		}
		if action == "edit" && in.Password != "" {
			if err = run(in.Password, "passwd", in.Name, "-f", draft); err != nil {
				return err
			}
		}
		off[in.Name] = !in.Enabled
	case "delete":
		if err = run("", "userdel", in.Name, "-f", draft); err != nil {
			return err
		}
		delete(off, in.Name)
	case "toggle":
		off[in.Name] = !in.Enabled
	default:
		return errors.New("FTP 用户操作无效")
	}
	all, err := os.ReadFile(draft)
	if err != nil {
		return err
	}
	var active, disabled strings.Builder
	for line := range strings.SplitSeq(string(all), "\n") {
		if user, _, ok := strings.Cut(line, ":"); ok {
			if off[user] {
				disabled.WriteString(user + "\n")
			} else {
				active.WriteString(line + "\n")
			}
		}
	}
	filtered := filepath.Join(stage, "active")
	next := filepath.Join(stage, "puredb")
	if err = os.WriteFile(filtered, []byte(active.String()), 0600); err != nil {
		return err
	}
	if err = run("", "mkdb", next, "-f", filtered); err != nil {
		return err
	}
	if err = writeHostConfig(passwd, all); err != nil {
		return err
	}
	if err = writeHostConfig(disabledPath, []byte(disabled.String())); err != nil {
		return err
	}
	if err = os.Rename(next, db); err != nil {
		return err
	}
	values, err := m.ftpValues(state)
	if err != nil {
		return err
	}
	if state.PkgManager == "apt-get" {
		auth := filepath.Join(filepath.Dir(ftpConfDir), "auth")
		if err = os.MkdirAll(auth, 0755); err != nil {
			return err
		}
		if names, err := os.ReadDir(auth); err == nil {
			for _, entry := range names {
				if target, e := filepath.EvalSymlinks(filepath.Join(auth, entry.Name())); e == nil && target == filepath.Join(ftpConfDir, "PureDB") {
					return nil
				}
			}
		}
		if err = os.Symlink("../conf/PureDB", filepath.Join(auth, "50ezpanel-puredb")); err != nil && !os.IsExist(err) {
			return err
		}
	} else if values["PureDB"] != "" {
		return nil
	}
	return m.writeFTPValues(ctx, state, map[string]string{"PureDB": db})
}
