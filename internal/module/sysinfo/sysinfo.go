package sysinfo

import (
	"context"
	"encoding/json"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/version"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	netstat "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
	"gorm.io/gorm"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CPU struct {
	Model         string             `json:"model"`
	Cores         *int               `json:"cores"`
	PhysicalCores *int               `json:"physicalCores"`
	FrequencyMHz  float64            `json:"frequencyMHz"`
	Usage         *float64           `json:"usage"`
	PerCore       []CoreUsage        `json:"perCore"`
	Breakdown     map[string]float64 `json:"breakdown"`
}
type CoreUsage struct {
	Name  string  `json:"name"`
	Usage float64 `json:"usage"`
}
type Load struct {
	L1  float64 `json:"l1"`
	L5  float64 `json:"l5"`
	L15 float64 `json:"l15"`
}
type Memory struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	Available   uint64  `json:"available"`
	Shared      uint64  `json:"shared"`
	Cached      uint64  `json:"cached"`
	UsedPercent float64 `json:"usedPercent"`
	SwapTotal   *uint64 `json:"swapTotal"`
	SwapUsed    *uint64 `json:"swapUsed"`
}
type Disk struct {
	Mount             string  `json:"mount"`
	Device            string  `json:"device"`
	FSType            string  `json:"fstype"`
	Total             uint64  `json:"total"`
	Used              uint64  `json:"used"`
	Free              uint64  `json:"free"`
	UsedPercent       float64 `json:"usedPercent"`
	InodesTotal       uint64  `json:"inodesTotal"`
	InodesUsed        uint64  `json:"inodesUsed"`
	InodesFree        uint64  `json:"inodesFree"`
	InodesUsedPercent float64 `json:"inodesUsedPercent"`
}
type Overview struct {
	Hostname        string     `json:"hostname"`
	OS              string     `json:"os"`
	Platform        string     `json:"platform"`
	PlatformName    string     `json:"platformName"`
	PlatformVersion string     `json:"platformVersion"`
	Release         string     `json:"release"`
	Kernel          string     `json:"kernel"`
	Arch            string     `json:"arch"`
	Uptime          *uint64    `json:"uptime"`
	BootTime        *time.Time `json:"bootTime"`
	IPs             []string   `json:"ips"`
	PanelVersion    string     `json:"panelVersion"`
	CPU             CPU        `json:"cpu"`
	Load            *Load      `json:"load"`
	Mem             *Memory    `json:"mem"`
	Disks           []Disk     `json:"disks"`
}
type Network struct {
	Name string   `json:"name"`
	Up   *float64 `json:"up"`
	Down *float64 `json:"down"`
	Sent uint64   `json:"sent"`
	Recv uint64   `json:"recv"`
}
type DiskIO struct {
	ReadLatency  *float64 `json:"readLatency"`
	WriteLatency *float64 `json:"writeLatency"`
	Name         string   `json:"name"`
	Read         *float64 `json:"read"`
	Write        *float64 `json:"write"`
	ReadOps      *float64 `json:"readOps"`
	WriteOps     *float64 `json:"writeOps"`
	Latency      *float64 `json:"latency"`
}
type Point struct {
	DiskReadLatency  *float64  `json:"diskReadLatency"`
	DiskWriteLatency *float64  `json:"diskWriteLatency"`
	Disks            []DiskIO  `json:"diskIO"`
	DiskReadOps      *float64  `json:"diskReadOps"`
	DiskWriteOps     *float64  `json:"diskWriteOps"`
	DiskLatency      *float64  `json:"diskLatency"`
	Networks         []Network `json:"networks"`
	NetSent          *uint64   `json:"netSent"`
	NetRecv          *uint64   `json:"netRecv"`
	TS               time.Time `json:"ts"`
	CPU              *float64  `json:"cpu"`
	MemPercent       *float64  `json:"memPercent"`
	Load1            *float64  `json:"load1"`
	NetUp            *float64  `json:"netUp"`
	NetDown          *float64  `json:"netDown"`
	DiskRead         *float64  `json:"diskRead"`
	DiskWrite        *float64  `json:"diskWrite"`
}
type Process struct {
	PPID            int32    `json:"ppid"`
	Started         int64    `json:"started"`
	MemoryBytes     *uint64  `json:"memoryBytes"`
	Protected       bool     `json:"protected"`
	ProtectedReason string   `json:"protectedReason"`
	PID             int32    `json:"pid"`
	Name            string   `json:"name"`
	User            string   `json:"user"`
	CPU             *float64 `json:"cpu"`
	Mem             *float64 `json:"mem"`
	Status          []string `json:"status"`
}
type procSample struct {
	started int64
	seconds float64
}
type counters struct {
	at    time.Time
	cpu   cpu.TimesStat
	cores map[string]cpu.TimesStat
	net   map[string]netstat.IOCountersStat
	io    map[string]disk.IOCountersStat
	procs map[int32]procSample
}
type Module struct {
	db             *gorm.DB
	countDatabases func(context.Context) (int64, error)
	mu             sync.RWMutex
	overview       Overview
	point          Point
	history        []Point
	processes      map[string][]Process
	clients        map[chan Point]struct{}
	previous       counters
	auth           *auth.Service
	ctx            context.Context
	cancel         context.CancelFunc
	done           chan struct{}
	networkMu      sync.Mutex
	networkCache   networkSnapshot
	networkAttempt time.Time
	networkError   error
	panelMeta      panelMetaCache
}

