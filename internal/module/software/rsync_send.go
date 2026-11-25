package software

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ezpanel/internal/auth"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const rsyncWorkspaceKey = "software.rsync.workspace"

var rsyncID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var rsyncHost = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*$`)
var rsyncModulePath = regexp.MustCompile(`^/[a-zA-Z0-9_][a-zA-Z0-9_.-]*(/[a-zA-Z0-9_][a-zA-Z0-9_.-]*)*/?$`)
var rsyncUser = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,63}$`)

type rsyncSender struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	RuleID      string `json:"ruleId"`
}

type rsyncRule struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Patterns string `json:"patterns"`
}

type rsyncWorkspace struct {
	Revision string            `json:"revision"`
	Senders  []rsyncSender     `json:"senders"`
	Rules    []rsyncRule       `json:"rules"`
	Secrets  map[string]string `json:"secrets,omitempty"`
}

func (m *Module) readRsyncWorkspace(ctx context.Context) (rsyncWorkspace, error) {
	out := rsyncWorkspace{Senders: []rsyncSender{}, Rules: []rsyncRule{}}
	var row store.Setting
	err := m.db.WithContext(ctx).First(&row, "key = ?", rsyncWorkspaceKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal([]byte(row.Value), &out)
}

func (m *Module) rsyncWorkspace(c *gin.Context) {
	ctx := c.Request.Context()
	m.rsyncMu.Lock()
	defer m.rsyncMu.Unlock()
	current, err := m.readRsyncWorkspace(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	if c.Request.Method == "PUT" {
		var in rsyncWorkspace
		if !response.Bind(c, &in) {
			return
		}
		if in.Revision != current.Revision {
			response.Fail(c, response.Conflict, "Rsync 配置已修改，请刷新后保存")
			return
		}
		if err := m.prepareRsyncWorkspace(&in, current); err != nil {
			response.Fail(c, response.Invalid, err.Error())
			return
		}
		in.Revision = store.ID()
		data, _ := json.Marshal(in)
		if err = m.db.WithContext(ctx).Save(&store.Setting{Key: rsyncWorkspaceKey, Value: string(data)}).Error; err != nil {
			fail(c, err)
			return
		}
		current = in
	}
	current.Secrets = nil
	response.OK(c, current)
}

func (m *Module) prepareRsyncWorkspace(in *rsyncWorkspace, old rsyncWorkspace) error {
	if len(in.Senders) > 100 || len(in.Rules) > 100 {
		return errors.New("发送任务和排除规则各最多可保存 100 项")
	}
	rules := map[string]bool{}
	for i := range in.Rules {
		rule := &in.Rules[i]
		rule.Name = strings.TrimSpace(rule.Name)
		if rule.ID == "" {
			rule.ID = store.ID()
		}
		if !rsyncID.MatchString(rule.ID) || rules[rule.ID] || rule.Name == "" || len(rule.Name) > 256 {
			return errors.New("排除规则名称或标识无效")
		}
		var patterns []string
		for line := range strings.SplitSeq(rule.Patterns, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				if strings.IndexFunc(line, unicode.IsControl) >= 0 || strings.ContainsRune(line, '\\') {
					return errors.New("排除规则包含无效字符")
				}
				patterns = append(patterns, line)
			}
		}
		if len(patterns) == 0 || len(patterns) > 100 || len(rule.Patterns) > 8192 {
			return errors.New("请按每行一条填写排除规则，最多 100 条")
		}
		rule.Patterns = strings.Join(patterns, "\n")
		rules[rule.ID] = true
	}
	in.Secrets = map[string]string{}
	ids := map[string]bool{}
	key := m.cfg.Current().AESKey
	for i := range in.Senders {
		row := &in.Senders[i]
		row.Name = strings.TrimSpace(row.Name)
		if row.ID == "" {
			row.ID = store.ID()
		}
		if !rsyncID.MatchString(row.ID) || ids[row.ID] || row.Name == "" || len(row.Name) > 256 {
			return errors.New("发送任务名称或标识无效")
		}
		if row.RuleID != "" && !rules[row.RuleID] {
			return fmt.Errorf("排除规则仍被发送任务「%s」使用", row.Name)
		}
		row.Destination = strings.TrimSpace(row.Destination)
		row.Username = strings.TrimSpace(row.Username)
		previous := slices.IndexFunc(old.Senders, func(item rsyncSender) bool { return item.ID == row.ID })
		sameRemote := previous >= 0 && old.Senders[previous].Username == row.Username && old.Senders[previous].Destination == row.Destination
		if !sameRemote || old.Senders[previous].Source != row.Source {
			if _, _, err := m.rsyncSendPaths(*row); err != nil {
				return fmt.Errorf("%s：%w", row.Name, err)
			}
		}
		if len(row.Password) > 256 || strings.IndexFunc(row.Password, unicode.IsControl) >= 0 {
			return errors.New("发送任务密码格式无效")
		}
		if row.Username != "" {
			if row.Password != "" {
				secret, err := store.Encrypt(key, row.Password)
				if err != nil {
					return errors.New("无法保存发送任务密码")
				}
				in.Secrets[row.ID] = secret
			} else if sameRemote && old.Secrets[row.ID] != "" {
				in.Secrets[row.ID] = old.Secrets[row.ID]
			}
		}
		row.Password = ""
		ids[row.ID] = true
	}
	if in.Senders == nil {
		in.Senders = []rsyncSender{}
	}
	if in.Rules == nil {
		in.Rules = []rsyncRule{}
	}
	return nil
}

func (m *Module) rsyncSendPaths(row rsyncSender) (string, string, error) {
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		return "", "", err
	}
	checkDir := func(raw string) (string, error) {
		real, err := policy.Check(raw, true)
		if err != nil || policy.CheckTree(real) != nil {
			return "", errors.New("请选择允许同步的目录")
		}
		if info, err := os.Stat(real); err != nil || !info.IsDir() {
			return "", errors.New("同步目录必须已存在")
		}
		return real, nil
	}
	source, err := checkDir(row.Source)
	if err != nil {
		return "", "", err
	}
	address := strings.TrimSpace(row.Destination)
	if filepath.IsAbs(address) {
		destination, err := checkDir(address)
		if err != nil {
			return "", "", err
		}
		if pathsafe.Contains(source, destination) || pathsafe.Contains(destination, source) {
			return "", "", errors.New("源目录和目标目录不能重叠")
		}
		return source + "/", destination + "/", nil
	}
	destination, err := rsyncDaemonTarget(address, row.Username)
	return source + "/", destination, err
}

