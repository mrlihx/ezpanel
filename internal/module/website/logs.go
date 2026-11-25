package website

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/eventstream"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
)

// 日志只能由站点ID与日志类型定位，不接收任意文件路径。
func (m *Module) openLog(c *gin.Context, write bool) (*os.File, string, bool) {
	path, ok := m.logPath(c)
	if !ok {
		return nil, "", false
	}
	f, _, err := openLogFile(path, write)
	if err != nil {
		failure(c, err)
	}
	return f, path, err == nil
}

func (m *Module) logPath(c *gin.Context) (string, bool) {
	row, ok := m.row(c)
	if !ok {
		return "", false
	}
	kind := c.DefaultQuery("type", "access")
	if kind != "access" && kind != "error" {
		response.Fail(c, response.Invalid, "日志类型无效")
		return "", false
	}
	return siteLogPath(siteLogDirectory(row), kind), true
}

func openLogFile(path string, write bool) (*os.File, os.FileInfo, error) {
	flags := os.O_RDONLY
	if write {
		flags = os.O_WRONLY
	}
	f, err := os.OpenFile(path, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, errors.New("日志不是可读取的普通文件")
	}
	return f, info, nil
}

func (m *Module) logs(c *gin.Context) {
	m.serveLogTail(c, false)
}

func (m *Module) streamLogs(c *gin.Context) {
	m.serveLogTail(c, true)
}

func (m *Module) serveLogTail(c *gin.Context, streaming bool) {
	match, ok := m.logFilter(c)
	if !ok {
		return
	}
	lines, err := strconv.Atoi(c.DefaultQuery("lines", "200"))
	if err != nil || lines < 1 || lines > 1000 {
		response.Fail(c, response.Invalid, "日志行数需要1至1000")
		return
	}
	path, ok := m.logPath(c)
	if !ok {
		return
	}
	if !streaming {
		result, err := readLogTail(path, lines, match)
		if err != nil {
			failure(c, err)
			return
		}
		response.OK(c, result)
		return
	}

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	changes := make(chan struct{}, 1)
	last, _ := os.Lstat(path)
	go func() {
		// 检测追加、截断和轮转，仅在日志变化后读取末尾内容。
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, err := os.Lstat(path)
				changed := (current == nil) != (last == nil)
				if current != nil && last != nil {
					changed = !os.SameFile(last, current) || current.Size() != last.Size() || !current.ModTime().Equal(last.ModTime())
				}
				last = current
				if changed || (err != nil && !errors.Is(err, os.ErrNotExist)) {
					select {
					case changes <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	eventstream.Serve(c, m.auth, m.tasks.Done(), changes, func(stream eventstream.Stream) bool {
		result, err := readLogTail(path, lines, match)
		if err != nil {
			stream.Send("stream-error", "", response.Summary(err.Error()))
			return false
		}
		return stream.Send("logs", "", result)
	})
}

func readLogTail(path string, lines int, match func(string) bool) (gin.H, error) {
	f, info, err := openLogFile(path, false)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return gin.H{"path": path, "content": "", "truncated": false, "size": 0}, nil
	}
	defer f.Close()
	const limit = 256 << 10
	start := max(int64(0), info.Size()-limit)
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return nil, err
	}
	if start > 0 {
		if pos := bytes.IndexByte(data, '\n'); pos >= 0 {
			data = data[pos+1:]
		}
	}
	parts := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	filtered := parts[:0]
	for _, line := range parts {
		if match(line) {
			filtered = append(filtered, line)
		}
	}
	parts = filtered
	truncated := start > 0 || len(parts) > lines
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return gin.H{"path": path, "content": strings.ToValidUTF8(strings.Join(parts, "\n"), "�"), "truncated": truncated, "size": info.Size()}, nil
}

func (m *Module) clearLog(c *gin.Context) {
	f, path, ok := m.openLog(c, true)
	if !ok {
		return
	}
	auditSite(c, "", path)
	if f != nil {
		defer f.Close()
		// 截断原文件，保留Nginx已打开的文件句柄和所有者。
		if err := f.Truncate(0); err != nil {
			failure(c, err)
			return
		}
	}
	if c.DefaultQuery("type", "access") == "access" {
		m.trafficMu.Lock()
		delete(m.traffic, c.Param("id"))
		m.trafficMu.Unlock()
	}
	response.OK(c, gin.H{})
}

func (m *Module) downloadLog(c *gin.Context) {
	match, ok := m.logFilter(c)
	if !ok {
		return
	}
	f, path, ok := m.openLog(c, false)
	if !ok {
		return
	}
	if f == nil {
		response.Fail(c, response.NotFound, "日志尚未生成")
		return
	}
	defer f.Close()
	auditSite(c, "", path)
	name := path[strings.LastIndex(path, "/")+1:]
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Status(http.StatusOK)
	if c.Query("keyword") == "" && c.Query("startTime") == "" && c.Query("endTime") == "" {
		_, _ = io.Copy(c.Writer, f)
		return
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		if match(scanner.Text()) {
			if _, err := io.WriteString(c.Writer, scanner.Text()+"\n"); err != nil {
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		c.Error(err)
	}
}

func (m *Module) logFilter(c *gin.Context) (func(string) bool, bool) {
	var start, end time.Time
	for _, field := range []struct {
		name string
		dst  *time.Time
	}{{"startTime", &start}, {"endTime", &end}} {
		if raw := c.Query(field.name); raw != "" {
			value, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				response.Fail(c, response.Invalid, "时间须为RFC3339")
				return nil, false
			}
			*field.dst = value
		}
	}
	if !start.IsZero() && !end.IsZero() && start.After(end) {
		response.Fail(c, response.Invalid, "开始时间不能晚于结束时间")
		return nil, false
	}
	keyword := c.Query("keyword")
	location, err := time.LoadLocation(m.cfg.Current().Timezone)
	if err != nil {
		location = time.UTC
	}
	return func(line string) bool {
		if keyword != "" && !strings.Contains(line, keyword) {
			return false
		}
		if start.IsZero() && end.IsZero() {
			return true
		}
		var stamp time.Time
		if pos := strings.IndexByte(line, '['); pos >= 0 {
			if end := strings.IndexByte(line[pos:], ']'); end >= 0 {
				stamp, _ = time.Parse("02/Jan/2006:15:04:05 -0700", line[pos+1:pos+end])
			}
		}
		if stamp.IsZero() && len(line) >= 19 {
			stamp, _ = time.ParseInLocation("2006/01/02 15:04:05", line[:19], location)
		}
		return !stamp.IsZero() && (start.IsZero() || !stamp.Before(start)) && (end.IsZero() || !stamp.After(end))
	}, true
}
