package sysinfo

import (
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"

	netstat "github.com/shirou/gopsutil/v3/net"
)

func serverIPAddresses(interfaces netstat.InterfaceStatList, publicIP, netRoot string) []string {
	out := []string{}
	seen := map[netip.Addr]bool{}
	add := func(raw string) {
		host, _, _ := strings.Cut(raw, "/")
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.IsGlobalUnicast() {
			return
		}
		ip = ip.Unmap().WithZone("")
		if !seen[ip] {
			out = append(out, ip.String())
			seen[ip] = true
		}
	}
	add(publicIP)
	for _, iface := range interfaces {
		if !slices.Contains(iface.Flags, "up") || !hostNetworkInterface(netRoot, iface.Name) {
			continue
		}
		for _, address := range iface.Addrs {
			add(address.Addr)
		}
	}
	return out
}

func hostNetworkInterface(netRoot, name string) bool {
	path := filepath.Join(netRoot, name)
	link, err := os.Readlink(path)
	if err != nil || !strings.Contains(link, "/devices/virtual/net/") {
		return true
	}
	// 主机的网桥、bond 和 VLAN 可以承载局域网地址，须有实际网卡作为下层。
	links, _ := os.ReadDir(path)
	for _, child := range links {
		if lower, ok := strings.CutPrefix(child.Name(), "lower_"); ok && hostNetworkInterface(netRoot, lower) {
			return true
		}
	}
	return false
}
