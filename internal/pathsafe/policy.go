package pathsafe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrProtected = errors.New("受保护路径")

type Policy struct {
	install       string
	roots         []string
	manageInstall bool
}

func Contains(root, path string) bool {
	r, e := filepath.Rel(root, path)
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))
}
func New(install string) (*Policy, error) {
	if !filepath.IsAbs(install) {
		return nil, errors.New("安装目录必须是绝对路径")
	}
	real, e := resolve(install)
	if e != nil {
		return nil, e
	}
	p := &Policy{install: real}
	for _, s := range []string{"/", "/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc", "/root", "/sbin", "/sys", "/usr", "/var"} {
		p.roots = append(p.roots, s)
		if r, e := filepath.EvalSymlinks(s); e == nil {
			p.roots = append(p.roots, r)
		}
	}
	return p, nil
}

func NewFileManager(install string) (*Policy, error) {
	p, err := New(install)
	if err != nil {
		return nil, err
	}
	p.manageInstall = true
	return p, nil
}

// 新路径从最近存在的父目录解析，不能经父链接写入面板目录。
func resolve(path string) (string, error) {
	r, e := filepath.EvalSymlinks(path)
	if e == nil {
		return r, nil
	}
	if !os.IsNotExist(e) {
		return "", e
	}
	// ENOENT也可能来自悬空链接，不能把已有链接当成待创建的普通文件。
	if _, err := os.Lstat(path); err == nil {
		return "", e
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", e
	}
	r, e = resolve(parent)
	if e != nil {
		return "", e
	}
	return filepath.Join(r, filepath.Base(path)), nil
}
func (p *Policy) check(path string, write, follow bool) (string, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", errors.New("路径必须为绝对路径")
	}
	path = filepath.Clean(path)
	var real string
	var e error
	if follow {
		real, e = resolve(path)
	} else {
		var parent string
		parent, e = resolve(filepath.Dir(path))
		real = filepath.Join(parent, filepath.Base(path))
	}
	if e != nil {
		return "", e
	}
	if !p.manageInstall && Contains(p.install, real) {
		return "", ErrProtected
	}
	if write {
		if real != p.install && Contains(real, p.install) {
			return "", ErrProtected
		}
		for _, r := range p.roots {
			if real == r {
				return "", ErrProtected
			}
		}
	}
	return real, nil
}
func (p *Policy) Check(path string, write bool) (string, error) { return p.check(path, write, true) }
func (p *Policy) CheckLink(path string, write bool) (string, error) {
	return p.check(path, write, false)
}

func (p *Policy) List(path string) (string, error) { return p.Check(path, false) }
func (p *Policy) CheckTree(path string) error {
	real, e := p.Check(path, false)
	if e != nil {
		return e
	}
	if (!p.manageInstall || real != p.install) && Contains(real, p.install) {
		return ErrProtected
	}
	for _, r := range []string{"/proc", "/sys", "/dev"} {
		if Contains(real, r) || Contains(r, real) {
			return ErrProtected
		}
	}
	return nil
}
