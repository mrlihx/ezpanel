package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strings"
	"time"
)

const (
	ManagedNetwork = "ezpanel-network"
	ManagedBridge  = "ezpanel0"
)

func (m *Manager) ensureManagedNetwork(ctx context.Context) error {
	check := func() (bool, error) {
		out, err := m.docker(ctx, 5*time.Second, "network", "inspect", "--format", "{{.Name}} {{.Driver}} {{.Scope}}", ManagedNetwork)
		if err != nil {
			if strings.Contains(out.Stderr, "network "+ManagedNetwork+" not found") || strings.Contains(out.Stderr, "No such network: "+ManagedNetwork) {
				return false, nil
			}
			return false, err
		}
		if strings.TrimSpace(out.Stdout) != ManagedNetwork+" bridge local" {
			return false, fmt.Errorf("%s 已存在，但不是本机 bridge 网络", ManagedNetwork)
		}
		return true, nil
	}
	if exists, err := check(); err != nil || exists {
		return err
	}
	if _, err := m.docker(ctx, 15*time.Second, "network", "create", "--driver", "bridge", "--opt", "com.docker.network.bridge.name="+ManagedBridge, ManagedNetwork); err != nil {
		// 其他安装任务可能已经创建了同名网络。
		if exists, inspectErr := check(); !exists {
			return errors.Join(err, inspectErr)
		}
	}
	return nil
}

func (info containerInfo) managedAddress() string {
	if ip := info.NetworkSettings.Networks[ManagedNetwork].IPAddress; ip != "" {
		return ip
	}
	return info.NetworkSettings.Networks["bridge"].IPAddress
}

func (m *Manager) connectManagedNetwork(ctx context.Context, info containerInfo) error {
	if _, connected := info.NetworkSettings.Networks[ManagedNetwork]; connected {
		return nil
	}
	if err := m.ensureManagedNetwork(ctx); err != nil {
		return err
	}
	_, err := m.docker(ctx, 15*time.Second, "network", "connect", ManagedNetwork, info.ID)
	return err
}

// ManagedNetworkSubnet 返回 ezpanel-network 的 IPv4 子网（MySQL 兼容的 IP/Netmask 格式，如 172.18.0.0/255.255.0.0）。
func (m *Manager) ManagedNetworkSubnet(ctx context.Context) (string, error) {
	if err := m.ensureManagedNetwork(ctx); err != nil {
		return "", err
	}
	out, err := m.docker(ctx, 5*time.Second, "network", "inspect", "--format", "{{range .IPAM.Config}}{{.Subnet}}\n{{end}}", ManagedNetwork)
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(out.Stdout), "\n") {
		cidr := strings.TrimSpace(line)
		if cidr == "" {
			continue
		}
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil || len(ipNet.Mask) != net.IPv4len {
			continue
		}
		mask := net.IP(ipNet.Mask).To4()
		if mask == nil {
			continue
		}
		return ip4.String() + "/" + mask.String(), nil
	}
	return "", fmt.Errorf("未找到 %s 的 IPv4 子网配置", ManagedNetwork)
}

const (
	managedHostsMarker = "# ezpanel-managed"
	managedHostsLine   = "127.0.0.1 ezpanel-mysql ezpanel-postgresql ezpanel-redis ezpanel-mongodb ezpanel-memcached " + managedHostsMarker
)

// EnsureManagedHosts 在宿主机 /etc/hosts 中幂等写入默认数据库容器主机名映射到 127.0.0.1，
// 使宿主机原生进程也可统一使用 ezpanel-mysql 等地址连接数据库。
func (m *Manager) EnsureManagedHosts() {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return
	}
	if err := ensureManagedHostsFile("/etc/hosts"); err != nil {
		slog.Warn("更新 /etc/hosts 失败", "error", err)
	}
}

func ensureManagedHostsFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	content := string(raw)
	lines := strings.Split(content, "\n")
	found := false
	changed := false
	for i, line := range lines {
		if strings.Contains(line, managedHostsMarker) {
			found = true
			if strings.TrimSpace(line) != managedHostsLine {
				lines[i] = managedHostsLine
				changed = true
			}
			break
		}
	}
	var next string
	if found {
		if !changed {
			return nil
		}
		next = strings.Join(lines, "\n")
	} else {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		next = content + managedHostsLine + "\n"
	}
	return os.WriteFile(path, []byte(next), 0644)
}
