package waf

import "time"

// GlobalConfig 存储在 panel.db 的 waf_config 表（JSON）。
type GlobalConfig struct {
	Enabled            bool     `json:"enabled"`
	Mode               string   `json:"mode"`               // block | observe
	CCEnabled          bool     `json:"ccEnabled"`          // CC 限频总开关
	CCWindowSeconds    int      `json:"ccWindowSeconds"`    // 统计窗口（秒）
	CCMaxRequests      int      `json:"ccMaxRequests"`      // 窗口内最大请求数
	CCAction           string   `json:"ccAction"`           // block | observe | challenge
	AutoBanEnabled     bool     `json:"autoBanEnabled"`     // 高频拦截自动封禁
	AutoBanWindow      int      `json:"autoBanWindow"`      // 触发窗口（秒）
	AutoBanThreshold   int      `json:"autoBanThreshold"`   // 窗口内命中次数阈值
	AutoBanDuration    int      `json:"autoBanDuration"`    // 自动封禁时长（秒）
	SyncFirewall       bool     `json:"syncFirewall"`       // 自动封禁是否同步至系统防火墙
	RetentionDays      int      `json:"retentionDays"`      // 攻击日志保留天数
	AllowIPs           []string `json:"allowIPs"`           // 全局 IP/CIDR 白名单
	DenyIPs            []string `json:"denyIPs"`            // 全局 IP/CIDR 黑名单
	AllowPaths         []string `json:"allowPaths"`         // 全局 URL 前缀白名单
	BlockHTML          string   `json:"blockHtml"`          // 自定义拦截页 HTML（留空使用默认）
	DisabledRuleGroups []string `json:"disabledRuleGroups"` // 全局关闭的规则分组
}

// DefaultGlobalConfig 返回默认全局 WAF 配置。
func DefaultGlobalConfig() GlobalConfig {
	return GlobalConfig{
		Enabled:            false,
		Mode:               "block",
		CCEnabled:          true,
		CCWindowSeconds:    60,
		CCMaxRequests:      120,
		CCAction:           "block",
		AutoBanEnabled:     true,
		AutoBanWindow:      300,
		AutoBanThreshold:   10,
		AutoBanDuration:    1800,
		SyncFirewall:       false,
		RetentionDays:      30,
		AllowIPs:           []string{},
		DenyIPs:            []string{},
		AllowPaths:         []string{},
		DisabledRuleGroups: []string{},
	}
}