func rsyncDaemonTarget(address, username string) (string, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "rsync" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		!rsyncModulePath.MatchString(u.Path) || u.EscapedPath() != u.Path {
		return "", errors.New("目标须为本机目录或 rsync://服务器:端口/模块")
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); (err != nil || ip.Zone() != "") && !rsyncHost.MatchString(host) {
		return "", errors.New("Rsync 服务器地址无效")
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return "", errors.New("Rsync 服务器端口无效")
		}
	}
	if username != "" {
		if !rsyncUser.MatchString(username) {
			return "", errors.New("Rsync 用户名格式无效")
		}
		u.User = url.User(username)
	}
	u.Path = path.Clean(u.Path) + "/"
	return u.String(), nil
}

func (m *Module) runRsyncSender(c *gin.Context) {
	ctx := c.Request.Context()
	workspace, err := m.readRsyncWorkspace(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	index := slices.IndexFunc(workspace.Senders, func(row rsyncSender) bool { return row.ID == c.Param("id") })
	if index < 0 {
		response.Fail(c, response.NotFound, "发送任务不存在")
		return
	}
	row := workspace.Senders[index]
	state, err := m.packages.Status(ctx, "rsync")
	if err != nil || !state.Manageable {
		fail(c, pkgmgr.ErrUnsupported)
		return
	}
	bin, err := m.packages.LookPath("rsync")
	if err != nil {
		fail(c, err)
		return
	}
	source, destination, err := m.rsyncSendPaths(row)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	args := []string{"--recursive", "--links", "--safe-links", "--times", "--itemize-changes", "--stats", "--timeout=120"}
	if strings.HasPrefix(destination, "rsync://") {
		args = append(args, "--contimeout=15")
	}
	if i := slices.IndexFunc(workspace.Rules, func(rule rsyncRule) bool { return rule.ID == row.RuleID }); i >= 0 {
		for pattern := range strings.SplitSeq(workspace.Rules[i].Patterns, "\n") {
			args = append(args, "--exclude="+pattern)
		}
	}
	session := auth.Current(c)
	id, err := m.tasks.Submit(task.Meta{
		Module: "software", Action: "rsync-send", Target: row.Name, ResourceID: "rsync",
		LockKey: "software-install:rsync", Username: session.User.Username, IP: m.auth.ClientIP(c),
	}, func(ctx context.Context, log *task.Log) (any, error) {
		if secret := workspace.Secrets[row.ID]; secret != "" && strings.HasPrefix(destination, "rsync://") {
			password, err := store.Decrypt(m.cfg.Current().AESKey, secret)
			if err != nil {
				return nil, errors.New("无法读取发送任务密码")
			}
			file, err := os.CreateTemp(m.cfg.DataDir(), ".rsync-password-")
			if err != nil {
				return nil, err
			}
			defer os.Remove(file.Name())
			_, err = file.WriteString(password + "\n")
			if err = errors.Join(err, file.Close()); err != nil {
				return nil, err
			}
			args = append(args, "--password-file="+file.Name())
		}
		log.Line("源目录 " + source)
		log.Line("目标 " + destination)
		_, err := m.packages.Stream(ctx, time.Hour, nil, log.Line, bin, append(args, "--", source, destination)...)
		return nil, err
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
