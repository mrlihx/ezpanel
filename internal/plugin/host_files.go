package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
)

type fileMetadata struct {
	Name       string    `json:"name"`
	Mode       string    `json:"mode"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Directory  bool      `json:"directory"`
	Link       bool      `json:"link"`
}

func describeFile(info fs.FileInfo) fileMetadata {
	return fileMetadata{
		Name: info.Name(), Mode: info.Mode().String(), Size: info.Size(),
		ModifiedAt: info.ModTime(), Directory: info.IsDir(), Link: info.Mode()&os.ModeSymlink != 0,
	}
}

func (m *Module) fsStat(c *gin.Context) {
	root, name, err := m.hostPath(c, c.Query("path"), false, false)
	if err != nil {
		response.Fail(c, response.Forbidden, err.Error())
		return
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		response.Fail(c, response.NotFound, "文件或目录不可访问")
		return
	}
	response.OK(c, describeFile(info))
}

type directoryListCache struct {
	info    fs.FileInfo
	entries []fs.DirEntry
	until   time.Time
}

func (m *Module) fsList(c *gin.Context) {
	root, name, err := m.hostPath(c, c.Query("path"), false, true)
	if err != nil {
		response.Fail(c, response.Forbidden, err.Error())
		return
	}
	defer root.Close()
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		response.Fail(c, response.Invalid, "目录分页位置无效")
		return
	}
	info, err := root.Stat(name)
	if err != nil || !info.IsDir() {
		response.Fail(c, response.Invalid, "目录不可访问")
		return
	}
	var entries []fs.DirEntry
	now := time.Now()
	if offset > 0 {
		m.mu.Lock()
		cached := m.fileList
		m.mu.Unlock()
		if now.Before(cached.until) && os.SameFile(info, cached.info) && info.ModTime().Equal(cached.info.ModTime()) {
			entries = cached.entries
		}
	}
	if entries == nil {
		entries, err = fs.ReadDir(root.FS(), filepath.ToSlash(name))
		if err != nil {
			response.Fail(c, response.Invalid, "目录不可访问")
			return
		}
		m.mu.Lock()
		m.fileList = directoryListCache{info: info, entries: entries, until: now.Add(3 * time.Second)}
		m.mu.Unlock()
	}
	const pageSize = 200
	offset = min(offset, len(entries))
	end := min(offset+pageSize, len(entries))
	rows := make([]fileMetadata, 0, end-offset)
	for _, entry := range entries[offset:end] {
		if info, err := root.Lstat(filepath.Join(name, entry.Name())); err == nil {
			rows = append(rows, describeFile(info))
		}
	}
	response.OK(c, gin.H{"entries": rows, "nextOffset": end, "more": end < len(entries)})
}

func (m *Module) fsHash(c *gin.Context) {
	root, name, err := m.hostPath(c, c.Query("path"), false, true)
	if err != nil {
		response.Fail(c, response.Forbidden, err.Error())
		return
	}
	defer root.Close()
	f, info, err := openRegular(root, name)
	if err != nil {
		response.Fail(c, response.Invalid, "无法读取文件")
		return
	}
	defer f.Close()
	hash := sha256.New()
	buf := make([]byte, 64<<10)
	for {
		if c.Request.Context().Err() != nil {
			response.Fail(c, response.System, "摘要计算已取消")
			return
		}
		n, readErr := f.Read(buf)
		_, _ = hash.Write(buf[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			response.Fail(c, response.System, "读取文件失败")
			return
		}
	}
	response.OK(c, gin.H{"sha256": hex.EncodeToString(hash.Sum(nil)), "size": info.Size()})
}

func openRegular(root *os.Root, name string) (*os.File, fs.FileInfo, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, errors.New("仅支持普通文件")
	}
	return f, info, nil
}

func (m *Module) hostPath(c *gin.Context, p string, write, follow bool) (*os.Root, string, error) {
	var real string
	var e error
	if follow {
		real, e = m.policy.Check(p, write)
	} else {
		real, e = m.policy.CheckLink(p, write)
	}
	if e != nil {
		return nil, "", e
	}
	for _, root := range identity(c).manifest.FSRoots {
		if within(real, root) {
			if write && real == root {
				return nil, "", errors.New("禁止修改授权根目录本身")
			}
			dir, err := openAuthorizedRoot(root)
			if err != nil {
				return nil, "", err
			}
			relative, _ := filepath.Rel(root, real)
			return dir, relative, nil
		}
	}
	return nil, "", errors.New("路径未获授权")
}
func (m *Module) fsRead(c *gin.Context) {
	p := c.Query("path")
	root, name, e := m.hostPath(c, p, false, true)
	defer func() { m.hostAudit(c, "fs.read", p, c.Writer.Status() < 400) }()
	if e != nil {
		response.Fail(c, response.Forbidden, "路径未获授权")
		return
	}
	defer root.Close()
	f, _, e := openRegular(root, name)
	if e != nil {
		response.Fail(c, response.Invalid, "只支持可读取的普通文本文件")
		return
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if e != nil {
		response.Fail(c, response.System, "读取失败")
		return
	}
	if len(b) > 2<<20 {
		response.Fail(c, response.TooLarge, "文本超过 2MiB")
		return
	}
	if !utf8.Valid(b) {
		response.Fail(c, response.Invalid, "文件不是 UTF-8 文本")
		return
	}
	response.OK(c, gin.H{"content": redact(string(b), m.hostSecrets(identity(c)))})
}

const maxStreamChunkLimit int64 = 50 << 20

func (m *Module) fsOpen(c *gin.Context) {
	path := c.Query("path")
	defer func() { m.hostAudit(c, "fs.open", path, c.Writer.Status() < 400) }()
	var offset, limit int64
	if raw := c.Query("offset"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			response.Fail(c, response.Invalid, "offset 无效")
			return
		}
		offset = v
	}
	if raw := c.Query("limit"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 || v > maxStreamChunkLimit {
			response.Fail(c, response.Invalid, "limit 无效或超过 50MiB")
			return
		}
		limit = v
	}
	root, name, err := m.hostPath(c, path, false, true)
	if err != nil {
		response.Fail(c, response.Forbidden, "路径未获授权")
		return
	}
	defer root.Close()
	f, info, err := openRegular(root, name)
	if err != nil {
		response.Fail(c, response.NotFound, "文件不可读取")
		return
	}
	defer f.Close()
	c.Header("Cache-Control", "no-store")
	c.Header("X-File-Size", strconv.FormatInt(info.Size(), 10))
	c.Header("X-File-ModTime", info.ModTime().UTC().Format(time.RFC3339Nano))
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		c.Header("X-File-Inode", strconv.FormatUint(st.Ino, 10))
	}
	if offset >= info.Size() {
		c.Header("X-Next-Offset", strconv.FormatInt(info.Size(), 10))
		c.Data(http.StatusOK, "application/octet-stream", nil)
		return
	}
	readLen := info.Size() - offset
	if limit > 0 && readLen > limit {
		readLen = limit
	}
	if readLen > maxStreamChunkLimit {
		response.Fail(c, response.TooLarge, "单次读取最多 50 MiB，请使用 offset 和 limit 分段读取")
		return
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			response.Fail(c, response.System, "文件定位失败")
			return
		}
	}
	c.Header("X-Next-Offset", strconv.FormatInt(offset+readLen, 10))
	c.DataFromReader(http.StatusOK, readLen, "application/octet-stream", io.LimitReader(f, readLen), nil)
}

func (m *Module) fsUpload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 100<<20)
	m.writeHostFile(c, c.Query("path"), 0600, c.Request.Body)
}

var fileMode = regexp.MustCompile(`^0[0-7]{3}$`)

func (m *Module) fsWrite(c *gin.Context) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Mode    string `json:"mode"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Content) > 2<<20 {
		response.Fail(c, response.TooLarge, "文本超过 2MiB")
		return
	}
	if in.Mode == "" {
		in.Mode = "0600"
	}
	if !fileMode.MatchString(in.Mode) {
		response.Fail(c, response.Invalid, "mode 必须为 0000-0777")
		return
	}
	mode, _ := strconv.ParseUint(in.Mode, 8, 32)
	m.writeHostFile(c, in.Path, os.FileMode(mode), strings.NewReader(in.Content))
}

