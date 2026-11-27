package hardening

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"ezpanel/internal/hardening/dirguard"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errSnapshotLimit = errors.New("监测超过限制")

type RuleInput struct {
	Path       string `json:"path"`
	Category   string `json:"category"`
	Mode       string `json:"mode"`
	Enabled    bool   `json:"enabled"`
	Recursive  bool   `json:"recursive"`
	Permission string `json:"permission"`
	Remark     string `json:"remark"`
	Exclusions string `json:"exclusions"`
}
type Snapshot struct {
	Digest        string `json:"digest"`
	ContentDigest string `json:"contentDigest,omitempty"`
	Size          int64  `json:"size"`
	Permission    string `json:"permission"`
	UID           uint32 `json:"uid"`
	GID           uint32 `json:"gid"`
	Entries       int    `json:"entries"`
}
type Rule struct {
	RuleInput
	ID          string     `json:"id"`
	Directory   bool       `json:"directory"`
	Status      string     `json:"status"`
	Message     string     `json:"message"`
	Baseline    *Snapshot  `json:"baseline,omitempty"`
	Current     *Snapshot  `json:"current,omitempty"`
	CheckedAt   *time.Time `json:"checkedAt,omitempty"`
	PausedUntil *time.Time `json:"pausedUntil,omitempty"`
	AddedFlags  uint32     `json:"addedFlags,omitempty"`
	Identity    string     `json:"identity,omitempty"`
}
type Event struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	RuleID  string    `json:"ruleId"`
	Path    string    `json:"path"`
	Action  string    `json:"action"`
	Message string    `json:"message"`
}
type State struct {
	Enabled            bool       `json:"enabled"`
	Rules              []Rule     `json:"rules"`
	Events             []Event    `json:"events"`
	CheckedAt          *time.Time `json:"checkedAt,omitempty"`
	IntervalSeconds    int        `json:"intervalSeconds"`
	Supported          bool       `json:"supported"`
	Notice             string     `json:"notice"`
	DirectorySupported bool       `json:"directorySupported"`
	DirectoryNotice    string     `json:"directoryNotice"`
}
type Manager struct {
	mu        sync.Mutex
	path      string
	state     State
	flags     func(string, *uint32) (uint32, string, error)
	directory DirectoryBackend
}

