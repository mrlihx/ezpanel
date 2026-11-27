package settings

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/module/certificate"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"fmt"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	hiddenMenusSettingKey = "panel.hidden_menus"
	menuOrderSettingKey   = "panel.menu_order"
	menuGroupsSettingKey  = "panel.menu_groups"
	menuGroupKeyPrefix    = "group:"
)

type MenuGroup struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Icon  string   `json:"icon"`
	Items []string `json:"items"`
}

var (
	lockedMenuKeys = map[string]bool{
		"/":         true,
		"/apps":     true,
		"/settings": true,
		"logout":    true,
	}
	hideableBuiltinMenuKeys = map[string]bool{
		"/websites":  true,
		"/databases": true,
		"/docker":    true,
		"/security":  true,
		"/waf":       true,
		"/files":     true,
		"/crons":     true,
		"/terminal":  true,
		"/oplogs":    true,
		"/tools":     true,
	}
	orderableBuiltinMenuKeys = map[string]bool{
		"/websites":  true,
		"/databases": true,
		"/docker":    true,
		"/security":  true,
		"/waf":       true,
		"/files":     true,
		"/crons":     true,
		"/terminal":  true,
		"/oplogs":    true,
		"/apps":      true,
		"/tools":     true,
		"/settings":  true,
	}
	allowedMenuGroupIcons = map[string]bool{
		"folder":      true,
		"blocks":      true,
		"layers":      true,
		"server":      true,
		"globe":       true,
		"shield":      true,
		"wrench":      true,
		"database":    true,
		"terminal":    true,
		"settings":    true,
		"layout-grid": true,
		"file-text":   true,
	}
	menuGroupIDPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	pluginMenuKeyPattern = regexp.MustCompile(`^/p/[a-z0-9][a-z0-9-]{0,63}$`)
)

type Module struct {
	networkMu       sync.Mutex
	db              *gorm.DB
	cfg             config.Config
	auth            *auth.Service
	applyNetwork    func(config.Config) error
	refreshFirewall func(context.Context) error
	policy          *pathsafe.Policy
}

func New(db *gorm.DB, cfg config.Config, a *auth.Service, apply func(config.Config) error, refreshFirewall func(context.Context) error) (*Module, error) {
	p, e := pathsafe.New(cfg.InstallDir)
	return &Module{db: db, cfg: cfg, auth: a, applyNetwork: apply, refreshFirewall: refreshFirewall, policy: p}, e
}
func (m *Module) Name() string { return "settings" }
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/settings")
	r.GET("", m.read)
	r.PUT("/basic", m.basic)
	r.PUT("/menus", m.menus)
	r.PUT("/account", m.account)
	r.PUT("/network", m.network)
	r.GET("/terminal", m.terminal)
	r.PUT("/terminal", m.terminal)
	r.GET("/navigation", m.navigation)
	r.PUT("/navigation", m.navigation)
}
func failed(c *gin.Context, e error) { response.Fail(c, response.Invalid, response.Summary(e.Error())) }
func (m *Module) read(c *gin.Context) {
	s := m.cfg.Current()
	cert := ""
	var certificateInfo any
	if s.TLSCert != "" {
		b, e := os.ReadFile(s.TLSCert)
		if e != nil {
			failed(c, errors.New("无法读取当前面板证书"))
			return
		}
		cert = string(b)
		if block, _ := pem.Decode(b); block != nil {
			if leaf, err := x509.ParseCertificate(block.Bytes); err == nil {
				domains := append([]string{}, leaf.DNSNames...)
				for _, ip := range leaf.IPAddresses {
					domains = append(domains, ip.String())
				}
				certificateInfo = gin.H{"domains": domains, "issuer": leaf.Issuer.String(), "notAfter": leaf.NotAfter}
			}
		}
	}
	privateKey := ""
	if s.TLSKey != "" {
		if b, e := os.ReadFile(s.TLSKey); e == nil {
			privateKey = string(b)
		}
	}
	policy := m.auth.SecurityPolicy()
	groups := m.readMenuGroups()
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"alias": s.Alias, "idleMinutes": s.IdleMinutes, "siteRoot": s.SiteRoot, "backupRoot": s.BackupRoot, "hiddenMenus": m.readHiddenMenus(), "menuOrder": m.readMenuOrder(groups), "menuGroups": groups, "username": auth.Current(c).User.Username, "boundDomain": s.BoundDomain, "sslEnabled": s.TLSCert != "", "certificate": cert, "privateKey": privateKey, "certificateId": s.PanelCertificateID, "certificateInfo": certificateInfo, "keyConfigured": s.TLSKey != "", "publicUrl": s.PublicURL, "port": s.Port, "entry": s.Entry, "userAgentEnabled": policy.UserAgentEnabled, "userAgentAllow": policy.UserAgentAllow, "unauthStatus": policy.UnauthStatus, "unauthHtml": policy.UnauthHTML})
}

