//go:build linux

package dirguard

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/btf"
	"github.com/cilium/ebpf/link"
	"golang.org/x/sys/unix"
)

//go:embed bpf/guard.o
var program []byte

type Guard struct {
	mu        sync.Mutex
	stateDir  string
	pin       string
	active    *ebpf.Map
	applied   map[uint32]record
	matchers  map[string]*Matcher
	links     []link.Link
	supported bool
	notice    string
}

func New(stateDir string) (*Guard, error) {
	absolute, err := canonicalStatePath(stateDir)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(absolute))
	guard := &Guard{stateDir: absolute, pin: fmt.Sprintf("/sys/fs/bpf/ezpanel_hardening_%x", sum[:12])}
	raw, err := os.ReadFile("/sys/kernel/security/lsm")
	if err != nil || !strings.Contains(","+strings.TrimSpace(string(raw))+",", ",bpf,") {
		guard.notice = "内核未启用 BPF LSM，无法提供目录实时保护"
		return guard, nil
	}
	spec, err := btf.LoadKernelSpec()
	if err != nil {
		guard.notice = "内核缺少可读取的 BTF 信息"
		return guard, nil
	}
	var function *btf.Func
	if err = spec.TypeByName("bpf_path_d_path", &function); err != nil {
		guard.notice = "内核缺少目录保护所需的 bpf_path_d_path 接口"
		return guard, nil
	}
	guard.supported = true
	guard.notice = "目录使用 BPF LSM 实时保护，通配符排除同时适用于未来路径"
	if _, err = os.Stat(filepath.Join(guard.pin, "policies_active")); err == nil {
		guard.active, err = ebpf.LoadPinnedMap(filepath.Join(guard.pin, "policies_active"), nil)
		if err != nil {
			return nil, fmt.Errorf("读取已有目录保护策略：%w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return guard, nil
}
func (g *Guard) Supported() (bool, string) { return g.supported, g.notice }
func (g *Guard) attach() error {
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(program))
	if err != nil {
		return err
	}
	collection, err := ebpf.NewCollection(spec)
	if err != nil {
		return fmt.Errorf("加载目录保护内核程序：%w", err)
	}
	defer collection.Close()
	if err = os.MkdirAll(g.pin, 0700); err != nil {
		return err
	}
	names := make([]string, 0, len(collection.Programs))
	for name := range collection.Programs {
		names = append(names, name)
	}
	sort.Strings(names)
	cleanup := func() {
		for _, l := range g.links {
			l.Unpin()
			l.Close()
		}
		g.links = nil
		os.Remove(filepath.Join(g.pin, "policies_active"))
		os.Remove(g.pin)
	}
	for _, name := range names {
		l, e := link.AttachLSM(link.LSMOptions{Program: collection.Programs[name]})
		if e != nil {
			cleanup()
			return fmt.Errorf("挂接目录保护 %s：%w", name, e)
		}
		if e = l.Pin(filepath.Join(g.pin, name)); e != nil {
			l.Close()
			cleanup()
			return e
		}
		g.links = append(g.links, l)
	}
	active := collection.Maps["policies_active"]
	if active == nil {
		cleanup()
		return errors.New("目录保护策略映射缺失")
	}
	if err = active.Pin(filepath.Join(g.pin, "policies_active")); err != nil {
		cleanup()
		return err
	}
	g.active, err = active.Clone()
	if err != nil {
		cleanup()
		return err
	}
	return nil
}
func (g *Guard) Sync(policies []Policy) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(policies) == 0 {
		if g.active != nil {
			if err := g.active.Delete(uint32(0)); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				return fmt.Errorf("清除目录保护策略：%w", err)
			}
		}
		g.applied = nil
		g.matchers = nil
		entries, err := os.ReadDir(g.pin)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, entry := range entries {
			if err = os.Remove(filepath.Join(g.pin, entry.Name())); err != nil {
				return err
			}
		}
		for _, l := range g.links {
			l.Close()
		}
		g.links = nil
		if g.active != nil {
			g.active.Close()
			g.active = nil
		}
		if err = os.Remove(g.pin); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if !g.supported {
		return errors.New(g.notice)
	}
	if len(policies) > MaxPolicies {
		return fmt.Errorf("目录实时保护最多支持 %d 条规则", MaxPolicies)
	}
	rows := map[uint32]record{}
	matchers := make(map[string]*Matcher, len(policies))
	rootCount := 0
	mountData, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	for n, policy := range policies {
		canonical, err := filepath.EvalSymlinks(policy.Path)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(canonical) || canonical == "/" || len(canonical) >= MaxRootPath {
			return errors.New("保护目录必须是 1 至 1023 字节的非根绝对路径")
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errors.New("实时目录保护对象不是目录")
		}
		key := strings.Join(policy.Exclusions, "\x00")
		matcher := matchers[key]
		if matcher == nil {
			matcher = g.matchers[key]
		}
		if matcher == nil {
			matcher, err = Compile(policy.Exclusions)
			if err != nil {
				return err
			}
		}
		matchers[key] = matcher
		if g.stateDir == canonical || strings.HasPrefix(g.stateDir, canonical+string(os.PathSeparator)) {
			return errors.New("不能保护加固状态目录或其上级目录")
		}
		start := uint32(MaxPolicies + 1 + n*MaxStates)
		for _, anchor := range mountAnchors(string(mountData), canonical) {
			if rootCount >= MaxPolicies {
				return errors.New("保护目录及其挂载点合计不能超过 32 个")
			}
			info, err := os.Stat(anchor)
			if err != nil {
				return err
			}
			stat := info.Sys().(*syscall.Stat_t)
			root := record{Length: uint32(len(canonical)), Start: start, Device: uint64(unix.Major(uint64(stat.Dev)))<<20 | uint64(unix.Minor(uint64(stat.Dev))), Inode: stat.Ino}
			prefix := strings.TrimPrefix(strings.TrimPrefix(anchor, canonical), "/")
			if prefix != "" {
				prefix += "/"
				var state uint16
				for index := 0; index < len(prefix); index++ {
					state = matcher.transitions[state][prefix[index]]
				}
				root.Initial = uint32(state)
			}
			copy(root.Root[:], canonical)
			rootCount++
			rows[uint32(rootCount)] = root
		}
		for index, transitions := range matcher.transitions {
			rows[start+uint32(index)] = record{Accept: uint32(matcher.accepting[index]), Next: transitions}
		}
	}
	rows[0] = record{Count: uint32(rootCount)}
	g.matchers = matchers
	if maps.Equal(g.applied, rows) {
		return nil
	}
	next, err := ebpf.NewMap(&ebpf.MapSpec{Name: "ezp_dir_rules", Type: ebpf.Hash, KeySize: 4, ValueSize: uint32(binary.Size(record{})), MaxEntries: MaxPolicies*(MaxStates+1) + 1, Flags: 1})
	if err != nil {
		return err
	}
	defer next.Close()
	for key, value := range rows {
		if err = next.Put(key, value); err != nil {
			return err
		}
	}
	if g.active == nil {
		if err = g.attach(); err != nil {
			return err
		}
	}
	if err = g.active.Put(uint32(0), next); err != nil {
		return fmt.Errorf("切换目录保护策略：%w", err)
	}
	g.applied = rows
	return nil
}

// Close 仅关闭本进程句柄，内核固定的策略跨面板退出继续生效；Sync(nil) 才解除保护。
func (g *Guard) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var errs []error
	for _, l := range g.links {
		errs = append(errs, l.Close())
	}
	g.links = nil
	g.applied = nil
	if g.active != nil {
		errs = append(errs, g.active.Close())
		g.active = nil
	}
	return errors.Join(errs...)
}
