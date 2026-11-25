package task

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"ezpanel/internal/auth"
	"ezpanel/internal/eventstream"
	"ezpanel/internal/logview"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (m *Manager) subscribe(key string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	m.eventMu.Lock()
	if m.listeners == nil {
		m.listeners = make(map[string]map[chan struct{}]struct{})
	}
	if m.listeners[key] == nil {
		m.listeners[key] = make(map[chan struct{}]struct{})
	}
	m.listeners[key][ch] = struct{}{}
	m.eventMu.Unlock()
	return ch, func() {
		m.eventMu.Lock()
		delete(m.listeners[key], ch)
		if len(m.listeners[key]) == 0 {
			delete(m.listeners, key)
		}
		m.eventMu.Unlock()
	}
}

func (m *Manager) notify(key string) {
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	for ch := range m.listeners[key] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) WatchCron(id string) (<-chan struct{}, func()) {
	return m.subscribe("cron:" + id)
}

func (m *Manager) NotifyCron(id string) {
	if id != "" {
		m.notify("cron:" + id)
	}
}

func (m *Manager) Done() <-chan struct{} { return m.ctx.Done() }

func (m *Manager) stream(c *gin.Context, service *auth.Service) {
	id := c.Param("taskId")
	withLogs := c.Query("logs") != "false"
	key := "task:" + id
	if !withLogs {
		key = "status:" + id
	}
	changes, unsubscribe := m.subscribe(key)
	defer unsubscribe()

	ctx := c.Request.Context()
	var row store.Task
	if err := m.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.NotFound, "任务不存在")
		} else {
			response.Fail(c, response.Internal, "")
		}
		return
	}
	var offset int64
	if cursor := c.GetHeader("Last-Event-ID"); withLogs && cursor != "" {
		var err error
		offset, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || offset < 0 {
			response.Fail(c, response.Invalid, "日志位置无效")
			return
		}
	}
	first := true
	eventstream.Serve(c, service, m.Done(), changes, func(s eventstream.Stream) bool {
		initial := first
		first = false
		if !initial {
			if err := m.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
				s.Send("stream-error", "", "任务状态读取失败")
				return false
			}
		}
		data := view(row)
		for {
			lines := []string{}
			more := false
			if withLogs {
				var err error
				lines, offset, more, err = readLogChunk(row, offset)
				if err != nil {
					s.Send("stream-error", "", "任务日志无法读取")
					return false
				}
			}
			data["logs"], data["nextOffset"] = lines, offset
			if !more && row.Status != "running" {
				s.Send("done", strconv.FormatInt(offset, 10), data)
				return false
			}
			if initial || len(lines) != 0 || row.LogTruncated {
				if !s.Send("task", strconv.FormatInt(offset, 10), data) {
					return false
				}
			}
			initial = false
			if !more || ctx.Err() != nil {
				return true
			}
		}
	})
}

func logSection(row store.Task) (*os.File, *io.SectionReader, error) {
	f, err := logview.Open(row.LogPath, false)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	var start int64
	size := info.Size()
	if sharedLog(row) {
		base, err := logBase(f)
		if err != nil || row.LogStart < base {
			f.Close()
			return nil, nil, errors.New("共享日志的位置已失效")
		}
		start = row.LogStart - base + int64(logHeaderSize)
		size = max(0, size-start)
		if row.LogSize != nil {
			size = min(size, *row.LogSize)
		}
	}
	return f, io.NewSectionReader(f, start, size), nil
}

func readLogChunk(row store.Task, offset int64) ([]string, int64, bool, error) {
	file, section, err := logSection(row)
	if err != nil {
		return nil, offset, false, err
	}
	defer file.Close()
	if _, err := section.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, false, err
	}
	reader := bufio.NewReader(section)
	lines := []string{}
	start := offset
	for len(lines) < 200 && offset-start < 256<<10 {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			return lines, offset, false, nil
		}
		if err != nil {
			return nil, offset, false, err
		}
		lines = append(lines, strings.TrimSuffix(line, "\n"))
		offset += int64(len(line))
	}
	return lines, offset, offset < section.Size(), nil
}
