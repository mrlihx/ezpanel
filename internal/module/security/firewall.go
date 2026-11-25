package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/geoip"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	gnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

type Rule struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Protocol   string     `json:"protocol"`
	Ports      string     `json:"ports"`
	Peer       string     `json:"peer"`
	Policy     string     `json:"policy"`
	Direction  string     `json:"direction"`
	TargetIP   string     `json:"targetIP"`
	TargetPort int        `json:"targetPort"`
	Country    string     `json:"country"`
	Remark     string     `json:"remark"`
	Origin     string     `json:"origin"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
}
type firewallRow struct {
	Rule
	ReadOnly bool   `json:"readOnly"`
	Detail   string `json:"detail,omitempty"`
	Location string `json:"location,omitempty"`
}

type firewallState struct {
	Enabled        bool   `json:"enabled"`
	BlockPing      bool   `json:"blockPing"`
	ScanProtection bool   `json:"scanProtection"`
	TrustedIP      string `json:"trustedIP"`
	Seeded         bool   `json:"seeded,omitempty"`
	Rules          []Rule `json:"rules"`
}

func (m *Module) firewallPath() string {
	return filepath.Join(m.cfg.DataDir(), "security", "firewall.json")
}
func (m *Module) readFirewall() (firewallState, error) {
	state := firewallState{Rules: []Rule{}}
	data, err := os.ReadFile(m.firewallPath())
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(data, &state)
	for i := range state.Rules {
		row := &state.Rules[i]
		if row.Origin == "" {
			row.Origin = "manual"
			if row.Kind == "port" && row.Remark == "网站端口自动放行" {
				row.Origin, row.Remark = "website", ""
			}
		}
	}
	return state, err
}

func (m *Module) WithWebsitePorts(ctx context.Context, ports []int, commit func() error) error {
	if len(ports) == 0 {
		return commit()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	previous, err := m.readFirewall()
	if err != nil {
		return err
	}
	next := previous
	now := time.Now().UTC()
	for _, port := range ports {
		allowed := false
		for _, row := range next.Rules {
			if row.Kind != "port" || !row.Enabled || row.Direction == "out" || row.Peer != "any" || row.Protocol == "udp" ||
				(row.ExpiresAt != nil && !row.ExpiresAt.After(now)) || !ruleCoversPort(row.Ports, port) {
				continue
			}
			if row.Policy == "deny" && previous.Enabled {
				return fmt.Errorf("系统防火墙已拒绝 TCP %d，请先修改对应规则", port)
			}
			if row.Policy == "allow" && row.ExpiresAt == nil {
				allowed = true
			}
		}
		if !allowed {
			if len(next.Rules) >= 1000 {
				return errors.New("系统防火墙已达到1000条规则，无法自动放行网站端口")
			}
			next.Rules = append(next.Rules, Rule{
				ID: store.ID(), Kind: "port", Protocol: "tcp", Ports: strconv.Itoa(port),
				Peer: "any", Policy: "allow", Direction: "in", Enabled: true, CreatedAt: now,
				Origin: "website",
			})
		}
	}
	if len(next.Rules) == len(previous.Rules) {
		return commit()
	}
	save := func(ctx context.Context, state firewallState) error {
		if state.Enabled {
			return m.applyFirewall(ctx, state, state.TrustedIP)
		}
		data, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		return write(m.firewallPath(), data, 0600)
	}
	if err = save(ctx, next); err != nil {
		return fmt.Errorf("自动放行网站端口失败：%w", err)
	}
	if err = commit(); err != nil {
		if rollbackErr := save(context.WithoutCancel(ctx), previous); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("网站端口规则恢复失败：%w", rollbackErr))
		}
		return err
	}
	return nil
}

func (m *Module) RefreshPanelPort(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readFirewall()
	if err != nil || !state.Enabled {
		return err
	}
	return m.applyFirewall(ctx, state, state.TrustedIP)
}

// BlockIPFromWAF 供 WAF 模块将高频攻击 IP 下沉至系统防火墙。
func (m *Module) BlockIPFromWAF(ctx context.Context, ip, remark string, expiresAt *time.Time) error {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || addr.Zone() != "" {
		return errors.New("IP 地址无效")
	}
	peer := addr.Unmap().String()
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readFirewall()
	if err != nil {
		return err
	}
	if state.TrustedIP != "" && state.TrustedIP == peer {
		return errors.New("不能封禁面板受信任 IP")
	}
	now := time.Now().UTC()
	updated := false
	for i := range state.Rules {
		r := &state.Rules[i]
		if r.Origin == "waf" && r.Kind == "ip" && r.Peer == peer && r.Policy == "deny" {
			r.Enabled = true
			r.ExpiresAt = expiresAt
			if remark != "" {
				r.Remark = remark
			}
			updated = true
			break
		}
	}
	if !updated {
		if len(state.Rules) >= 1000 {
			return errors.New("系统防火墙规则已达上限")
		}
		state.Rules = append(state.Rules, Rule{
			ID:        store.ID(),
			Kind:      "ip",
			Protocol:  "both",
			Peer:      peer,
			Policy:    "deny",
			Direction: "in",
			Remark:    remark,
			Origin:    "waf",
			Enabled:   true,
			CreatedAt: now,
			ExpiresAt: expiresAt,
		})
	}
	if state.Enabled {
		return m.applyFirewall(ctx, state, state.TrustedIP)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return write(m.firewallPath(), data, 0600)
}

// UnblockIPFromWAF 供 WAF 模块移除由 WAF 自动下沉的系统防火墙封禁规则。
func (m *Module) UnblockIPFromWAF(ctx context.Context, ip string) error {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || addr.Zone() != "" {
		return nil
	}
	peer := addr.Unmap().String()
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readFirewall()
	if err != nil {
		return err
	}
	filtered := make([]Rule, 0, len(state.Rules))
	removed := false
	for _, r := range state.Rules {
		if r.Kind == "ip" && r.Peer == peer && r.Policy == "deny" && r.Origin == "waf" {
			removed = true
			continue
		}
		filtered = append(filtered, r)
	}
	if !removed {
		return nil
	}
	state.Rules = filtered
	if state.Enabled {
		return m.applyFirewall(ctx, state, state.TrustedIP)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return write(m.firewallPath(), data, 0600)
}

func ruleCoversPort(ports string, port int) bool {
	for part := range strings.SplitSeq(ports, ",") {
		start, end, hasRange := strings.Cut(part, "-")
		first, _ := strconv.Atoi(start)
		if hasRange {
			last, _ := strconv.Atoi(end)
			if first <= port && port <= last {
				return true
			}
		} else if first == port {
			return true
		}
	}
	return false
}

func (m *Module) firewall(c *gin.Context) {
	state, err := m.readFirewall()
	if err != nil {
		fail(c, err)
		return
	}
	_, available := executor.LookPath("nft")
	active := false
	if available == nil {
		tables, e := run(c.Request.Context(), "nft", "list", "tables")
		active = e == nil && strings.Contains(tables, "table inet ezpanel\n")
	}
	ports, err := sshPorts(c.Request.Context())
	if err != nil {
		ports = []int{22}
	}
	if !state.Seeded {
		m.mu.Lock()
		if fresh, rerr := m.readFirewall(); rerr == nil && !fresh.Seeded {
			m.seedManagementRules(&fresh, ports)
			if b, merr := json.MarshalIndent(fresh, "", "  "); merr == nil {
				_ = write(m.firewallPath(), b, 0600)
			}
			state = fresh
		}
		m.mu.Unlock()
	}
	listeners := []gin.H{}
	commands := map[int32]string{}
	connections, listenErr := gnet.ConnectionsWithContext(c.Request.Context(), "inet")
	if listenErr == nil {
		for _, row := range connections {
			if row.Status == "LISTEN" || row.Type == 2 {
				protocol := "tcp"
				if row.Type == 2 {
					protocol = "udp"
				}
				if _, known := commands[row.Pid]; !known {
					commands[row.Pid] = ""
					if p, e := process.NewProcessWithContext(c.Request.Context(), row.Pid); e == nil {
						commands[row.Pid], _ = p.CmdlineWithContext(c.Request.Context())
					}
				}
				listeners = append(listeners, gin.H{"protocol": protocol, "port": row.Laddr.Port, "address": row.Laddr.IP, "pid": row.Pid, "command": commands[row.Pid]})
			}
		}
	}
	external := []string{}
	if _, e := executor.LookPath("ufw"); e == nil {
		status, _ := run(c.Request.Context(), "ufw", "status")
		if strings.Contains(status, "Status: active") {
			external = append(external, "UFW")
		}
	}
	if result, _ := run(c.Request.Context(), "systemctl", "is-active", "firewalld.service"); strings.TrimSpace(result) == "active" {
		external = append(external, "firewalld")
	}
	display, notice := m.firewallRows(c.Request.Context(), state, ports, active)
	for i := range display {
		if display[i].Kind != "ip" {
			continue
		}
		var locations []string
		seen := map[string]bool{}
		for _, peer := range strings.Fields(display[i].Peer) {
			if prefix, err := netip.ParsePrefix(peer); err == nil && prefix.IsSingleIP() {
				peer = prefix.Addr().String()
			}
			if location := geoip.Location(peer); location != "" && !seen[location] {
				seen[location] = true
				locations = append(locations, location)
			}
		}
		display[i].Location = strings.Join(locations, "、")
	}
	response.OK(c, gin.H{"rules": display, "inventoryNotice": notice, "config": state, "active": active, "available": available == nil, "panelPort": m.cfg.Current().Port, "sshPorts": ports, "listeners": listeners, "listenersKnown": listenErr == nil, "externalFirewalls": external})
}
func validateRule(row *Rule) error {
	if len(row.Remark) > 500 {
		return errors.New("备注不能超过500字节")
	}
	if row.Kind != "port" && row.Kind != "ip" && row.Kind != "forward" && row.Kind != "region" {
		return errors.New("规则类型无效")
	}
	if row.Kind == "region" {
		row.Direction = "both"
		row.Peer = "any"
		row.Protocol = "both"
	}
	if row.Direction != "in" && row.Direction != "out" && row.Direction != "both" {
		return errors.New("方向必须为入站或出站")
	}
	if row.Policy != "allow" && row.Policy != "deny" {
		return errors.New("策略必须为放行或拒绝")
	}
	if row.Enabled && row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) {
		return errors.New("过期时间必须晚于当前时间")
	}
	if row.Peer == "" || row.Peer == "any" {
		row.Peer = "any"
	} else {
		values := strings.Fields(row.Peer)
		if len(values) == 0 || len(values) > 100 {
			return errors.New("每条规则支持1至100个IP或网段，每行一个")
		}
		normalized := []string{}
		seen := map[string]bool{}
		for _, value := range values {
			var prefix netip.Prefix
			if ip, e := netip.ParseAddr(value); e == nil {
				prefix = netip.PrefixFrom(ip, ip.BitLen())
			} else {
				var err error
				prefix, err = netip.ParsePrefix(value)
				if err != nil {
					return fmt.Errorf("IP或网段无效：%s", value)
				}
			}
			value = prefix.Masked().String()
			if !seen[value] {
				normalized = append(normalized, value)
				seen[value] = true
			}
		}
		row.Peer = strings.Join(normalized, "\n")
	}
	if row.Kind == "ip" && row.Peer == "any" {
		return errors.New("IP规则必须指定IP或网段")
	}
	if row.Kind == "region" {
		codes := strings.Split(strings.ToLower(row.Country), ",")
		if len(codes) < 1 || len(codes) > 250 {
			return errors.New("请选择有效国家/地区")
		}
		selected := []string{}
		seen := map[string]bool{}
		for _, code := range codes {
			code = strings.TrimSpace(code)
			if !regexp.MustCompile(`^[a-z]{2}$`).MatchString(code) {
				return errors.New("国家/地区代码无效")
			}
			if !seen[code] {
				selected = append(selected, code)
				seen[code] = true
			}
		}
		row.Country = strings.Join(selected, ",")
		if row.Ports == "" || row.Ports == "all" {
			row.Ports = "all"
			return nil
		}
	}
	if row.Kind == "ip" {
		return nil
	}
	if row.Protocol != "tcp" && row.Protocol != "udp" && row.Protocol != "both" {
		return errors.New("协议必须为TCP、UDP或TCP/UDP")
	}
	parts := strings.Split(strings.ReplaceAll(row.Ports, ":", "-"), ",")
	if len(parts) > 32 {
		return errors.New("每条最多32组端口")
	}
	for i, part := range parts {
		ends := strings.Split(strings.TrimSpace(part), "-")
		if len(ends) > 2 {
			return errors.New("端口格式无效")
		}
		start, last := 0, 0
		for j, end := range ends {
			n, err := strconv.Atoi(end)
			if err != nil || n < 1 || n > 65535 {
				return errors.New("端口须在1至65535之间")
			}
			if j == 0 {
				start = n
			}
			last = n
			ends[j] = strconv.Itoa(n)
		}
		if last < start {
			return errors.New("端口范围顺序无效")
		}
		parts[i] = strings.Join(ends, "-")
	}
	row.Ports = strings.Join(parts, ",")
	if row.Kind == "forward" {
		if len(parts) != 1 || strings.Contains(row.Ports, "-") {
			return errors.New("端口转发仅支持单个源端口")
		}
		if strings.TrimSpace(row.TargetIP) == "" {
			row.TargetIP = "127.0.0.1"
		}
		ip, err := netip.ParseAddr(row.TargetIP)
		if err != nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() && row.TargetIP != "127.0.0.1" {
			return errors.New("转发目标须为有效IP，本机转发请使用127.0.0.1")
		}
		if row.Peer != "any" {
			for _, peer := range strings.Fields(row.Peer) {
				prefix, _ := netip.ParsePrefix(peer)
				if prefix.Addr().Is6() != ip.Is6() {
					return errors.New("转发来源和目标IP必须使用相同地址族")
				}
			}
		}
		row.TargetIP = ip.String()
		if row.TargetPort < 1 || row.TargetPort > 65535 {
			return errors.New("目标端口无效")
		}
		row.Direction = "in"
		row.Policy = "allow"
	}
	return nil
}
func (m *Module) saveRule(c *gin.Context) {
	var row Rule
	if !response.Bind(c, &row) {
		return
	}
	row.Country = strings.ToLower(row.Country)
	if err := validateRule(&row); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readFirewall()
	if err != nil {
		fail(c, err)
		return
	}
	id := c.Param("id")
	if id == "" {
		if len(state.Rules) >= 1000 {
			response.Fail(c, response.Invalid, "最多1000条规则")
			return
		}
		row.ID = store.ID()
		row.CreatedAt = time.Now().UTC()
		row.Origin = "manual"
		state.Rules = append(state.Rules, row)
	} else {
		found := false
		for i, old := range state.Rules {
			if old.ID == id {
				row.ID = id
				row.CreatedAt = old.CreatedAt
				row.Origin = old.Origin
				state.Rules[i] = row
				found = true
				break
			}
		}
		if !found {
			response.Fail(c, response.NotFound, "规则不存在")
			return
		}
	}
	if row.Kind == "region" {
		for _, code := range strings.Split(row.Country, ",") {
			if err = m.cacheCountry(c.Request.Context(), code); err != nil {
				fail(c, err)
				return
			}
		}
	}
	if err = m.applyFirewall(c.Request.Context(), state, m.auth.ClientIP(c)); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"saved": true})
}
func (m *Module) deleteRule(c *gin.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readFirewall()
	if err != nil {
		fail(c, err)
		return
	}
	found := false
	rows := []Rule{}
	for _, row := range state.Rules {
		if row.ID == c.Param("id") {
			found = true
		} else {
			rows = append(rows, row)
		}
	}
	if !found {
		response.Fail(c, response.NotFound, "规则不存在")
		return
	}
	state.Rules = rows
	if err = m.applyFirewall(c.Request.Context(), state, m.auth.ClientIP(c)); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}
func (m *Module) firewallSettings(c *gin.Context) {
	var in struct {
		Enabled        bool `json:"enabled"`
		BlockPing      bool `json:"blockPing"`
		ScanProtection bool `json:"scanProtection"`
	}
	if !response.Bind(c, &in) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readFirewall()
	if err != nil {
		fail(c, err)
		return
	}
	if in.Enabled && !state.Seeded {
		ports, perr := sshPorts(c.Request.Context())
		if perr != nil {
			ports = []int{22}
		}
		m.seedManagementRules(&state, ports)
	}
	state.Enabled = in.Enabled
	state.BlockPing = in.BlockPing
	state.ScanProtection = in.ScanProtection
	if err = m.applyFirewall(c.Request.Context(), state, m.auth.ClientIP(c)); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"saved": true})
}
func (m *Module) importRules(c *gin.Context) {
	var in struct {
		Kind  string `json:"kind"`
		Rules []Rule `json:"rules"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Rules) == 0 || len(in.Rules) > 1000 {
		response.Fail(c, response.Invalid, "导入需要1至1000条规则")
		return
	}
	for i := range in.Rules {
		row := &in.Rules[i]
		if row.Kind != in.Kind {
			response.Fail(c, response.Invalid, "只能导入当前页面类型的规则，请使用对应页面的导出文件")
			return
		}
		row.Country = strings.ToLower(row.Country)
		if err := validateRule(row); err != nil {
			response.Fail(c, response.Invalid, fmt.Sprintf("第%d条：%s", i+1, err.Error()))
			return
		}
		row.ID = store.ID()
		row.CreatedAt = time.Now().UTC()
		row.Origin = "imported"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readFirewall()
	if err != nil {
		fail(c, err)
		return
	}
	if len(state.Rules)+len(in.Rules) > 1000 {
		response.Fail(c, response.Invalid, "导入后超出1000条规则")
		return
	}
	for _, row := range in.Rules {
		if row.Kind == "region" {
			for _, code := range strings.Split(row.Country, ",") {
				if err = m.cacheCountry(c.Request.Context(), code); err != nil {
					fail(c, err)
					return
				}
			}
		}
	}
	state.Rules = append(state.Rules, in.Rules...)
	if err = m.applyFirewall(c.Request.Context(), state, m.auth.ClientIP(c)); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"count": len(in.Rules)})
}

