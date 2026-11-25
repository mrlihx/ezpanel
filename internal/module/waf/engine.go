package waf

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/store"
	"golang.org/x/sys/unix"
)

func (m *Module) wafLuaDir() string      { return m.cfg.Layout().WAFDir() }
func (m *Module) wafConfPath() string    { return m.cfg.Layout().WAFConf() }
func (m *Module) wafLogDir() string      { return m.cfg.Layout().WAFLogDir() }
func (m *Module) wafEventLog() string    { return m.cfg.Layout().WAFEventLog() }
func (m *Module) wafConfigFile() string  { return filepath.Join(m.wafLuaDir(), "config.json") }
func (m *Module) wafVersionFile() string { return filepath.Join(m.wafLuaDir(), "version.txt") }

type engineConfig struct {
	Enabled            bool                        `json:"enabled"`
	Mode               string                      `json:"mode"`
	CC                 engineCC                    `json:"cc"`
	AutoBan            engineAutoBan               `json:"autoBan"`
	AllowIPs           []string                    `json:"allowIPs"`
	DenyIPs            []string                    `json:"denyIPs"`
	AllowPaths         []string                    `json:"allowPaths"`
	DisabledRuleGroups []string                    `json:"disabledRuleGroups"`
	BlockHTML          string                      `json:"blockHtml"`
	ChallengeSecret    string                      `json:"challengeSecret"`
	BuiltinRules       []BuiltinRule               `json:"builtinRules"`
	CustomRules        []CustomRule                `json:"customRules"`
	Exemptions         []Exemption                 `json:"exemptions"`
	Bans               []engineBan                 `json:"bans"`
	Sites              map[string]engineSitePolicy `json:"sites"`
	HostToSite         map[string]string           `json:"hostToSite"`
}

type engineCC struct {
	Enabled       bool   `json:"enabled"`
	WindowSeconds int    `json:"windowSeconds"`
	MaxRequests   int    `json:"maxRequests"`
	Action        string `json:"action"`
}

type engineAutoBan struct {
	Enabled   bool `json:"enabled"`
	Window    int  `json:"window"`
	Threshold int  `json:"threshold"`
	Duration  int  `json:"duration"`
}

type engineBan struct {
	IP        string `json:"ip"`
	SiteID    string `json:"siteId"`
	ExpiresAt int64  `json:"expiresAt"`
}

type engineSitePolicy struct {
	InheritGlobal   bool     `json:"inheritGlobal"`
	Mode            string   `json:"mode"`
	CCEnabled       bool     `json:"ccEnabled"`
	CCWindowSeconds int      `json:"ccWindowSeconds"`
	CCMaxRequests   int      `json:"ccMaxRequests"`
	CCAction        string   `json:"ccAction"`
	AllowIPs        []string `json:"allowIPs"`
	DenyIPs         []string `json:"denyIPs"`
	AllowPaths      []string `json:"allowPaths"`
	DenyPaths       []string `json:"denyPaths"`
	DisabledGroups  []string `json:"disabledGroups"`
}

type EngineStatus struct {
	OpenRestyInstalled bool   `json:"openRestyInstalled"`
	OpenRestyRunning   bool   `json:"openRestyRunning"`
	EngineMounted      bool   `json:"engineMounted"`
	ConfigVersion      string `json:"configVersion"`
	LogPath            string `json:"logPath"`
}