func New(dataDir string) (*Manager, error) {
	m := &Manager{path: filepath.Join(dataDir, "hardening.json"), flags: fileFlags, directory: newDirectoryBackend(dataDir)}
	m.state = State{Rules: []Rule{}, Events: []Event{}, IntervalSeconds: 60, Supported: runtime.GOOS == "linux", Notice: "每 60 秒检查；目录默认仅监测直接子项。监测不拦截操作；目录实时保护由内核策略执行，排除模式适用于未来路径。"}
	defaults := m.state
	fresh := false
	b, e := os.ReadFile(m.path)
	if e == nil {
		e = json.Unmarshal(b, &m.state)
	} else if os.IsNotExist(e) {
		e = nil
		fresh = true
		for _, v := range []struct{ p, c string }{{"/etc/passwd", "account"}, {"/etc/group", "account"}, {"/etc/shadow", "account"}, {"/etc/sudoers", "account"}, {"/etc/profile", "environment"}, {"/etc/ssh/sshd_config", "environment"}, {"/etc/systemd/system", "service"}, {"/etc/crontab", "schedule"}, {"/etc/cron.d", "schedule"}} {
			if _, err := os.Lstat(v.p); err == nil {
				m.state.Rules = append(m.state.Rules, Rule{ID: rand.Text(), RuleInput: RuleInput{Path: v.p, Category: v.c, Mode: "monitor", Enabled: true}, Status: "off"})
			}
		}
	}
	m.state.DirectorySupported, m.state.DirectoryNotice = m.directory.Supported()
	m.state.Supported = defaults.Supported
	m.state.IntervalSeconds = defaults.IntervalSeconds
	m.state.Notice = defaults.Notice
	if e == nil && fresh {
		e = m.save()
	}
	return m, e
}
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, _ := json.Marshal(m.state)
	var s State
	json.Unmarshal(b, &s)
	return s
}
func (m *Manager) save() error {
	if e := os.MkdirAll(filepath.Dir(m.path), 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(m.state, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(m.path), ".hardening-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), m.path)
}
func (m *Manager) event(r *Rule, a, msg string) {
	v := Event{ID: rand.Text(), Time: time.Now().UTC(), Action: a, Message: msg}
	if r != nil {
		v.RuleID = r.ID
		v.Path = r.Path
	}
	m.state.Events = append([]Event{v}, m.state.Events...)
	if len(m.state.Events) > 500 {
		m.state.Events = m.state.Events[:500]
	}
}
func (m *Manager) restore(r *Rule) error {
	if r.AddedFlags == 0 {
		return nil
	}
	flags, id, e := m.flags(r.Path, nil)
	if e != nil {
		return e
	}
	if id != r.Identity {
		return errors.New("文件已被替换，无法确认原保护对象，请人工检查")
	}
	if flags&r.AddedFlags == 0 {
		r.AddedFlags = 0
		r.Identity = ""
		return nil
	}
	v := flags &^ r.AddedFlags
	if _, _, e = m.flags(r.Path, &v); e != nil {
		return e
	}
	r.AddedFlags = 0
	r.Identity = ""
	return nil
}
func (m *Manager) apply(r *Rule) error {
	bit := uint32(0x10)
	if r.Mode == "append" {
		bit = 0x20
	}
	f, id, e := m.flags(r.Path, nil)
	if e != nil {
		return e
	}
	if r.AddedFlags != 0 && id != r.Identity {
		return errors.New("保护文件已被替换，请先检查并解除原规则")
	}
	if f&bit != 0 {
		return nil
	}
	r.AddedFlags |= bit
	r.Identity = id
	// 先保存恢复依据，再改变内核属性，避免异常退出后无法解除。
	if e = m.save(); e != nil {
		return e
	}
	v := f | bit
	_, _, e = m.flags(r.Path, &v)
	return e
}
func (m *Manager) SetEnabled(enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if enabled {
		m.state.Enabled = true
		m.event(nil, "enable", "已开启系统加固")
		return m.scan()
	}
	m.state.Enabled = false
	directoryErr := m.syncDirectories()
	var errs []error
	if directoryErr != nil {
		errs = append(errs, directoryErr)
	}
	for i := range m.state.Rules {
		r := &m.state.Rules[i]
		if e := m.restore(r); e != nil {
			r.Status = "error"
			r.Message = e.Error()
			errs = append(errs, fmt.Errorf("%s: %w", r.Path, e))
		} else if r.Directory && r.Mode == "immutable" && directoryErr != nil {
			r.Status = "error"
			r.Message = directoryErr.Error()
		} else {
			r.Status = "off"
			r.Message = "总开关已关闭"
			r.PausedUntil = nil
		}
	}
	m.state.Enabled = false
	m.event(nil, "disable", "已关闭监测并尝试解除本功能施加的保护")
	return errors.Join(append(errs, m.save())...)
}
func (m *Manager) Recover() error { return m.SetEnabled(false) }
func (m *Manager) SaveRule(id string, in RuleInput) (Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !filepath.IsAbs(in.Path) || strings.ContainsAny(in.Path, "\x00\n\r") {
		return Rule{}, errors.New("请填写有效的绝对路径")
	}
	in.Path = filepath.Clean(in.Path)
	if !strings.Contains("|service|environment|account|critical|schedule|", "|"+in.Category+"|") || in.Category == "" {
		return Rule{}, errors.New("规则分类无效")
	}
	if in.Mode != "monitor" && in.Mode != "immutable" && in.Mode != "append" {
		return Rule{}, errors.New("保护模式无效")
	}
	if in.Permission != "" {
		v, e := strconv.ParseUint(in.Permission, 8, 32)
		if e != nil || v > 07777 {
			return Rule{}, errors.New("权限必须是有效八进制权限")
		}
		in.Permission = fmt.Sprintf("%04o", v)
	}
	index := -1
	for i, r := range m.state.Rules {
		if r.ID == id {
			index = i
		} else if r.Path == in.Path {
			return Rule{}, errors.New("该路径已有保护规则")
		}
	}
	fi, e := os.Lstat(in.Path)
	if e != nil && !(os.IsNotExist(e) && index >= 0 && m.state.Rules[index].Path == in.Path && (!in.Enabled || in.Mode == "monitor")) {
		return Rule{}, e
	}
	if fi != nil && fi.Mode()&os.ModeSymlink != 0 {
		return Rule{}, errors.New("请填写真实路径，不支持符号链接")
	}
	if fi != nil && !fi.IsDir() && !fi.Mode().IsRegular() {
		return Rule{}, errors.New("仅支持普通文件和目录")
	}
	patterns := exclusionPatterns(in.Exclusions)
	if err := dirguard.Validate(patterns); err != nil {
		return Rule{}, err
	}
	in.Exclusions = strings.Join(patterns, "\n")
	isDirectory := fi != nil && fi.IsDir()
	if fi == nil && index >= 0 {
		isDirectory = m.state.Rules[index].Directory
	}
	if !isDirectory && (in.Exclusions != "" || in.Recursive) {
		return Rule{}, errors.New("排除模式和递归仅用于目录")
	}
	if isDirectory && in.Mode == "append" {
		return Rule{}, errors.New("目录不支持仅追加模式")
	}
	if isDirectory && in.Mode == "immutable" {
		in.Recursive = true
	}
	inside, _ := filepath.Rel(filepath.Dir(m.path), in.Path)
	ancestor, _ := filepath.Rel(in.Path, filepath.Dir(m.path))
	if in.Mode != "monitor" && (inside != ".." && !strings.HasPrefix(inside, ".."+string(os.PathSeparator)) || ancestor != ".." && !strings.HasPrefix(ancestor, ".."+string(os.PathSeparator))) {
		return Rule{}, errors.New("不能保护加固状态目录或其上级目录")
	}

	previousRules := append([]Rule(nil), m.state.Rules...)
	r := Rule{ID: id, RuleInput: in, Directory: isDirectory, Status: "off"}
	changesDirectoryPolicy := r.Directory && r.Mode == "immutable"
	if id != "" {
		if index < 0 {
			return Rule{}, errors.New("规则不存在")
		}
		old := &m.state.Rules[index]
		changesDirectoryPolicy = changesDirectoryPolicy || old.Directory && old.Mode == "immutable"
		if e = m.restore(old); e != nil {
			return Rule{}, e
		}
		previousRules[index] = *old
		if old.Path == in.Path && old.Recursive == in.Recursive && old.Exclusions == in.Exclusions {
			r.Baseline = old.Baseline
		}
	} else {
		r.ID = rand.Text()
		index = 0
		m.state.Rules = append([]Rule{r}, m.state.Rules...)
	}
	m.state.Rules[index] = r
	if m.state.Enabled {
		// 先确认目录策略，再扫描本次提交的规则，避免回退时丢失文件属性的恢复依据。
		directoryErr := m.syncDirectories()
		if directoryErr != nil && changesDirectoryPolicy {
			m.state.Rules = previousRules
			m.event(nil, "error", "规则保存失败，已恢复原规则配置")
			return Rule{}, errors.Join(directoryErr, m.scanRules(directoryErr))
		}
		m.event(&r, "save", "已保存保护规则")
		e = m.scanRules(directoryErr)
	} else {
		m.event(&r, "save", "已保存保护规则")
		e = m.save()
	}
	return m.state.Rules[index], e
}
func (m *Manager) DeleteRule(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Rules {
		r := &m.state.Rules[i]
		if r.ID != id {
			continue
		}
		if e := m.restore(r); e != nil {
			return e
		}
		previous := append([]Rule(nil), m.state.Rules...)
		removed := *r
		m.state.Rules = append(m.state.Rules[:i], m.state.Rules[i+1:]...)
		directoryErr := m.syncDirectories()
		if directoryErr != nil && removed.Directory && removed.Mode == "immutable" {
			m.state.Rules = previous
			m.event(&m.state.Rules[i], "error", "移除规则失败，已保留恢复依据")
			return errors.Join(directoryErr, m.scanRules(directoryErr))
		}
		m.event(&removed, "delete", "已移除规则，未删除文件")
		return m.scanRules(directoryErr)
	}
	return errors.New("规则不存在")
}
func (m *Manager) Action(id, action string, minutes int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.state.Rules {
		r := &m.state.Rules[i]
		if r.ID != id {
			continue
		}
		switch action {
		case "baseline":
			s, e := snapshot(r.Path, r.Recursive, r.Exclusions)
			if e != nil {
				return e
			}
			r.Baseline = &s
		case "pause":
			if minutes < 1 || minutes > 1440 {
				return errors.New("暂停时间应为 1 至 1440 分钟")
			}
			if e := m.restore(r); e != nil {
				return e
			}
			until := time.Now().UTC().Add(time.Duration(minutes) * time.Minute)
			r.PausedUntil = &until
		case "resume":
			r.PausedUntil = nil
		default:
			return errors.New("操作无效")
		}
		m.event(r, action, "已执行规则操作")
		return m.scan()
	}
	return errors.New("规则不存在")
}
func (m *Manager) Scan() error { m.mu.Lock(); defer m.mu.Unlock(); return m.scan() }
func (m *Manager) scan() error {
	directoryErr := m.syncDirectories()
	return errors.Join(directoryErr, m.scanRules(directoryErr))
}

func (m *Manager) scanRules(directoryErr error) error {
	now := time.Now().UTC()
	for i := range m.state.Rules {
		r := &m.state.Rules[i]
		before := r.Status
		previousMessage := r.Message
		previous := r.Current
		if directoryErr != nil && r.Directory && r.Mode == "immutable" {
			r.CheckedAt = &now
			r.Status = "error"
			if !m.state.DirectorySupported {
				r.Status = "unsupported"
			}
			r.Message = directoryErr.Error()
			if before != r.Status || previousMessage != r.Message {
				m.event(r, "check", r.Message)
			}
			continue
		}
		if !m.state.Enabled && r.AddedFlags != 0 {
			r.Status = "error"
			if r.Message == "" {
				r.Message = "仍有未解除的文件保护，请执行恢复"
			}
			continue
		}
		switch {
		case m.state.Enabled && !r.Enabled:
			r.Status = "disabled"
			r.Message = "规则已关闭"
			continue
		case m.state.Enabled && r.PausedUntil != nil && now.Before(*r.PausedUntil):
			r.Status = "paused"
			r.Message = "保护已临时解除"
			continue
		}
		r.PausedUntil = nil
		r.CheckedAt = &now
		s, e := snapshot(r.Path, r.Recursive, r.Exclusions)
		if e != nil {
			r.Status = "error"
			if os.IsNotExist(e) {
				r.Status = "missing"
			}
			r.Message = e.Error()
		} else {
			r.Current = &s
			if r.Baseline == nil {
				r.Baseline = &s
			}
			r.Status = "monitoring"
			r.Message = "与基线一致"
			changed := *r.Baseline != s
			if r.Mode == "append" && r.Baseline.ContentDigest != "" && s.Size >= r.Baseline.Size && r.Baseline.Permission == s.Permission && r.Baseline.UID == s.UID && r.Baseline.GID == s.GID {
				f, err := os.Open(r.Path)
				if err == nil {
					h := sha256.New()
					_, err = io.CopyN(h, f, r.Baseline.Size)
					f.Close()
					changed = err != nil || hex.EncodeToString(h.Sum(nil)) != r.Baseline.ContentDigest
				}
			}
			if changed || r.Permission != "" && r.Permission != s.Permission {
				r.Status = "changed"
				r.Message = "内容、权限、属主或目录项与基线不一致"
			}
			if !m.state.Enabled && r.Status != "changed" {
				r.Status = "off"
				r.Message = "总开关已关闭，检查未发现变化"
			}
		}
		if m.state.Enabled && r.Mode != "monitor" && (e == nil || errors.Is(e, errSnapshotLimit)) {
			var applyErr error
			if !r.Directory {
				applyErr = m.apply(r)
			}
			switch {
			case applyErr != nil:
				r.Status = "error"
				if !m.state.Supported {
					r.Status = "unsupported"
				}
				r.Message = applyErr.Error()
			case e != nil:
				r.Status = "protected"
				r.Message = "保护已生效，监测未完成：" + e.Error()
			case r.Status != "changed":
				r.Status = "protected"
				r.Message = "文件属性保护已生效"
				if r.Directory {
					r.Message = "内核目录保护已生效，排除规则包含未来匹配路径"
				}
			}
		}
		if (before != r.Status || previousMessage != r.Message || previous != nil && r.Current != nil && *previous != *r.Current) &&
			(r.Status == "changed" || r.Status == "error" || r.Status == "missing" || r.Status == "unsupported" || e != nil && r.Status == "protected") {
			m.event(r, "check", r.Message)
		}
	}
	m.state.CheckedAt = &now
	return m.save()
}
func (m *Manager) isEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Enabled
}

func (m *Manager) Start(ctx context.Context) {
	if m.isEnabled() {
		m.Scan()
	} else {
		m.mu.Lock()
		m.syncDirectories()
		m.mu.Unlock()
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if m.isEnabled() {
				m.Scan()
			}
		}
	}
}
func snapshot(path string, recursive bool, exclusions ...string) (Snapshot, error) {
	var matcher *dirguard.Matcher
	if len(exclusions) > 0 && exclusions[0] != "" {
		var err error
		matcher, err = dirguard.Compile(exclusionPatterns(exclusions[0]))
		if err != nil {
			return Snapshot{}, err
		}
	}
	h := sha256.New()
	s := Snapshot{}
	total := int64(0)
	e := filepath.WalkDir(path, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p != path && matcher != nil {
			relative, err := filepath.Rel(path, p)
			if err != nil {
				return err
			}
			if matcher.Match(filepath.ToSlash(relative)) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		fi, e := d.Info()
		if e != nil {
			return e
		}
		if p == path && fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("路径已变为符号链接")
		}
		uid, gid, _ := identity(fi)
		if p == path {
			s.Permission = fmt.Sprintf("%04o", fi.Mode().Perm()|specialMode(fi.Mode()))
			s.UID = uid
			s.GID = gid
		}
		s.Entries++
		if s.Entries > 10000 {
			return fmt.Errorf("%w（10000 项），请缩小目录范围", errSnapshotLimit)
		}
		fmt.Fprintf(h, "%s|%v|%d|%d|", p, fi.Mode(), uid, gid)
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			fmt.Fprint(h, target)
		}
		if fi.Mode().IsRegular() {
			if fi.Size() > 32<<20 || total+fi.Size() > 64<<20 {
				return fmt.Errorf("%w（单文件 32 MiB，总计 64 MiB）", errSnapshotLimit)
			}
			f, e := os.Open(p)
			if e != nil {
				return e
			}
			content := sha256.New()
			n, e := io.Copy(io.MultiWriter(h, content), io.LimitReader(f, 32<<20+1))
			if p == path {
				s.Size = n
				s.ContentDigest = hex.EncodeToString(content.Sum(nil))
			}
			f.Close()
			if e != nil {
				return e
			}
			total += n
		}
		if d.IsDir() && p != path && !recursive {
			return filepath.SkipDir
		}
		return nil
	})
	s.Digest = hex.EncodeToString(h.Sum(nil))
	return s, e
}
func specialMode(m os.FileMode) os.FileMode {
	var v os.FileMode
	if m&os.ModeSetuid != 0 {
		v |= 04000
	}
	if m&os.ModeSetgid != 0 {
		v |= 02000
	}
	if m&os.ModeSticky != 0 {
		v |= 01000
	}
	return v
}