func New(ctx context.Context, a *auth.Service) *Module {
	ctx, cancel := context.WithCancel(ctx)
	m := &Module{auth: a, ctx: ctx, cancel: cancel, done: make(chan struct{}), clients: make(map[chan Point]struct{})}
	m.sample(ctx)
	m.startPanelMetaLoop(ctx)
	go func() {
		defer close(m.done)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				m.sample(ctx)
			}
		}
	}()
	return m
}
func (m *Module) Close()       { m.cancel(); <-m.done }
func (m *Module) Name() string { return "sysinfo" }
func rate(before, after uint64, seconds float64) *float64 {
	if seconds <= 0 || after < before {
		return nil
	}
	v := float64(after-before) / seconds
	return &v
}
func parseQuery(raw string) (string, int, error) {
	q, e := url.ParseQuery(raw)
	if e != nil {
		return "", 0, e
	}
	s := q.Get("sort")
	if s == "" {
		s = "cpu"
	}
	n := 10
	if q.Has("limit") {
		n, e = strconv.Atoi(q.Get("limit"))
	}
	if e != nil || (s != "cpu" && s != "mem") || n < 1 || n > 100 {
		return "", 0, errors.New("invalid query")
	}
	return s, n, nil
}
func (m *Module) Routes(g *gin.RouterGroup) {
	g.GET("/api/sysinfo/summary", m.summary)
	g.GET("/api/sysinfo/history", m.HistoryPoints)
	g.GET("/api/sysinfo/network", m.networkList)
	g.GET("/api/sysinfo/network/:pid", m.networkDetail)
	g.GET("/api/sysinfo/processes/:pid", m.processDetail)
	g.POST("/api/sysinfo/processes/:pid/terminate", m.terminate)
	g.POST("/api/sysinfo/restart", m.restart)
	g.GET("/api/sysinfo/panel-status", m.panelStatus)
	g.POST("/api/sysinfo/panel-update/check", m.panelUpdateCheck)
	g.POST("/api/sysinfo/panel-update/apply", m.panelUpdateApply)
	g.DELETE("/api/sysinfo/panel-update/error", m.dismissPanelUpdateError)
	g.GET("/api/sysinfo/overview", func(c *gin.Context) { m.mu.RLock(); v := m.overview; m.mu.RUnlock(); response.OK(c, v) })
	g.GET("/api/sysinfo/processes", func(c *gin.Context) {
		s, n, e := parseQuery(c.Request.URL.RawQuery)
		if e != nil {
			response.Fail(c, response.Invalid, "排序或数量无效")
			return
		}
		m.mu.RLock()
		rows := m.processes[s]
		m.mu.RUnlock()
		response.OK(c, filterProcesses(rows, c.Query("keyword"), n))
	})
	g.GET("/ws/sysinfo", m.socket)
}
func filterProcesses(rows []Process, keyword string, limit int) []Process {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		if rows == nil {
			return []Process{}
		}
		return rows[:min(len(rows), limit)]
	}
	found := make([]Process, 0, min(len(rows), limit))
	for _, item := range rows {
		if strings.Contains(strings.ToLower(item.Name), keyword) ||
			strings.Contains(strings.ToLower(item.User), keyword) ||
			strings.Contains(strconv.Itoa(int(item.PID)), keyword) {
			found = append(found, item)
			if len(found) == limit {
				break
			}
		}
	}
	return found
}