// SyncEngine 将最新 WAF 策略与封禁列表写入 OpenResty ezwaf 目录并触发热更新。
// Lua 脚本变更、首次挂载或手动同步时需要重载 OpenResty。
func (m *Module) SyncEngine(ctx context.Context, forceReload bool) error {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()

	scriptsChanged, err := m.ensureEngineFiles()
	if err != nil {
		return err
	}

	payload, err := m.buildEngineConfig(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(m.wafConfigFile(), raw, 0644); err != nil {
		return err
	}
	ver := strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := atomicWriteFile(m.wafVersionFile(), []byte(ver+"\n"), 0644); err != nil {
		return err
	}

	confPath := m.wafConfPath()
	confExists := fileExists(confPath)
	needMount := !confExists
	if needMount {
		if err := os.MkdirAll(filepath.Dir(confPath), 0755); err != nil {
			return err
		}
		if err := atomicWriteFile(confPath, []byte(openRestyWAFConf(m.wafLuaDir())), 0644); err != nil {
			return err
		}
	}

	if forceReload || needMount || scriptsChanged {
		status, err := m.packages.Status(ctx, "openresty")
		if forceReload && err != nil {
			return err
		}
		if err == nil && status.Installed && status.Manageable {
			if res, err := m.packages.OpenRestyCommand(ctx, "-t"); err != nil {
				if needMount {
					_ = os.Remove(confPath)
				}
				return fmt.Errorf("WAF 配置语法检查失败：%s", strings.TrimSpace(res.Stderr+" "+res.Stdout))
			}
			if status.Running != nil && *status.Running {
				if res, err := m.packages.OpenRestyCommand(ctx, "-s", "reload"); err != nil {
					return fmt.Errorf("WAF 规则已保存，但重载 OpenResty 失败：%s", strings.TrimSpace(res.Stderr+" "+res.Stdout))
				}
			}
		}
	}
	return nil
}

func (m *Module) ensureEngineFiles() (bool, error) {
	l := m.cfg.Layout()
	luaDir := m.wafLuaDir()
	logDir := m.wafLogDir()
	eventLog := m.wafEventLog()
	// 面板使用 umask 077，工作进程仍需穿过应用目录读取热更新配置。
	for _, dir := range []string{l.Root(), l.AppsDir(), l.OpenRestyDir(), luaDir, l.LogDir(), logDir} {
		if err := pkgmgr.EnsureWebDirectory(dir); err != nil {
			return false, err
		}
	}
	logFile, err := os.OpenFile(eventLog, os.O_CREATE|os.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return false, err
	}
	if err := errors.Join(logFile.Chmod(0600), logFile.Close()); err != nil {
		return false, err
	}
	files := map[string]string{
		filepath.Join(luaDir, "core.lua"):   luaCoreScript(luaDir),
		filepath.Join(luaDir, "init.lua"):   luaInitScript(luaDir),
		filepath.Join(luaDir, "worker.lua"): luaWorkerScript,
		filepath.Join(luaDir, "access.lua"): luaAccessScript,
	}
	changed := false
	for p, content := range files {
		if existing, err := os.ReadFile(p); err == nil && string(existing) == content {
			continue
		}
		if err := atomicWriteFile(p, []byte(content), 0644); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

func (m *Module) buildEngineConfig(ctx context.Context) (engineConfig, error) {
	global, err := m.loadGlobalConfig(ctx)
	if err != nil {
		return engineConfig{}, err
	}
	builtins, err := m.mergedBuiltinRules(ctx)
	if err != nil {
		return engineConfig{}, err
	}
	var customs []CustomRule
	if err := m.db.WithContext(ctx).Order("priority ASC, created_at ASC").Find(&customs).Error; err != nil {
		return engineConfig{}, err
	}
	var exemptions []Exemption
	if err := m.db.WithContext(ctx).Find(&exemptions).Error; err != nil {
		return engineConfig{}, err
	}
	var bans []Ban
	now := time.Now().Unix()
	if err := m.wafDB.WithContext(ctx).Where("expires_at = 0 OR expires_at > ?", now).Find(&bans).Error; err != nil {
		return engineConfig{}, err
	}
	engineBans := make([]engineBan, 0, len(bans))
	for _, b := range bans {
		engineBans = append(engineBans, engineBan{IP: b.IP, SiteID: b.SiteID, ExpiresAt: b.ExpiresAt})
	}

	var websites []store.Website
	if err := m.db.WithContext(ctx).Select("id, domains, enabled").Find(&websites).Error; err != nil {
		return engineConfig{}, err
	}
	var storedPolicies []SitePolicy
	if err := m.db.WithContext(ctx).Find(&storedPolicies).Error; err != nil {
		return engineConfig{}, err
	}
	policyMap := make(map[string]SitePolicy, len(storedPolicies))
	for _, p := range storedPolicies {
		policyMap[p.SiteID] = p
	}

	sites := make(map[string]engineSitePolicy, len(websites))
	hostToSite := make(map[string]string, len(websites))
	for _, w := range websites {
		if w.Enabled {
			for _, host := range extractSiteHosts(w.Domains) {
				hostToSite[host] = w.ID
			}
		}
		p, ok := policyMap[w.ID]
		if !ok {
			p = defaultSitePolicy(w.ID, global)
		}
		sites[w.ID] = engineSitePolicy{
			InheritGlobal: p.InheritGlobal, Mode: p.Mode,
			CCEnabled: p.CCEnabled, CCWindowSeconds: p.CCWindowSeconds,
			CCMaxRequests: p.CCMaxRequests, CCAction: p.CCAction,
			AllowIPs: nonNilSlice(p.AllowIPs), DenyIPs: nonNilSlice(p.DenyIPs),
			AllowPaths: nonNilSlice(p.AllowPaths), DenyPaths: nonNilSlice(p.DenyPaths),
			DisabledGroups: nonNilSlice(p.DisabledGroups),
		}
	}

	return engineConfig{
		Enabled: global.Enabled,
		Mode:    global.Mode,
		CC: engineCC{
			Enabled:       global.CCEnabled,
			WindowSeconds: global.CCWindowSeconds,
			MaxRequests:   global.CCMaxRequests,
			Action:        global.CCAction,
		},
		AutoBan: engineAutoBan{
			Enabled:   global.AutoBanEnabled,
			Window:    global.AutoBanWindow,
			Threshold: global.AutoBanThreshold,
			Duration:  global.AutoBanDuration,
		},
		AllowIPs:           nonNilSlice(global.AllowIPs),
		DenyIPs:            nonNilSlice(global.DenyIPs),
		AllowPaths:         nonNilSlice(global.AllowPaths),
		DisabledRuleGroups: nonNilSlice(global.DisabledRuleGroups),
		BlockHTML:          global.BlockHTML,
		ChallengeSecret:    m.challengeSecret,
		BuiltinRules:       builtins,
		CustomRules:        customs,
		Exemptions:         exemptions,
		Bans:               engineBans,
		Sites:              sites,
		HostToSite:         hostToSite,
	}, nil
}

func (m *Module) engineStatus(ctx context.Context) EngineStatus {
	st := EngineStatus{
		EngineMounted: fileExists(m.wafConfPath()) && fileExists(m.wafConfigFile()),
		LogPath:       m.wafEventLog(),
	}
	if data, err := os.ReadFile(m.wafVersionFile()); err == nil {
		st.ConfigVersion = strings.TrimSpace(string(data))
	}
	if app, err := m.packages.Status(ctx, "openresty"); err == nil {
		st.OpenRestyInstalled = app.Installed
		st.OpenRestyRunning = app.Running != nil && *app.Running
	}
	return st
}

func extractSiteHosts(domains string) []string {
	var bindings []string
	_ = json.Unmarshal([]byte(domains), &bindings)
	out := make([]string, 0, len(bindings))
	for _, host := range bindings {
		if strings.HasPrefix(host, "[") {
			if idx := strings.LastIndex(host, "]"); idx > 0 {
				host = host[:idx+1]
			}
		} else if idx := strings.LastIndex(host, ":"); idx > 0 {
			host = host[:idx]
		}
		out = append(out, strings.ToLower(host))
	}
	return out
}

func atomicWriteFile(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ezwaf-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(content)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func nonNilSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// isPermissionOrNotFound 判断 dev 环境无 /www 权限时是否可降级忽略。
func isNonFatalSyncErr(err error, dev bool) bool {
	return dev && (errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist))
}
