package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

var ErrCompatibility = errors.New("面板主版本、最低版本或 SDK 不兼容")

const SDKVersion = 1

// CoreVersion 是开发源码的兼容版本，不伪装正式发布标签。
const CoreVersion = "0.0.0"

type Manifest struct {
	ID              string            `yaml:"id" json:"id"`
	Name            string            `yaml:"name" json:"name"`
	Version         string            `yaml:"version" json:"version"`
	Description     string            `yaml:"description" json:"description"`
	Author          string            `yaml:"author" json:"author"`
	Type            string            `yaml:"type" json:"type"`
	MinPanelVersion string            `yaml:"minPanelVersion" json:"minPanelVersion"`
	SDKVersion      int               `yaml:"sdkVersion" json:"sdkVersion"`
	Entry           map[string]string `yaml:"entry,omitempty" json:"entry,omitempty"`
	Menu            *Menu             `yaml:"menu,omitempty" json:"menu,omitempty"`
	UI              string            `yaml:"ui" json:"ui"`
	Permissions     []string          `yaml:"permissions" json:"permissions"`
	ExecAllow       []string          `yaml:"execAllow,omitempty" json:"execAllow,omitempty"`
	FSRoots         []string          `yaml:"fsRoots,omitempty" json:"fsRoots,omitempty"`
	Env             []Env             `yaml:"env,omitempty" json:"env,omitempty"`
}
type Menu struct {
	Title string `yaml:"title" json:"title"`
	Icon  string `yaml:"icon" json:"icon"`
	Order int    `yaml:"order" json:"order"`
}
type Env struct {
	Key      string `yaml:"key" json:"key"`
	Label    string `yaml:"label" json:"label"`
	Default  string `yaml:"default" json:"default"`
	Required bool   `yaml:"required" json:"required"`
	Secret   bool   `yaml:"secret" json:"secret"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,31}$`)
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
	if !idPattern.MatchString(m.ID) || strings.Contains("|api|ui|host|panel|plugins|", "|"+m.ID+"|") {
		return m, errors.New("插件 id 无效或保留")
	}
	if utf8.RuneCountInString(m.Name) < 1 || utf8.RuneCountInString(m.Name) > 64 || utf8.RuneCountInString(m.Description) > 512 || utf8.RuneCountInString(m.Author) > 128 {
		return m, errors.New("清单文本长度无效")
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
	if !packagePath(m.UI) || !strings.HasPrefix(m.UI, "web/") || path.Ext(m.UI) != ".js" {
		return m, errors.New("UI 必须为 web 下的 JavaScript 文件")
	}
	if e := ordinary(root, m.UI); e != nil {
		return m, e
	}
	if m.Menu != nil && (m.Menu.Title == "" || m.Menu.Order < 0 || m.Menu.Order > 10000) {
		return m, errors.New("菜单无效")
	}
	seen := map[string]bool{}
	for _, p := range m.Permissions {
		if seen[p] || (p != "exec.run" && p != "fs.read" && p != "fs.write" && p != "sysinfo.read") {
			return m, errors.New("权限无效或重复")
		}
		seen[p] = true
	}
	if m.Type == "static" {
		if len(m.Entry)+len(m.ExecAllow)+len(m.FSRoots)+len(m.Env)+len(m.Permissions) > 0 {
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
	for i, p := range m.ExecAllow {
		real, e := filepath.EvalSymlinks(p)
		if e != nil || !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return m, errors.New("命令必须为存在的绝对路径")
		}
		info, e := os.Stat(real)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 {
			return m, errors.New("命令必须为不可由插件写入的可执行文件")
		}
		base := filepath.Base(real)
		if regexp.MustCompile(`^(ba|da|z|k|fi|c|tc)?sh$|^(python[0-9.]*|node|perl|ruby|php[0-9.]*|lua[0-9.]*|env|sudo|su|xargs|busybox|find|awk|gawk|sed|make|nohup|setsid|systemd-run)$`).MatchString(base) {
			return m, errors.New("禁止 Shell、解释器及命令跳板")
		}
		if e := protectedExecutable(real); e != nil {
			return m, e
		}
		m.ExecAllow[i] = real
	}
	if (seen["fs.read"] || seen["fs.write"]) != (len(m.FSRoots) > 0) {
		return m, errors.New("文件权限与 fsRoots 必须同时声明")
	}
	panel, e := filepath.EvalSymlinks(install)
	if e != nil {
		return m, e
	}
	for i, p := range m.FSRoots {
		real, e := filepath.EvalSymlinks(p)
		if e != nil || !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return m, errors.New("fsRoots 必须为存在的绝对目录")
		}
		info, e := os.Stat(real)
		if e != nil || !info.IsDir() || real == "/" || within(real, panel) || within(panel, real) || within(real, "/proc") || within(real, "/sys") || within(real, "/dev") {
			return m, errors.New("fsRoots 指向禁止目录")
		}
		m.FSRoots[i] = real
	}
	keys := map[string]bool{}
	for _, v := range m.Env {
		if keys[v.Key] || !envPattern.MatchString(v.Key) || strings.HasPrefix(v.Key, "PANEL_") || strings.HasPrefix(v.Key, "LD_") || strings.HasPrefix(v.Key, "DYLD_") || v.Key == "PATH" || v.Key == "HOME" || len(v.Default) > 8192 || strings.ContainsRune(v.Default, 0) {
			return m, errors.New("环境配置键或值无效")
		}
		keys[v.Key] = true
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
		if key == "sdkVersion" || key == "order" {
			expected = "!!int"
		}
		if key == "required" || key == "secret" {
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
	m.Env = append([]Env(nil), m.Env...)
	for i := range m.Env {
		if m.Env[i].Secret {
			m.Env[i].Default = ""
		}
	}
	return m
}