func validHideableMenuKey(key string) bool {
	return hideableBuiltinMenuKeys[key] || pluginMenuKeyPattern.MatchString(key)
}

func validGroupableMenuKey(key string) bool {
	return orderableBuiltinMenuKeys[key] || pluginMenuKeyPattern.MatchString(key)
}

func normalizeMenuGroupIcon(id, icon string) string {
	if allowedMenuGroupIcons[icon] {
		return icon
	}
	if id == "extensions" {
		return "blocks"
	}
	return "folder"
}

func (m *Module) readMenuList(settingKey string, allow func(string) bool) []string {
	if m.db == nil {
		return []string{}
	}
	var row store.Setting
	if err := m.db.First(&row, "key = ?", settingKey).Error; err != nil || row.Value == "" {
		return []string{}
	}
	var raw []string
	if err := json.Unmarshal([]byte(row.Value), &raw); err != nil {
		return []string{}
	}
	out := make([]string, 0, len(raw))
	for _, key := range raw {
		if allow(key) && !slices.Contains(out, key) {
			out = append(out, key)
		}
	}
	return out
}

func (m *Module) readMenuGroups() []MenuGroup {
	if m.db == nil {
		return []MenuGroup{}
	}
	var row store.Setting
	if err := m.db.First(&row, "key = ?", menuGroupsSettingKey).Error; err != nil || row.Value == "" {
		return []MenuGroup{}
	}
	var raw []MenuGroup
	if err := json.Unmarshal([]byte(row.Value), &raw); err != nil {
		return []MenuGroup{}
	}
	out := make([]MenuGroup, 0, len(raw))
	seenGroups := make(map[string]bool, len(raw))
	seenItems := make(map[string]bool)
	for _, g := range raw {
		id := strings.TrimSpace(g.ID)
		title := strings.TrimSpace(g.Title)
		if !menuGroupIDPattern.MatchString(id) || seenGroups[id] {
			continue
		}
		if titleLen := utf8.RuneCountInString(title); titleLen == 0 || titleLen > 20 {
			continue
		}
		seenGroups[id] = true
		items := make([]string, 0, len(g.Items))
		for _, item := range g.Items {
			if validGroupableMenuKey(item) && !seenItems[item] {
				seenItems[item] = true
				items = append(items, item)
			}
		}
		out = append(out, MenuGroup{
			ID:    id,
			Title: title,
			Icon:  normalizeMenuGroupIcon(id, strings.TrimSpace(g.Icon)),
			Items: items,
		})
	}
	return out
}

func (m *Module) readHiddenMenus() []string {
	return m.readMenuList(hiddenMenusSettingKey, validHideableMenuKey)
}

func (m *Module) readMenuOrder(groups []MenuGroup) []string {
	validGroups := make(map[string]bool, len(groups)+1)
	validGroups[menuGroupKeyPrefix+"extensions"] = true
	for _, g := range groups {
		validGroups[menuGroupKeyPrefix+g.ID] = true
	}
	return m.readMenuList(menuOrderSettingKey, func(key string) bool {
		return validGroupableMenuKey(key) || validGroups[key]
	})
}

