//go:build linux

package sysinfo

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/process"
	"golang.org/x/sys/unix"
)

const networkNotice = "仅显示面板当前网络命名空间内、可读取归属的 TCP/UDP socket；连接数不含监听和未连接 UDP，IP 数为去重远端 IP。速度为两次采样间持续存在的 TCP socket 的内核 bytes_acked/bytes_received 差值（B/s），不含 UDP、重传流量及采样间已退出的短连接；首次或不可读取时显示未知。共享 socket 会列在各持有进程下，不宜跨进程相加；数据最多每两秒更新。"

type networkSocket struct {
	NetworkConnection
	inode   uint32
	key     string
	counter *networkCounter
}

func collectNetwork(ctx context.Context, previous networkSnapshot) (networkSnapshot, error) {
	next := networkSnapshot{at: time.Now().UTC(), list: []NetworkProcess{}, connections: map[int32][]NetworkConnection{}, counters: map[string]networkCounter{}, notice: networkNotice}
	sockets := []networkSocket{}
	for _, family := range []uint8{unix.AF_INET, unix.AF_INET6} {
		for _, protocol := range []uint8{unix.IPPROTO_TCP, unix.IPPROTO_UDP} {
			if err := ctx.Err(); err != nil {
				return next, errors.New("网络连接采样超时或已取消")
			}
			rows, err := dumpNetworkSockets(ctx, family, protocol)
			if err != nil {
				if family == unix.AF_INET6 && (errors.Is(err, unix.EAFNOSUPPORT) || errors.Is(err, unix.EPROTONOSUPPORT)) {
					next.notice += " IPv6 inet_diag 不受当前系统支持，未包含 IPv6。"
					continue
				}
				return next, fmt.Errorf("无法读取 Linux inet_diag（需内核支持与相应权限）：%w", err)
			}
			sockets = append(sockets, rows...)
		}
	}
	byInode := make(map[uint32][]int)
	elapsed := next.at.Sub(previous.at).Seconds()
	for i := range sockets {
		row := &sockets[i]
		byInode[row.inode] = append(byInode[row.inode], i)
		if row.counter == nil || row.key == "" {
			continue
		}
		next.counters[row.key] = *row.counter
		if before, ok := previous.counters[row.key]; ok && row.RemotePort != 0 && row.State != "LISTEN" {
			row.Upload = rate(before.acked, row.counter.acked, elapsed)
			row.Download = rate(before.received, row.counter.received, elapsed)
		}
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return next, fmt.Errorf("无法读取进程 socket 归属：%w", err)
	}
	unreadable := 0
	for _, entry := range entries {
		pid, err := strconv.ParseInt(entry.Name(), 10, 32)
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return next, errors.New("网络连接采样超时或已取消")
		}
		fdRoot := filepath.Join("/proc", entry.Name(), "fd")
		fds, err := os.ReadDir(fdRoot)
		if err != nil {
			if os.IsPermission(err) {
				unreadable++
			}
			continue
		}
		pr := &process.Process{Pid: int32(pid)}
		started, err := pr.CreateTimeWithContext(ctx)
		if err != nil || started <= 0 {
			continue
		}
		indices := []int{}
		seen := map[uint32]bool{}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdRoot, fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
				continue
			}
			inode, err := strconv.ParseUint(target[8:len(target)-1], 10, 32)
			if err == nil && !seen[uint32(inode)] {
				seen[uint32(inode)] = true
				indices = append(indices, byInode[uint32(inode)]...)
			}
		}
		if len(indices) == 0 {
			continue
		}
		name, err := pr.NameWithContext(ctx)
		if err != nil {
			unreadable++
			continue
		}
		item := NetworkProcess{PID: int32(pid), Started: started, Name: name, ListenIPs: []string{}, Ports: []string{}}
		connections := make([]NetworkConnection, 0, len(indices))
		listenIPs, ports, remoteIPs := map[string]bool{}, map[string]bool{}, map[string]bool{}
		var up, down float64
		measured, tcpCount := 0, 0
		for _, index := range indices {
			row := sockets[index].NetworkConnection
			connections = append(connections, row)
			listening := row.State == "LISTEN" || (row.Protocol == "UDP" && row.RemotePort == 0)
			if listening {
				listenIPs[row.LocalIP] = true
			}
			if row.LocalPort != 0 {
				ports[fmt.Sprintf("%s/%d", row.Protocol, row.LocalPort)] = true
			}
			if !listening && row.RemotePort != 0 {
				item.ConnectionCount++
				remoteIPs[row.RemoteIP] = true
				if row.Protocol == "TCP" {
					tcpCount++
				}
			}
			if row.Upload != nil && row.Download != nil {
				up += *row.Upload
				down += *row.Download
				measured++
			}
		}
		check := &process.Process{Pid: int32(pid)}
		if current, err := check.CreateTimeWithContext(ctx); err != nil || current != started {
			continue
		}
		item.IPCount = len(remoteIPs)
		for ip := range listenIPs {
			item.ListenIPs = append(item.ListenIPs, ip)
		}
		for port := range ports {
			item.Ports = append(item.Ports, port)
		}
		sort.Strings(item.ListenIPs)
		sort.Strings(item.Ports)
		item.RateNote = fmt.Sprintf("仅汇总 %d/%d 条连续可采样 TCP 连接；UDP、新连接及采样间已退出的连接不计入，单位 B/s", measured, tcpCount)
		if measured > 0 {
			item.Upload, item.Download = &up, &down
		}
		sort.Slice(connections, func(i, j int) bool {
			a, b := connections[i], connections[j]
			if a.Protocol != b.Protocol {
				return a.Protocol < b.Protocol
			}
			if a.LocalPort != b.LocalPort {
				return a.LocalPort < b.LocalPort
			}
			if a.RemoteIP != b.RemoteIP {
				return a.RemoteIP < b.RemoteIP
			}
			return a.RemotePort < b.RemotePort
		})
		next.list = append(next.list, item)
		next.connections[item.PID] = connections
	}
	sort.Slice(next.list, func(i, j int) bool {
		if next.list[i].ConnectionCount != next.list[j].ConnectionCount {
			return next.list[i].ConnectionCount > next.list[j].ConnectionCount
		}
		return next.list[i].PID < next.list[j].PID
	})
	if unreadable > 0 {
		next.notice += fmt.Sprintf(" 有 %d 个进程的归属或名称受权限限制，结果可能不完整。", unreadable)
	}
	return next, nil
}

