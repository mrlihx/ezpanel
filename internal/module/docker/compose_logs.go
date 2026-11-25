package docker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

const composeServiceLabel = "com.docker.compose.service"

var composeServiceName = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

type composeLogLine struct {
	time time.Time
	text string
}

type composeLogFilters struct {
	service    string
	keyword    string
	timestamps bool
	params     url.Values
}

func parseComposeLogFilters(c *gin.Context) (composeLogFilters, error) {
	tail, err := strconv.Atoi(c.DefaultQuery("tail", "100"))
	if err != nil || tail < 1 || tail > 1000 {
		return composeLogFilters{}, errors.New("日志行数须为 1–1000")
	}
	since, service, keyword := c.Query("since"), c.Query("service"), c.Query("keyword")
	if (service != "" && (len(service) > 128 || !composeServiceName.MatchString(service))) || len(keyword) > 256 || len(since) > 64 {
		return composeLogFilters{}, errors.New("日志筛选条件无效")
	}
	params := url.Values{"stdout": {"true"}, "stderr": {"true"}, "timestamps": {"true"}, "tail": {strconv.Itoa(tail)}}
	if since != "" {
		seconds, err := composeLogSince(since)
		if err != nil {
			return composeLogFilters{}, err
		}
		params.Set("since", seconds)
	}
	return composeLogFilters{service, keyword, c.Query("timestamps") == "true", params}, nil
}

func (m *Module) composeLogContainers(ctx context.Context, name, service string) ([]composeContainer, error) {
	labels := []string{composeProjectLabel + "=" + name}
	if service != "" {
		labels = append(labels, composeServiceLabel+"="+service)
	}
	filters, _ := json.Marshal(map[string][]string{"label": labels})
	query := url.Values{"all": {"true"}, "filters": {string(filters)}}
	var containers []composeContainer
	if err := m.json(ctx, "GET", "/containers/json?"+query.Encode(), nil, &containers); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(containers, func(item composeContainer) bool {
		return item.Labels[composeProjectLabel] != name || (service != "" && item.Labels[composeServiceLabel] != service)
	}), nil
}

type composeLogSnapshot struct {
	Text        string `json:"text"`
	Truncated   bool   `json:"truncated"`
	Unavailable int    `json:"unavailable"`
}

func (m *Module) composeProjectLogs(c *gin.Context) {
	name, ok := composeNameParam(c)
	if !ok {
		return
	}
	filters, err := parseComposeLogFilters(c)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	containers, err := m.composeLogContainers(ctx, name, filters.service)
	if err != nil {
		fail(c, err)
		return
	}
	download := c.Query("download") == "true"
	limit := 1 << 20
	if download {
		limit = 8 << 20
	}
	lines, truncated, unavailable, err := m.readComposeProjectLogs(ctx, containers, filters.params.Encode(), filters.keyword, filters.timestamps, limit)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	if download {
		filename := name
		if filters.service != "" {
			filename += "-" + filters.service
		}
		if truncated {
			lines += "\n[日志达到导出大小上限，已截断]"
		}
		if unavailable > 0 {
			lines += "\n[有 " + strconv.Itoa(unavailable) + " 个容器的日志无法读取]"
		}
		c.Header("Content-Disposition", `attachment; filename="`+filename+`.log"`)
		c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(lines))
		return
	}
	response.OK(c, composeLogSnapshot{lines, truncated, unavailable})
}

func composeLogSince(value string) (string, error) {
	if unix, err := strconv.ParseInt(value, 10, 64); err == nil && unix >= 0 {
		return strconv.FormatInt(unix, 10), nil
	}
	if at, err := time.Parse(time.RFC3339Nano, value); err == nil && at.Unix() >= 0 {
		return strconv.FormatInt(at.Unix(), 10), nil
	}
	if age, err := time.ParseDuration(value); err == nil && age >= 0 {
		if unix := time.Now().Add(-age).Unix(); unix >= 0 {
			return strconv.FormatInt(unix, 10), nil
		}
	}
	return "", errors.New("日志时间范围无效")
}

func (m *Module) readComposeProjectLogs(ctx context.Context, containers []composeContainer, query, keyword string, timestamps bool, limit int) (string, bool, int, error) {
	var lines []composeLogLine
	var readErr error
	unavailable := 0
	truncated := false
	// 所有副本共享读取上限，单个副本达到额度后仅合并已读日志。
	perContainer := limit / max(1, len(containers))
	for _, container := range containers {
		res, err := m.call(ctx, "GET", "/containers/"+url.PathEscape(container.ID)+"/logs?"+query, nil, "")
		if err != nil {
			unavailable++
			readErr = err
			continue
		}
		data, cut, err := decodeComposeLog(res.Body, perContainer)
		res.Body.Close()
		if err != nil {
			unavailable++
			readErr = err
			continue
		}
		truncated = truncated || cut
		prefix := composeLogPrefix(container)
		var at time.Time
		for line := range strings.SplitSeq(strings.TrimSuffix(data, "\n"), "\n") {
			if line == "" && data == "" {
				break
			}
			if stamp, text, found := strings.Cut(line, " "); found {
				if parsed, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
					at = parsed
					if !timestamps {
						line = text
					}
				}
			}
			line = prefix + strings.TrimSuffix(line, "\r")
			if len(line) > limit {
				truncated = true
				continue
			}
			if keyword == "" || strings.Contains(line, keyword) {
				lines = append(lines, composeLogLine{time: at, text: line})
			}
		}
	}
	if unavailable > 0 && unavailable == len(containers) {
		return "", false, unavailable, readErr
	}
	slices.SortStableFunc(lines, func(a, b composeLogLine) int { return a.time.Compare(b.time) })
	start, size := len(lines), 0
	for start > 0 {
		length := len(lines[start-1].text)
		if size > 0 {
			length++
		}
		if size+length > limit {
			truncated = true
			break
		}
		size += length
		start--
	}
	var text strings.Builder
	text.Grow(size)
	for _, line := range lines[start:] {
		if text.Len() > 0 {
			text.WriteByte('\n')
		}
		text.WriteString(line.text)
	}
	return text.String(), truncated, unavailable, nil
}

func composeLogPrefix(container composeContainer) string {
	if len(container.Names) > 0 {
		return strings.TrimPrefix(container.Names[0], "/") + " | "
	}
	return container.ID[:min(len(container.ID), 12)] + " | "
}

func decodeComposeLog(body io.Reader, limit int) (string, bool, error) {
	data, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
	}
	// 非 TTY 容器以 8 字节帧头区分 stdout 和 stderr。TTY 输出为文本。
	if len(data) >= 8 && (data[0] == 1 || data[0] == 2) && data[1] == 0 && data[2] == 0 && data[3] == 0 {
		plain := make([]byte, 0, len(data))
		for len(data) >= 8 {
			size := int(binary.BigEndian.Uint32(data[4:8]))
			data = data[8:]
			if size > len(data) {
				plain = append(plain, data...)
				truncated = true
				break
			}
			plain = append(plain, data[:size]...)
			data = data[size:]
		}
		data = plain
	}
	text := strings.ToValidUTF8(string(data), "�")
	if truncated {
		text = text[:strings.LastIndexByte(text, '\n')+1]
	}
	return text, truncated, nil
}
