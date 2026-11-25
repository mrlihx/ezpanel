package waf

import (
	"errors"
	"net/netip"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

var validHeaderName = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

func normalizeIPOrCIDRList(list []string, maxLen int) ([]string, error) {
	if len(list) > maxLen {
		return nil, errors.New("IP/网段名单条数超过上限")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, raw := range list {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		var normalized string
		if strings.Contains(item, "/") {
			prefix, err := netip.ParsePrefix(item)
			if err != nil || !prefix.Addr().Is4() {
				return nil, errors.New("网段仅支持标准 IPv4 CIDR（如 192.168.1.0/24）")
			}
			normalized = prefix.Masked().String()
		} else {
			addr, err := netip.ParseAddr(item)
			if err != nil || addr.Zone() != "" {
				return nil, errors.New("IP 名单每行须填写有效的 IPv4、IPv6 或 IPv4 CIDR")
			}
			normalized = addr.Unmap().String()
		}
		if !seen[normalized] {
			seen[normalized] = true
			out = append(out, normalized)
		}
	}
	return out, nil
}

func normalizePathList(list []string, maxLen int, allowRoot bool) ([]string, error) {
	if len(list) > maxLen {
		return nil, errors.New("路径列表超过上限")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(list))
	for _, raw := range list {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if len(v) > 256 || !strings.HasPrefix(v, "/") || strings.ContainsAny(v, "?#\\") ||
			strings.IndexFunc(v, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
			return nil, errors.New("路径须为 / 开头的有效 URL 路径")
		}
		cleaned := path.Clean(v)
		if !allowRoot && cleaned == "/" {
			return nil, errors.New("拦截路径不能为根路径 /")
		}
		if !seen[cleaned] {
			seen[cleaned] = true
			out = append(out, cleaned)
		}
	}
	return out, nil
}

func validateGlobalConfig(cfg *GlobalConfig) error {
	if !slices.Contains([]string{"block", "observe"}, cfg.Mode) {
		return errors.New("防护模式仅支持 block 或 observe")
	}
	if !slices.Contains([]string{"block", "observe", "challenge"}, cfg.CCAction) {
		return errors.New("CC 处置动作仅支持 block、observe 或 challenge")
	}
	if cfg.CCWindowSeconds < 1 || cfg.CCWindowSeconds > 3600 {
		return errors.New("CC 统计窗口须在 1 到 3600 秒之间")
	}
	if cfg.CCMaxRequests < 1 || cfg.CCMaxRequests > 100000 {
		return errors.New("CC 最大请求数须在 1 到 100000 之间")
	}
	if cfg.AutoBanWindow < 10 || cfg.AutoBanWindow > 86400 {
		return errors.New("自动封禁检测窗口须在 10 到 86400 秒之间")
	}
	if cfg.AutoBanThreshold < 1 || cfg.AutoBanThreshold > 10000 {
		return errors.New("自动封禁阈值须在 1 到 10000 之间")
	}
	if cfg.AutoBanDuration < 0 || cfg.AutoBanDuration > 86400*365 {
		return errors.New("自动封禁时长无效")
	}
	if cfg.RetentionDays < 1 || cfg.RetentionDays > 365 {
		return errors.New("日志保留天数须在 1 到 365 天之间")
	}
	if len(cfg.BlockHTML) > 32768 {
		return errors.New("自定义拦截页 HTML 不能超过 32KB")
	}
	var err error
	if cfg.AllowIPs, err = normalizeIPOrCIDRList(cfg.AllowIPs, 512); err != nil {
		return err
	}
	if cfg.DenyIPs, err = normalizeIPOrCIDRList(cfg.DenyIPs, 512); err != nil {
		return err
	}
	if cfg.AllowPaths, err = normalizePathList(cfg.AllowPaths, 256, true); err != nil {
		return err
	}
	cfg.DisabledRuleGroups = nonNilSlice(cfg.DisabledRuleGroups)
	return nil
}

func validateSitePolicy(p *SitePolicy) error {
	if !slices.Contains([]string{"off", "observe", "block"}, p.Mode) {
		return errors.New("站点防护模式仅支持 off、observe 或 block")
	}
	if !slices.Contains([]string{"block", "observe", "challenge"}, p.CCAction) {
		return errors.New("CC 处置动作无效")
	}
	if p.CCWindowSeconds < 1 || p.CCWindowSeconds > 3600 || p.CCMaxRequests < 1 || p.CCMaxRequests > 100000 {
		return errors.New("站点 CC 参数超出范围")
	}
	var err error
	if p.AllowIPs, err = normalizeIPOrCIDRList(p.AllowIPs, 256); err != nil {
		return err
	}
	if p.DenyIPs, err = normalizeIPOrCIDRList(p.DenyIPs, 256); err != nil {
		return err
	}
	if p.AllowPaths, err = normalizePathList(p.AllowPaths, 256, true); err != nil {
		return err
	}
	if p.DenyPaths, err = normalizePathList(p.DenyPaths, 256, false); err != nil {
		return err
	}
	p.DisabledGroups = nonNilSlice(p.DisabledGroups)
	return nil
}

func validateCustomRule(r *CustomRule) error {
	r.Name = strings.TrimSpace(r.Name)
	r.MatchValue = strings.TrimSpace(r.MatchValue)
	r.HeaderName = strings.TrimSpace(r.HeaderName)
	r.Remark = strings.TrimSpace(r.Remark)
	if r.Name == "" || len(r.Name) > 64 {
		return errors.New("规则名称不能为空且不超过 64 字符")
	}
	if !slices.Contains([]string{"block", "observe", "allow"}, r.Action) {
		return errors.New("动作仅支持 block、observe 或 allow")
	}
	if !slices.Contains([]string{"uri", "query", "args", "user_agent", "method", "ip", "header"}, r.MatchField) {
		return errors.New("匹配字段无效")
	}
	if !slices.Contains([]string{"contains", "prefix", "exact", "regex"}, r.MatchOperator) {
		return errors.New("匹配操作符无效")
	}
	if r.MatchValue == "" || len(r.MatchValue) > 512 {
		return errors.New("匹配内容不能为空且不超过 512 字符")
	}
	if r.MatchField == "header" && !validHeaderName.MatchString(r.HeaderName) {
		return errors.New("请求头名称仅支持字母、数字和连字符")
	}
	if r.MatchOperator == "regex" {
		if _, err := regexp.Compile(r.MatchValue); err != nil {
			return errors.New("正则表达式语法无效")
		}
	}
	if r.Priority < 1 || r.Priority > 9999 {
		r.Priority = 100
	}
	return nil
}
