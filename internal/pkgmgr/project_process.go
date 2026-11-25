package pkgmgr

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	gnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

type ProjectProcess struct {
	PID  int32  `json:"pid"`
	Name string `json:"name"`
}

type ProjectProcessLoad struct {
	ProjectProcess
	Parent  int32    `json:"parent"`
	User    string   `json:"user"`
	Status  []string `json:"status"`
	Started int64    `json:"started"`
	Threads int32    `json:"threads"`
	Sockets int      `json:"sockets"`
	CPU     float64  `json:"cpu"`
	RSS     uint64   `json:"rss"`
	Read    uint64   `json:"read"`
	Write   uint64   `json:"write"`
	Command string   `json:"command"`
}

type ProjectOpenFile struct {
	process.OpenFilesStat
	Position string `json:"position"`
	Flags    string `json:"flags"`
	Mode     string `json:"mode"`
}

func (m *Manager) ProjectLoad(ctx context.Context, id string, pid int32, port int) (data struct {
	Processes       []ProjectProcess      `json:"processes"`
	Process         *ProjectProcessLoad   `json:"process"`
	Connections     []gnet.ConnectionStat `json:"connections"`
	ConnectionError string                `json:"connectionError,omitempty"`
	OpenFiles       []ProjectOpenFile     `json:"openFiles"`
}, err error) {
	name := "ezpanel-site-" + id
	info, exists, err := m.inspectContainer(ctx, name)
	if err != nil || !exists || !info.State.Running {
		return data, err
	}
	result, err := m.docker(ctx, 10*time.Second, "top", name, "-eo", "pid,comm")
	if err != nil {
		return data, err
	}
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseInt(fields[0], 10, 32)
		if err != nil || value <= 0 {
			continue
		}
		data.Processes = append(data.Processes, ProjectProcess{PID: int32(value), Name: strings.Join(fields[1:], " ")})
	}
	if len(data.Processes) == 0 {
		return data, nil
	}
	containerNS, err := os.Stat(fmt.Sprintf("/proc/%d/ns/pid", info.State.Pid))
	if err != nil {
		return data, err
	}
	candidates := data.Processes
	if index := slices.IndexFunc(candidates, func(item ProjectProcess) bool { return item.PID == pid }); index >= 0 {
		candidates = candidates[index : index+1]
	}
	// npm、pnpm 和 Yarn 常通过子进程监听项目端口。
	var selected ProjectProcess
	var networkErr error
	for _, item := range candidates {
		processNS, err := os.Stat(fmt.Sprintf("/proc/%d/ns/pid", item.PID))
		if err != nil || !os.SameFile(containerNS, processNS) {
			continue
		}
		connections, err := gnet.ConnectionsPidWithoutUidsWithContext(ctx, "all", item.PID)
		listening := slices.ContainsFunc(connections, func(conn gnet.ConnectionStat) bool {
			return conn.Status == "LISTEN" && conn.Laddr.Port == uint32(port)
		})
		if selected.PID == 0 || listening || (len(data.Connections) == 0 && (len(connections) > 0 || (networkErr != nil && err == nil))) {
			selected, data.Connections, networkErr = item, connections, err
		}
		if listening {
			break
		}
	}
	if selected.PID == 0 {
		return data, nil
	}
	if networkErr != nil {
		data.ConnectionError = "无法读取该进程的网络连接"
	}

	proc, err := process.NewProcessWithContext(ctx, selected.PID)
	if err != nil {
		return data, err
	}
	load := &ProjectProcessLoad{ProjectProcess: selected}
	load.Started, err = proc.CreateTimeWithContext(ctx)
	if err != nil {
		return data, err
	}
	load.Parent, _ = proc.PpidWithContext(ctx)
	load.Status, _ = proc.StatusWithContext(ctx)
	load.User, _ = proc.UsernameWithContext(ctx)
	load.Threads, _ = proc.NumThreadsWithContext(ctx)
	load.CPU, _ = proc.CPUPercentWithContext(ctx)
	load.Command, _ = proc.CmdlineWithContext(ctx)
	if memory, err := proc.MemoryInfoWithContext(ctx); err == nil && memory != nil {
		load.RSS = memory.RSS
	}
	if io, err := proc.IOCountersWithContext(ctx); err == nil && io != nil {
		load.Read, load.Write = io.ReadBytes, io.WriteBytes
	}
	load.Sockets = len(data.Connections)
	files, _ := proc.OpenFilesWithContext(ctx)
	for _, file := range files {
		if !strings.HasPrefix(file.Path, "/") {
			continue
		}
		item := ProjectOpenFile{OpenFilesStat: file}
		fdinfo, _ := os.ReadFile(fmt.Sprintf("/proc/%d/fdinfo/%d", proc.Pid, file.Fd))
		for line := range strings.SplitSeq(string(fdinfo), "\n") {
			key, value, _ := strings.Cut(line, ":")
			switch key {
			case "pos":
				item.Position = strings.TrimSpace(value)
			case "flags":
				item.Flags = strings.TrimSpace(value)
				flags, err := strconv.ParseUint(item.Flags, 8, 32)
				if err == nil {
					switch {
					case flags&02000 != 0:
						item.Mode = "a"
					case flags&3 == 2:
						item.Mode = "rw"
					case flags&3 == 1:
						item.Mode = "w"
					default:
						item.Mode = "r"
					}
				}
			}
		}
		data.OpenFiles = append(data.OpenFiles, item)
	}
	// 进程退出并重用 PID 时，丢弃本次采样。
	current := &process.Process{Pid: selected.PID}
	if started, err := current.CreateTimeWithContext(ctx); err != nil || started != load.Started {
		data.Connections, data.OpenFiles = nil, nil
		return data, nil
	}
	sort.Slice(data.OpenFiles, func(i, j int) bool { return data.OpenFiles[i].Fd < data.OpenFiles[j].Fd })
	data.Process = load
	return data, nil
}