func (m *Module) socket(c *gin.Context) {
	session := auth.Current(c)
	// 来源已由 server 的 SourceGuard 按配置验证。
	conn, e := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(c.Writer, c.Request, nil)
	if e != nil {
		return
	}
	defer conn.Close()
	dashboard := c.Query("view") == "dashboard"
	conn.SetReadLimit(4096)
	updates := make(chan dashboardSubscription, 1)
	streamCtx, stopStream := context.WithCancel(c.Request.Context())
	defer stopStream()
	ch := make(chan Point, 1)
	m.mu.Lock()
	m.clients[ch] = struct{}{}
	if dashboard || c.Query("fresh") != "1" {
		ch <- m.point
	}
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.clients, ch); m.mu.Unlock() }()
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if dashboard {
				var subscription dashboardSubscription
				if json.Unmarshal(data, &subscription) != nil || !subscription.valid() {
					continue
				}
				select {
				case <-updates:
				default:
				}
				updates <- subscription
			}
		}
	}()
	var subscription dashboardSubscription
	var lastOverview, lastNetwork time.Time
	networkResults := make(chan dashboardNetwork, 1)
	networkBusy := false
	tick := time.NewTicker(auth.SessionRecheckInterval)
	defer tick.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-closed:
			return
		case <-tick.C:
			if _, e := m.auth.CheckSession(session.Token); e != nil {
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "session expired"), time.Now().Add(time.Second))
				return
			}
		case subscription = <-updates:
			lastOverview, lastNetwork = time.Time{}, time.Time{}
		case result := <-networkResults:
			networkBusy = false
			if subscription.Network == nil || result.Filter != *subscription.Network {
				continue
			}
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if conn.WriteJSON(dashboardFrame{Network: &result}) != nil {
				return
			}
		case p := <-ch:
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if !dashboard {
				if conn.WriteJSON(p) != nil {
					return
				}
				continue
			}
			frame := dashboardFrame{Point: &p}
			if p.TS.Sub(lastOverview) >= dashboardDetailsInterval {
				m.populateDashboard(&frame, subscription.Processes)
				lastOverview = p.TS
			}
			if subscription.Network != nil && !networkBusy && p.TS.Sub(lastNetwork) >= dashboardDetailsInterval {
				networkBusy = true
				lastNetwork = p.TS
				go func(filter dashboardNetworkFilter) {
					networkResults <- m.dashboardNetwork(streamCtx, filter)
				}(*subscription.Network)
			}
			if conn.WriteJSON(frame) != nil {
				return
			}
		}
	}
}

// Linux 的 guest 时间已计入 user 和 nice，汇总时不再重复相加。
func cpuTotal(t cpu.TimesStat) float64 {
	return t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
}

