package toolbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/response"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

const netplanBus = "io.netplan.Netplan"
const ipTrySeconds = 120

type ipTrial struct {
	path     string
	Deadline time.Time `json:"deadline"`
}

type ipFamilySettings struct {
	Enabled   *bool    `json:"enabled,omitempty"`
	DHCP      bool     `json:"dhcp"`
	Addresses []string `json:"addresses"`
	Gateway   string   `json:"gateway"`
	AcceptRA  *bool    `json:"acceptRA"`
}

type netplanEthernet struct {
	SetName   string           `yaml:"set-name"`
	DHCP4     bool             `yaml:"dhcp4"`
	DHCP6     bool             `yaml:"dhcp6"`
	AcceptRA  *bool            `yaml:"accept-ra"`
	LinkLocal []string         `yaml:"link-local"`
	Addresses []any            `yaml:"addresses"`
	Gateway4  string           `yaml:"gateway4"`
	Gateway6  string           `yaml:"gateway6"`
	Routes    []map[string]any `yaml:"routes"`
}

func netplanCall[T string | bool](ctx context.Context, path, method string, args ...string) (T, error) {
	iface := netplanBus + ".Config"
	if path == "/io/netplan/Netplan" {
		iface = netplanBus
	}
	var result T
	out, err := command(ctx, "busctl", append([]string{"--system", "--json=short", "--timeout=25s", "call", netplanBus, path, iface, method}, args...)...)
	if err != nil {
		return result, err
	}
	var reply struct {
		Data []T `json:"data"`
	}
	if json.Unmarshal([]byte(out), &reply) != nil || len(reply.Data) != 1 || any(reply.Data[0]) == false {
		return result, fmt.Errorf("Netplan %s 操作失败", method)
	}
	return reply.Data[0], nil
}

func defaultRouteFamily(route map[string]any) int {
	if table, ok := route["table"]; ok && table != 254 {
		return 0
	}
	switch route["to"] {
	case "default", "0.0.0.0/0", "::/0":
		via, _ := route["via"].(string)
		if addr, err := netip.ParseAddr(via); err == nil {
			return addr.BitLen()
		}
	}
	return 0
}

func addressText(value any) string {
	switch addr := value.(type) {
	case string:
		return addr
	case map[string]any:
		for name := range addr {
			return name
		}
	}
	return ""
}

func (n netplanEthernet) family(bits int) ipFamilySettings {
	settings := ipFamilySettings{DHCP: n.DHCP4, Gateway: n.Gateway4, Addresses: []string{}}
	if bits == 128 {
		settings.DHCP, settings.Gateway, settings.AcceptRA = n.DHCP6, n.Gateway6, n.AcceptRA
	}
	for _, value := range n.Addresses {
		if addr, err := netip.ParsePrefix(addressText(value)); err == nil && addr.Addr().BitLen() == bits {
			settings.Addresses = append(settings.Addresses, addr.String())
		}
	}
	for _, route := range n.Routes {
		if settings.Gateway == "" && defaultRouteFamily(route) == bits {
			settings.Gateway, _ = route["via"].(string)
		}
	}
	if gateway, err := netip.ParseAddr(settings.Gateway); err == nil {
		settings.Gateway = gateway.String()
	}
	if bits == 128 {
		enabled := n.LinkLocal == nil || slices.Contains(n.LinkLocal, "ipv6") || n.DHCP6 ||
			n.AcceptRA == nil || *n.AcceptRA || len(settings.Addresses) > 0 || settings.Gateway != ""
		settings.Enabled = &enabled
	}
	return settings
}

func (m *Module) readIP(c *gin.Context) {
	for _, name := range []string{"netplan", "busctl"} {
		if _, err := executor.LookPath(name); err != nil {
			response.OK(c, gin.H{"supported": false})
			return
		}
	}
	m.ipMu.Lock()
	defer m.ipMu.Unlock()
	out, err := command(c.Request.Context(), "netplan", "get", "ethernets")
	if err != nil {
		fail(c, err)
		return
	}
	var ethernets map[string]netplanEthernet
	if err := yaml.Unmarshal([]byte(out), &ethernets); err != nil {
		fail(c, err)
		return
	}
	list := []gin.H{}
	for id, nic := range ethernets {
		name := id
		if nic.SetName != "" {
			name = nic.SetName
		}
		iface, err := net.InterfaceByName(name)
		if err != nil || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		current := []string{}
		addresses, _ := iface.Addrs()
		for _, addr := range addresses {
			current = append(current, addr.String())
		}
		list = append(list, gin.H{"id": id, "name": name, "current": current, "ipv4": nic.family(32), "ipv6": nic.family(128)})
	}
	sort.Slice(list, func(i, j int) bool { return list[i]["name"].(string) < list[j]["name"].(string) })
	if m.ipTry != nil && time.Now().After(m.ipTry.Deadline) {
		m.ipTry = nil
	}
	response.OK(c, gin.H{"supported": true, "list": list, "trial": m.ipTry})
}

