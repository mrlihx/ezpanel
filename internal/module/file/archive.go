package file

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/executor"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func archiveWrite(ctx context.Context, w io.Writer, paths []string, format string) error {
	var zw *zip.Writer
	var tw *tar.Writer
	var gz *gzip.Writer
	if format == "zip" {
		zw = zip.NewWriter(w)
	} else {
		gz = gzip.NewWriter(w)
		tw = tar.NewWriter(gz)
	}
	for _, source := range paths {
		e := filepath.Walk(source, func(p string, info os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			if e = ctx.Err(); e != nil {
				return e
			}
			if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				return errors.New("不支持特殊文件")
			}
			name, e := filepath.Rel(filepath.Dir(source), p)
			if e != nil {
				return e
			}
			name = filepath.ToSlash(name)
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, e = os.Readlink(p)
				if e != nil {
					return e
				}
			}
			var out io.Writer
			if zw != nil {
				h, e := zip.FileInfoHeader(info)
				if e != nil {
					return e
				}
				h.Name = name
				if info.IsDir() {
					h.Name += "/"
				}
				h.Method = zip.Deflate
				out, e = zw.CreateHeader(h)
				if e != nil {
					return e
				}
				if link != "" {
					_, e = io.WriteString(out, link)
					return e
				}
			} else {
				h, e := tar.FileInfoHeader(info, link)
				if e != nil {
					return e
				}
				h.Name = name
				if e = tw.WriteHeader(h); e != nil {
					return e
				}
				out = tw
			}
			if info.Mode().IsRegular() {
				f, e := os.Open(p)
				if e != nil {
					return e
				}
				_, e = io.Copy(out, contextReader{ctx, f})
				ce := f.Close()
				if e != nil {
					return e
				}
				return ce
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	if zw != nil {
		return zw.Close()
	}
	if e := tw.Close(); e != nil {
		return e
	}
	return gz.Close()
}
func (m *Module) compress(c *gin.Context) {
	var r struct {
		Paths  []string `json:"paths"`
		Dest   string   `json:"dest"`
		Format string   `json:"format"`
	}
	if !response.Bind(c, &r) {
		return
	}
	if r.Format != "zip" && r.Format != "tar.gz" && r.Format != "7z" {
		response.Fail(c, response.Invalid, "仅支持tar.gz、zip和7z")
		return
	}
	if !strings.HasSuffix(strings.ToLower(r.Dest), "."+r.Format) {
		response.Fail(c, response.Invalid, "保存文件名扩展名与压缩类型不一致")
		return
	}
	var sevenZip string
	if r.Format == "7z" {
		for _, name := range []string{"7zz", "7z", "7za"} {
			if bin, err := executor.LookPath(name); err == nil {
				sevenZip = bin
				break
			}
		}
		if sevenZip == "" {
			response.Fail(c, response.Invalid, "7z压缩需要安装服务器的7zip软件包")
			return
		}
	}
	paths, e := m.sources(r.Paths, false)
	if e != nil {
		fail(c, e)
		return
	}
	// 文件名已在窗口中生成，按显示值保存，不再追加随机后缀。
	dst, e := m.policy.CheckLink(r.Dest, true)
	if e != nil {
		fail(c, e)
		return
	}
	names := map[string]bool{}
	for _, p := range paths {
		if e = m.policy.CheckTree(p); e != nil {
			fail(c, e)
			return
		}
		if pathsafe.Contains(p, dst) || names[filepath.Base(p)] {
			response.Fail(c, response.Invalid, "输出不能位于输入中，输入名称不能重复")
			return
		}
		names[filepath.Base(p)] = true
	}
	if _, e = os.Lstat(dst); !os.IsNotExist(e) {
		fail(c, errConflict)
		return
	}
	m.submit(c, "compress", []string{dst}, func(ctx context.Context, _ string) error {
		for _, p := range paths {
			if e := m.policy.CheckTree(p); e != nil {
				return e
			}
		}
		dir, e := os.MkdirTemp(filepath.Dir(dst), ".panel-*")
		if e != nil {
			return e
		}
		defer os.RemoveAll(dir)
		part := filepath.Join(dir, "archive.part")
		if r.Format == "7z" {
			groups := map[string][]string{}
			for _, source := range paths {
				if e = filepath.Walk(source, func(_ string, info os.FileInfo, err error) error {
					if err != nil {
						return err
					}
					if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
						return errors.New("不支持特殊文件")
					}
					return ctx.Err()
				}); e != nil {
					return e
				}
				parent := filepath.Dir(source)
				groups[parent] = append(groups[parent], filepath.Base(source))
			}
			// 固定工作目录保留所选项目名称；禁用通配和列表文件解析，链接按链接归档。
			for parent, names := range groups {
				args := append([]string{"a", "-t7z", "-snl", "-sse", "-spd", "-y", "--", part}, names...)
				if _, e = executor.RunFixed(ctx, time.Hour, parent, nil, sevenZip, args...); e != nil {
					return e
				}
			}
		}
		f, e := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0755)
		if e != nil {
			return e
		}
		if r.Format != "7z" {
			e = archiveWrite(ctx, f, paths, r.Format)
		}
		if e == nil {
			e = f.Chmod(0755)
		}
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			return e
		}
		if e = os.Link(part, dst); e != nil {
			return e
		}
		inheritParentOwner(dst)
		return nil
	})
}
func (m *Module) download(c *gin.Context) {
	success := false
	if c.Request.Method != http.MethodHead && m.auth != nil {
		defer func() {
			session := auth.Current(c)
			name := session.User.Username
			c.Set("panel.auditRecorded", true)
			code := response.System
			if success {
				code = 0
			}
			m.db.Create(&store.OpLog{Username: &name, IP: m.auth.ClientIP(c), Module: "file", Action: "download", Target: "/api/files/download", Phase: "completed", Success: success, ErrorCode: code, CreatedAt: time.Now().UTC()})
		}()
	}

	p, e := m.policy.Check(c.Query("path"), false)
	if e != nil {
		fail(c, e)
		return
	}
	info, e := os.Stat(p)
	if e != nil {
		fail(c, e)
		return
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		response.Fail(c, response.Forbidden, "不支持特殊文件下载")
		return
	}
	name := info.Name()
	if info.IsDir() {
		if e = m.policy.CheckTree(p); e != nil {
			fail(c, e)
			return
		}
		name += ".zip"
	} else {
		f, e := os.Open(p)
		if e != nil {
			fail(c, e)
			return
		}
		defer f.Close()
		c.Header("Content-Type", "application/octet-stream")
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": safeDownloadName(name)}))
		http.ServeContent(c.Writer, c.Request, name, info.ModTime(), contextReadSeeker{c.Request.Context(), f})
		success = c.Request.Context().Err() == nil && c.Writer.Status() < 400
		return
	}
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": safeDownloadName(name)}))
	if c.Request.Method == http.MethodHead {
		return
	}
	if e = archiveWrite(c.Request.Context(), c.Writer, []string{p}, "zip"); e != nil {
		panic(http.ErrAbortHandler)
	}
	success = true
}
func safeDownloadName(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}
