package software

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v3/process"
)

func (m *Module) nginxRoutes(r *gin.RouterGroup) {
	r.GET("/nginx/load", m.nginxLoad)
	r.GET("/nginx/performance", m.nginxPerformance)
	r.PUT("/nginx/performance", m.saveNginxPerformance)
	r.GET("/nginx/error-log", m.nginxErrorLog)
}

func (m *Module) nginxUnit(ctx context.Context) (string, error) {
	state, err := m.packages.Status(ctx, "nginx")
	if err != nil {
		return "", err
	}
	if !state.Installed || !state.Manageable || state.Unit != "nginx.service" {
		return "", pkgmgr.ErrUnsupported
	}
	return state.Unit, nil
}

func (m *Module) nginxLoad(c *gin.Context) {
	ctx := c.Request.Context()
	unit, err := m.nginxUnit(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	bin, err := m.packages.LookPath("systemctl")
	if err != nil {
		fail(c, err)
		return
	}
	result, err := m.packages.Run(ctx, 15*time.Second, nil, bin, "show", unit, "--property=MainPID", "--value")
	if err != nil {
		fail(c, err)
		return
	}
	pid, err := strconv.ParseInt(strings.TrimSpace(result.Stdout), 10, 32)
	if err != nil {
		response.Fail(c, response.System, "无法读取 Nginx 主进程")
		return
	}
	out := gin.H{"pid": pid, "processes": 0, "residentBytes": nil, "cpuSeconds": nil, "uptimeSeconds": nil, "connections": nil, "listeners": nil,
		"notice": "显示 Nginx 主进程及其直接子进程。内存为 RSS 合计，包含共享页；CPU 为进程累计用时。连接为当前可读取的 TCP 已建立连接，含反向代理上游连接；监听按地址去重。未启用请求统计，不能据此推算请求数或 QPS。"}
	if pid == 0 {
		response.OK(c, out)
		return
	}
	master := &process.Process{Pid: int32(pid)}
	children, err := master.ChildrenWithContext(ctx)
	if err != nil {
		response.Fail(c, response.System, "无法读取 Nginx 子进程，请刷新重试")
		return
	}
	processes := append([]*process.Process{master}, children...)
	out["processes"] = len(processes)
	var memory uint64
	var cpu float64
	metricsOK, socketsOK := true, true
	connections, listeners := map[string]bool{}, map[string]bool{}
	for _, pr := range processes {
		mem, memErr := pr.MemoryInfoWithContext(ctx)
		times, cpuErr := pr.TimesWithContext(ctx)
		if memErr != nil || cpuErr != nil {
			metricsOK = false
		} else {
			memory += mem.RSS
			cpu += times.User + times.System
		}
		rows, socketErr := pr.ConnectionsWithContext(ctx)
		if socketErr != nil {
			socketsOK = false
			continue
		}
		for _, row := range rows {
			if row.Type != 1 {
				continue
			}
			key := fmt.Sprintf("%d/%s/%d/%s/%d", row.Family, row.Laddr.IP, row.Laddr.Port, row.Raddr.IP, row.Raddr.Port)
			if row.Status == "ESTABLISHED" {
				connections[key] = true
			}
			if row.Status == "LISTEN" {
				listeners[key] = true
			}
		}
	}
	if metricsOK {
		out["residentBytes"], out["cpuSeconds"] = memory, cpu
	}
	if socketsOK {
		out["connections"], out["listeners"] = len(connections), len(listeners)
	}
	if started, e := master.CreateTimeWithContext(ctx); e == nil {
		out["uptimeSeconds"] = max(int64(0), time.Now().Unix()-started/1000)
	}
	if !metricsOK || !socketsOK {
		out["notice"] = out["notice"].(string) + " 部分数据无法读取或进程刚发生变化，对应指标显示未知，请刷新。"
	}
	response.OK(c, out)
}

// 只识别 Nginx 语法边界并保留原文位置，更新不重排其余配置和注释。
type nginxDirective struct {
	name       string
	args       []string
	context    string
	start, end int
}
type nginxDocument struct {
	directives []nginxDirective
	blocks     map[string][]int
}

func parseNginx(content string) (nginxDocument, error) {
	doc := nginxDocument{blocks: map[string][]int{}}
	type token struct {
		value string
		start int
	}
	var statement []token
	var contexts []string
	for i := 0; i < len(content); {
		ch := content[i]
		if ch == ' ' || ch == '\n' || ch == '\r' || ch == '\t' {
			i++
			continue
		}
		if ch == '#' {
			for i < len(content) && content[i] != '\n' {
				i++
			}
			continue
		}
		if ch == ';' {
			if len(statement) == 0 {
				return doc, errors.New("配置含空指令，请在配置文件中调整")
			}
			args := make([]string, 0, len(statement)-1)
			for _, item := range statement[1:] {
				args = append(args, item.value)
			}
			doc.directives = append(doc.directives, nginxDirective{statement[0].value, args, strings.Join(contexts, "/"), statement[0].start, i + 1})
			statement = nil
			i++
			continue
		}
		if ch == '{' {
			if len(statement) == 0 {
				return doc, errors.New("无法识别配置块，请在配置文件中调整")
			}
			contexts = append(contexts, statement[0].value)
			statement = nil
			i++
			continue
		}
		if ch == '}' {
			if len(contexts) == 0 || len(statement) > 0 {
				return doc, errors.New("配置块不完整，请在配置文件中调整")
			}
			key := strings.Join(contexts, "/")
			doc.blocks[key] = append(doc.blocks[key], i)
			contexts = contexts[:len(contexts)-1]
			i++
			continue
		}
		start := i
		var value strings.Builder
		var quote byte
		for i < len(content) {
			ch = content[i]
			if ch == '\\' {
				if i+1 == len(content) {
					return doc, errors.New("配置转义不完整")
				}
				value.WriteByte(content[i+1])
				i += 2
				continue
			}
			if quote != 0 {
				if ch == quote {
					quote = 0
				} else {
					value.WriteByte(ch)
				}
				i++
				continue
			}
			if ch == '\'' || ch == '"' {
				quote = ch
				i++
				continue
			}
			if strings.ContainsRune(" \t\r\n;{}#", rune(ch)) {
				break
			}
			value.WriteByte(ch)
			i++
		}
		if quote != 0 {
			return doc, errors.New("配置引号不完整")
		}
		statement = append(statement, token{value.String(), start})
	}
	if len(contexts) > 0 || len(statement) > 0 {
		return doc, errors.New("配置不完整，请在配置文件中调整")
	}
	return doc, nil
}

var nginxPerformanceContexts = map[string]string{"worker_processes": "", "worker_connections": "events", "keepalive_timeout": "http", "client_max_body_size": "http", "gzip": "http", "sendfile": "http"}
var nginxDuration = regexp.MustCompile(`^[0-9]+[smhd]?( [0-9]+[smhd]?)?$`)
var nginxBodySize = regexp.MustCompile(`^[0-9]+[kKmMgG]?$`)

func (m *Module) nginxPerformance(c *gin.Context) {
	settings, err := m.packages.Settings(c.Request.Context(), "nginx")
	if err != nil {
		fail(c, err)
		return
	}
	doc, err := parseNginx(settings.Content)
	if err != nil {
		response.Fail(c, response.System, err.Error())
		return
	}
	values := map[string]string{}
	for _, directive := range doc.directives {
		context, ok := nginxPerformanceContexts[directive.name]
		if !ok || directive.context != context {
			continue
		}
		if _, exists := values[directive.name]; exists {
			response.Fail(c, response.System, "主配置含重复性能指令，请先在配置文件中调整")
			return
		}
		values[directive.name] = strings.Join(directive.args, " ")
	}
	response.OK(c, gin.H{"values": values, "revision": settings.Revision, "path": settings.Path})
}

func (m *Module) saveNginxPerformance(c *gin.Context) {
	var in struct {
		Values   map[string]string `json:"values"`
		Revision string            `json:"revision"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Revision) != 64 || len(in.Values) == 0 || len(in.Values) > len(nginxPerformanceContexts) {
		response.Fail(c, response.Invalid, "性能配置或修订号无效")
		return
	}
	for name, value := range in.Values {
		if _, ok := nginxPerformanceContexts[name]; !ok {
			response.Fail(c, response.Invalid, "包含不支持的性能指令")
			return
		}
		valid := value == ""
		switch name {
		case "worker_processes":
			n, err := strconv.Atoi(value)
			valid = valid || value == "auto" || err == nil && n >= 1 && n <= 1024
		case "worker_connections":
			n, err := strconv.Atoi(value)
			valid = valid || err == nil && n >= 1 && n <= 1048576
		case "keepalive_timeout":
			valid = valid || len(value) <= 32 && nginxDuration.MatchString(value)
		case "client_max_body_size":
			valid = valid || len(value) <= 16 && nginxBodySize.MatchString(value)
		case "gzip", "sendfile":
			valid = valid || value == "on" || value == "off"
		}
		if !valid {
			response.Fail(c, response.Invalid, name+" 的值无效，请按字段提示填写")
			return
		}
	}
	settings, err := m.packages.Settings(c.Request.Context(), "nginx")
	if err != nil {
		fail(c, err)
		return
	}
	if settings.Revision != in.Revision {
		response.Fail(c, response.Conflict, "配置已变更，请重新加载后保存")
		return
	}
	doc, err := parseNginx(settings.Content)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	additions := map[string][]string{}
	for name, value := range in.Values {
		context := nginxPerformanceContexts[name]
		count := 0
		for _, directive := range doc.directives {
			if directive.name != name || directive.context != context {
				continue
			}
			count++
			text := ""
			if value != "" {
				text = name + " " + value + ";"
			}
			edits = append(edits, edit{directive.start, directive.end, text})
		}
		if count > 1 {
			response.Fail(c, response.Invalid, "主配置含重复性能指令，请先在配置文件中调整")
			return
		}
		if count == 0 && value != "" {
			additions[context] = append(additions[context], name+" "+value+";")
		}
	}
	for context, lines := range additions {
		sort.Strings(lines)
		if context == "" {
			edits = append(edits, edit{0, 0, strings.Join(lines, "\n") + "\n"})
			continue
		}
		if len(doc.blocks[context]) != 1 {
			response.Fail(c, response.Invalid, "主配置需直接包含唯一 "+context+" 块，请在配置文件中调整")
			return
		}
		position := doc.blocks[context][0]
		edits = append(edits, edit{position, position, "\n    " + strings.Join(lines, "\n    ") + "\n"})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	content := settings.Content
	for _, edit := range edits {
		content = content[:edit.start] + edit.text + content[edit.end:]
	}
	// SaveSettings 自带应用互斥、修订号校验、备份和 nginx -t；这里不能嵌套 NginxChange。
	if err = m.packages.SaveSettings(c.Request.Context(), "nginx", content, in.Revision); err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"saved": true})
}

func (m *Module) nginxErrorLog(c *gin.Context) {
	ctx := c.Request.Context()
	settings, err := m.packages.Settings(ctx, "nginx")
	if err != nil {
		fail(c, err)
		return
	}
	doc, err := parseNginx(settings.Content)
	if err != nil {
		response.Fail(c, response.System, err.Error())
		return
	}
	var paths []string
	for _, directive := range doc.directives {
		if directive.name == "error_log" && directive.context == "" && len(directive.args) > 0 {
			paths = append(paths, directive.args[0])
		}
	}
	notice := "显示主配置顶层的错误日志；站点或 http 块自定义日志请在文件管理中查看。"
	if len(paths) == 0 {
		bin, err := m.packages.LookPath("nginx")
		if err != nil {
			fail(c, err)
			return
		}
		result, err := m.packages.Run(ctx, 15*time.Second, nil, bin, "-V")
		if err != nil {
			fail(c, err)
			return
		}
		// 没有显式 error_log 时使用当前二进制的编译默认路径，不猜测日志文件。
		re := regexp.MustCompile(`--error-log-path=(?:'([^']*)'|"([^"]*)"|([^\s]+))`)
		match := re.FindStringSubmatch(result.Stderr + " " + result.Stdout)
		if len(match) > 0 {
			for _, value := range match[1:] {
				if value != "" {
					paths = append(paths, value)
					break
				}
			}
		}
		notice += " 主配置未显式设置顶层 error_log，当前显示二进制编译默认路径；include 可能覆盖日志目标。"
		if len(paths) == 0 {
			response.OK(c, gin.H{"files": []any{}, "notice": notice + " 当前二进制未提供绝对默认日志路径，请在主配置中设置 error_log。"})
			return
		}
	}
	files := []gin.H{}
	for _, path := range paths {
		if !filepath.IsAbs(path) || strings.Contains(path, "$") || strings.HasPrefix(path, "/dev/") || strings.HasPrefix(path, "/proc/") || strings.HasPrefix(path, "/sys/") {
			files = append(files, gin.H{"path": path, "content": "", "notice": "该目标不是可读取的绝对日志文件路径；stderr、syslog 或相对路径请在终端查看。"})
			continue
		}
		file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			if os.IsNotExist(err) {
				files = append(files, gin.H{"path": path, "content": "", "notice": "日志文件尚不存在。"})
				continue
			}
			response.Fail(c, response.System, "无法读取 Nginx 错误日志")
			return
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			file.Close()
			response.Fail(c, response.Invalid, "错误日志目标不是普通文件")
			return
		}
		start := max(int64(0), info.Size()-(128<<10))
		_, err = file.Seek(start, io.SeekStart)
		if err != nil {
			file.Close()
			response.Fail(c, response.System, "无法定位 Nginx 错误日志")
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, 128<<10))
		file.Close()
		if err != nil {
			response.Fail(c, response.System, "无法读取 Nginx 错误日志")
			return
		}
		text := string(data)
		if start > 0 {
			if _, rest, ok := strings.Cut(text, "\n"); ok {
				text = rest
			}
		}
		lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		if len(lines) > 200 {
			lines = lines[len(lines)-200:]
		}
		files = append(files, gin.H{"path": path, "content": strings.Join(lines, "\n"), "notice": "最近 200 行，最多读取文件尾部 128 KiB。"})
	}
	response.OK(c, gin.H{"files": files, "notice": notice})
}
