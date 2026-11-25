package file

import (
	"context"
	"errors"
	"ezpanel/internal/pathsafe"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type idCacheEntry struct {
	name      string
	expiresAt time.Time
}

var (
	ownerCacheMu sync.RWMutex
	userIDCache  = map[uint32]idCacheEntry{}
	groupIDCache = map[uint32]idCacheEntry{}
)

func lookupUserName(uid uint32) string {
	now := time.Now()
	ownerCacheMu.RLock()
	if entry, ok := userIDCache[uid]; ok && now.Before(entry.expiresAt) {
		ownerCacheMu.RUnlock()
		return entry.name
	}
	ownerCacheMu.RUnlock()
	name := strconv.Itoa(int(uid))
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	ownerCacheMu.Lock()
	userIDCache[uid] = idCacheEntry{name: name, expiresAt: now.Add(30 * time.Second)}
	ownerCacheMu.Unlock()
	return name
}

func lookupGroupName(gid uint32) string {
	now := time.Now()
	ownerCacheMu.RLock()
	if entry, ok := groupIDCache[gid]; ok && now.Before(entry.expiresAt) {
		ownerCacheMu.RUnlock()
		return entry.name
	}
	ownerCacheMu.RUnlock()
	name := strconv.Itoa(int(gid))
	if g, err := user.LookupGroupId(name); err == nil {
		name = g.Name
	}
	ownerCacheMu.Lock()
	groupIDCache[gid] = idCacheEntry{name: name, expiresAt: now.Add(30 * time.Second)}
	ownerCacheMu.Unlock()
	return name
}

const textLimit = 2 << 20

var errConflict = errors.New("目标已存在或类型冲突")
var errLimit = errors.New("超过容量或数量上限")

type Entry struct {
	RemarkReadonly bool      `json:"remarkReadonly"`
	LinkTarget     string    `json:"linkTarget,omitempty"`
	LinkIsDir      bool      `json:"linkIsDir,omitempty"`
	Remark         string    `json:"remark"`
	Name           string    `json:"name"`
	Path           string    `json:"path"`
	IsDir          bool      `json:"isDir"`
	IsLink         bool      `json:"isLink"`
	Protected      bool      `json:"protected"`
	Size           *int64    `json:"size"`
	Mode           string    `json:"mode"`
	Owner          string    `json:"owner"`
	Group          string    `json:"group"`
	ModTime        time.Time `json:"modTime"`
}

func (m *Module) entry(path string, info os.FileInfo) Entry {
	e := Entry{Name: info.Name(), Path: path, IsDir: info.IsDir(), IsLink: info.Mode()&os.ModeSymlink != 0, Mode: fmtMode(info.Mode()), ModTime: info.ModTime()}
	if e.IsLink {
		e.LinkTarget, _ = filepath.EvalSymlinks(path)
		if e.LinkTarget == "" {
			e.LinkTarget, _ = os.Readlink(path)
			if e.LinkTarget != "" && !filepath.IsAbs(e.LinkTarget) {
				e.LinkTarget = filepath.Join(filepath.Dir(path), e.LinkTarget)
			}
		}
		if target, err := os.Stat(path); err == nil {
			e.LinkIsDir = target.IsDir()
		}
	}
	if !info.IsDir() {
		s := info.Size()
		e.Size = &s
	}
	_, err := m.policy.Check(path, false)
	e.Protected = errors.Is(err, pathsafe.ErrProtected)
	if s, ok := info.Sys().(*syscall.Stat_t); ok {
		e.Owner = lookupUserName(s.Uid)
		e.Group = lookupGroupName(s.Gid)
	}
	return e
}
func fmtMode(mode os.FileMode) string { return fmt.Sprintf("%04o", mode.Perm()) }
func nameValid(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\\x00")
}
func (m *Module) sources(paths []string, write bool) ([]string, error) {
	if len(paths) < 1 || len(paths) > 100 {
		return nil, errors.New("每批需要1至100项")
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		v, e := m.policy.CheckLink(p, write)
		if e != nil {
			return nil, e
		}
		if _, e = os.Lstat(v); e != nil {
			return nil, e
		}
		for _, old := range out[:i] {
			if pathsafe.Contains(old, v) || pathsafe.Contains(v, old) {
				return nil, errors.New("不能同时选择父项与子项")
			}
		}
		out[i] = v
	}
	return out, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func metadata(dst string, info os.FileInfo) error {
	if s, ok := info.Sys().(*syscall.Stat_t); ok {
		if e := os.Lchown(dst, int(s.Uid), int(s.Gid)); e != nil {
			return e
		}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t := unix.NsecToTimespec(info.ModTime().UnixNano())
		return unix.UtimesNanoAt(unix.AT_FDCWD, dst, []unix.Timespec{t, t}, unix.AT_SYMLINK_NOFOLLOW)
	}
	if e := os.Chmod(dst, info.Mode()); e != nil {
		return e
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}
func (m *Module) atomic(ctx context.Context, dst string, r io.Reader, overwrite bool, limit int64, createMode os.FileMode) error {
	if _, e := m.policy.CheckLink(dst, true); e != nil {
		return e
	}
	old, e := os.Lstat(dst)
	if e == nil && (!overwrite || !old.Mode().IsRegular()) {
		return errConflict
	}
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(dst), ".panel-*.part")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	n, e := io.Copy(f, io.LimitReader(contextReader{ctx, r}, limit+1))
	if e == nil && n > limit {
		e = errLimit
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	return commitFile(tmp, dst, old, overwrite, createMode)
}

func inheritParentOwner(path string) {
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && (st.Uid != 0 || st.Gid != 0) {
		_ = os.Chown(path, int(st.Uid), int(st.Gid))
	}
}

func commitFile(tmp, dst string, old os.FileInfo, overwrite bool, createMode os.FileMode) error {
	if old != nil {
		// 编辑与上传保留权限属主，但新内容应产生新的修改时间。
		if st, ok := old.Sys().(*syscall.Stat_t); ok {
			if e := os.Chown(tmp, int(st.Uid), int(st.Gid)); e != nil {
				return e
			}
		}
		if e := os.Chmod(tmp, old.Mode()); e != nil {
			return e
		}
	} else {
		if e := os.Chmod(tmp, createMode); e != nil {
			return e
		}
		inheritParentOwner(tmp)
	}
	if !overwrite {
		return os.Link(tmp, dst)
	}
	return os.Rename(tmp, dst)
}
func copyTree(ctx context.Context, src, dst string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	info, e := os.Lstat(src)
	if e != nil {
		return e
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, e := os.Readlink(src)
		if e != nil {
			return e
		}
		if e = os.Symlink(target, dst); e != nil {
			return e
		}
	case info.IsDir():
		if e = os.Mkdir(dst, 0700); e != nil {
			return e
		}
		entries, e := os.ReadDir(src)
		if e != nil {
			return e
		}
		for _, v := range entries {
			if e = copyTree(ctx, filepath.Join(src, v.Name()), filepath.Join(dst, v.Name())); e != nil {
				return e
			}
		}
	case info.Mode().IsRegular():
		in, e := os.Open(src)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = io.Copy(out, contextReader{ctx, in})
		ce := out.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	default:
		return errors.New("不支持特殊文件")
	}
	return metadata(dst, info)
}
func treeSize(ctx context.Context, path string) (int64, error) {
	var size int64
	e := filepath.Walk(path, func(_ string, i os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if !i.IsDir() {
			size += i.Size()
		}
		return nil
	})
	return size, e
}
func removeTree(ctx context.Context, path string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if info.IsDir() {
		children, e := os.ReadDir(path)
		if e != nil {
			return e
		}
		for _, child := range children {
			if e = removeTree(ctx, filepath.Join(path, child.Name())); e != nil {
				return e
			}
		}
	}
	return os.Remove(path)
}