func dumpNetworkSockets(ctx context.Context, family, protocol uint8) ([]networkSocket, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.NETLINK_SOCK_DIAG)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, err
	}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 1}); err != nil {
		return nil, err
	}
	// UAPI inet_diag_req_v2 固定为 56 字节，所有整数字段按各自 ABI 字节序写入。
	request := make([]byte, 16+56)
	binary.NativeEndian.PutUint32(request[0:4], uint32(len(request)))
	binary.NativeEndian.PutUint16(request[4:6], unix.SOCK_DIAG_BY_FAMILY)
	binary.NativeEndian.PutUint16(request[6:8], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	binary.NativeEndian.PutUint32(request[8:12], 1)
	request[16], request[17] = family, protocol
	if protocol == unix.IPPROTO_TCP {
		request[18] = 1 << (2 - 1) // INET_DIAG_INFO
	}
	binary.NativeEndian.PutUint32(request[20:24], ^uint32(0))
	binary.NativeEndian.PutUint64(request[64:72], ^uint64(0)) // INET_DIAG_NOCOOKIE
	if err := unix.Sendto(fd, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, err
	}
	rows := []networkSocket{}
	buffer := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, _, flags, sender, err := unix.Recvmsg(fd, buffer, nil, 0)
		if err != nil {
			return nil, err
		}
		address, ok := sender.(*unix.SockaddrNetlink)
		if !ok || address.Pid != 0 || flags&unix.MSG_TRUNC != 0 {
			return nil, errors.New("inet_diag 返回了无效或截断的数据")
		}
		for offset := 0; offset < n; {
			if n-offset < 16 {
				return nil, errors.New("inet_diag 消息头不完整")
			}
			header := buffer[offset:n]
			length := int(binary.NativeEndian.Uint32(header[0:4]))
			if length < 16 || length > len(header) || binary.NativeEndian.Uint32(header[8:12]) != 1 {
				return nil, errors.New("inet_diag 消息长度或序号无效")
			}
			kind := binary.NativeEndian.Uint16(header[4:6])
			if binary.NativeEndian.Uint16(header[6:8])&unix.NLM_F_DUMP_INTR != 0 {
				return nil, errors.New("inet_diag 采样被内核中断，请稍后重试")
			}
			data := header[16:length]
			if kind == unix.NLMSG_ERROR || kind == unix.NLMSG_DONE {
				if len(data) >= 4 {
					code := int32(binary.NativeEndian.Uint32(data[:4]))
					if code < 0 {
						return nil, unix.Errno(-code)
					}
				} else if kind == unix.NLMSG_ERROR {
					return nil, errors.New("inet_diag 错误响应不完整")
				}
				if kind == unix.NLMSG_DONE {
					return rows, nil
				}
			} else if kind == unix.SOCK_DIAG_BY_FAMILY {
				if len(data) < 72 || data[0] != family {
					return nil, errors.New("inet_diag socket 数据不完整")
				}
				inode := binary.NativeEndian.Uint32(data[68:72])
				if inode != 0 {
					row := networkSocket{inode: inode}
					row.Protocol = "TCP"
					row.State = networkTCPState(data[1])
					row.LocalPort, row.RemotePort = binary.BigEndian.Uint16(data[4:6]), binary.BigEndian.Uint16(data[6:8])
					addressSize := 16
					if family == unix.AF_INET {
						addressSize = 4
					}
					row.LocalIP, row.RemoteIP = net.IP(data[8:8+addressSize]).String(), net.IP(data[24:24+addressSize]).String()
					if protocol == unix.IPPROTO_UDP {
						row.Protocol, row.State = "UDP", "UNCONNECTED"
						if row.RemotePort != 0 {
							row.State = "CONNECTED"
						}
					}
					cookie := binary.NativeEndian.Uint64(data[44:52])
					if cookie != ^uint64(0) {
						row.key = fmt.Sprintf("%d/%d/%d", family, protocol, cookie)
					}
					for attrs := data[72:]; len(attrs) > 0; {
						if len(attrs) < 4 {
							return nil, errors.New("inet_diag 属性头不完整")
						}
						attrLength := int(binary.NativeEndian.Uint16(attrs[:2]))
						if attrLength < 4 || attrLength > len(attrs) {
							return nil, errors.New("inet_diag 属性长度无效")
						}
						if protocol == unix.IPPROTO_TCP && binary.NativeEndian.Uint16(attrs[2:4]) == 2 && attrLength >= 4+136 {
							// tcp_info 含位域；按 Linux UAPI 固定偏移取 uint64，避免 Go 结构体对齐差异。
							info := attrs[4:attrLength]
							row.counter = &networkCounter{acked: binary.NativeEndian.Uint64(info[120:128]), received: binary.NativeEndian.Uint64(info[128:136])}
						}
						aligned := (attrLength + 3) &^ 3
						if aligned > len(attrs) {
							return nil, errors.New("inet_diag 属性对齐无效")
						}
						attrs = attrs[aligned:]
					}
					rows = append(rows, row)
				}
			} else if kind == unix.NLMSG_OVERRUN {
				return nil, errors.New("inet_diag 接收缓冲区溢出")
			}
			offset += (length + 3) &^ 3
		}
	}
}

func networkTCPState(state uint8) string {
	states := [...]string{"UNKNOWN", "ESTABLISHED", "SYN_SENT", "SYN_RECV", "FIN_WAIT1", "FIN_WAIT2", "TIME_WAIT", "CLOSE", "CLOSE_WAIT", "LAST_ACK", "LISTEN", "CLOSING", "NEW_SYN_RECV", "BOUND_INACTIVE"}
	if int(state) >= len(states) {
		return "UNKNOWN"
	}
	return states[state]
}