func (m *Module) tryIP(c *gin.Context) {
	var in struct {
		Interface string `json:"interface"`
		Family    int    `json:"family"`
		ipFamilySettings
	}
	if !response.Bind(c, &in) {
		return
	}
	bits := 32
	switch in.Family {
	case 4:
	case 6:
		bits = 128
	default:
		response.Fail(c, response.Invalid, "请选择 IPv4 或 IPv6")
		return
	}
	disableIPv6 := bits == 128 && in.Enabled != nil && !*in.Enabled
	if disableIPv6 {
		in.DHCP, in.AcceptRA, in.Addresses, in.Gateway = false, new(bool), nil, ""
	}
	addresses := []string{}
	for _, value := range in.Addresses {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		a := prefix.Addr()
		if err != nil || a.BitLen() != bits || a.Is4In6() || a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() {
			response.Fail(c, response.Invalid, "请填写有效的 IP 地址及前缀长度，且与当前协议一致")
			return
		}
		if p := prefix.String(); !slices.Contains(addresses, p) {
			addresses = append(addresses, p)
		}
	}
	in.Gateway = strings.TrimSpace(in.Gateway)
	if in.Gateway != "" {
		gateway, err := netip.ParseAddr(in.Gateway)
		if err != nil || gateway.BitLen() != bits || gateway.Zone() != "" || gateway.Is4In6() || gateway.IsLoopback() || gateway.IsUnspecified() || gateway.IsMulticast() {
			response.Fail(c, response.Invalid, "默认网关须为同一协议的有效 IP 地址")
			return
		}
		in.Gateway = gateway.String()
	}
	m.ipMu.Lock()
	defer m.ipMu.Unlock()
	if m.ipTry != nil && time.Now().Before(m.ipTry.Deadline) {
		response.Fail(c, response.Invalid, "请先确认或取消当前 IP 试用")
		return
	}
	ctx := context.WithoutCancel(c.Request.Context())
	path, err := netplanCall[string](ctx, "/io/netplan/Netplan", "Config")
	if err != nil {
		fail(c, err)
		return
	}
	trial := false
	defer func() {
		if !trial {
			_, _ = netplanCall[bool](ctx, path, "Cancel")
		}
	}()
	out, err := netplanCall[string](ctx, path, "Get")
	if err != nil {
		fail(c, err)
		return
	}
	var config struct {
		Network struct {
			Ethernets map[string]netplanEthernet
		}
	}
	if err := yaml.Unmarshal([]byte(out), &config); err != nil {
		fail(c, err)
		return
	}
	nic, ok := config.Network.Ethernets[in.Interface]
	if !ok {
		response.Fail(c, response.Invalid, "请选择由 Netplan 管理的有线网卡")
		return
	}
	family := fmt.Sprint(in.Family)
	updates := map[string]any{"dhcp" + family: in.DHCP}
	remove := map[string]any{}
	old := nic.family(bits)
	if !slices.Equal(old.Addresses, addresses) {
		var values []any
		for _, value := range nic.Addresses {
			addr, err := netip.ParsePrefix(addressText(value))
			if err != nil || addr.Addr().BitLen() != bits {
				values = append(values, value)
			}
		}
		for _, addr := range addresses {
			index := slices.IndexFunc(nic.Addresses, func(value any) bool { return addressText(value) == addr })
			if index >= 0 {
				values = append(values, nic.Addresses[index])
			} else {
				values = append(values, addr)
			}
		}
		remove["addresses"] = nil
		if len(values) > 0 {
			updates["addresses"] = values
		}
	}
	if old.Gateway != in.Gateway {
		var routes []map[string]any
		for _, route := range nic.Routes {
			if defaultRouteFamily(route) != bits {
				routes = append(routes, route)
			}
		}
		if in.Gateway != "" {
			routes = append(routes, map[string]any{"to": "default", "via": in.Gateway})
		}
		remove["routes"], remove["gateway"+family] = nil, nil
		if len(routes) > 0 {
			updates["routes"] = routes
		}
	}
	if bits == 128 {
		if in.Enabled != nil && (disableIPv6 || !*old.Enabled) {
			linkLocal := []string{}
			for _, family := range nic.LinkLocal {
				if family != "ipv6" {
					linkLocal = append(linkLocal, family)
				}
			}
			if !disableIPv6 {
				linkLocal = append(linkLocal, "ipv6")
			}
			remove["link-local"] = nil
			updates["link-local"] = linkLocal
		}
		if in.AcceptRA == nil && nic.AcceptRA != nil {
			remove["accept-ra"] = nil
		} else if in.AcceptRA != nil {
			updates["accept-ra"] = *in.AcceptRA
		}
	}
	key := "ethernets." + strings.ReplaceAll(in.Interface, ".", "\\.") + "="
	// Netplan 合并地址和路由序列，先在试用副本中清除旧值再写入完整列表。
	for _, patch := range []map[string]any{remove, updates} {
		if len(patch) == 0 {
			continue
		}
		data, err := json.Marshal(patch)
		if err == nil {
			_, err = netplanCall[bool](ctx, path, "Set", "ss", key+string(data), "")
		}
		if err != nil {
			fail(c, err)
			return
		}
	}
	deadline := time.Now().Add(ipTrySeconds * time.Second)
	if _, err := netplanCall[bool](ctx, path, "Try", "u", fmt.Sprint(ipTrySeconds)); err != nil {
		fail(c, err)
		return
	}
	trial = true
	m.ipTry = &ipTrial{path: path, Deadline: deadline}
	response.OK(c, m.ipTry)
}

func (m *Module) finishIP(c *gin.Context) {
	method := "Cancel"
	switch c.Param("action") {
	case "confirm":
		method = "Apply"
	case "cancel":
	default:
		response.Fail(c, response.NotFound, "操作不存在")
		return
	}
	m.ipMu.Lock()
	defer m.ipMu.Unlock()
	if m.ipTry == nil || time.Now().After(m.ipTry.Deadline) {
		m.ipTry = nil
		response.Fail(c, response.Invalid, "IP 设置试用已结束")
		return
	}
	if _, err := netplanCall[bool](context.WithoutCancel(c.Request.Context()), m.ipTry.path, method); err != nil {
		fail(c, err)
		return
	}
	m.ipTry = nil
	response.OK(c, nil)
}
