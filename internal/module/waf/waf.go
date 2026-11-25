package waf

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"ezpanel/internal/config"
	"ezpanel/internal/geoip"
	"ezpanel/internal/module/security"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Module struct {
	db              *gorm.DB
	wafDB           *gorm.DB
	cfg             config.Config
	packages        *pkgmgr.Manager
	firewall        *security.Module
	syncMu          sync.Mutex
	challengeSecret string
}

func New(db *gorm.DB, cfg config.Config, packages *pkgmgr.Manager, firewall *security.Module) (*Module, error) {
	wafDB, err := openWAFDB(filepath.Join(cfg.DataDir(), "waf.db"))
	if err != nil {
		return nil, err
	}
	return &Module{
		db:              db,
		wafDB:           wafDB,
		cfg:             cfg,
		packages:        packages,
		firewall:        firewall,
		challengeSecret: randomHex(16),
	}, nil
}

func (m *Module) Close() {
	if m.wafDB != nil {
		if sqlDB, err := m.wafDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

func (m *Module) Name() string { return "waf" }

func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/waf")
	r.GET("/overview", m.overview)
	r.GET("/config", m.getConfig)
	r.PUT("/config", m.saveConfig)
	r.POST("/engine/sync", m.syncEngineHandler)
	r.GET("/rules", m.listRules)
	r.PUT("/rules/builtin/:id", m.updateBuiltinRule)
	r.POST("/rules/custom", m.createCustomRule)
	r.PUT("/rules/custom/:id", m.updateCustomRule)
	r.DELETE("/rules/custom/:id", m.deleteCustomRule)
	r.GET("/sites", m.listSites)
	r.GET("/sites/:id", m.getSitePolicy)
	r.PUT("/sites/:id", m.updateSitePolicy)
	r.GET("/bans", m.listBans)
	r.POST("/bans", m.createBan)
	r.DELETE("/bans/:id", m.deleteBan)
	r.POST("/bans/:id/firewall", m.syncBanToFirewall)
	r.GET("/events", m.listEvents)
	r.DELETE("/events", m.clearEvents)
	r.POST("/events/:id/allow", m.allowFromEvent)
	r.GET("/exemptions", m.listExemptions)
	r.DELETE("/exemptions/:id", m.deleteExemption)
}

func (m *Module) loadGlobalConfig(ctx context.Context) (GlobalConfig, error) {
	cfg := DefaultGlobalConfig()
	var row wafConfigRow
	err := m.db.WithContext(ctx).First(&row, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if row.Data != "" && row.Data != "{}" {
		if err := json.Unmarshal([]byte(row.Data), &cfg); err != nil {
			return cfg, err
		}
	}
	cfg.AllowIPs = nonNilSlice(cfg.AllowIPs)
	cfg.DenyIPs = nonNilSlice(cfg.DenyIPs)
	cfg.AllowPaths = nonNilSlice(cfg.AllowPaths)
	cfg.DisabledRuleGroups = nonNilSlice(cfg.DisabledRuleGroups)
	return cfg, nil
}

func (m *Module) mergedBuiltinRules(ctx context.Context) ([]BuiltinRule, error) {
	rules := defaultBuiltinRules()
	var overrides []BuiltinOverride
	if err := m.db.WithContext(ctx).Find(&overrides).Error; err != nil {
		return nil, err
	}
	om := make(map[string]BuiltinOverride, len(overrides))
	for _, o := range overrides {
		om[o.RuleID] = o
	}
	for i := range rules {
		if o, ok := om[rules[i].ID]; ok {
			rules[i].Enabled = o.Enabled
			if o.Action == "block" || o.Action == "observe" {
				rules[i].Action = o.Action
			}
		}
	}
	return rules, nil
}

func (m *Module) overview(c *gin.Context) {
	ctx := c.Request.Context()
	span := c.DefaultQuery("range", "24h")
	hours := 24
	if span == "7d" {
		hours = 24 * 7
	}
	now := time.Now()
	nowUnix := now.Unix()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	sevenDaysStart := nowUnix - 7*86400
	trendStart := ((nowUnix / 3600) - int64(hours-1)) * 3600

	var todayBlocked, todayObserved, weekBlocked, activeBans int64
	_ = m.wafDB.WithContext(ctx).Model(&Event{}).Where("occurred_at >= ? AND action != 'observe'", todayStart).Count(&todayBlocked).Error
	_ = m.wafDB.WithContext(ctx).Model(&Event{}).Where("occurred_at >= ? AND action = 'observe'", todayStart).Count(&todayObserved).Error
	_ = m.wafDB.WithContext(ctx).Model(&Event{}).Where("occurred_at >= ? AND action != 'observe'", sevenDaysStart).Count(&weekBlocked).Error
	_ = m.wafDB.WithContext(ctx).Model(&Ban{}).Where("expires_at = 0 OR expires_at > ?", nowUnix).Count(&activeBans).Error

	// 站点防护统计
	cfg, _ := m.loadGlobalConfig(ctx)
	var websites []store.Website
	_ = m.db.WithContext(ctx).Select("id, name, enabled").Find(&websites).Error
	var policies []SitePolicy
	_ = m.db.WithContext(ctx).Find(&policies).Error
	pm := make(map[string]SitePolicy, len(policies))
	for _, p := range policies {
		pm[p.SiteID] = p
	}
	siteNames := make(map[string]string, len(websites))
	protectedSites := 0
	for _, w := range websites {
		siteNames[w.ID] = w.Name
		if !w.Enabled {
			continue
		}
		if p, ok := pm[w.ID]; ok {
			if p.InheritGlobal {
				if cfg.Enabled {
					protectedSites++
				}
			} else if p.Mode == "block" || p.Mode == "observe" {
				protectedSites++
			}
		} else if cfg.Enabled {
			protectedSites++
		}
	}

	// 趋势曲线
	type trendPoint struct {
		TS       int64 `json:"ts"`
		Blocked  int64 `json:"blocked"`
		Observed int64 `json:"observed"`
	}
	var statRows []HourlyStat
	_ = m.wafDB.WithContext(ctx).Where("hour_ts >= ?", trendStart).Find(&statRows).Error
	trendMap := map[int64]*trendPoint{}
	groupMap := map[string]int64{}
	siteHitMap := map[string]int64{}
	for _, s := range statRows {
		pt, ok := trendMap[s.HourTS]
		if !ok {
			pt = &trendPoint{TS: s.HourTS}
			trendMap[s.HourTS] = pt
		}
		pt.Blocked += s.BlockedCount
		pt.Observed += s.ObservedCount
		groupMap[s.RuleGroup] += s.BlockedCount + s.ObservedCount
		if s.SiteID != "" {
			siteHitMap[s.SiteID] += s.BlockedCount + s.ObservedCount
		}
	}
	trend := make([]trendPoint, 0, hours)
	for i := 0; i < hours; i++ {
		ts := trendStart + int64(i)*3600
		if pt, ok := trendMap[ts]; ok {
			trend = append(trend, *pt)
		} else {
			trend = append(trend, trendPoint{TS: ts})
		}
	}

	// 攻击类型分布
	type groupItem struct {
		Group string `json:"group"`
		Label string `json:"label"`
		Count int64  `json:"count"`
	}
	groups := make([]groupItem, 0, len(groupMap))
	for g, cnt := range groupMap {
		if cnt > 0 {
			groups = append(groups, groupItem{Group: g, Label: ruleGroupLabel(g), Count: cnt})
		}
	}
	slices.SortFunc(groups, func(a, b groupItem) int {
		if a.Count == b.Count {
			return strings.Compare(a.Group, b.Group)
		}
		if b.Count > a.Count {
			return 1
		}
		return -1
	})

	// 受攻击站点 Top5
	type siteTop struct {
		SiteID string `json:"siteId"`
		Name   string `json:"name"`
		Count  int64  `json:"count"`
	}
	topSites := make([]siteTop, 0, len(siteHitMap))
	for sid, cnt := range siteHitMap {
		name := siteNames[sid]
		if name == "" {
			name = sid
		}
		topSites = append(topSites, siteTop{SiteID: sid, Name: name, Count: cnt})
	}
	slices.SortFunc(topSites, func(a, b siteTop) int {
		if b.Count > a.Count {
			return 1
		}
		if b.Count < a.Count {
			return -1
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(topSites) > 5 {
		topSites = topSites[:5]
	}

	// 高频攻击 IP Top10
	type ipTop struct {
		IP       string `json:"ip"`
		Location string `json:"location"`
		Count    int64  `json:"count"`
	}
	var topIPs []ipTop
	_ = m.wafDB.WithContext(ctx).Model(&Event{}).
		Select("client_ip AS ip, MAX(location) AS location, COUNT(*) AS count").
		Where("occurred_at >= ?", trendStart).
		Group("client_ip").
		Order("count DESC").
		Limit(10).
		Scan(&topIPs).Error
	if topIPs == nil {
		topIPs = []ipTop{}
	}

	response.OK(c, gin.H{
		"enabled":        cfg.Enabled,
		"mode":           cfg.Mode,
		"engine":         m.engineStatus(ctx),
		"todayBlocked":   todayBlocked,
		"todayObserved":  todayObserved,
		"weekBlocked":    weekBlocked,
		"activeBans":     activeBans,
		"totalSites":     len(websites),
		"protectedSites": protectedSites,
		"trend":          trend,
		"ruleGroups":     groups,
		"topSites":       topSites,
		"topIPs":         topIPs,
	})
}

func (m *Module) getConfig(c *gin.Context) {
	cfg, err := m.loadGlobalConfig(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, "读取 WAF 全局配置失败")
		return
	}
	response.OK(c, gin.H{"config": cfg, "engine": m.engineStatus(c.Request.Context())})
}

func (m *Module) saveConfig(c *gin.Context) {
	var in GlobalConfig
	if !response.Bind(c, &in) {
		return
	}
	if err := validateGlobalConfig(&in); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	data, err := json.Marshal(in)
	if err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	row := wafConfigRow{ID: 1, Data: string(data), UpdatedAt: time.Now().UTC()}
	if err := m.db.WithContext(c.Request.Context()).Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
		response.Fail(c, response.System, "保存 WAF 配置失败")
		return
	}
	if err := m.SyncEngine(c.Request.Context(), false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, gin.H{"config": in, "engine": m.engineStatus(c.Request.Context())})
}

func (m *Module) syncEngineHandler(c *gin.Context) {
	if err := m.SyncEngine(c.Request.Context(), true); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, m.engineStatus(c.Request.Context()))
}

func (m *Module) listRules(c *gin.Context) {
	ctx := c.Request.Context()
	builtins, err := m.mergedBuiltinRules(ctx)
	if err != nil {
		response.Fail(c, response.System, "读取内置规则失败")
		return
	}
	var customs []CustomRule
	if err := m.db.WithContext(ctx).Order("priority ASC, created_at DESC").Find(&customs).Error; err != nil {
		response.Fail(c, response.System, "读取自定义规则失败")
		return
	}
	if customs == nil {
		customs = []CustomRule{}
	}
	response.OK(c, gin.H{"builtin": builtins, "custom": customs})
}

func (m *Module) updateBuiltinRule(c *gin.Context) {
	id := c.Param("id")
	found := false
	for _, r := range builtinCatalog {
		if r.ID == id {
			found = true
			break
		}
	}
	if !found {
		response.Fail(c, response.NotFound, "内置规则不存在")
		return
	}
	var in struct {
		Enabled bool   `json:"enabled"`
		Action  string `json:"action"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Action != "block" && in.Action != "observe" {
		response.Fail(c, response.Invalid, "动作仅支持 block 或 observe")
		return
	}
	row := BuiltinOverride{RuleID: id, Enabled: in.Enabled, Action: in.Action, UpdatedAt: time.Now().UTC()}
	if err := m.db.WithContext(c.Request.Context()).Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
		response.Fail(c, response.System, "更新内置规则失败")
		return
	}
	if err := m.SyncEngine(c.Request.Context(), false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, row)
}

func (m *Module) createCustomRule(c *gin.Context) {
	var in CustomRule
	if !response.Bind(c, &in) {
		return
	}
	if err := validateCustomRule(&in); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	now := time.Now().UTC()
	in.ID = store.ID()
	in.CreatedAt = now
	in.UpdatedAt = now
	if err := m.db.WithContext(c.Request.Context()).Create(&in).Error; err != nil {
		response.Fail(c, response.System, "创建自定义规则失败")
		return
	}
	if err := m.SyncEngine(c.Request.Context(), false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, in)
}

func (m *Module) updateCustomRule(c *gin.Context) {
	id := c.Param("id")
	var existing CustomRule
	if err := m.db.WithContext(c.Request.Context()).First(&existing, "id = ?", id).Error; err != nil {
		response.Fail(c, response.NotFound, "自定义规则不存在")
		return
	}
	var in CustomRule
	if !response.Bind(c, &in) {
		return
	}
	if err := validateCustomRule(&in); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	in.ID = existing.ID
	in.CreatedAt = existing.CreatedAt
	in.UpdatedAt = time.Now().UTC()
	if err := m.db.WithContext(c.Request.Context()).Save(&in).Error; err != nil {
		response.Fail(c, response.System, "更新自定义规则失败")
		return
	}
	if err := m.SyncEngine(c.Request.Context(), false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, in)
}

func (m *Module) deleteCustomRule(c *gin.Context) {
	id := c.Param("id")
	res := m.db.WithContext(c.Request.Context()).Delete(&CustomRule{}, "id = ?", id)
	if res.Error != nil {
		response.Fail(c, response.System, "删除自定义规则失败")
		return
	}
	if res.RowsAffected == 0 {
		response.Fail(c, response.NotFound, "自定义规则不存在")
		return
	}
	if err := m.SyncEngine(c.Request.Context(), false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, gin.H{"id": id})
}

func (m *Module) listSites(c *gin.Context) {
	ctx := c.Request.Context()
	global, _ := m.loadGlobalConfig(ctx)
	var websites []store.Website
	if err := m.db.WithContext(ctx).Select("id, name, domains, enabled").Order("name ASC").Find(&websites).Error; err != nil {
		response.Fail(c, response.System, "读取站点列表失败")
		return
	}
	var policies []SitePolicy
	_ = m.db.WithContext(ctx).Find(&policies).Error
	pm := make(map[string]SitePolicy, len(policies))
	for _, p := range policies {
		pm[p.SiteID] = p
	}

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	type siteCount struct {
		SiteID string
		Count  int64
	}
	var counts []siteCount
	_ = m.wafDB.WithContext(ctx).Model(&Event{}).
		Select("site_id, COUNT(*) AS count").
		Where("occurred_at >= ? AND action != 'observe'", todayStart).
		Group("site_id").
		Scan(&counts).Error
	cm := make(map[string]int64, len(counts))
	for _, sc := range counts {
		cm[sc.SiteID] = sc.Count
	}

	type siteRow struct {
		SiteID       string     `json:"siteId"`
		Name         string     `json:"name"`
		Domains      []string   `json:"domains"`
		SiteEnabled  bool       `json:"siteEnabled"`
		TodayBlocked int64      `json:"todayBlocked"`
		Policy       SitePolicy `json:"policy"`
	}
	out := make([]siteRow, 0, len(websites))
	for _, w := range websites {
		p, ok := pm[w.ID]
		if !ok {
			p = defaultSitePolicy(w.ID, global)
		} else {
			p.AllowIPs = nonNilSlice(p.AllowIPs)
			p.DenyIPs = nonNilSlice(p.DenyIPs)
			p.AllowPaths = nonNilSlice(p.AllowPaths)
			p.DenyPaths = nonNilSlice(p.DenyPaths)
			p.DisabledGroups = nonNilSlice(p.DisabledGroups)
		}
		out = append(out, siteRow{
			SiteID:       w.ID,
			Name:         w.Name,
			Domains:      extractSiteHosts(w.Domains),
			SiteEnabled:  w.Enabled,
			TodayBlocked: cm[w.ID],
			Policy:       p,
		})
	}
	response.OK(c, gin.H{"list": out})
}

func defaultSitePolicy(id string, global GlobalConfig) SitePolicy {
	return SitePolicy{
		SiteID: id, InheritGlobal: true, Mode: global.Mode,
		CCEnabled: global.CCEnabled, CCWindowSeconds: global.CCWindowSeconds,
		CCMaxRequests: global.CCMaxRequests, CCAction: global.CCAction,
		AllowIPs: []string{}, DenyIPs: []string{}, AllowPaths: []string{},
		DenyPaths: []string{}, DisabledGroups: []string{},
	}
}

func (m *Module) getSitePolicy(c *gin.Context) {
	ctx := c.Request.Context()
	id := c.Param("id")
	var site store.Website
	if err := m.db.WithContext(ctx).Select("id").First(&site, "id = ?", id).Error; err != nil {
		response.Fail(c, response.NotFound, "站点不存在")
		return
	}
	var policy SitePolicy
	err := m.db.WithContext(ctx).First(&policy, "site_id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var global GlobalConfig
		if global, err = m.loadGlobalConfig(ctx); err == nil {
			policy = defaultSitePolicy(id, global)
		}
	}
	if err != nil {
		response.Fail(c, response.System, "读取站点防护策略失败")
		return
	}
	response.OK(c, policy)
}

func (m *Module) updateSitePolicy(c *gin.Context) {
	ctx := c.Request.Context()
	siteID := c.Param("id")
	var site store.Website
	if err := m.db.WithContext(ctx).First(&site, "id = ?", siteID).Error; err != nil {
		response.Fail(c, response.NotFound, "站点不存在")
		return
	}
	var in SitePolicy
	if !response.Bind(c, &in) {
		return
	}
	in.SiteID = siteID
	if err := validateSitePolicy(&in); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	in.UpdatedAt = time.Now().UTC()
	c.Set("panel.auditModule", "website")
	c.Set("panel.auditAction", "修改 WAF 策略")
	c.Set("panel.auditName", site.Name)
	c.Set("panel.auditResource", siteID)
	if err := m.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&in).Error; err != nil {
		response.Fail(c, response.System, "保存站点防护策略失败")
		return
	}

	if err := m.SyncEngine(ctx, false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, in)
}

func (m *Module) listBans(c *gin.Context) {
	ctx := c.Request.Context()
	now := time.Now().Unix()
	var list []Ban
	q := m.wafDB.WithContext(ctx).Model(&Ban{}).Where("expires_at = 0 OR expires_at > ?", now)
	if ip := strings.TrimSpace(c.Query("ip")); ip != "" {
		q = q.Where("ip LIKE ?", "%"+ip+"%")
	}
	if err := q.Order("created_at DESC").Find(&list).Error; err != nil {
		response.Fail(c, response.System, "读取封禁列表失败")
		return
	}
	if list == nil {
		list = []Ban{}
	}
	response.OK(c, gin.H{"list": list})
}

func (m *Module) createBan(c *gin.Context) {
	ctx := c.Request.Context()
	var in struct {
		IP           string `json:"ip"`
		SiteID       string `json:"siteId"`
		Reason       string `json:"reason"`
		Duration     int    `json:"duration"` // 秒，0 表示永久
		SyncFirewall bool   `json:"syncFirewall"`
	}
	if !response.Bind(c, &in) {
		return
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(in.IP))
	if err != nil || addr.Zone() != "" {
		response.Fail(c, response.Invalid, "请输入有效的 IPv4 或 IPv6 地址")
		return
	}
	ip := addr.Unmap().String()
	if in.Duration < 0 || in.Duration > 86400*3650 {
		response.Fail(c, response.Invalid, "封禁时长无效")
		return
	}
	now := time.Now().Unix()
	var expiresAt int64
	if in.Duration > 0 {
		expiresAt = now + int64(in.Duration)
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "管理员手动封禁"
	}
	if in.SyncFirewall && m.firewall == nil {
		response.Fail(c, response.System, "系统防火墙模块不可用")
		return
	}
	ban := Ban{
		IP:        ip,
		SiteID:    strings.TrimSpace(in.SiteID),
		Location:  geoip.Location(ip),
		Reason:    reason,
		Source:    "manual",
		NFTSynced: in.SyncFirewall,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}
	if err := m.saveBan(ctx, &ban); err != nil {
		response.Fail(c, response.System, "保存封禁失败："+err.Error())
		return
	}
	if err := m.SyncEngine(ctx, false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, ban)
}

func (m *Module) deleteBan(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.Fail(c, response.Invalid, "封禁 ID 无效")
		return
	}
	if err := m.wafDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ban Ban
		if err := tx.First(&ban, id).Error; err != nil {
			return err
		}
		if err := tx.Delete(&ban).Error; err != nil || !ban.NFTSynced {
			return err
		}
		return m.reconcileFirewallBan(ctx, tx, ban.IP)
	}); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.NotFound, "封禁记录不存在")
		} else {
			response.Fail(c, response.System, "解封失败："+err.Error())
		}
		return
	}
	if err := m.SyncEngine(ctx, false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, gin.H{"id": id})
}

var errBanExpired = errors.New("该封禁已到期")

func (m *Module) syncBanToFirewall(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.Fail(c, response.Invalid, "封禁 ID 无效")
		return
	}
	if m.firewall == nil {
		response.Fail(c, response.System, "系统防火墙模块不可用")
		return
	}
	var ban Ban
	if err := m.wafDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&ban, id).Error; err != nil {
			return err
		}
		if ban.ExpiresAt != 0 && ban.ExpiresAt <= time.Now().Unix() {
			return errBanExpired
		}
		if err := tx.Model(&ban).Update("nft_synced", true).Error; err != nil {
			return err
		}
		return m.reconcileFirewallBan(ctx, tx, ban.IP)
	}); err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			response.Fail(c, response.NotFound, "封禁记录不存在")
		case errors.Is(err, errBanExpired):
			response.Fail(c, response.Invalid, err.Error())
		default:
			response.Fail(c, response.System, err.Error())
		}
		return
	}
	ban.NFTSynced = true
	response.OK(c, ban)
}

func (m *Module) saveBan(ctx context.Context, ban *Ban) error {
	return m.wafDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var previous Ban
		err := tx.Where("ip = ? AND site_id = ?", ban.IP, ban.SiteID).First(&previous).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if ban.Source == "auto" && previous.ID != 0 && (previous.ExpiresAt == 0 || previous.ExpiresAt > time.Now().Unix()) {
			return nil
		}
		ban.ID = previous.ID
		if err := tx.Save(ban).Error; err != nil || (!ban.NFTSynced && !previous.NFTSynced) {
			return err
		}
		return m.reconcileFirewallBan(ctx, tx, ban.IP)
	})
}

// 站点封禁独立记录，系统防火墙只维护同一 IP 的最长有效期。
func (m *Module) reconcileFirewallBan(ctx context.Context, tx *gorm.DB, ip string) error {
	if m.firewall == nil {
		return nil
	}
	var ban Ban
	err := tx.Where("ip = ? AND nft_synced = ? AND (expires_at = 0 OR expires_at > ?)", ip, true, time.Now().Unix()).
		Order("CASE WHEN expires_at = 0 THEN 0 ELSE 1 END, expires_at DESC, id DESC").First(&ban).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return m.firewall.UnblockIPFromWAF(ctx, ip)
	}
	if err != nil {
		return err
	}
	var expiry *time.Time
	if ban.ExpiresAt != 0 {
		value := time.Unix(ban.ExpiresAt, 0).UTC()
		expiry = &value
	}
	return m.firewall.BlockIPFromWAF(ctx, ip, ban.Reason, expiry)
}

func (m *Module) listEvents(c *gin.Context) {
	ctx := c.Request.Context()
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	q := m.wafDB.WithContext(ctx).Model(&Event{})
	if siteID := strings.TrimSpace(c.Query("siteId")); siteID != "" {
		q = q.Where("site_id = ?", siteID)
	}
	if ip := strings.TrimSpace(c.Query("ip")); ip != "" {
		q = q.Where("client_ip LIKE ?", "%"+ip+"%")
	}
	if group := strings.TrimSpace(c.Query("ruleGroup")); group != "" {
		q = q.Where("rule_group = ?", group)
	}
	if action := strings.TrimSpace(c.Query("action")); action != "" {
		q = q.Where("action = ?", action)
	}
	if kw := strings.TrimSpace(c.Query("keyword")); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("uri LIKE ? OR domain LIKE ? OR rule_name LIKE ? OR matched_data LIKE ?", like, like, like, like)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		response.Fail(c, response.System, "查询攻击日志失败")
		return
	}
	var list []Event
	if err := q.Order("occurred_at DESC, id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		response.Fail(c, response.System, "查询攻击日志失败")
		return
	}
	if list == nil {
		list = []Event{}
	}
	response.OK(c, gin.H{"list": list, "total": total, "page": page, "pageSize": pageSize})
}

func (m *Module) clearEvents(c *gin.Context) {
	ctx := c.Request.Context()
	if err := m.wafDB.WithContext(ctx).Exec("DELETE FROM waf_events").Error; err != nil {
		response.Fail(c, response.System, "清空攻击日志失败")
		return
	}
	response.OK(c, gin.H{"cleared": true})
}

func (m *Module) allowFromEvent(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.Fail(c, response.Invalid, "日志 ID 无效")
		return
	}
	var ev Event
	if err := m.wafDB.WithContext(ctx).First(&ev, id).Error; err != nil {
		response.Fail(c, response.NotFound, "攻击日志不存在")
		return
	}
	ex := Exemption{
		ID:        store.ID(),
		SiteID:    ev.SiteID,
		RuleID:    ev.RuleID,
		URI:       ev.URI,
		Remark:    "误报放行：" + ev.RuleName,
		CreatedAt: time.Now().UTC(),
	}
	if err := m.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&ex).Error; err != nil {
		response.Fail(c, response.System, "添加误报放行失败")
		return
	}
	if err := m.SyncEngine(ctx, false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, ex)
}

func (m *Module) listExemptions(c *gin.Context) {
	var list []Exemption
	if err := m.db.WithContext(c.Request.Context()).Order("created_at DESC").Find(&list).Error; err != nil {
		response.Fail(c, response.System, "读取误报豁免列表失败")
		return
	}
	if list == nil {
		list = []Exemption{}
	}
	response.OK(c, gin.H{"list": list})
}

func (m *Module) deleteExemption(c *gin.Context) {
	id := c.Param("id")
	res := m.db.WithContext(c.Request.Context()).Delete(&Exemption{}, "id = ?", id)
	if res.Error != nil {
		response.Fail(c, response.System, "删除误报豁免失败")
		return
	}
	if res.RowsAffected == 0 {
		response.Fail(c, response.NotFound, "豁免记录不存在")
		return
	}
	if err := m.SyncEngine(c.Request.Context(), false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, gin.H{"id": id})
}
