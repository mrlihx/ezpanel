package docker

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/eventstream"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

type composeLiveLog struct {
	composeLogLine
	truncated bool
	err       error
}

func (m *Module) streamComposeProjectLogs(c *gin.Context) {
	name, ok := composeNameParam(c)
	if !ok {
		return
	}
	filters, err := parseComposeLogFilters(c)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	lines := make(chan composeLiveLog, 64)
	changes := make(chan struct{}, 1)
	publish := func(line composeLiveLog) {
		select {
		case <-ctx.Done():
			return
		case lines <- line:
		}
		select {
		case changes <- struct{}{}:
		default:
		}
	}

	first, interrupted := true, 0
	eventstream.Serve(c, m.auth, m.tasks.Done(), changes, func(s eventstream.Stream) bool {
		if first {
			first = false
			snapshotCtx, stop := context.WithTimeout(ctx, 40*time.Second)
			defer stop()
			containers, err := m.composeLogContainers(snapshotCtx, name, filters.service)
			if err != nil {
				s.Send("stream-error", "", response.Summary(err.Error()))
				return false
			}
			// 快照与实时读取用同一个时间边界，补回建立跟随连接前写入的日志。
			now := time.Now()
			stamp := func(t time.Time) string {
				return fmt.Sprintf("%d.%09d", t.Unix(), t.Nanosecond())
			}
			filters.params.Set("until", stamp(now))
			text, cut, unavailable, err := m.readComposeProjectLogs(snapshotCtx, containers, filters.params.Encode(), filters.keyword, filters.timestamps, 1<<20)
			if err != nil {
				s.Send("stream-error", "", response.Summary(err.Error()))
				return false
			}
			if !s.Send("snapshot", "", composeLogSnapshot{text, cut, unavailable}) {
				return false
			}
			filters.params.Del("until")
			start := now.Add(time.Nanosecond)
			if seconds, err := strconv.ParseInt(filters.params.Get("since"), 10, 64); err == nil && time.Unix(seconds, 0).After(start) {
				start = time.Unix(seconds, 0)
			}
			filters.params.Set("since", stamp(start))
			filters.params.Set("tail", "all")
			filters.params.Set("follow", "true")
			query := filters.params.Encode()
			for _, container := range containers {
				if container.State != "running" && container.State != "paused" {
					continue
				}
				go func() {
					if err := m.followComposeContainerLogs(ctx, container, query, filters, publish); err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
						publish(composeLiveLog{err: err})
					}
				}()
			}
			return true
		}
		batch := make([]composeLiveLog, 0, len(lines))
		truncated := false
		for count := len(lines); count > 0; count-- {
			line := <-lines
			if line.err != nil {
				interrupted++
				continue
			}
			batch = append(batch, line)
			truncated = truncated || line.truncated
		}
		slices.SortStableFunc(batch, func(a, b composeLiveLog) int { return a.time.Compare(b.time) })
		var text strings.Builder
		for _, line := range batch {
			if text.Len() != 0 {
				text.WriteByte('\n')
			}
			text.WriteString(line.text)
		}
		return (text.Len() == 0 && interrupted == 0) || s.Send("append", "", gin.H{
			"text": text.String(), "truncated": truncated, "interrupted": interrupted,
		})
	})
}

func (m *Module) followComposeContainerLogs(ctx context.Context, container composeContainer, query string, filters composeLogFilters, publish func(composeLiveLog)) error {
	res, err := m.call(ctx, "GET", "/containers/"+url.PathEscape(container.ID)+"/logs?"+query, nil, "")
	if err != nil {
		return err
	}
	defer res.Body.Close()

	// 旧版 Docker 的复用流和 TTY 文本使用同一个 Content-Type。
	source := bufio.NewReader(res.Body)
	var body io.Reader = source
	header, _ := source.Peek(8)
	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/vnd.docker.multiplexed-stream") ||
		(len(header) == 8 && header[0] >= 1 && header[0] <= 3 && header[1] == 0 && header[2] == 0 && header[3] == 0) {
		body = &composeLogFrames{source: source}
	}
	reader := bufio.NewReaderSize(body, 8<<10)
	prefix := composeLogPrefix(container)
	var at time.Time
	for {
		part, more, err := reader.ReadLine()
		if err != nil {
			return err
		}
		line, cut := strings.ToValidUTF8(string(part), "�"), more
		for more {
			_, more, err = reader.ReadLine()
			if err != nil {
				break
			}
		}
		if stamp, text, found := strings.Cut(line, " "); found {
			if parsed, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
				at = parsed
				if !filters.timestamps {
					line = text
				}
			}
		}
		line = prefix + line
		if filters.keyword == "" || strings.Contains(line, filters.keyword) {
			publish(composeLiveLog{composeLogLine: composeLogLine{at, line}, truncated: cut})
		}
		if err != nil {
			return err
		}
	}
}

// Docker 多路复用帧可横跨多次读取，无需为整帧分配内存。
type composeLogFrames struct {
	source    io.Reader
	remaining int64
}

func (f *composeLogFrames) Read(dst []byte) (int, error) {
	for f.remaining == 0 {
		var header [8]byte
		if _, err := io.ReadFull(f.source, header[:]); err != nil {
			return 0, err
		}
		if header[0] != 1 && header[0] != 2 {
			return 0, errors.New("Docker 日志流已中断")
		}
		f.remaining = int64(binary.BigEndian.Uint32(header[4:]))
	}
	n, err := f.source.Read(dst[:min(int64(len(dst)), f.remaining)])
	f.remaining -= int64(n)
	return n, err
}
