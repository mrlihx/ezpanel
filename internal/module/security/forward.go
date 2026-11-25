package security

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"ezpanel/internal/executor"
)

// Docker通常将FORWARD策略设为DROP；只为面板DNAT连接增加独立放行链。
func (m *Module) forwardConfig(state firewallState) (string, map[string][]byte, error) {
	const base = "/etc/nftables.d/ezpanel-forward"
	files := map[string][]byte{}
	localForward := false
	for _, row := range state.Rules {
		localForward = localForward || state.Enabled && row.Enabled && row.Kind == "forward" && row.TargetIP == "127.0.0.1"
	}
	var script strings.Builder
	script.WriteString(`#!/bin/sh
set -eu
cleanup() {
 if "$1" -w -S EZPANEL-FWD >/dev/null 2>&1; then
  if "$1" -w -C FORWARD -j EZPANEL-FWD >/dev/null 2>&1; then "$1" -w -D FORWARD -j EZPANEL-FWD; fi
  "$1" -w -F EZPANEL-FWD
  "$1" -w -X EZPANEL-FWD
 fi
}
`)
	for _, v6 := range []bool{false, true} {
		command, restore, suffix := "iptables", "iptables-restore", "v4"
		if v6 {
			command, restore, suffix = "ip6tables", "ip6tables-restore", "v6"
		}
		rules := []string{}
		for _, row := range state.Rules {
			if !state.Enabled || !row.Enabled || row.Kind != "forward" || row.TargetIP == "127.0.0.1" || strings.Contains(row.TargetIP, ":") != v6 || row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) {
				continue
			}
			peers := []string{"any"}
			if row.Peer != "any" {
				peers = strings.Fields(row.Peer)
			}
			protocols := []string{row.Protocol}
			if row.Protocol == "both" {
				protocols = []string{"tcp", "udp"}
			}
			for _, peer := range peers {
				source, replySource := "", ""
				if peer != "any" {
					source = " -s " + peer
					replySource = " -d " + peer
				}
				for _, protocol := range protocols {
					rules = append(rules, fmt.Sprintf("-A EZPANEL-FWD -p %s%s -d %s --dport %d -m conntrack --ctstatus DNAT --ctorigdstport %s -j ACCEPT", protocol, source, row.TargetIP, row.TargetPort, row.Ports))
					rules = append(rules, fmt.Sprintf("-A EZPANEL-FWD -p %s%s -s %s --sport %d -m conntrack --ctdir REPLY --ctstatus DNAT --ctorigdstport %s -j ACCEPT", protocol, replySource, row.TargetIP, row.TargetPort, row.Ports))
				}
			}
		}
		if len(rules) == 0 {
			fmt.Fprintf(&script, "if command -v %s >/dev/null 2>&1; then cleanup %s; fi\n", command, command)
			continue
		}
		if _, err := executor.LookPath(command); err != nil {
			return "", nil, errors.New("端口转发需要发行版提供的iptables兼容工具")
		}
		content := "*filter\n:EZPANEL-FWD - [0:0]\n-F EZPANEL-FWD\n" + strings.Join(rules, "\n") + "\nCOMMIT\n"
		path := base + "." + suffix
		files[path] = []byte(content)
		fmt.Fprintf(&script, "if [ \"$1\" = stop ]; then cleanup %s; else\n%s -w --noflush < %s\nif ! %s -w -C FORWARD -j EZPANEL-FWD >/dev/null 2>&1; then %s -w -I FORWARD 1 -j EZPANEL-FWD; fi\nfi\n", command, restore, path, command, command)
	}
	// /run中的原值只保存到本次开机；停止服务或移除本机转发时恢复。
	script.WriteString("route_backup=/run/ezpanel-route-localnet\nroute_value=/proc/sys/net/ipv4/conf/all/route_localnet\n")
	if localForward {
		script.WriteString(`if [ "$1" != stop ]; then
 if [ ! -f "$route_backup" ]; then (umask 077; cat "$route_value" > "$route_backup"); fi
 echo 1 > "$route_value"
elif [ -f "$route_backup" ]; then
 cat "$route_backup" > "$route_value"
 rm -f "$route_backup"
fi
`)
	} else {
		script.WriteString(`if [ -f "$route_backup" ]; then
 cat "$route_backup" > "$route_value"
 rm -f "$route_backup"
fi
`)
	}
	path := base + ".sh"
	files[path] = []byte(script.String())
	return path, files, nil
}