func (m *Module) batchRules(c *gin.Context) {
	var in struct {
		IDs    []string `json:"ids"`
		Action string   `json:"action"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.IDs) < 1 || len(in.IDs) > 1000 || (in.Action != "delete" && in.Action != "enable" && in.Action != "disable") {
		response.Fail(c, response.Invalid, "批量参数无效")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readFirewall()
	if err != nil {
		fail(c, err)
		return
	}
	ids := map[string]bool{}
	for _, id := range in.IDs {
		ids[id] = true
	}
	count := 0
	rows := []Rule{}
	for _, row := range state.Rules {
		if !ids[row.ID] {
			rows = append(rows, row)
			continue
		}
		count++
		if in.Action == "delete" {
			continue
		}
		if in.Action == "enable" && row.ExpiresAt != nil && !row.ExpiresAt.After(time.Now()) {
			response.Fail(c, response.Invalid, "选中规则已过期，请先修改过期时间")
			return
		}
		row.Enabled = in.Action == "enable"
		rows = append(rows, row)
	}
	if count != len(ids) {
		response.Fail(c, response.NotFound, "部分规则已不存在，请刷新后重试")
		return
	}
	state.Rules = rows
	if err = m.applyFirewall(c.Request.Context(), state, m.auth.ClientIP(c)); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"count": count})
}

func (m *Module) updateIPPools(c *gin.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readFirewall()
	if err != nil {
		fail(c, err)
		return
	}
	codes := map[string]bool{}
	for _, row := range state.Rules {
		if row.Kind == "region" {
			for _, code := range strings.Split(row.Country, ",") {
				codes[code] = true
			}
		}
	}
	if len(codes) == 0 {
		response.OK(c, gin.H{"count": 0})
		return
	}
	previous := map[string][]byte{}
	for code := range codes {
		data, e := os.ReadFile(m.countryPath(code))
		if e != nil && !os.IsNotExist(e) {
			fail(c, e)
			return
		}
		previous[code] = data
	}
	restore := func() {
		for code, data := range previous {
			if data == nil {
				_ = os.Remove(m.countryPath(code))
			} else {
				_ = write(m.countryPath(code), data, 0600)
			}
		}
	}
	for code := range codes {
		if err = m.cacheCountry(c.Request.Context(), code, true); err != nil {
			restore()
			fail(c, err)
			return
		}
	}
	if state.Enabled {
		if err = m.applyFirewall(c.Request.Context(), state, m.auth.ClientIP(c)); err != nil {
			restore()
			fail(c, err)
			return
		}
	}
	response.OK(c, gin.H{"count": len(codes)})
}

func (m *Module) seedManagementRules(state *firewallState, ssh []int) {
	now := time.Now().UTC()
	addIfMissing := func(port int, remark string) {
		if port < 1 || port > 65535 {
			return
		}
		for _, row := range state.Rules {
			if row.Kind == "port" && row.Direction == "in" && (row.Protocol == "tcp" || row.Protocol == "both") && ruleCoversPort(row.Ports, port) {
				return
			}
		}
		state.Rules = append(state.Rules, Rule{
			ID:        store.ID(),
			Kind:      "port",
			Protocol:  "tcp",
			Ports:     strconv.Itoa(port),
			Peer:      "any",
			Policy:    "allow",
			Direction: "in",
			Remark:    remark,
			Origin:    "system",
			Enabled:   true,
			CreatedAt: now,
		})
	}
	seen := map[int]bool{}
	for _, port := range ssh {
		if !seen[port] {
			addIfMissing(port, "SSH管理端口")
			seen[port] = true
		}
	}
	addIfMissing(m.cfg.Current().Port, "面板管理端口")
	state.Seeded = true
}

// SyncDatabasePort 在数据库实例切换“允许远程访问”或变更端口/删除时自动联动防火墙规则。
func (m *Module) SyncDatabasePort(ctx context.Context, app, instance string, port int, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.readFirewall()
	if err != nil {
		return err
	}
	if !state.Seeded {
		ssh, serr := sshPorts(ctx)
		if serr != nil {
			ssh = []int{22}
		}
		m.seedManagementRules(&state, ssh)
	}
	if app == "memcached" && enabled {
		return errors.New("Memcached 仅支持本机和容器内网访问")
	}
	origin := "database:" + app + ":" + instance
	label := strings.ToUpper(app)
	switch app {
	case "mysql":
		label = "MySQL"
	case "postgresql":
		label = "PostgreSQL"
	case "mongodb":
		label = "MongoDB"
	case "redis":
		label = "Redis"
	case "memcached":
		label = "Memcached"
	}
	remark := label + " (" + instance + ") 远程访问"

	found := false
	nextRules := make([]Rule, 0, len(state.Rules)+1)
	for _, row := range state.Rules {
		if row.Origin != origin {
			nextRules = append(nextRules, row)
			continue
		}
		found = true
		if !enabled {
			// 关闭远程访问或删除实例时，自动移除对应的端口放行规则
			continue
		}
		if port > 0 {
			row.Ports = strconv.Itoa(port)
		}
		row.Enabled = enabled
		if row.Remark == "" {
			row.Remark = remark
		}
		nextRules = append(nextRules, row)
	}
	if !found && enabled && port > 0 {
		nextRules = append(nextRules, Rule{
			ID:        store.ID(),
			Kind:      "port",
			Protocol:  "tcp",
			Ports:     strconv.Itoa(port),
			Peer:      "any",
			Policy:    "allow",
			Direction: "in",
			Remark:    remark,
			Origin:    origin,
			Enabled:   true,
			CreatedAt: time.Now().UTC(),
		})
	}
	if slices.Equal(state.Rules, nextRules) {
		return nil
	}
	state.Rules = nextRules
	if state.Enabled {
		return m.applyFirewall(ctx, state, state.TrustedIP)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return write(m.firewallPath(), data, 0600)
}