func (m *Module) menus(c *gin.Context) {
	var in struct {
		HiddenMenus []string    `json:"hiddenMenus"`
		MenuOrder   []string    `json:"menuOrder"`
		MenuGroups  []MenuGroup `json:"menuGroups"`
	}
	if !response.Bind(c, &in) {
		return
	}

	var cleanHidden []string
	if in.HiddenMenus != nil {
		cleanHidden = make([]string, 0, len(in.HiddenMenus))
		for _, key := range in.HiddenMenus {
			if lockedMenuKeys[key] {
				response.Fail(c, response.Invalid, "该菜单不可隐藏")
				return
			}
			if !validHideableMenuKey(key) {
				response.Fail(c, response.Invalid, "菜单标识无效")
				return
			}
			if !slices.Contains(cleanHidden, key) {
				cleanHidden = append(cleanHidden, key)
			}
		}
	}

	var cleanGroups []MenuGroup
	if in.MenuGroups != nil {
		if len(in.MenuGroups) > 20 {
			response.Fail(c, response.Invalid, "菜单分组数量不能超过 20 个")
			return
		}
		cleanGroups = make([]MenuGroup, 0, len(in.MenuGroups))
		seenGroups := make(map[string]bool, len(in.MenuGroups))
		seenItems := make(map[string]bool)
		for _, g := range in.MenuGroups {
			id := strings.TrimSpace(g.ID)
			if !menuGroupIDPattern.MatchString(id) || seenGroups[id] {
				response.Fail(c, response.Invalid, "菜单分组标识无效或重复")
				return
			}
			seenGroups[id] = true
			title := strings.TrimSpace(g.Title)
			if titleLen := utf8.RuneCountInString(title); titleLen == 0 || titleLen > 20 {
				response.Fail(c, response.Invalid, "菜单分组名称长度需在 1–20 个字符之间")
				return
			}
			items := make([]string, 0, len(g.Items))
			for _, item := range g.Items {
				if !validGroupableMenuKey(item) {
					response.Fail(c, response.Invalid, "分组内包含无效或不可分组的菜单")
					return
				}
				if slices.Contains(items, item) {
					continue
				}
				if seenItems[item] {
					response.Fail(c, response.Invalid, "同一菜单不能同时属于多个分组")
					return
				}
				seenItems[item] = true
				items = append(items, item)
			}
			cleanGroups = append(cleanGroups, MenuGroup{
				ID:    id,
				Title: title,
				Icon:  normalizeMenuGroupIcon(id, strings.TrimSpace(g.Icon)),
				Items: items,
			})
		}
	}

	var cleanOrder []string
	if in.MenuOrder != nil {
		activeGroups := cleanGroups
		if in.MenuGroups == nil {
			activeGroups = m.readMenuGroups()
		}
		validGroupKeys := make(map[string]bool, len(activeGroups))
		for _, g := range activeGroups {
			validGroupKeys[menuGroupKeyPrefix+g.ID] = true
		}
		cleanOrder = make([]string, 0, len(in.MenuOrder))
		for _, key := range in.MenuOrder {
			if !validGroupableMenuKey(key) && !validGroupKeys[key] {
				response.Fail(c, response.Invalid, "菜单顺序标识无效")
				return
			}
			if !slices.Contains(cleanOrder, key) {
				cleanOrder = append(cleanOrder, key)
			}
		}
	}

	err := m.db.Transaction(func(tx *gorm.DB) error {
		saveJSON := func(key string, val any) error {
			encoded, err := json.Marshal(val)
			if err != nil {
				return err
			}
			return tx.Save(&store.Setting{Key: key, Value: string(encoded)}).Error
		}
		if in.HiddenMenus != nil {
			if err := saveJSON(hiddenMenusSettingKey, cleanHidden); err != nil {
				return err
			}
		}
		if in.MenuGroups != nil {
			if err := saveJSON(menuGroupsSettingKey, cleanGroups); err != nil {
				return err
			}
		}
		if in.MenuOrder != nil {
			if err := saveJSON(menuOrderSettingKey, cleanOrder); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	m.read(c)
}
func (m *Module) basic(c *gin.Context) {
	var in struct {
		Alias       string `json:"alias"`
		IdleMinutes int    `json:"idleMinutes"`
		SiteRoot    string `json:"siteRoot"`
		BackupRoot  string `json:"backupRoot"`
	}
	if !response.Bind(c, &in) {
		return
	}
	_, e := m.cfg.Apply(func(next *config.Config) error {
		next.Alias = strings.TrimSpace(in.Alias)
		next.IdleMinutes = in.IdleMinutes
		next.SiteRoot = in.SiteRoot
		next.BackupRoot = in.BackupRoot
		roots := []*string{&next.SiteRoot, &next.BackupRoot}
		for _, path := range roots {
			real, err := m.policy.Check(*path, true)
			if err != nil || m.policy.CheckTree(real) != nil {
				return errors.New("默认目录受保护或包含无效链接")
			}
			if info, err := os.Stat(real); err == nil && !info.IsDir() {
				return errors.New("默认路径不是目录")
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}
			for _, managed := range []string{m.cfg.InstallDir} {
				if pathsafe.Contains(managed, real) || pathsafe.Contains(real, managed) {
					return errors.New("默认目录不能与应用程序或服务数据目录重叠")
				}
			}
			*path = real
		}
		if pathsafe.Contains(next.SiteRoot, next.BackupRoot) || pathsafe.Contains(next.BackupRoot, next.SiteRoot) {
			return errors.New("建站目录与备份目录不能相同或互相包含")
		}
		return nil
	})
	if e != nil {
		failed(c, e)
		return
	}
	m.read(c)
}
func (m *Module) account(c *gin.Context) {
	var in struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if err := auth.ValidateUsername(in.Username); err != nil {
		failed(c, err)
		return
	}
	user := auth.Current(c).User
	values := map[string]any{"username": in.Username, "auth_version": gorm.Expr("auth_version + 1")}
	if in.NewPassword != "" {
		if e := auth.ValidatePassword(in.NewPassword); e != nil {
			failed(c, e)
			return
		}
		hash, e := bcrypt.GenerateFromPassword([]byte(in.NewPassword), bcrypt.DefaultCost)
		if e != nil {
			response.Fail(c, response.Internal, "")
			return
		}
		values["password_hash"] = string(hash)
	}
	e := m.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if e := tx.Model(&store.User{}).Where("username = ? AND id <> ?", in.Username, user.ID).Count(&count).Error; e != nil {
			return e
		}
		if count > 0 {
			return errors.New("账号已被使用")
		}
		if e := tx.Model(&store.User{}).Where("id = ?", user.ID).Updates(values).Error; e != nil {
			return e
		}
		return tx.Where("user_id = ?", user.ID).Delete(&store.AuthSession{}).Error
	})
	if e != nil {
		failed(c, e)
		return
	}
	response.OK(c, gin.H{"loginRequired": true, "loginUrl": m.auth.RequestOrigin(c).String() + "/" + m.cfg.Current().Entry})
}
func (m *Module) network(c *gin.Context) {
	m.networkMu.Lock()
	defer m.networkMu.Unlock()
	var in struct {
		BoundDomain      *string `json:"boundDomain"`
		SSLEnabled       *bool   `json:"sslEnabled"`
		Certificate      string  `json:"certificate"`
		CertificateID    string  `json:"certificateId"`
		PrivateKey       string  `json:"privateKey"`
		Port             *int    `json:"port"`
		Entry            *string `json:"entry"`
		UserAgentEnabled *bool   `json:"userAgentEnabled"`
		UserAgentAllow   *string `json:"userAgentAllow"`
		UnauthStatus     *int    `json:"unauthStatus"`
		UnauthHTML       *string `json:"unauthHtml"`
	}
	if !response.Bind(c, &in) {
		return
	}
	policyChanged := in.UserAgentEnabled != nil || in.UserAgentAllow != nil || in.UnauthStatus != nil || in.UnauthHTML != nil
	var policy auth.SecurityPolicy
	if policyChanged {
		policy = m.auth.SecurityPolicy()
		if in.UserAgentEnabled != nil {
			policy.UserAgentEnabled = *in.UserAgentEnabled
		}
		if in.UserAgentAllow != nil {
			policy.UserAgentAllow = *in.UserAgentAllow
		}
		if in.UnauthStatus != nil {
			policy.UnauthStatus = *in.UnauthStatus
		}
		if in.UnauthHTML != nil {
			policy.UnauthHTML = *in.UnauthHTML
		}
	}
	old := m.cfg.Current()
	oldOrigin := m.auth.RequestOrigin(c)
	hasCertificate := in.Certificate != "" || in.CertificateID != "" || in.PrivateKey != ""
	if in.BoundDomain == nil && in.SSLEnabled == nil && in.Port == nil && in.Entry == nil && !hasCertificate {
		if policyChanged {
			if err := m.auth.SaveSecurityPolicy(c.Request.Context(), c.Request.UserAgent(), policy); err != nil {
				failed(c, err)
				return
			}
		}
		response.OK(c, gin.H{"publicUrl": old.PublicURL, "loginRequired": false, "loginUrl": oldOrigin.String() + "/" + old.Entry})
		return
	}

	nextDomain := old.BoundDomain
	if in.BoundDomain != nil {
		nextDomain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(*in.BoundDomain), "."))
	}
	sslEnabled := old.TLSCert != ""
	if in.SSLEnabled != nil {
		sslEnabled = *in.SSLEnabled
	}
	host := nextDomain
	if host == "" {
		origin := oldOrigin
		if old.BoundDomain != "" {
			origin, _ = url.Parse(old.UnboundURL)
		}
		if origin != nil {
			host = origin.Hostname()
		}
		if host == "" {
			host = old.LocalListenHost()
		}
	}
	certPath, keyPath, certificateID := "", "", ""
	// 一对证书放在独立目录，配置只在全部写好后切换，API永不回显私钥。
	var staged string
	if sslEnabled && !hasCertificate && old.TLSCert != "" && nextDomain == old.BoundDomain {
		certPath, keyPath, certificateID = old.TLSCert, old.TLSKey, old.PanelCertificateID
	} else if sslEnabled {
		if !hasCertificate && old.TLSCert != "" {
			var err error
			var data []byte
			data, err = os.ReadFile(old.TLSCert)
			if err != nil {
				failed(c, err)
				return
			}
			in.Certificate, in.CertificateID = string(data), old.PanelCertificateID
		} else if in.CertificateID != "" {
			var err error
			in.Certificate, in.PrivateKey, err = certificate.ReadPEM(m.db, old, in.CertificateID)
			if err != nil {
				failed(c, err)
				return
			}
		}
		key := []byte(in.PrivateKey)
		if len(key) == 0 && old.TLSKey != "" {
			var e error
			key, e = os.ReadFile(old.TLSKey)
			if e != nil {
				failed(c, e)
				return
			}
		}
		pair, e := tls.X509KeyPair([]byte(in.Certificate), key)
		if e != nil || len(pair.Certificate) == 0 {
			failed(c, errors.New("证书与私钥不匹配或格式无效"))
			return
		}
		leaf, e := x509.ParseCertificate(pair.Certificate[0])
		if e != nil || time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
			failed(c, errors.New("证书尚未生效或已过期"))
			return
		}
		if e = leaf.VerifyHostname(host); e != nil {
			failed(c, fmt.Errorf("证书未覆盖面板访问地址 %s", host))
			return
		}
		if len(in.Certificate) > 1<<20 || len(key) > 64<<10 {
			failed(c, errors.New("证书或私钥过大"))
			return
		}
		if in.CertificateID == "" {
			stored, err := certificate.Store(m.db, old, in.Certificate, string(key), "import", "", "", "")
			if err != nil {
				failed(c, err)
				return
			}
			in.CertificateID = stored.ID
		}
		certificateID = in.CertificateID
		base := filepath.Join(old.DataDir(), "panel-ssl")
		if e = os.MkdirAll(base, 0700); e != nil {
			failed(c, e)
			return
		}
		staged, e = os.MkdirTemp(base, "certificate-")
		if e != nil {
			failed(c, e)
			return
		}
		certPath = filepath.Join(staged, "fullchain.pem")
		keyPath = filepath.Join(staged, "private.key")
		if e = os.WriteFile(certPath, []byte(in.Certificate), 0600); e == nil {
			e = os.WriteFile(keyPath, key, 0600)
		}
		if e != nil {
			os.RemoveAll(staged)
			failed(c, e)
			return
		}
	}
	next, e := m.cfg.Apply(func(out *config.Config) error {
		if in.Port != nil {
			out.Port = *in.Port
		}
		if in.Entry != nil {
			out.Entry = strings.TrimSpace(*in.Entry)
		}
		if old.BoundDomain == "" && nextDomain != "" {
			out.UnboundURL = old.PublicURL
			if old.DirectIPAccess() {
				out.UnboundURL = oldOrigin.String()
			}
		}
		out.BoundDomain = nextDomain
		out.TLSCert = certPath
		out.TLSKey = keyPath
		out.PanelCertificateID = certificateID
		// 只修改端口或安全入口时，继续允许使用任一有效的服务器 IP 直连。
		if old.DirectIPAccess() && nextDomain == "" && !sslEnabled {
			return nil
		}
		scheme := "http"
		if sslEnabled {
			scheme = "https"
		}
		out.PublicURL = scheme + "://" + net.JoinHostPort(host, strconv.Itoa(out.Port))
		return nil
	})
	if e != nil {
		if staged != "" {
			os.RemoveAll(staged)
		}
		failed(c, e)
		return
	}
	portChanged := next.Port != old.Port
	listenerApplied := false
	restoreNetwork := func(cause error) {
		_, restore := m.cfg.Apply(func(out *config.Config) error {
			out.Port, out.Entry = old.Port, old.Entry
			out.UnboundURL = old.UnboundURL
			out.BoundDomain = old.BoundDomain
			out.TLSCert = old.TLSCert
			out.TLSKey = old.TLSKey
			out.PanelCertificateID = old.PanelCertificateID
			out.PublicURL = old.PublicURL
			return nil
		})
		if restore == nil {
			if listenerApplied {
				restore = m.applyNetwork(old)
			}
			if portChanged && m.refreshFirewall != nil {
				restore = errors.Join(restore, m.refreshFirewall(context.WithoutCancel(c.Request.Context())))
			}
			if staged != "" {
				os.RemoveAll(staged)
			}
		}
		failed(c, errors.Join(cause, restore))
	}
	if portChanged && m.refreshFirewall != nil {
		if e = m.refreshFirewall(c.Request.Context()); e != nil {
			restoreNetwork(e)
			return
		}
	}
	if next.Port != old.Port || next.TLSCert != old.TLSCert {
		if e = m.applyNetwork(next); e != nil {
			restoreNetwork(e)
			return
		}
		listenerApplied = true
	}
	nextOrigin := next.PanelOrigin(oldOrigin.Hostname()).String()
	loginRequired := nextOrigin != oldOrigin.String()
	if policyChanged {
		if e = m.auth.ApplySecurityPolicy(c.Request.Context(), c.Request.UserAgent(), policy, loginRequired); e != nil {
			restoreNetwork(e)
			return
		}
	} else if loginRequired {
		if e = m.db.Model(&store.User{}).Where("id > 0").Update("auth_version", gorm.Expr("auth_version + 1")).Error; e != nil {
			restoreNetwork(errors.New("无法刷新面板登录会话"))
			return
		}
	}
	response.OK(c, gin.H{"publicUrl": next.PublicURL, "loginRequired": loginRequired, "loginUrl": nextOrigin + "/" + next.Entry})
}

func (m *Module) terminal(c *gin.Context) {
	if c.Request.Method == "GET" {
		response.OK(c, gin.H{"fontSize": m.cfg.Current().TerminalFontSize})
		return
	}
	var in struct {
		FontSize int `json:"fontSize"`
	}
	if !response.Bind(c, &in) {
		return
	}
	next, err := m.cfg.Apply(func(next *config.Config) error {
		next.TerminalFontSize = in.FontSize
		return nil
	})
	if err != nil {
		failed(c, err)
		return
	}
	response.OK(c, gin.H{"fontSize": next.TerminalFontSize})
}
