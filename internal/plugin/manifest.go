package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"ezpanel/internal/module/backup"

	"gopkg.in/yaml.v3"
)

var ErrCompatibility = errors.New("面板主版本、最低版本或 SDK 不兼容")

const SDKVersion = 1

// CoreVersion 是开发源码的兼容版本，不伪装正式发布标签。
const CoreVersion = "0.0.0"

type Manifest struct {
	ID                   string                 `yaml:"id" json:"id"`
	Name                 string                 `yaml:"name" json:"name"`
	Version              string                 `yaml:"version" json:"version"`
	Description          string                 `yaml:"description" json:"description"`
	Author               string                 `yaml:"author" json:"author"`
	Homepage             string                 `yaml:"homepage,omitempty" json:"homepage,omitempty"`
	OpenMode             string                 `yaml:"openMode,omitempty" json:"openMode,omitempty"`
	Type                 string                 `yaml:"type" json:"type"`
	MinPanelVersion      string                 `yaml:"minPanelVersion" json:"minPanelVersion"`
	SDKVersion           int                    `yaml:"sdkVersion" json:"sdkVersion"`
	Entry                map[string]string      `yaml:"entry,omitempty" json:"entry,omitempty"`
	Menu                 *Menu                  `yaml:"menu,omitempty" json:"menu,omitempty"`
	Settings             *SettingsWindow        `yaml:"settings,omitempty" json:"settings,omitempty"`
	UI                   string                 `yaml:"ui" json:"ui"`
	UIMode               string                 `yaml:"uiMode,omitempty" json:"uiMode,omitempty"`
	Permissions          []string               `yaml:"permissions" json:"permissions"`
	ExecAllow            []string               `yaml:"execAllow,omitempty" json:"execAllow,omitempty"`
	FSRoots              []string               `yaml:"fsRoots,omitempty" json:"fsRoots,omitempty"`
	Env                  []Env                  `yaml:"env,omitempty" json:"env,omitempty"`
	Jobs                 []Job                  `yaml:"jobs,omitempty" json:"jobs,omitempty"`
	Events               []string               `yaml:"events,omitempty" json:"events,omitempty"`
	SiteAction           *SiteAction            `yaml:"siteAction,omitempty" json:"siteAction,omitempty"`
	FileAction           *FileAction            `yaml:"fileAction,omitempty" json:"fileAction,omitempty"`
	Dashboard            *ExtensionTitle        `yaml:"dashboard,omitempty" json:"dashboard,omitempty"`
	BackupProvider       *backup.PluginProvider `yaml:"backupProvider,omitempty" json:"backupProvider,omitempty"`
	NotificationProvider *ExtensionTitle        `yaml:"notificationProvider,omitempty" json:"notificationProvider,omitempty"`
	DNSProvider          *ExtensionTitle        `yaml:"dnsProvider,omitempty" json:"dnsProvider,omitempty"`
	SSOProvider          *ExtensionTitle        `yaml:"ssoProvider,omitempty" json:"ssoProvider,omitempty"`
	Dependencies         map[string]string      `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	Services             []string               `yaml:"services,omitempty" json:"services,omitempty"`
	Checks               []MonitorCheck         `yaml:"checks,omitempty" json:"checks,omitempty"`
	CredentialSlots      []CredentialSlot       `yaml:"credentialSlots,omitempty" json:"credentialSlots,omitempty"`
	ResourceActions      map[string]string      `yaml:"resourceActions,omitempty" json:"resourceActions,omitempty"`
	Sandbox              *Sandbox               `yaml:"sandbox,omitempty" json:"sandbox,omitempty"`
}
type Sandbox struct {
	CPUPercent   int    `yaml:"cpuPercent" json:"cpuPercent"`
	MemoryMiB    int    `yaml:"memoryMiB" json:"memoryMiB"`
	MaxProcesses int    `yaml:"maxProcesses" json:"maxProcesses"`
	Network      string `yaml:"network" json:"network"`
}
type Job struct {
	ID      string   `yaml:"id" json:"id"`
	Title   string   `yaml:"title" json:"title"`
	Params  []Env    `yaml:"params,omitempty" json:"params,omitempty"`
	Webhook bool     `yaml:"webhook,omitempty" json:"webhook,omitempty"`
	Steps   []string `yaml:"steps,omitempty" json:"steps,omitempty"`
}
type SiteAction struct {
	Title string `yaml:"title" json:"title"`
	Embed bool   `yaml:"embed,omitempty" json:"embed,omitempty"`
}
type FileAction struct {
	Title    string `yaml:"title" json:"title"`
	Multiple bool   `yaml:"multiple" json:"multiple"`
}
type ExtensionTitle struct {
	Title string `yaml:"title" json:"title"`
}
type MonitorCheck struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
}
type CredentialSlot struct {
	ID    string `yaml:"id" json:"id"`
	Title string `yaml:"title" json:"title"`
}
type Menu struct {
	Title    string     `yaml:"title" json:"title"`
	Icon     string     `yaml:"icon" json:"icon"`
	Order    int        `yaml:"order" json:"order"`
	Children []MenuItem `yaml:"children,omitempty" json:"children,omitempty"`
}
type MenuItem struct {
	Title string `yaml:"title" json:"title"`
	Path  string `yaml:"path" json:"path"`
	Icon  string `yaml:"icon,omitempty" json:"icon,omitempty"`
}
type SettingsWindow struct {
	Type   string `yaml:"type" json:"type"`
	Width  int    `yaml:"width,omitempty" json:"width,omitempty"`
	Height int    `yaml:"height,omitempty" json:"height,omitempty"`
}
type Env struct {
	Key      string   `yaml:"key" json:"key"`
	Label    string   `yaml:"label" json:"label"`
	Default  string   `yaml:"default" json:"default"`
	Required bool     `yaml:"required" json:"required"`
	Secret   bool     `yaml:"secret" json:"secret"`
	Type     string   `yaml:"type,omitempty" json:"type,omitempty"`
	Options  []string `yaml:"options,omitempty" json:"options,omitempty"`
	Help     string   `yaml:"help,omitempty" json:"help,omitempty"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,31}$`)
var menuPath = regexp.MustCompile(`^[a-zA-Z0-9_-]+(/[a-zA-Z0-9_-]+)*$`)
var envPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func versionParts(v string) ([3]uint64, error) {
	var out [3]uint64
	if !versionPattern.MatchString(v) {
		return out, errors.New("版本必须为无前导零的 x.y.z")
	}
	for i, p := range strings.Split(v, ".") {
		n, e := strconv.ParseUint(p, 10, 64)
		if e != nil {
			return out, errors.New("版本整数超出范围")
		}
		out[i] = n
	}
	return out, nil
}
func CompareVersion(a, b string) (int, error) {
	x, e := versionParts(a)
	if e != nil {
		return 0, e
	}
	y, e := versionParts(b)
	if e != nil {
		return 0, e
	}
	for i := range x {
		if x[i] < y[i] {
			return -1, nil
		}
		if x[i] > y[i] {
			return 1, nil
		}
	}
	return 0, nil
}
func packagePath(p string) bool {
	return p != "" && !strings.ContainsAny(p, "\\\x00") && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../")
}
func ReadManifest(r io.Reader, root, install string, dev bool, core string) (Manifest, error) {
	var m Manifest
	raw, err := io.ReadAll(io.LimitReader(r, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return m, errors.New("清单超过 1MiB 或读取失败")
	}
	var doc yaml.Node
	if err = yaml.Unmarshal(raw, &doc); err != nil {
		return m, errors.New("清单 YAML 无效")
	}
	if err = strictYAML(&doc, ""); err != nil {
		return m, err
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if e := d.Decode(&m); e != nil {
		return m, errors.New("清单格式无效或含未知/重复字段")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return m, errors.New("清单只允许一个文档")
	}
	if m.SDKVersion == 0 {
		m.SDKVersion = SDKVersion
	}
	if m.MinPanelVersion == "" {
		m.MinPanelVersion = CoreVersion
	}
	if m.Type == "" {
		if len(m.Entry) > 0 {
			m.Type = "process"
		} else {
			m.Type = "static"
		}
	}
	if m.UI == "" {
		m.UI = "web/entry.js"
	}
	if !idPattern.MatchString(m.ID) || strings.Contains("|api|ui|host|panel|plugins|", "|"+m.ID+"|") {
		return m, errors.New("插件 id 无效或保留")
	}
	if utf8.RuneCountInString(m.Name) < 1 || utf8.RuneCountInString(m.Name) > 64 || utf8.RuneCountInString(m.Description) > 512 || utf8.RuneCountInString(m.Author) > 128 {
		return m, errors.New("清单文本长度无效")
	}
	m.Homepage = strings.TrimSpace(m.Homepage)
	if m.Homepage != "" {
		if utf8.RuneCountInString(m.Homepage) > 512 {
			return m, errors.New("作者主页地址过长")
		}
		u, err := url.Parse(m.Homepage)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return m, errors.New("作者主页必须为有效的 http 或 https 地址")
		}
	}
	switch m.OpenMode {
	case "", "embed", "new_tab":
		if m.OpenMode == "" {
			m.OpenMode = "embed"
		}
	default:
		return m, errors.New("openMode 必须为 embed 或 new_tab")
	}
	if _, e := versionParts(m.Version); e != nil {
		return m, e
	}
	min, e := versionParts(m.MinPanelVersion)
	if e != nil {
		return m, e
	}
	current, e := versionParts(core)
	if e != nil {
		return m, e
	}
	cmp, _ := CompareVersion(core, m.MinPanelVersion)
	if min[0] != current[0] || cmp < 0 || m.SDKVersion != SDKVersion {
		return m, ErrCompatibility
	}
	if m.Type != "process" && m.Type != "static" {
		return m, errors.New("插件类型无效")
	}
	if sandbox := m.Sandbox; sandbox != nil {
		if m.Type != "process" || sandbox.CPUPercent < 0 || sandbox.CPUPercent > 1600 ||
			sandbox.MemoryMiB < 0 || sandbox.MemoryMiB > 16384 || sandbox.MemoryMiB > 0 && sandbox.MemoryMiB < 16 ||
			sandbox.MaxProcesses < 0 || sandbox.MaxProcesses > 1024 || sandbox.MaxProcesses > 0 && sandbox.MaxProcesses < 8 ||
			(sandbox.Network != "" && sandbox.Network != "loopback" && sandbox.Network != "outbound") {
			return m, errors.New("插件沙箱配置无效")
		}
	}
	if !packagePath(m.UI) || !strings.HasPrefix(m.UI, "web/") || path.Ext(m.UI) != ".js" {
		return m, errors.New("UI 必须为 web 下的 JavaScript 文件")
	}
	if m.UIMode != "" && m.UIMode != "isolated" && m.UIMode != "legacy" {
		return m, errors.New("uiMode 必须为 isolated 或 legacy")
	}
	if m.UIMode == "" {
		m.UIMode = "isolated"
	}
	if e := ordinary(root, m.UI); e != nil {
		return m, e
	}
	if menu := m.Menu; menu != nil {
		if strings.TrimSpace(menu.Title) == "" || menu.Order < 0 || menu.Order > 10000 || len(menu.Children) > 16 {
			return m, errors.New("菜单无效，二级菜单最多 16 项")
		}
		seen := map[string]bool{}
		for _, child := range menu.Children {
			key := strings.ToLower(child.Path)
			if strings.TrimSpace(child.Title) == "" || len([]rune(child.Title)) > 64 || len(key) > 120 || !menuPath.MatchString(child.Path) || seen[key] {
				return m, errors.New("插件二级菜单标题或相对路径无效")
			}
			seen[key] = true
		}
	}
	if window := m.Settings; window != nil {
		switch window.Type {
		case "modal", "drawer":
			if window.Width < 0 || window.Height < 0 {
				return m, errors.New("设置窗口尺寸不能为负数")
			}
		case "none":
			if window.Width != 0 || window.Height != 0 {
				return m, errors.New("无设置窗口时不能指定尺寸")
			}
		default:
			return m, errors.New("设置窗口类型必须为 modal、drawer 或 none")
		}
	}
	seen := map[string]bool{}
	for _, p := range m.Permissions {
		if seen[p] || !validPermission(p) {
			return m, errors.New("权限无效或重复")
		}
		seen[p] = true
	}
	if err := validateCapabilities(m, seen); err != nil {
		return m, err
	}
	if provider := m.SSOProvider; provider != nil && (strings.TrimSpace(provider.Title) == "" || len([]rune(provider.Title)) > 64) {
		return m, errors.New("单点登录渠道名称无效")
	}
	if len(m.Dependencies) > 20 {
		return m, errors.New("插件依赖最多 20 项")
	}
	for id, minimum := range m.Dependencies {
		if !idPattern.MatchString(id) || id == m.ID || !versionPattern.MatchString(minimum) {
			return m, errors.New("插件依赖 ID 或最低版本无效")
		}
	}
	if m.Type == "static" {
		if len(m.Entry)+len(m.ExecAllow)+len(m.FSRoots)+len(m.Env)+len(m.Permissions)+len(m.Dependencies) > 0 || m.SiteAction != nil || m.SSOProvider != nil {
			return m, errors.New("static 禁止后端声明")
		}
		return m, nil
	}
	target := "linux-" + runtime.GOARCH
	if dev {
		target = runtime.GOOS + "-" + runtime.GOARCH
	}
	if m.Entry[target] == "" {
		return m, errors.New("缺少当前架构入口")
	}
	for arch, p := range m.Entry {
		if arch != "linux-amd64" && arch != "linux-arm64" && !(dev && arch == target) {
			return m, errors.New("入口架构不支持")
		}
		if !packagePath(p) {
			return m, errors.New("入口路径无效")
		}
		if e := ordinary(root, p); e != nil {
			return m, e
		}
	}
	if seen["exec.run"] != (len(m.ExecAllow) > 0) {
		return m, errors.New("exec.run 与 execAllow 必须同时声明")
	}
	if m.Sandbox != nil && seen["exec.run"] {
		return m, errors.New("隔离插件不能声明特权命令；请在插件进程内运行自己的程序")
	}
	for i, p := range m.ExecAllow {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return m, errors.New("命令必须为规范的绝对路径")
		}
		targetExec := p
		if install != "" {
			real, e := filepath.EvalSymlinks(p)
			if e != nil {
				return m, errors.New("命令必须为存在的绝对路径")
			}
			info, e := os.Stat(real)
			if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 {
				return m, errors.New("命令必须为不可由插件写入的可执行文件")
			}
			if e := protectedExecutable(real); e != nil {
				return m, e
			}
			targetExec = real
		}
		base := filepath.Base(targetExec)
		if regexp.MustCompile(`^(ba|da|z|k|fi|c|tc)?sh$|^(python[0-9.]*|node|perl|ruby|php[0-9.]*|lua[0-9.]*|env|sudo|su|xargs|busybox|find|awk|gawk|sed|make|nohup|setsid|systemd-run)$`).MatchString(base) {
			return m, errors.New("禁止 Shell、解释器及命令跳板")
		}
		m.ExecAllow[i] = targetExec
	}
	if (seen["fs.read"] || seen["fs.write"]) != (len(m.FSRoots) > 0) {
		return m, errors.New("文件权限与 fsRoots 必须同时声明")
	}
	// 离线打包时尚不知道目标安装路径，导入时再校验面板目录。
	panel := ""
	if install != "" {
		var err error
		panel, err = filepath.EvalSymlinks(install)
		if err != nil {
			return m, err
		}
	}
	for i, p := range m.FSRoots {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return m, errors.New("fsRoots 必须为规范的绝对目录")
		}
		targetRoot := p
		if install != "" {
			real, e := filepath.EvalSymlinks(p)
			if e != nil {
				return m, errors.New("fsRoots 必须为存在的绝对目录")
			}
			info, e := os.Stat(real)
			if e != nil || !info.IsDir() {
				return m, errors.New("fsRoots 指向禁止目录")
			}
			targetRoot = real
		}
		if targetRoot == "/" || (panel != "" && (within(targetRoot, panel) || within(panel, targetRoot))) || within(targetRoot, "/proc") || within(targetRoot, "/sys") || within(targetRoot, "/dev") {
			return m, errors.New("fsRoots 指向禁止目录")
		}
		m.FSRoots[i] = targetRoot
	}
	keys := map[string]bool{}
	for i, v := range m.Env {
		if keys[v.Key] || !envPattern.MatchString(v.Key) || strings.HasPrefix(v.Key, "PANEL_") || strings.HasPrefix(v.Key, "LD_") || strings.HasPrefix(v.Key, "DYLD_") || v.Key == "PATH" || v.Key == "HOME" || len(v.Default) > 8192 || strings.ContainsRune(v.Default, 0) {
			return m, errors.New("环境配置键或值无效")
		}
		keys[v.Key] = true
		if err := validateEnvField(v); err != nil {
			return m, err
		}
		if v.Type == "password" {
			m.Env[i].Secret = true
		}
	}
	return m, nil
}
func within(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}
func ordinary(root, p string) error {
	f, e := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
	if e != nil || !f.Mode().IsRegular() {
		return fmt.Errorf("包内缺少普通文件：%s", p)
	}
	return nil
}

// YAML 的隐式数字/布尔转换会掩盖错误配置，因此字符串字段按原始节点校验。
func strictYAML(n *yaml.Node, key string) error {
	if n.Kind == yaml.AliasNode {
		return errors.New("清单禁止 YAML 别名")
	}
	if n.Kind == yaml.ScalarNode {
		expected := "!!str"
		if key == "sdkVersion" || key == "order" || key == "width" || key == "height" || key == "cpuPercent" || key == "memoryMiB" || key == "maxProcesses" {
			expected = "!!int"
		}
		if key == "required" || key == "secret" || key == "multiple" || key == "webhook" || key == "embed" {
			expected = "!!bool"
		}
		if n.Tag != expected {
			return errors.New("清单字段类型不匹配，禁止隐式转换")
		}
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i < len(n.Content); i += 2 {
			if n.Content[i].Tag != "!!str" {
				return errors.New("清单键必须是字符串")
			}
			if e := strictYAML(n.Content[i+1], n.Content[i].Value); e != nil {
				return e
			}
		}
		return nil
	}
	for _, child := range n.Content {
		if e := strictYAML(child, key); e != nil {
			return e
		}
	}
	return nil
}

func publicManifest(m Manifest) Manifest {
	if m.UIMode == "" {
		m.UIMode = "isolated"
	}
	if m.OpenMode == "" {
		m.OpenMode = "embed"
	}
	m.Env = append([]Env(nil), m.Env...)
	for i := range m.Env {
		if m.Env[i].Secret {
			m.Env[i].Default = ""
		}
	}
	return m
}
