package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/pkgmgr"
)

func (m *Module) countryPath(code string) string {
	return filepath.Join(m.cfg.DataDir(), "security", "countries", code+".json")
}
func (m *Module) cacheCountry(ctx context.Context, code string, refresh ...bool) error {
	path := m.countryPath(code)
	if _, err := os.Stat(path); err == nil && (len(refresh) == 0 || !refresh[0]) {
		return nil
	}
	prefixes := []string{}
	client := &http.Client{Timeout: 20 * time.Second}
	for i, base := range []string{"https://www.ipdeny.com/ipblocks/data/aggregated/", "https://www.ipdeny.com/ipv6/ipaddresses/aggregated/"} {
		req, err := http.NewRequestWithContext(ctx, "GET", base+code+"-aggregated.zone", nil)
		if err != nil {
			return err
		}
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		if res.StatusCode == 404 && i == 1 {
			res.Body.Close()
			continue
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			return fmt.Errorf("地区IP来源返回HTTP %d，请确认国家代码", res.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
		res.Body.Close()
		if err != nil {
			return err
		}
		if len(data) > 4<<20 {
			return errors.New("地区IP列表过大")
		}
		for _, value := range strings.Fields(string(data)) {
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return errors.New("地区IP来源返回了无效CIDR，未应用")
			}
			prefixes = append(prefixes, prefix.Masked().String())
		}
	}
	if len(prefixes) == 0 {
		return errors.New("该地区没有可用IP数据")
	}
	data, _ := json.Marshal(prefixes)
	return write(path, data, 0600)
}

var firewallInterfaceName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

func dockerGuardInterfaces(ctx context.Context) []string {
	// 覆盖默认 docker0、面板托管网桥及 Docker Compose/自定义桥接网络（br-*）。
	names := []string{"docker0", pkgmgr.ManagedBridge, "br-*"}
	bin, err := executor.LookPath("docker")
	if err != nil {
		return names
	}
	result, _ := executor.Run(ctx, 5*time.Second, bin, "network", "inspect", "bridge", pkgmgr.ManagedNetwork)
	var networks []struct {
		ID, Name, Driver string
		Options          map[string]string
	}
	if json.Unmarshal([]byte(result.Stdout), &networks) != nil {
		return names
	}
	for _, network := range networks {
		if network.Driver != "bridge" {
			continue
		}
		name := network.Options["com.docker.network.bridge.name"]
		if name == "" && len(network.ID) >= 12 && network.Name == pkgmgr.ManagedNetwork {
			name = "br-" + network.ID[:12]
		}
		if firewallInterfaceName.MatchString(name) && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

func (m *Module) nftConfig(ctx context.Context, state firewallState) (string, error) {
	ports, err := sshPorts(ctx)
	if err != nil {
		return "", err
	}
	ports = append(ports, m.cfg.Current().Port)
	allowed := []string{}
	for _, port := range ports {
		allowed = append(allowed, strconv.Itoa(port))
	}
	var text strings.Builder
	text.WriteString("add table inet ezpanel\ndelete table inet ezpanel\ntable inet ezpanel {\n")
	localForward := false
	for _, row := range state.Rules {
		localForward = localForward || row.Enabled && row.Kind == "forward" && row.TargetIP == "127.0.0.1"
	}
	if localForward {
		// route_localnet开启时，只允许经过端口DNAT的流量进入回环地址。
		text.WriteString("chain loopback_guard { type filter hook prerouting priority raw; policy accept; iifname != lo ip saddr 127.0.0.0/8 drop; iifname != lo ip daddr 127.0.0.0/8 drop; }\n")
	}
	regionSets := map[string]bool{}
	for _, row := range state.Rules {
		if !row.Enabled || row.Kind != "region" {
			continue
		}
		prefixes := []string{}
		for _, code := range strings.Split(row.Country, ",") {
			data, err := os.ReadFile(m.countryPath(code))
			if err != nil {
				return "", err
			}
			var values []string
			if err = json.Unmarshal(data, &values); err != nil {
				return "", err
			}
			prefixes = append(prefixes, values...)
		}
		for _, v6 := range []bool{false, true} {
			values := []string{}
			for _, p := range prefixes {
				if strings.Contains(p, ":") == v6 {
					values = append(values, p)
				}
			}
			if len(values) == 0 {
				continue
			}
			kind, suffix := "ipv4_addr", "v4"
			if v6 {
				kind, suffix = "ipv6_addr", "v6"
			}
			regionSets[row.ID+suffix] = true
			fmt.Fprintf(&text, "set r_%s_%s { type %s; flags interval; auto-merge; elements = { %s }; }\n", row.ID, suffix, kind, strings.Join(values, ", "))
		}
	}
	for _, direction := range []string{"in", "out"} {
		chain, hook, policy := "input", "input", "drop"
		if direction == "out" {
			chain, hook, policy = "output", "output", "accept"
		}
		fmt.Fprintf(&text, "chain %s { type filter hook %s priority 0; policy %s;\n", chain, hook, policy)
		if direction == "in" {
			text.WriteString("iifname lo accept\n")
			if state.BlockPing {
				text.WriteString("icmp type echo-request drop\nicmpv6 type echo-request drop\n")
			}
		}
		text.WriteString("ct state established,related accept\nct state invalid drop\n")
		if direction == "in" {
			text.WriteString("udp sport 67 udp dport 68 accept\nudp sport 547 udp dport 546 accept\nicmpv6 type { nd-router-solicit, nd-router-advert, nd-neighbor-solicit, nd-neighbor-advert, packet-too-big, destination-unreachable, time-exceeded, parameter-problem } accept\n")
			if ip, err := netip.ParseAddr(state.TrustedIP); err == nil {
				family := "ip"
				if ip.Is6() {
					family = "ip6"
				}
				fmt.Fprintf(&text, "%s saddr %s tcp dport { %s } accept\n", family, ip.String(), strings.Join(allowed, ", "))
			}
			if state.ScanProtection {
				text.WriteString("tcp flags & (fin | syn | rst | psh | ack | urg) == 0 drop\ntcp flags & (fin | psh | urg) == (fin | psh | urg) drop\n")
			}
		} else {
			text.WriteString("oifname lo accept\n")
		}
		// IP白名单优先，其次拒绝规则，再应用普通放行规则。
		ordered := []Rule{}
		for _, row := range state.Rules {
			if row.Kind == "ip" && row.Policy == "allow" {
				ordered = append(ordered, row)
			}
		}
		for _, row := range state.Rules {
			if row.Policy == "deny" {
				ordered = append(ordered, row)
			}
		}
		for _, row := range state.Rules {
			if row.Policy == "allow" && row.Kind != "ip" {
				ordered = append(ordered, row)
			}
		}
		for _, row := range ordered {
			if !row.Enabled || (row.Direction != direction && row.Direction != "both") || row.Kind == "forward" && (direction != "in" || row.TargetIP != "127.0.0.1") {
				continue
			}
			if row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) {
				continue
			}
			match := ""
			if row.ExpiresAt != nil {
				match = fmt.Sprintf("meta time < %d ", row.ExpiresAt.Unix())
			}
			field := "saddr"
			if direction == "out" {
				field = "daddr"
			}
			peerMatches := []string{""}
			if row.Peer != "any" {
				peerMatches = nil
				for _, v6 := range []bool{false, true} {
					values := []string{}
					for _, peer := range strings.Fields(row.Peer) {
						if strings.Contains(peer, ":") == v6 {
							values = append(values, peer)
						}
					}
					if len(values) > 0 {
						family := "ip"
						if v6 {
							family = "ip6"
						}
						peerMatches = append(peerMatches, fmt.Sprintf("%s %s { %s } ", family, field, strings.Join(values, ", ")))
					}
				}
			}
			verdict := "accept"
			if row.Policy == "deny" {
				verdict = "drop"
			}
			if row.Kind == "region" {
				for _, family := range []string{"ip", "ip6"} {
					suffix := "v4"
					if family == "ip6" {
						suffix = "v6"
					}
					if !regionSets[row.ID+suffix] {
						continue
					}
					regionMatch := fmt.Sprintf("%s%s %s @r_%s_%s ", match, family, field, row.ID, suffix)
					if row.Ports == "" || row.Ports == "all" {
						fmt.Fprintf(&text, "%scounter %s\n", regionMatch, verdict)
					} else {
						for _, protocol := range []string{"tcp", "udp"} {
							fmt.Fprintf(&text, "%s%s dport { %s } counter %s\n", regionMatch, protocol, row.Ports, verdict)
						}
					}
				}
			} else if row.Kind == "forward" {
				protocols := []string{row.Protocol}
				if row.Protocol == "both" {
					protocols = []string{"tcp", "udp"}
				}
				for _, protocol := range protocols {
					for _, peerMatch := range peerMatches {
						fmt.Fprintf(&text, "%s%sip daddr 127.0.0.1 %s dport %d ct status dnat ct original proto-dst %s counter accept\n", match, peerMatch, protocol, row.TargetPort, row.Ports)
					}
				}
			} else if row.Kind == "port" {
				protocols := []string{row.Protocol}
				if row.Protocol == "both" {
					protocols = []string{"tcp", "udp"}
				}
				for _, protocol := range protocols {
					for _, peerMatch := range peerMatches {
						fmt.Fprintf(&text, "%s%s%s dport { %s } counter %s\n", match, peerMatch, protocol, row.Ports, verdict)
					}
				}
			} else {
				for _, peerMatch := range peerMatches {
					fmt.Fprintf(&text, "%s%scounter %s\n", match, peerMatch, verdict)
				}
			}
		}
		if direction == "in" {
			text.WriteString("meta l4proto { icmp, ipv6-icmp } accept\n")
		}
		text.WriteString("}\n")
	}
	// docker_guard 拦截外部经 Docker DNAT 进入容器的流量，仅放行防火墙中明确允许的端口/来源。
	bridges := dockerGuardInterfaces(ctx)
	for i, name := range bridges {
		bridges[i] = strconv.Quote(name)
	}
	fmt.Fprintf(&text, "chain docker_guard { type filter hook forward priority -10; policy accept; ct status dnat oifname { %s } jump docker_policy; }\n", strings.Join(bridges, ", "))
	text.WriteString("chain docker_policy {\niifname lo accept\nct state established,related accept\nct state invalid drop\n")
	ordered := []Rule{}
	for _, row := range state.Rules {
		if row.Kind == "ip" && row.Policy == "allow" {
			ordered = append(ordered, row)
		}
	}
	for _, row := range state.Rules {
		if row.Policy == "deny" {
			ordered = append(ordered, row)
		}
	}
	for _, row := range state.Rules {
		if row.Policy == "allow" && row.Kind != "ip" {
			ordered = append(ordered, row)
		}
	}
	for _, row := range ordered {
		if !row.Enabled || (row.Direction != "in" && row.Direction != "both") {
			continue
		}
		if row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) {
			continue
		}
		match := "ct status dnat "
		if row.ExpiresAt != nil {
			match += fmt.Sprintf("meta time < %d ", row.ExpiresAt.Unix())
		}
		peerMatches := []string{""}
		if row.Peer != "any" {
			peerMatches = nil
			for _, v6 := range []bool{false, true} {
				values := []string{}
				for _, peer := range strings.Fields(row.Peer) {
					if strings.Contains(peer, ":") == v6 {
						values = append(values, peer)
					}
				}
				if len(values) > 0 {
					family := "ip"
					if v6 {
						family = "ip6"
					}
					peerMatches = append(peerMatches, fmt.Sprintf("%s saddr { %s } ", family, strings.Join(values, ", ")))
				}
			}
		}
		verdict := "accept"
		if row.Policy == "deny" {
			verdict = "drop"
		}
		switch row.Kind {
		case "region":
			for _, family := range []string{"ip", "ip6"} {
				suffix := "v4"
				if family == "ip6" {
					suffix = "v6"
				}
				if !regionSets[row.ID+suffix] {
					continue
				}
				regionMatch := fmt.Sprintf("%s%s saddr @r_%s_%s ", match, family, row.ID, suffix)
				if row.Ports == "" || row.Ports == "all" {
					fmt.Fprintf(&text, "%scounter %s\n", regionMatch, verdict)
				} else {
					for _, protocol := range []string{"tcp", "udp"} {
						fmt.Fprintf(&text, "%smeta l4proto %s ct original proto-dst { %s } counter %s\n", regionMatch, protocol, row.Ports, verdict)
					}
				}
			}
		case "port", "forward":
			protocols := []string{row.Protocol}
			if row.Protocol == "both" {
				protocols = []string{"tcp", "udp"}
			}
			for _, protocol := range protocols {
				for _, peerMatch := range peerMatches {
					fmt.Fprintf(&text, "%s%smeta l4proto %s ct original proto-dst { %s } counter %s\n", match, peerMatch, protocol, row.Ports, verdict)
				}
			}
		default:
			for _, peerMatch := range peerMatches {
				fmt.Fprintf(&text, "%s%scounter %s\n", match, peerMatch, verdict)
			}
		}
	}
	text.WriteString("counter drop\n}\n")
	text.WriteString("chain prerouting { type nat hook prerouting priority dstnat; policy accept;\n")
	for _, row := range state.Rules {
		if !row.Enabled || row.Kind != "forward" || row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) {
			continue
		}
		for _, port := range ports {
			if row.Ports == strconv.Itoa(port) {
				return "", errors.New("不能转发面板或SSH正在使用的管理端口")
			}
		}
		family, target := "ip", row.TargetIP
		if strings.Contains(target, ":") {
			family = "ip6"
			target = "[" + target + "]"
		}
		protocols := []string{row.Protocol}
		if row.Protocol == "both" {
			protocols = []string{"tcp", "udp"}
		}
		match := "fib daddr type local "
		if row.Peer != "any" {
			peers := []string{}
			for _, peer := range strings.Fields(row.Peer) {
				if strings.Contains(peer, ":") == strings.Contains(row.TargetIP, ":") {
					peers = append(peers, peer)
				} else {
					return "", errors.New("转发来源和目标IP必须使用相同地址族")
				}
			}
			match += fmt.Sprintf("%s saddr { %s } ", family, strings.Join(peers, ", "))
		}
		if row.ExpiresAt != nil {
			match += fmt.Sprintf("meta time < %d ", row.ExpiresAt.Unix())
		}
		for _, protocol := range protocols {
			fmt.Fprintf(&text, "%s%s dport %s dnat %s to %s:%d\n", match, protocol, row.Ports, family, target, row.TargetPort)
		}
	}
	text.WriteString("}\nchain postrouting { type nat hook postrouting priority srcnat; policy accept;\n")
	for _, row := range state.Rules {
		if !row.Enabled || row.Kind != "forward" || row.TargetIP == "127.0.0.1" || row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) {
			continue
		}
		family := "ip"
		if strings.Contains(row.TargetIP, ":") {
			family = "ip6"
		}
		protocols := []string{row.Protocol}
		if row.Protocol == "both" {
			protocols = []string{"tcp", "udp"}
		}
		for _, protocol := range protocols {
			fmt.Fprintf(&text, "%s daddr %s %s dport %d ct status dnat ct original proto-dst %s masquerade\n", family, row.TargetIP, protocol, row.TargetPort, row.Ports)
		}
	}
	text.WriteString("}\n}\n")
	return text.String(), nil
}
func (m *Module) applyFirewall(ctx context.Context, state firewallState, clientIP string) (err error) {
	state.TrustedIP = clientIP
	oldState, err := m.readFirewall()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	const script = "/etc/nftables.d/ezpanel.nft"
	const unit = "/etc/systemd/system/ezpanel-firewall.service"
	if !state.Enabled {
		if _, e := os.Stat(unit); e == nil {
			if _, err = run(ctx, "systemctl", "disable", "--now", "ezpanel-firewall.service"); err != nil {
				return err
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		if _, e := run(ctx, "nft", "list", "table", "inet", "ezpanel"); e == nil {
			if _, err = run(ctx, "nft", "delete", "table", "inet", "ezpanel"); err != nil {
				return err
			}
		}
		return write(m.firewallPath(), data, 0600)
	}
	nft, err := executor.LookPath("nft")
	if err != nil {
		return errors.New("请先安装发行版提供的nftables软件包")
	}
	content, err := m.nftConfig(ctx, state)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp("", "ezpanel-nft-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, err = temp.WriteString(content)
	err = errors.Join(err, temp.Close())
	if err != nil {
		return err
	}
	if _, err = run(ctx, nft, "--check", "--file", temp.Name()); err != nil {
		return err
	}
	forwardScript, files, err := m.forwardConfig(state)
	if err != nil {
		return err
	}
	files[script] = []byte(content)
	files[unit] = []byte(fmt.Sprintf("[Unit]\nDescription=ezpanel managed firewall\nAfter=network.target docker.service\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=%s -f %s\nExecStartPost=/bin/sh %s apply\nExecReload=%s -f %s\nExecReload=/bin/sh %s apply\nExecStop=/bin/sh %s stop\nExecStop=%s delete table inet ezpanel\n[Install]\nWantedBy=multi-user.target\n", nft, script, forwardScript, nft, script, forwardScript, forwardScript, nft))
	forward := false
	for _, row := range state.Rules {
		forward = forward || row.Kind == "forward" && row.Enabled && row.TargetIP != "127.0.0.1"
	}
	const sysctlPath = "/etc/sysctl.d/90-ezpanel-forward.conf"
	if forward {
		files[sysctlPath] = []byte("net.ipv4.ip_forward=1\nnet.ipv6.conf.all.forwarding=1\n")
	}
	oldForward := map[string]string{}
	if forward {
		for _, key := range []string{"net.ipv4.ip_forward", "net.ipv6.conf.all.forwarding"} {
			value, e := run(ctx, "sysctl", "-n", key)
			if e != nil {
				return e
			}
			oldForward[key] = strings.TrimSpace(value)
		}
	}
	localForward := false
	for _, row := range state.Rules {
		localForward = localForward || row.Enabled && row.Kind == "forward" && row.TargetIP == "127.0.0.1"
	}
	_, routeErr := os.Stat("/run/ezpanel-route-localnet")
	if localForward || routeErr == nil {
		value, e := run(ctx, "sysctl", "-n", "net.ipv4.conf.all.route_localnet")
		if e != nil {
			return e
		}
		oldForward["net.ipv4.conf.all.route_localnet"] = strings.TrimSpace(value)
		if localForward && os.IsNotExist(routeErr) {
			files["/run/ezpanel-route-localnet"] = []byte(strings.TrimSpace(value) + "\n")
		}
	}
	// 文件写入、规则生效和持久化作为一次保存；失败时恢复上一份配置。
	type savedFile struct {
		data   []byte
		exists bool
		mode   os.FileMode
	}
	previous := map[string]savedFile{}
	for path := range files {
		b, e := os.ReadFile(path)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		mode := os.FileMode(0600)
		if info, e := os.Stat(path); e == nil {
			mode = info.Mode().Perm()
		}
		previous[path] = savedFile{b, e == nil, mode}
	}
	changed := false
	defer func() {
		if err == nil || !changed {
			return
		}
		recovery, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var restoreErr error
		for path, old := range previous {
			if old.exists {
				restoreErr = errors.Join(restoreErr, write(path, old.data, old.mode))
			} else {
				e := os.Remove(path)
				if !os.IsNotExist(e) {
					restoreErr = errors.Join(restoreErr, e)
				}
			}
		}
		_, e := run(recovery, "systemctl", "daemon-reload")
		restoreErr = errors.Join(restoreErr, e)
		if oldState.Enabled && previous[script].exists {
			_, e = run(recovery, nft, "--file", script)
			restoreErr = errors.Join(restoreErr, e)
			if previous[forwardScript].exists {
				_, e = run(recovery, "sh", forwardScript, "apply")
				restoreErr = errors.Join(restoreErr, e)
			}
		} else {
			_, _ = run(recovery, "systemctl", "disable", "ezpanel-firewall.service")
			if _, e = run(recovery, nft, "list", "table", "inet", "ezpanel"); e == nil {
				_, e = run(recovery, nft, "delete", "table", "inet", "ezpanel")
				restoreErr = errors.Join(restoreErr, e)
			}
			for _, bin := range []string{"iptables", "ip6tables"} {
				if _, e = executor.LookPath(bin); e == nil {
					if _, e = run(recovery, bin, "-w", "-C", "FORWARD", "-j", "EZPANEL-FWD"); e == nil {
						_, e = run(recovery, bin, "-w", "-D", "FORWARD", "-j", "EZPANEL-FWD")
						restoreErr = errors.Join(restoreErr, e)
					}
					_, _ = run(recovery, bin, "-w", "-F", "EZPANEL-FWD")
					_, _ = run(recovery, bin, "-w", "-X", "EZPANEL-FWD")
				}
			}
		}
		for key, value := range oldForward {
			_, e := run(recovery, "sysctl", "-w", key+"="+value)
			restoreErr = errors.Join(restoreErr, e)
		}
		if restoreErr != nil {
			slog.Error("防火墙保存恢复失败", "error", restoreErr)
			err = errors.Join(err, errors.New("恢复不完整，请检查服务日志及.panel-backup文件"))
		}
	}()
	for path, b := range files {
		mode := os.FileMode(0600)
		if path == unit || path == sysctlPath {
			mode = 0644
		}
		changed = true
		if err = write(path, b, mode); err != nil {
			return err
		}
	}
	if _, err = run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if forward {
		if _, err = run(ctx, "sysctl", "-p", sysctlPath); err != nil {
			return err
		}
	}
	if _, err = run(ctx, "systemctl", "enable", "ezpanel-firewall.service"); err != nil {
		return err
	}
	if _, err = run(ctx, "systemctl", "reload-or-restart", "ezpanel-firewall.service"); err != nil {
		return err
	}
	return write(m.firewallPath(), data, 0600)
}
