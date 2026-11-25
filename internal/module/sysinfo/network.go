package sysinfo

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v3/process"
)

type NetworkProcess struct {
	PID             int32    `json:"pid"`
	Started         int64    `json:"started"`
	Name            string   `json:"name"`
	ListenIPs       []string `json:"listenIPs"`
	Ports           []string `json:"ports"`
	IPCount         int      `json:"ipCount"`
	ConnectionCount int      `json:"connectionCount"`
	Upload          *float64 `json:"upload"`
	Download        *float64 `json:"download"`
	RateNote        string   `json:"rateNote"`
}

type NetworkConnection struct {
	Protocol   string   `json:"protocol"`
	LocalIP    string   `json:"localIP"`
	LocalPort  uint16   `json:"localPort"`
	RemoteIP   string   `json:"remoteIP"`
	RemotePort uint16   `json:"remotePort"`
	State      string   `json:"state"`
	Upload     *float64 `json:"upload"`
	Download   *float64 `json:"download"`
}

type networkCounter struct {
	acked, received uint64
}

type networkSnapshot struct {
	at          time.Time
	list        []NetworkProcess
	connections map[int32][]NetworkConnection
	counters    map[string]networkCounter
	notice      string
}

func (m *Module) networkSnapshot(ctx context.Context) (networkSnapshot, error) {
	// 详情采样独立于 CPU/WS，所有网络请求共用两秒缓存。
	m.networkMu.Lock()
	defer m.networkMu.Unlock()
	if time.Since(m.networkAttempt) < 2*time.Second {
		return m.networkCache, m.networkError
	}
	m.networkAttempt = time.Now()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	next, err := collectNetwork(ctx, m.networkCache)
	m.networkError = err
	if err == nil {
		m.networkCache = next
	}
	return m.networkCache, err
}

func (m *Module) networkList(c *gin.Context) {
	page, size, ok := response.Page(c, 200, 20)
	if !ok {
		return
	}
	snapshot, err := m.networkSnapshot(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, networkRows(snapshot, c.Query("keyword"), page, size))
}

func networkRows(snapshot networkSnapshot, keyword string, page, size int) gin.H {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	list := make([]NetworkProcess, 0, len(snapshot.list))
	for _, item := range snapshot.list {
		matched := keyword == "" || strings.Contains(strings.ToLower(item.Name), keyword) || strings.Contains(strconv.Itoa(int(item.PID)), keyword) || strings.Contains(strings.ToLower(strings.Join(item.Ports, " ")), keyword)
		if !matched {
			for _, connection := range snapshot.connections[item.PID] {
				if strings.Contains(strings.ToLower(connection.LocalIP+" "+connection.RemoteIP), keyword) || (connection.RemotePort > 0 && strings.Contains(strconv.Itoa(int(connection.RemotePort)), keyword)) {
					matched = true
					break
				}
			}
		}
		if matched {
			list = append(list, item)
		}
	}
	total := len(list)
	start, end := networkPage(total, page, size)
	return gin.H{"list": list[start:end], "total": total, "sampledAt": snapshot.at, "notice": snapshot.notice}
}

func (m *Module) networkDetail(c *gin.Context) {
	pid, ok := processPID(c)
	if !ok {
		return
	}
	started, err := strconv.ParseInt(c.Query("started"), 10, 64)
	if err != nil || started <= 0 {
		response.Fail(c, response.Invalid, "须提供列表中的有效创建时间")
		return
	}
	page, size, ok := response.Page(c, 200, 50)
	if !ok {
		return
	}
	snapshot, err := m.networkSnapshot(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, err.Error())
		return
	}
	detail, err := networkConnectionRows(c.Request.Context(), snapshot, pid, started, page, size)
	if err != nil {
		code := response.NotFound
		if errors.Is(err, errNetworkProcessChanged) {
			code = response.Conflict
		}
		response.Fail(c, code, err.Error())
		return
	}
	response.OK(c, detail)
}

var errNetworkProcessChanged = errors.New("进程已退出或身份已变化，请刷新列表")

func networkConnectionRows(ctx context.Context, snapshot networkSnapshot, pid int32, started int64, page, size int) (gin.H, error) {
	// 使用新对象校验当前身份，缓存不能掩盖进程退出或 PID 重用。
	pr := &process.Process{Pid: pid}
	current, err := pr.CreateTimeWithContext(ctx)
	if err != nil || current != started {
		return nil, errNetworkProcessChanged
	}
	for _, item := range snapshot.list {
		if item.PID != pid || item.Started != started {
			continue
		}
		connections := snapshot.connections[pid]
		start, end := networkPage(len(connections), page, size)
		return gin.H{"process": item, "connections": connections[start:end], "total": len(connections), "sampledAt": snapshot.at, "notice": snapshot.notice}, nil
	}
	return nil, errors.New("该进程暂无可读取的网络连接，请刷新列表")
}

func networkPage(total, page, size int) (int, int) {
	if page > total/size+1 {
		return total, total
	}
	start := (page - 1) * size
	end := start + min(size, total-start)
	return start, end
}
