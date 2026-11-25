package plugin

import (
	"context"
	"errors"
	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

func (m *Module) hostPath(c *gin.Context, p string, write, follow bool) (string, error) {
	var real string
	var e error
	if follow {
		real, e = m.policy.Check(p, write)
	} else {
		real, e = m.policy.CheckLink(p, write)
	}
	if e != nil {
		return "", e
	}
	for _, root := range identity(c).manifest.FSRoots {
		if within(real, root) {
			if write && real == root {
				return "", errors.New("禁止修改授权根目录本身")
			}
			return real, nil
		}
	}
	return "", errors.New("路径未获授权")
}
func (m *Module) fsRead(c *gin.Context) {
	p, e := m.hostPath(c, c.Query("path"), false, true)
	defer func() { m.hostAudit(c, "fs.read", p, c.Writer.Status() < 400) }()
	if e != nil {
		response.Fail(c, response.Forbidden, "路径未获授权")
		return
	}
	info, e := os.Stat(p)
	if e != nil || !info.Mode().IsRegular() {
		response.Fail(c, response.Invalid, "只支持普通文本文件")
		return
	}
	f, e := os.Open(p)
	if e != nil {
		response.Fail(c, response.System, "读取失败")
		return
	}
	defer f.Close()
	info, e = f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		response.Fail(c, response.Invalid, "只支持普通文本文件")
		return
	}
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
	p, e := m.hostPath(c, in.Path, true, false)
	defer func() { m.hostAudit(c, "fs.write", p, c.Writer.Status() < 400) }()
	if e != nil {
		response.Fail(c, response.Forbidden, "路径未获授权")
		return
	}
	if info, err := os.Lstat(p); err == nil && !info.Mode().IsRegular() {
		response.Fail(c, response.Forbidden, "拒绝覆盖链接或特殊文件")
		return
	} else if err != nil && !os.IsNotExist(err) {
		response.Fail(c, response.System, "目标不可访问")
		return
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".panel-plugin-*")
	if e != nil {
		response.Fail(c, response.System, "写入失败")
		return
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(os.FileMode(mode)); e == nil {
		_, e = f.WriteString(in.Content)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e == nil {
		e = os.Rename(f.Name(), p)
	}
	if e != nil {
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
	p, e := m.hostPath(c, in.Path, true, false)
	defer func() { m.hostAudit(c, "fs.mkdir", p, c.Writer.Status() < 400) }()
	if e != nil {
		response.Fail(c, response.Forbidden, "路径未获授权")
		return
	}
	if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
		response.Fail(c, response.Forbidden, "拒绝跟随目录链接")
		return
	}
	missing := []string{}
	for dir := p; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(dir); err == nil {
			break
		} else if !os.IsNotExist(err) {
			response.Fail(c, response.System, "父目录不可访问")
			return
		}
		missing = append(missing, dir)
	}
	if os.MkdirAll(p, 0755) != nil {
		response.Fail(c, response.System, "创建目录失败")
		return
	}
	for _, dir := range missing {
		if os.Chmod(dir, 0755) != nil {
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
	p, e := m.hostPath(c, in.Path, true, false)
	defer func() { m.hostAudit(c, "fs.delete", p, c.Writer.Status() < 400) }()
	if e != nil {
		response.Fail(c, response.Forbidden, "路径未获授权或为授权根")
		return
	}
	if e = os.RemoveAll(p); e != nil {
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