type wafConfigRow struct {
	ID        int       `gorm:"column:id;primaryKey"`
	Data      string    `gorm:"column:data"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (wafConfigRow) TableName() string { return "waf_config" }

type BuiltinOverride struct {
	RuleID    string    `json:"ruleId" gorm:"column:rule_id;primaryKey"`
	Enabled   bool      `json:"enabled" gorm:"column:enabled"`
	Action    string    `json:"action" gorm:"column:action"`
	UpdatedAt time.Time `json:"updatedAt" gorm:"column:updated_at"`
}

func (BuiltinOverride) TableName() string { return "waf_builtin_overrides" }

type CustomRule struct {
	ID            string    `json:"id" gorm:"column:id;primaryKey"`
	Name          string    `json:"name" gorm:"column:name"`
	SiteID        string    `json:"siteId" gorm:"column:site_id"`
	Enabled       bool      `json:"enabled" gorm:"column:enabled"`
	Priority      int       `json:"priority" gorm:"column:priority"`
	Action        string    `json:"action" gorm:"column:action"`               // block | observe | allow
	MatchField    string    `json:"matchField" gorm:"column:match_field"`      // uri | query | user_agent | method | ip | header | args
	MatchOperator string    `json:"matchOperator" gorm:"column:match_operator"` // contains | prefix | exact | regex
	MatchValue    string    `json:"matchValue" gorm:"column:match_value"`
	HeaderName    string    `json:"headerName" gorm:"column:header_name"`
	Remark        string    `json:"remark" gorm:"column:remark"`
	CreatedAt     time.Time `json:"createdAt" gorm:"column:created_at"`
	UpdatedAt     time.Time `json:"updatedAt" gorm:"column:updated_at"`
}

func (CustomRule) TableName() string { return "waf_custom_rules" }

type SitePolicy struct {
	SiteID          string    `json:"siteId" gorm:"column:site_id;primaryKey"`
	InheritGlobal   bool      `json:"inheritGlobal" gorm:"column:inherit_global"`
	Mode            string    `json:"mode" gorm:"column:mode"` // off | observe | block
	CCEnabled       bool      `json:"ccEnabled" gorm:"column:cc_enabled"`
	CCWindowSeconds int       `json:"ccWindowSeconds" gorm:"column:cc_window_seconds"`
	CCMaxRequests   int       `json:"ccMaxRequests" gorm:"column:cc_max_requests"`
	CCAction        string    `json:"ccAction" gorm:"column:cc_action"`
	AllowIPs        []string  `json:"allowIPs" gorm:"column:allow_ips;serializer:json"`
	DenyIPs         []string  `json:"denyIPs" gorm:"column:deny_ips;serializer:json"`
	AllowPaths      []string  `json:"allowPaths" gorm:"column:allow_paths;serializer:json"`
	DenyPaths       []string  `json:"denyPaths" gorm:"column:deny_paths;serializer:json"`
	DisabledGroups  []string  `json:"disabledGroups" gorm:"column:disabled_groups;serializer:json"`
	UpdatedAt       time.Time `json:"updatedAt" gorm:"column:updated_at"`
}

func (SitePolicy) TableName() string { return "waf_site_policies" }

type Exemption struct {
	ID        string    `json:"id" gorm:"column:id;primaryKey"`
	SiteID    string    `json:"siteId" gorm:"column:site_id"`
	RuleID    string    `json:"ruleId" gorm:"column:rule_id"`
	URI       string    `json:"uri" gorm:"column:uri"`
	Remark    string    `json:"remark" gorm:"column:remark"`
	CreatedAt time.Time `json:"createdAt" gorm:"column:created_at"`
}

func (Exemption) TableName() string { return "waf_exemptions" }

// ---- 以下模型存储在独立 waf.db ----

type Event struct {
	ID          int64  `json:"id" gorm:"column:id;primaryKey;autoIncrement"`
	OccurredAt  int64  `json:"occurredAt" gorm:"column:occurred_at"`
	SiteID      string `json:"siteId" gorm:"column:site_id"`
	Domain      string `json:"domain" gorm:"column:domain"`
	ClientIP    string `json:"clientIP" gorm:"column:client_ip"`
	Location    string `json:"location" gorm:"column:location"`
	Method      string `json:"method" gorm:"column:method"`
	URI         string `json:"uri" gorm:"column:uri"`
	QueryString string `json:"queryString" gorm:"column:query_string"`
	UserAgent   string `json:"userAgent" gorm:"column:user_agent"`
	RuleGroup   string `json:"ruleGroup" gorm:"column:rule_group"`
	RuleID      string `json:"ruleId" gorm:"column:rule_id"`
	RuleName    string `json:"ruleName" gorm:"column:rule_name"`
	Action      string `json:"action" gorm:"column:action"` // block | observe | challenge
	MatchedData string `json:"matchedData" gorm:"column:matched_data"`
	StatusCode  int    `json:"statusCode" gorm:"column:status_code"`
}

func (Event) TableName() string { return "waf_events" }

type Ban struct {
	ID        int64  `json:"id" gorm:"column:id;primaryKey;autoIncrement"`
	IP        string `json:"ip" gorm:"column:ip"`
	SiteID    string `json:"siteId" gorm:"column:site_id"`
	Location  string `json:"location" gorm:"column:location"`
	Reason    string `json:"reason" gorm:"column:reason"`
	Source    string `json:"source" gorm:"column:source"` // auto | manual
	NFTSynced bool   `json:"nftSynced" gorm:"column:nft_synced"`
	ExpiresAt int64  `json:"expiresAt" gorm:"column:expires_at"` // 0 表示永久
	CreatedAt int64  `json:"createdAt" gorm:"column:created_at"`
}

func (Ban) TableName() string { return "waf_bans" }

type HourlyStat struct {
	HourTS        int64  `json:"hourTs" gorm:"column:hour_ts;primaryKey"`
	SiteID        string `json:"siteId" gorm:"column:site_id;primaryKey"`
	RuleGroup     string `json:"ruleGroup" gorm:"column:rule_group;primaryKey"`
	BlockedCount  int64  `json:"blockedCount" gorm:"column:blocked_count"`
	ObservedCount int64  `json:"observedCount" gorm:"column:observed_count"`
}

func (HourlyStat) TableName() string { return "waf_hourly_stats" }

type logOffset struct {
	FilePath string `gorm:"column:file_path;primaryKey"`
	Inode    uint64 `gorm:"column:inode"`
	Offset   int64  `gorm:"column:offset"`
}

func (logOffset) TableName() string { return "waf_log_offsets" }
