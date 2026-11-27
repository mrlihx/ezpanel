package sysinfo

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	netstat "github.com/shirou/gopsutil/v3/net"
)

func TestServerIPAddresses(t *testing.T) {
	root := t.TempDir()
	classes := filepath.Join(root, "class")
	if err := os.Mkdir(classes, 0755); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"eno1":    "/devices/pci/net/eno1",
		"lan0":    "/devices/virtual/net/lan0",
		"docker0": "/devices/virtual/net/docker0",
	} {
		if err := os.MkdirAll(root+path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(root+path, filepath.Join(classes, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(classes, "eno1"), filepath.Join(classes, "lan0", "lower_eno1")); err != nil {
		t.Fatal(err)
	}
	up := []string{"up"}
	rows := netstat.InterfaceStatList{
		{Name: "lo", Flags: up, Addrs: netstat.InterfaceAddrList{{Addr: "127.0.0.1/8"}, {Addr: "::1/128"}}},
		{Name: "eno1", Flags: up, Addrs: netstat.InterfaceAddrList{{Addr: "203.0.113.10/24"}, {Addr: "fe80::12/64"}}},
		{Name: "lan0", Flags: up, Addrs: netstat.InterfaceAddrList{{Addr: "192.168.1.10/24"}, {Addr: "fd12::1/64"}}},
		{Name: "docker0", Flags: up, Addrs: netstat.InterfaceAddrList{{Addr: "172.17.0.1/16"}}},
		{Name: "eno2", Addrs: netstat.InterfaceAddrList{{Addr: "10.0.0.10/24"}}},
	}
	want := []string{"203.0.113.10", "192.168.1.10", "fd12::1"}
	if got := serverIPAddresses(rows, "203.0.113.10", classes); !slices.Equal(got, want) {
		t.Fatalf("服务器地址 = %v，期望 %v", got, want)
	}
}