func (m *Module) sample(ctx context.Context) {
	now := time.Now()
	prev := m.previous
	elapsed := 0.0
	if !prev.at.IsZero() {
		elapsed = now.Sub(prev.at).Seconds()
	}
	next := counters{at: now, net: map[string]netstat.IOCountersStat{}, io: map[string]disk.IOCountersStat{}, procs: map[int32]procSample{}, cores: map[string]cpu.TimesStat{}}
	o := Overview{Arch: runtime.GOARCH, PanelVersion: version.Version, IPs: []string{}, Disks: []Disk{}}
	p := Point{TS: now, Networks: []Network{}}
	if h, e := host.InfoWithContext(ctx); e == nil {
		o.Hostname = h.Hostname
		o.OS = h.OS
		o.Platform = h.Platform
		o.PlatformVersion = h.PlatformVersion
		o.Kernel = h.KernelVersion
		o.Uptime = &h.Uptime
		boot := time.Unix(int64(h.BootTime), 0).UTC()
		o.BootTime = &boot
	}
	if release, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(release), "\n") {
			key, value, _ := strings.Cut(line, "=")
			switch key {
			case "NAME":
				o.PlatformName = strings.Trim(value, "\"'")
			case "VERSION_ID":
				o.PlatformVersion = strings.Trim(value, "\"'")
			case "PRETTY_NAME":
				o.Release = strings.Trim(value, "\"'")
			}
		}
	}
	if infos, e := cpu.InfoWithContext(ctx); e == nil && len(infos) > 0 {
		o.CPU.Model = infos[0].ModelName
		o.CPU.FrequencyMHz = infos[0].Mhz
	}
	if cores, e := cpu.CountsWithContext(ctx, true); e == nil && cores > 0 {
		o.CPU.Cores = &cores
	}
	if cores, e := cpu.CountsWithContext(ctx, false); e == nil && cores > 0 {
		o.CPU.PhysicalCores = &cores
	}
	if times, e := cpu.TimesWithContext(ctx, false); e == nil && len(times) > 0 {
		t := times[0]
		next.cpu = t
		previousTotal := cpuTotal(prev.cpu)
		total, idle := cpuTotal(t)-previousTotal, t.Idle-prev.cpu.Idle
		wait := t.Iowait - prev.cpu.Iowait
		if previousTotal > 0 && total > 0 && idle+wait >= 0 && idle+wait <= total {
			scale := 100 / total
			v := (total - idle - wait) * scale
			p.CPU = &v
			o.CPU.Usage = &v
			o.CPU.Breakdown = map[string]float64{
				"user": (t.User - prev.cpu.User) * scale, "system": (t.System - prev.cpu.System) * scale,
				"idle": idle * scale, "nice": (t.Nice - prev.cpu.Nice) * scale,
				"iowait": wait * scale, "irq": (t.Irq - prev.cpu.Irq) * scale,
				"softirq": (t.Softirq - prev.cpu.Softirq) * scale, "steal": (t.Steal - prev.cpu.Steal) * scale,
			}
		}
	}
	if times, e := cpu.TimesWithContext(ctx, true); e == nil {
		for _, t := range times {
			next.cores[t.CPU] = t
			if previous, ok := prev.cores[t.CPU]; ok {
				total := cpuTotal(t) - cpuTotal(previous)
				idle := t.Idle + t.Iowait - previous.Idle - previous.Iowait
				if total > 0 && idle >= 0 && idle <= total {
					o.CPU.PerCore = append(o.CPU.PerCore, CoreUsage{Name: t.CPU, Usage: (total - idle) / total * 100})
				}
			}
		}
	}
	if v, e := load.AvgWithContext(ctx); e == nil {
		o.Load = &Load{v.Load1, v.Load5, v.Load15}
		p.Load1 = &v.Load1
	}
	if v, e := mem.VirtualMemoryWithContext(ctx); e == nil {
		o.Mem = &Memory{Total: v.Total, Used: v.Used, Free: v.Free, Available: v.Available, Shared: v.Shared, Cached: v.Cached, UsedPercent: v.UsedPercent}
		p.MemPercent = &v.UsedPercent
		if s, e := mem.SwapMemoryWithContext(ctx); e == nil {
			o.Mem.SwapTotal = &s.Total
			o.Mem.SwapUsed = &s.Used
		}
	}
	if parts, e := disk.PartitionsWithContext(ctx, false); e == nil {
		for _, part := range parts {
			if v, e := disk.UsageWithContext(ctx, part.Mountpoint); e == nil {
				o.Disks = append(o.Disks, Disk{
					Mount: part.Mountpoint, Device: part.Device, FSType: part.Fstype,
					Total: v.Total, Used: v.Used, Free: v.Free, UsedPercent: v.UsedPercent,
					InodesTotal: v.InodesTotal, InodesUsed: v.InodesUsed, InodesFree: v.InodesFree, InodesUsedPercent: v.InodesUsedPercent,
				})
			}
		}
	}
	interfaces, _ := netstat.InterfacesWithContext(ctx)
	m.panelMeta.mu.RLock()
	publicIP := m.panelMeta.publicIP
	m.panelMeta.mu.RUnlock()
	o.IPs = serverIPAddresses(interfaces, publicIP, "/sys/class/net")
	if stats, e := netstat.IOCountersWithContext(ctx, true); e == nil {
		var up, down float64
		var sent, recv uint64
		valid := elapsed > 0
		for _, v := range stats {
			next.net[v.Name] = v
			network := Network{Name: v.Name, Sent: v.BytesSent, Recv: v.BytesRecv}
			sent += v.BytesSent
			recv += v.BytesRecv
			if b, ok := prev.net[v.Name]; ok {
				network.Up = rate(b.BytesSent, v.BytesSent, elapsed)
				network.Down = rate(b.BytesRecv, v.BytesRecv, elapsed)
			}
			if network.Up == nil || network.Down == nil {
				valid = false
			} else {
				up += *network.Up
				down += *network.Down
			}
			p.Networks = append(p.Networks, network)
		}
		sort.Slice(p.Networks, func(i, j int) bool { return p.Networks[i].Name < p.Networks[j].Name })
		p.NetSent, p.NetRecv = &sent, &recv
		if valid && len(stats) > 0 {
			p.NetUp, p.NetDown = &up, &down
		}
	}
	if stats, e := disk.IOCountersWithContext(ctx); e == nil {
		// Linux diskstats 同时含整盘、分区和映射设备，只汇总物理整盘以免重复。
		if runtime.GOOS == "linux" {
			stats = physicalDisks(stats, "/sys/block")
		}
		next.io = stats

		var read, write, readOps, writeOps float64
		var operations, wait, readCount, writeCount, readWait, writeWait uint64
		valid := elapsed > 0
		p.Disks = []DiskIO{}
		for name, v := range stats {
			item := DiskIO{Name: name}
			if old, ok := prev.io[name]; ok {
				item.Read = rate(old.ReadBytes, v.ReadBytes, elapsed)
				item.Write = rate(old.WriteBytes, v.WriteBytes, elapsed)
				item.ReadOps = rate(old.ReadCount, v.ReadCount, elapsed)
				item.WriteOps = rate(old.WriteCount, v.WriteCount, elapsed)
				if item.Read != nil && item.Write != nil && item.ReadOps != nil && item.WriteOps != nil && v.ReadTime >= old.ReadTime && v.WriteTime >= old.WriteTime {
					count := v.ReadCount - old.ReadCount + v.WriteCount - old.WriteCount
					duration := v.ReadTime - old.ReadTime + v.WriteTime - old.WriteTime
					latency := float64(0)
					if count > 0 {
						latency = float64(duration) / float64(count)
					}
					item.Latency = &latency
					rlat, wlat := float64(0), float64(0)
					reads, writes := v.ReadCount-old.ReadCount, v.WriteCount-old.WriteCount
					if reads > 0 {
						rlat = float64(v.ReadTime-old.ReadTime) / float64(reads)
					}
					if writes > 0 {
						wlat = float64(v.WriteTime-old.WriteTime) / float64(writes)
					}
					item.ReadLatency = &rlat
					item.WriteLatency = &wlat
					readCount += reads
					writeCount += writes
					readWait += v.ReadTime - old.ReadTime
					writeWait += v.WriteTime - old.WriteTime
					read += *item.Read
					write += *item.Write
					readOps += *item.ReadOps
					writeOps += *item.WriteOps
					operations += count
					wait += duration
				} else {
					valid = false
				}
			} else {
				valid = false
			}
			p.Disks = append(p.Disks, item)
		}
		sort.Slice(p.Disks, func(i, j int) bool { return p.Disks[i].Name < p.Disks[j].Name })
		if valid && len(stats) > 0 {
			p.DiskRead = &read
			p.DiskWrite = &write
			p.DiskReadOps = &readOps
			p.DiskWriteOps = &writeOps
			latency := float64(0)
			if operations > 0 {
				latency = float64(wait) / float64(operations)
			}
			p.DiskLatency = &latency
			rlat, wlat := float64(0), float64(0)
			if readCount > 0 {
				rlat = float64(readWait) / float64(readCount)
			}
			if writeCount > 0 {
				wlat = float64(writeWait) / float64(writeCount)
			}
			p.DiskReadLatency = &rlat
			p.DiskWriteLatency = &wlat
		}
	}
	processes := []Process{}
	identities := map[int32]processIdentity{}
	if list, e := process.ProcessesWithContext(ctx); e == nil {
		for _, pr := range list {
			identity := readIdentity(ctx, pr)
			identities[pr.Pid] = identity
			item := identity.Process
			item.User, _ = pr.UsernameWithContext(ctx)
			item.Status, _ = pr.StatusWithContext(ctx)
			if v, e := pr.MemoryInfoWithContext(ctx); e == nil {
				item.MemoryBytes = &v.RSS
				if o.Mem != nil && o.Mem.Total > 0 {
					percent := float64(v.RSS) / float64(o.Mem.Total) * 100
					item.Mem = &percent
				}
			}
			started := item.Started
			if started > 0 {
				if t, e := pr.TimesWithContext(ctx); e == nil {
					s := procSample{started, t.User + t.System}
					next.procs[pr.Pid] = s
					if b, ok := prev.procs[pr.Pid]; ok && b.started == started && s.seconds >= b.seconds && elapsed > 0 && o.CPU.Cores != nil {
						v := (s.seconds - b.seconds) / elapsed / float64(*o.CPU.Cores) * 100
						item.CPU = &v
					}
				}
			}
			processes = append(processes, item)
		}
	}
	for i := range processes {
		reason := protection(identities[processes[i].PID], identities)
		processes[i].Protected = reason != ""
		processes[i].ProtectedReason = reason
	}
	byMemory := append([]Process{}, processes...)
	sort.SliceStable(processes, func(i, j int) bool {
		a, b := processes[i].CPU, processes[j].CPU
		return a != nil && (b == nil || *a > *b)
	})
	sort.SliceStable(byMemory, func(i, j int) bool {
		a, b := byMemory[i].Mem, byMemory[j].Mem
		return a != nil && (b == nil || *a > *b)
	})
	m.previous = next
	m.mu.Lock()
	m.overview = o
	m.point = p
	m.recordPoint(p)
	m.processes = map[string][]Process{"cpu": processes, "mem": byMemory}
	for ch := range m.clients {
		select {
		case ch <- p:
		default:
		}
	}
	m.mu.Unlock()
}

func physicalDisks(stats map[string]disk.IOCountersStat, root string) map[string]disk.IOCountersStat {
	devices, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	result := make(map[string]disk.IOCountersStat)
	for _, device := range devices {
		if _, err := os.Stat(filepath.Join(root, device.Name(), "device")); err == nil {
			if stat, ok := stats[device.Name()]; ok {
				result[device.Name()] = stat
			}
		}
	}
	return result
}

// Snapshot 供经授权的插件 Host 读取同一采样结果。
func (m *Module) Snapshot() Overview { m.mu.RLock(); defer m.mu.RUnlock(); return m.overview }