func (m *Module) writeHostFile(c *gin.Context, path string, mode os.FileMode, content io.Reader) {
	root, name, e := m.hostPath(c, path, true, false)
	defer func() { m.hostAudit(c, "fs.write", path, c.Writer.Status() < 400) }()
	if e != nil {
		response.Fail(c, response.Forbidden, "路径未获授权")
		return
	}
	defer root.Close()
	if info, err := root.Lstat(name); err == nil && !info.Mode().IsRegular() {
		response.Fail(c, response.Forbidden, "拒绝覆盖链接或特殊文件")
		return
	} else if err != nil && !os.IsNotExist(err) {
		response.Fail(c, response.System, "目标不可访问")
		return
	}
	temporary := filepath.Join(filepath.Dir(name), ".panel-plugin-"+store.ID())
	f, e := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		response.Fail(c, response.System, "写入失败")
		return
	}
	defer root.Remove(temporary)
	if _, e = io.Copy(f, content); e == nil {
		e = f.Chmod(mode)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = root.Rename(temporary, name)
	}
	if e != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(e, &maxBytes) {
			response.Fail(c, response.TooLarge, "二进制文件超过 100 MiB")
			return
		}
		response.Fail(c, response.System, "写入提交失败")
		return
	}
	response.OK(c, nil)
}
func (m *Module) fsMkdir(c *gin.Context) {
	var in struct {
		Path string `json:"path"`
	}
	if !response.Bind(c, &in) {
		return
	}
	root, name, e := m.hostPath(c, in.Path, true, false)
	defer func() { m.hostAudit(c, "fs.mkdir", in.Path, c.Writer.Status() < 400) }()
	if e != nil {
		response.Fail(c, response.Forbidden, "路径未获授权")
		return
	}
	defer root.Close()
	if info, err := root.Lstat(name); err == nil && info.Mode()&os.ModeSymlink != 0 {
		response.Fail(c, response.Forbidden, "拒绝跟随目录链接")
		return
	}
	missing := []string{}
	for dir := name; ; dir = filepath.Dir(dir) {
		if _, err := root.Lstat(dir); err == nil {
			break
		} else if !os.IsNotExist(err) {
			response.Fail(c, response.System, "父目录不可访问")
			return
		}
		missing = append(missing, dir)
	}
	if root.MkdirAll(name, 0755) != nil {
		response.Fail(c, response.System, "创建目录失败")
		return
	}
	for _, dir := range missing {
		fd, err := root.OpenFile(dir, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err == nil {
			err = errors.Join(fd.Chmod(0755), fd.Close())
		}
		if err != nil {
			response.Fail(c, response.System, "目录权限设置失败")
			return
		}
	}
	response.OK(c, nil)
}
func (m *Module) fsDelete(c *gin.Context) {
	var in struct {
		Path string `json:"path"`
	}
	if !response.Bind(c, &in) {
		return
	}
	root, name, e := m.hostPath(c, in.Path, true, false)
	defer func() { m.hostAudit(c, "fs.delete", in.Path, c.Writer.Status() < 400) }()
	if e != nil {
		response.Fail(c, response.Forbidden, "路径未获授权或为授权根")
		return
	}
	defer root.Close()
	if e = root.RemoveAll(name); e != nil {
		response.Fail(c, response.System, "删除失败")
		return
	}
	response.OK(c, nil)
}
func (m *Module) hostExec(c *gin.Context) {
	var in struct {
		Cmd        string   `json:"cmd"`
		Args       []string `json:"args"`
		TimeoutSec int      `json:"timeoutSec"`
	}
	if !response.Bind(c, &in) {
		return
	}
	i := identity(c)
	allowed := false
	for _, cmd := range i.manifest.ExecAllow {
		if cmd == in.Cmd {
			allowed = true
			break
		}
	}
	defer func() { m.hostAudit(c, "exec", in.Cmd, c.Writer.Status() < 400) }()
	if !allowed {
		response.Fail(c, response.Forbidden, "命令未获授权")
		return
	}
	if in.TimeoutSec == 0 {
		in.TimeoutSec = 30
	}
	if in.TimeoutSec < 1 || in.TimeoutSec > 300 {
		response.Fail(c, response.Invalid, "timeoutSec 必须为 1-300")
		return
	}
	data := filepath.Join(m.private(), i.id)
	env := append(executor.DefaultEnv(), "HOME="+data)
	result, e := executor.RunFixed(c.Request.Context(), time.Duration(in.TimeoutSec)*time.Second, data, env, in.Cmd, in.Args...)
	out := gin.H{"exitCode": result.ExitCode, "stdout": redact(result.Stdout, m.hostSecrets(i)), "stderr": redact(result.Stderr, m.hostSecrets(i)), "truncated": result.Truncated}
	if e != nil {
		message := "命令执行失败"
		if c.Request.Context().Err() != nil {
			message = "插件已停止或请求取消"
		} else if errors.Is(e, os.ErrDeadlineExceeded) || errors.Is(e, context.DeadlineExceeded) {
			message = "命令执行超时"
		}
		c.Set("panel.errorCode", response.System)
		c.JSON(500, gin.H{"code": response.System, "msg": message, "data": out})
		return
	}
	response.OK(c, out)
}
