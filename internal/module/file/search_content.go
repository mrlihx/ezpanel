package file

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"

	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
)

type contentMatch struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

type contentSearchRow struct {
	Entry
	Match contentMatch `json:"match"`
}

func (m *Module) searchContent(c *gin.Context) {
	keyword := []byte(c.Query("keyword"))
	if len(keyword) == 0 || len(keyword) > 256 || bytes.ContainsAny(keyword, "\x00\r\n") || !utf8.Valid(keyword) {
		response.Fail(c, response.Invalid, "请输入单行搜索内容，最多256字节")
		return
	}
	path, err := m.policy.List(c.DefaultQuery("path", "/"))
	if err != nil {
		fail(c, err)
		return
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		fail(c, err)
		return
	}
	defer root.Close()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	rows := []contentSearchRow{}
	truncated := false
	const maxEntries = 50000
	entries, remaining := 0, int64(128<<20)
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil || entries >= maxEntries || remaining <= 0 || len(rows) >= 500 {
			truncated = true
			return fs.SkipAll
		}
		entries++
		if walkErr != nil {
			if name == "." {
				return walkErr
			}
			return nil
		}
		absolute := filepath.Join(path, filepath.FromSlash(name))
		for _, skip := range []string{"/dev", "/proc", "/sys", m.uploadRoot()} {
			if pathsafe.Contains(skip, absolute) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		f, err := root.OpenFile(filepath.FromSlash(name), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return nil
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > textLimit {
			f.Close()
			return nil
		}
		data, err := io.ReadAll(io.LimitReader(contextReader{ctx, f}, min(textLimit+1, remaining)))
		f.Close()
		remaining -= int64(len(data))
		if err != nil || len(data) != int(info.Size()) || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		lineNumber := 0
		for line := range bytes.SplitSeq(data, []byte{'\n'}) {
			lineNumber++
			if at := bytes.Index(line, keyword); at >= 0 {
				snippet := line[max(0, at-80):min(len(line), at+len(keyword)+80)]
				rows = append(rows, contentSearchRow{
					Entry: m.entry(absolute, info),
					Match: contentMatch{Line: lineNumber, Text: strings.ToValidUTF8(string(bytes.TrimSpace(snippet)), "�")},
				})
				break
			}
		}
		return nil
	})
	if err != nil {
		fail(c, err)
		return
	}
	items := make([]Entry, len(rows))
	for i := range rows {
		items[i] = rows[i].Entry
	}
	if err := m.loadRemarks(items); err != nil {
		fail(c, err)
		return
	}
	for i := range rows {
		rows[i].Entry = items[i]
	}
	response.OK(c, gin.H{"list": rows, "truncated": truncated})
}
